package ptpanel

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"PortTool/src/portmap"
	"PortTool/src/ptboard"
	"PortTool/src/ptecho"
	"PortTool/src/ptproto"
	"PortTool/src/ptseq"
	"PortTool/src/serialx"
	"PortTool/src/simboard"
)

// A link port is the far end of a loop=link session: a serial adapter on the
// terminal under test, or a TCP connection to the board's Ethernet session.
// The echoing is ptecho's, the same as a plan step's (decision 77); the reply
// must come back over the link under test, never the control port.
type linkPort struct {
	Board string // the board's port name, e.g. "rs485"
	COM   string // the adapter on this machine, or host:port for TCP
	Baud  int
	// Kind is "serial" or "tcp" (the Ethernet session's far end).
	Kind string

	peer *ptecho.Peer
	say  func(msg) // the panel's own sentences about this link; nil in tests

	mu sync.Mutex
	// mode is what the board's session is doing, so this end does the
	// matching half; empty means echo.
	mode     string
	sinkStop func()
}

func newLinkPort(boardPort, com string, baud int, kind string, rw io.ReadWriteCloser, say func(msg)) *linkPort {
	l := &linkPort{Board: boardPort, COM: com, Baud: baud, Kind: kind, say: say}
	l.peer = ptecho.New(boardPort+"@"+com, rw, l.echoSay)
	return l
}

// echoSay words ptecho's lines through the panel dictionary.
func (l *linkPort) echoSay(key string, kv ...any) {
	if l.say != nil {
		l.say(m(key, kv...))
	}
}

func (l *linkPort) stop() {
	l.setMode(modeEcho)
	l.peer.Stop()
}

func (l *linkPort) snapshot() map[string]any {
	l.mu.Lock()
	mode := l.mode
	l.mu.Unlock()
	if mode == "" {
		mode = modeEcho
	}
	st := l.peer.Stats()
	out := map[string]any{
		"com":     l.COM,
		"kind":    l.Kind,
		"baud":    l.Baud,
		"rxLines": st.Reads,
		"echoed":  st.Echoes,
		"rxBytes": st.RxBytes,
		"txBytes": st.TxBytes,
		"mode":    mode,
		"error":   st.Err,
	}
	if fl, ok := l.peer.Flooding(); ok {
		out["flood"] = fl
	}
	return out
}

// The session modes this end has to answer differently.
const (
	modeEcho   = "echo"
	modeSink   = "sink"
	modeSource = "source"
	modeMirror = "mirror"
)

// bindLink opens the adapter and starts repeating whatever arrives on it.
func (s *Server) bindLink(boardPort, com string, baud int) (*linkPort, error) {
	if baud == 0 {
		baud = serialx.DefaultBaud
	}
	port, err := s.Open(com, baud)
	if err != nil {
		return nil, err
	}
	l := newLinkPort(boardPort, com, baud, "serial", port, func(x msg) { s.say(m("go.link.line", "board", boardPort, "com", com, "what", x)) })
	return l, nil
}

// setMode tells this end which half of the session it is running. Only sink
// and mirror originate traffic: in echo mode a byte started here reads as a
// corrupted link; in source mode this end only keeps reading (PORTTOOL-FLOW.md B.9).
func (l *linkPort) setMode(mode string) {
	if mode == "" {
		mode = modeEcho
	}
	l.mu.Lock()
	if l.mode == mode {
		l.mu.Unlock()
		return
	}
	l.mode = mode
	stop := l.sinkStop
	l.sinkStop = nil
	l.mu.Unlock()

	if stop != nil {
		stop()
	}
	_, _ = l.peer.StopFlood(0)
	l.peer.SetDrain(mode == modeSource)
	if mode == modeSink {
		l.startSink()
	}
	if mode == modeMirror {
		// The board mirrors, so this end floods and measures (PORTTOOL-FLOW.md B.9).
		if l.Kind == "tcp" {
			l.peer.StartFlood(ptecho.FloodTCP)
		} else {
			l.peer.StartFlood(ptecho.FloodUSB)
		}
	}
}

// startSink pushes at the board for as long as the mode lasts (ptecho.Push).
func (l *linkPort) startSink() {
	stop := l.peer.Push(l.echoSay, func(err error) {
		if l.say != nil {
			l.say(m("go.link.write_failed", "detail", err))
		}
	})
	l.mu.Lock()
	l.sinkStop = stop
	l.mu.Unlock()
}

// linkModeFromCmd reads the session mode out of a command on its way to the
// board, so the far end changes with the board; the frames carry it too late.
func linkModeFromCmd(cmd string) (port, mode string) {
	f := strings.Fields(cmd)
	if len(f) < 2 {
		return "", ""
	}
	switch f[0] {
	case "pt.start", "pt.set":
		for _, t := range f[2:] {
			if v, ok := strings.CutPrefix(t, "mode="); ok {
				mode = v
			}
		}
		if mode == "" {
			return "", ""
		}
		return f[1], mode
	case "pt.stop":
		// A stopped session has no mode; a sink left pushing would only log write errors.
		return f[1], modeEcho
	}
	return "", ""
}

// applyLinkMode moves the far end to whatever mode the command just put the
// board in. "all" reaches every bound link, the way pt.stop all does.
func (s *Server) applyLinkMode(cmd string) {
	if f := strings.Fields(cmd); len(f) >= 2 && f[0] == "pt.start" {
		s.openChosenLink(f[1])
	}
	port, mode := linkModeFromCmd(cmd)
	if port == "" {
		return
	}
	if f := strings.Fields(cmd); f[0] == "pt.stop" {
		// The far end is held only while the session runs (decision 168).
		s.mu.Lock()
		if s.linkGen == nil {
			s.linkGen = map[string]int{}
		}
		s.linkGen[port]++
		s.linkGen["all"]++
		s.mu.Unlock()
		if port == "all" {
			s.unbindAllLinks()
		} else {
			s.unbindLink(port)
		}
		return
	}
	s.mu.Lock()
	var targets []*linkPort
	if port == "all" {
		for _, l := range s.links {
			targets = append(targets, l)
		}
	} else if l := s.links[port]; l != nil {
		targets = append(targets, l)
	}
	s.mu.Unlock()

	for _, l := range targets {
		l.setMode(mode)
	}
}

// openChosenLink opens the far end chosen in the connection block for a
// session started from the command box, in the background: the board's USB
// port and TCP listener only appear once the session runs (decision 168).
func (s *Server) openChosenLink(port string) {
	s.mu.Lock()
	p, found := s.caps.Port(port)
	sim, held := simboard.IsSim(s.portNam), s.links[port] != nil
	ip := s.ip.addr
	gen, genAll := s.linkGen[port], s.linkGen["all"]
	s.mu.Unlock()
	// stopped says the session ended since this open began.
	stopped := func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.linkGen[port] != gen || s.linkGen["all"] != genAll
	}
	// kept closes a far end that came up after the session ended.
	kept := func() bool {
		if stopped() {
			s.unbindLink(port)
			return false
		}
		return true
	}
	// The simulated board plays its own peers; CAN has no line-echo far end.
	if !found || p.Loop != ptproto.LoopLink || sim || held || port == "can" {
		return
	}
	go func() {
		deadline := time.Now().Add(linkOpenWait)
		var err error
		for {
			if stopped() {
				return
			}
			switch port {
			case "eth":
				if ip == "" {
					s.say(m("go.link.no_ip"))
					return
				}
				lp := p.Values["port"]
				if lp == "" {
					lp = "5000"
				}
				var l *linkPort
				if l, err = s.bindTCP(port, net.JoinHostPort(ip, lp)); err == nil {
					s.mu.Lock()
					if s.links == nil {
						s.links = map[string]*linkPort{}
					}
					s.links[port] = l
					s.mu.Unlock()
					if kept() {
						l.setMode(p.Values["mode"])
					}
					return
				}
			default:
				find := func() (string, error) {
					return "", fmt.Errorf("no adapter chosen for %s in the connection block", port)
				}
				if port == "usb" {
					find = ptecho.FindCDC
				}
				var com string
				if com, err = pickedOr(port, find)(); err == nil {
					if _, err = s.bindSerialPeer(port, com, 0); err == nil {
						kept()
						return
					}
				}
			}
			if time.Now().After(deadline) {
				s.say(m("go.link.open_failed", "port", port, "detail", err))
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()
}

// linkOpenWait covers the board's USB enumeration and its DHCP lease.
const linkOpenWait = 10 * time.Second

// say pushes the panel's own sentence to every open event stream: the page
// words it in its language, the event also carries the English, and the log
// pane labels it as the panel's, not the board's.
func (s *Server) say(x msg) { s.emitEvent(panelEvent{Line: x.English(), Msg: &x}) }

func (s *Server) emitEvent(ev panelEvent) {
	s.mu.Lock()
	s.emitSeq++
	ev.Seq, ev.At = s.emitSeq, time.Now()
	if len(s.emitRing) >= emitRingCap {
		s.emitRing = s.emitRing[len(s.emitRing)-emitRingCap+1:]
	}
	s.emitRing = append(s.emitRing, ev)
	for _, c := range s.emitSubs {
		select {
		case c <- ev:
		default:
		}
	}
	s.mu.Unlock()
}

const emitRingCap = 4000

type panelEvent struct {
	Seq  uint64
	Line string
	Msg  *msg // set for the panel's own sentences
	At   time.Time
}

func (s *Server) subscribePanel(buffer int) (<-chan panelEvent, []panelEvent, func()) {
	ch := make(chan panelEvent, buffer)
	s.mu.Lock()
	if s.emitSubs == nil {
		s.emitSubs = map[int]chan panelEvent{}
	}
	id := s.emitNextSub
	s.emitNextSub++
	s.emitSubs[id] = ch
	backlog := make([]panelEvent, len(s.emitRing))
	copy(backlog, s.emitRing)
	s.mu.Unlock()

	return ch, backlog, func() {
		s.mu.Lock()
		if c, ok := s.emitSubs[id]; ok {
			delete(s.emitSubs, id)
			close(c)
		}
		s.mu.Unlock()
	}
}

// bindTCP connects to the board as a TCP client and echoes back whatever it
// sends: the far end the Ethernet session needs, since the board runs the
// server and somebody has to connect to it, or conn= stays 0.
func (s *Server) bindTCP(boardPort, addr string) (*linkPort, error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	l := newLinkPort(boardPort, addr, 0, "tcp", conn, func(x msg) { s.say(m("go.link.line", "board", boardPort, "com", addr, "what", x)) })
	return l, nil
}

// openRunPeer opens a test run's peer. A connection-block link holding the same
// adapter is released first and bound again once the run closes the peer: one
// serial port cannot be opened twice (PORTTOOL-FLOW.md C.3.6 「连接区」 in $PROD).
func (s *Server) openRunPeer(kind, addr string, baud int) (io.ReadWriteCloser, error) {
	held := s.linkOn(addr)
	if held != nil {
		s.unbindLink(held.Board)
	}
	var rw io.ReadWriteCloser
	var err error
	if kind == "tcp" {
		rw, err = ptseq.RealPeerOpener(kind, addr, baud)
	} else {
		rw, err = s.Open(addr, baud)
	}
	// A TCP link is not bound again: the board stops listening with its session.
	if held == nil || held.Kind != "serial" {
		return rw, err
	}
	if err != nil {
		s.rebindLink(held)
		return nil, err
	}
	return &rebindOnClose{ReadWriteCloser: rw, rebind: func() { s.rebindLink(held) }}, nil
}

// linkOn is the bound link on an adapter (or TCP address), or nil.
func (s *Server) linkOn(addr string) *linkPort {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.links {
		if strings.EqualFold(l.COM, addr) {
			return l
		}
	}
	return nil
}

func (s *Server) rebindLink(l *linkPort) {
	if _, err := s.bindSerialPeer(l.Board, l.COM, l.Baud); err != nil {
		s.say(m("go.link.connect_failed", "what", l.COM, "detail", err, "why", m("go.link.hint_busy")))
	}
}

// rebindOnClose binds the borrowed link again when the run lets its peer go.
type rebindOnClose struct {
	io.ReadWriteCloser
	once   sync.Once
	rebind func()
}

func (r *rebindOnClose) Close() error {
	err := r.ReadWriteCloser.Close()
	r.once.Do(r.rebind)
	return err
}

// handleRelease is 「强制释放端口」: stop every test and close every far end the
// panel holds, then say which chosen adapter still will not open (decision 168).
func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	s.stopLib()
	s.stopTimedRun(m("go.reason.released"))
	for end := time.Now().Add(releaseWait); s.libBusy() && time.Now().Before(end); {
		time.Sleep(100 * time.Millisecond)
	}
	s.mu.Lock()
	b, sim := s.board, simboard.IsSim(s.portNam)
	s.mu.Unlock()
	if b != nil {
		_, _ = b.Send("pt.stop all", ptboard.ExpectFor("pt.stop all"), ptboard.TimeoutFor("pt.stop all"))
	}
	s.unbindAllLinks()
	busy := []string{}
	if !sim {
		for _, port := range []string{"usb", "rs485", "can"} {
			com, baud, ok := portmap.Peer(port)
			if !ok {
				continue
			}
			rw, err := s.Open(com, baud)
			if err != nil {
				busy = append(busy, com)
				s.say(m("go.release.still_busy", "com", com, "detail", err))
				continue
			}
			_ = rw.Close()
		}
	}
	s.say(m("go.release.done"))
	writeJSON(w, 200, map[string]any{"busy": busy})
}

// releaseWait is how long a stopped run gets to close its far ends.
const releaseWait = 5 * time.Second
