package ptpanel

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"PortTool/src/portmap"
	"PortTool/src/ptboard"
	"PortTool/src/ptmatch"
	"PortTool/src/ptproto"
	"PortTool/src/serialx"
	"PortTool/src/simboard"
	"PortTool/src/slcan"
)

// Automatic adapter matching (decision 146): the control port found by asking
// for pt.caps, the RS485 and CAN rows of the connection block filled from
// what pt.run match heard. $PROD/docs/modules/M4/PORTTOOL-FLOW.md C.3.6,
// automatic adapter matching.

// matchListenMS is how long the board listens for tokens.
const matchListenMS = 3000

// matchControlWait bounds one candidate's pt.caps answer when finding the control port.
const matchControlWait = 1500 * time.Millisecond

type matchState struct {
	mu      sync.Mutex
	running bool
	board   *ptboard.Board // the connection the result belongs to
	matchResult
}

type matchResult struct {
	at      time.Time
	sim     bool
	terms   map[string]termResult
	unseen  []msg
	skipped []skippedPort
	err     msg
}

type termResult struct {
	OK  bool   `json:"ok"`
	COM string `json:"com"`
	Why msg    `json:"why"`
}

type skippedPort struct {
	COM string `json:"com"`
	Why msg    `json:"why"`
}

func (s *Server) matchRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/match", s.handleMatch)
	mux.HandleFunc("/api/match/control", s.handleMatchControl)
}

// handleMatch starts matching again (POST) and answers with where it stands;
// the result arrives with the once-a-second /api/conn.
func (s *Server) handleMatch(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	b := s.board
	s.mu.Unlock()
	if r.Method == http.MethodPost {
		if b == nil {
			writeJSON(w, 200, map[string]any{"error": m("go.match.need_control")})
			return
		}
		// Claimed before answering, so the answer already says running.
		if s.claimMatch(b) {
			go s.completeMatch(b)
		}
	}
	writeJSON(w, 200, map[string]any{"match": s.matchJSON()})
}

// matchJSON is the result for the board connected now; nil before the first run.
func (s *Server) matchJSON() any {
	s.mu.Lock()
	b := s.board
	s.mu.Unlock()
	st := &s.matchSt
	st.mu.Lock()
	defer st.mu.Unlock()
	if b == nil || st.board != b {
		return nil
	}
	terms := st.terms
	if terms == nil {
		terms = map[string]termResult{}
	}
	at := int64(0)
	if !st.at.IsZero() {
		at = st.at.UnixMilli()
	}
	unseen, skipped := st.unseen, st.skipped
	if unseen == nil {
		unseen = []msg{}
	}
	if skipped == nil {
		skipped = []skippedPort{}
	}
	return map[string]any{
		"running": st.running,
		"at":      at,
		"sim":     st.sim,
		"terms":   terms,
		"unseen":  unseen,
		"skipped": skipped,
		"error":   st.err,
	}
}

// runMatch matches the adapters for board b, once at a time.
func (s *Server) runMatch(b *ptboard.Board) {
	if s.claimMatch(b) {
		s.completeMatch(b)
	}
}

// claimMatch marks a run for b as started; false when one already is.
func (s *Server) claimMatch(b *ptboard.Board) bool {
	st := &s.matchSt
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.running && st.board == b {
		return false
	}
	st.running, st.board = true, b
	return true
}

func (s *Server) completeMatch(b *ptboard.Board) {
	st := &s.matchSt
	res, ran := s.doMatch(b)

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.board != b {
		return
	}
	st.running = false
	if ran {
		res.at = time.Now()
		st.matchResult = res
	}
}

// termNames are how the two terminals are named on the panel.
var termNames = map[string]string{ptmatch.TermRS485: "RS485", ptmatch.TermCAN: "CAN"}

// doMatch does the work; ran is false when nothing could be tried at all
// (a fake board in a test, no access to this PC's ports).
func (s *Server) doMatch(b *ptboard.Board) (res matchResult, ran bool) {
	s.mu.Lock()
	if s.board != b {
		s.mu.Unlock()
		return res, false
	}
	port, rw, caps := s.portNam, s.ctrlRW, s.caps
	bound := map[string]string{}
	for name, l := range s.links {
		bound[name] = l.COM
	}
	s.mu.Unlock()
	res.sim = simboard.IsSim(port)

	manual := func(why msg) map[string]termResult {
		return map[string]termResult{
			ptmatch.TermRS485: {Why: why},
			ptmatch.TermCAN:   {Why: why},
		}
	}

	var adapters []ptmatch.Adapter
	src := map[int]ptmatch.Source{}
	add := func(a ptmatch.Adapter, can bool) {
		adapters = append(adapters, a)
		src[len(adapters)] = ptmatch.Source{Name: a.Name(), CAN: can}
	}
	defer func() {
		for _, a := range adapters {
			_ = a.Close()
		}
	}()

	if res.sim {
		serial, can, err := simboard.MatchAdapters(rw)
		if err != nil {
			return res, false
		}
		for _, a := range serial {
			add(a, false)
		}
		add(can, true)
	} else {
		if s.MatchPorts == nil || s.MatchOpen == nil {
			return res, false
		}
		// The adapter on RS485 may be the one already bound: free it for its token.
		if _, ok := bound[ptmatch.TermRS485]; ok {
			s.unbindLink(ptmatch.TermRS485)
			delete(bound, ptmatch.TermRS485)
		}
		serial, can, skipped, err := s.openCandidates(port, bound)
		res.skipped = skipped
		if err != nil {
			res.err = m("go.match.failed", "detail", err)
			res.terms = manual(res.err)
			return res, true
		}
		for _, a := range serial {
			add(a, false)
		}
		for _, a := range can {
			add(a, true)
		}
	}
	if p, ok := caps.Port("match"); !ok || !hasRun(p, "match") {
		res.terms = manual(m("go.match.old_firmware"))
		return res, true
	}
	for _, t := range []string{ptmatch.TermRS485, ptmatch.TermCAN} {
		if p, ok := caps.Port(t); ok && p.Running {
			res.terms = manual(m("go.match.busy"))
			return res, true
		}
	}

	reply := ptmatch.Reply{RS485OK: true, CANOK: true}
	if len(adapters) > 0 {
		s.say(m("go.match.start", "n", len(adapters)))
		r, err := ptmatch.Listen(b, adapters, matchListenMS)
		// Closed before anything is bound: a port is open only once at a time.
		for _, a := range adapters {
			_ = a.Close()
		}
		adapters = nil
		if err != nil {
			res.err = m("go.match.failed", "detail", err)
			res.terms = manual(res.err)
			s.say(res.err)
			return res, true
		}
		if r.Busy {
			res.terms = manual(m("go.match.busy"))
			return res, true
		}
		reply = r
	}

	outcomes, unseen := ptmatch.Decide(reply, src)
	res.terms = map[string]termResult{}
	for _, t := range []string{ptmatch.TermRS485, ptmatch.TermCAN} {
		o := outcomes[t]
		tr := termResult{OK: o.OK, COM: o.COM, Why: s.matchWhy(t, o, res.sim, caps)}
		// A real RS485 adapter is bound as a person's Connect would; the simulated
		// board plays its own peers, and the CAN peer is found by USB id each time.
		if o.OK && t == ptmatch.TermRS485 && !res.sim {
			if _, err := s.bindSerialPeer(t, o.COM, serialx.DefaultBaud); err != nil {
				tr = termResult{Why: m("go.match.bind_failed", "com", o.COM, "detail", err)}
			}
		}
		res.terms[t] = tr
		s.say(tr.Why)
	}
	for _, name := range unseen {
		x := m("go.match.unseen", "com", name)
		res.unseen = append(res.unseen, x)
		s.say(x)
	}
	return res, true
}

func hasRun(p ptproto.Port, target string) bool {
	for _, r := range p.Runs {
		if r == target {
			return true
		}
	}
	return false
}

// matchWhy is the sentence for one terminal's outcome.
func (s *Server) matchWhy(term string, o ptmatch.Outcome, sim bool, caps ptproto.Caps) msg {
	name := termNames[term]
	pins := ""
	if p, ok := caps.Port(term); ok {
		pins = p.Term
	}
	coms := strings.Join(o.COMs, ", ")
	switch o.Why {
	case ptmatch.WhyMatched:
		if sim {
			return m("go.match.ok_sim", "com", o.COM, "term", name)
		}
		if term == ptmatch.TermCAN {
			return m("go.match.ok_can", "com", o.COM)
		}
		return m("go.match.ok_rs485", "com", o.COM)
	case ptmatch.WhySilent:
		return m("go.match.silent", "term", name, "pins", pins)
	case ptmatch.WhyGarbage:
		if term == ptmatch.TermCAN {
			return m("go.match.garbage_can")
		}
		return m("go.match.garbage_rs485")
	case ptmatch.WhySeveral:
		return m("go.match.several", "term", name, "coms", coms)
	case ptmatch.WhyCrossed:
		return m("go.match.crossed", "term", name, "coms", coms)
	case ptmatch.WhyNotUp:
		return m("go.match.not_up", "term", name)
	case ptmatch.WhyNoAdapter:
		if term == ptmatch.TermCAN {
			return m("go.match.no_canable")
		}
		return m("go.match.no_serial")
	}
	return m("go.match.failed", "detail", o.Why)
}

// skipWhy words why a port is never written to.
var skipWhy = map[ptmatch.Skip]string{
	ptmatch.SkipBluetooth: "go.match.skip.bluetooth",
	ptmatch.SkipSTLink:    "go.match.skip.stlink",
	ptmatch.SkipBoardUSB:  "go.match.skip.board_usb",
}

// candidatePorts lists this PC's ports, minus the ones never written to: the
// classes ptmatch.Classify names and the ports in use (control, reference
// board, bound links). CANables come back apart.
func (s *Server) candidatePorts(control string, bound map[string]string) (serial, canable []string, skipped []skippedPort, err error) {
	ports, err := s.MatchPorts()
	if err != nil {
		return nil, nil, nil, err
	}
	inUse := map[string]bool{strings.ToUpper(control): true}
	s.conn.mu.Lock()
	if s.conn.ref.port != "" {
		inUse[strings.ToUpper(s.conn.ref.port)] = true
	}
	s.conn.mu.Unlock()
	for _, com := range bound {
		inUse[strings.ToUpper(com)] = true
	}
	for _, p := range ports {
		if inUse[strings.ToUpper(p.Name)] {
			skipped = append(skipped, skippedPort{COM: p.Name, Why: m("go.match.skip.in_use")})
			continue
		}
		switch k := ptmatch.Classify(p); k {
		case ptmatch.SkipNone:
			serial = append(serial, p.Name)
		case ptmatch.SkipCANable:
			canable = append(canable, p.Name)
		default:
			skipped = append(skipped, skippedPort{COM: p.Name, Why: m(skipWhy[k])})
		}
	}
	return serial, canable, skipped, nil
}

// openCandidates opens every candidate adapter; one that will not open is
// listed with the reason, not fatal.
func (s *Server) openCandidates(control string, bound map[string]string) (serial, can []ptmatch.Adapter, skipped []skippedPort, err error) {
	sc, cc, skipped, err := s.candidatePorts(control, bound)
	if err != nil {
		return nil, nil, skipped, err
	}
	for _, com := range sc {
		rw, err := s.MatchOpen(com, serialx.DefaultBaud)
		if err != nil {
			skipped = append(skipped, skippedPort{COM: com, Why: m("go.match.skip.open_failed", "detail", err)})
			continue
		}
		serial = append(serial, &ptmatch.Serial{COM: com, RW: rw})
	}
	for _, com := range cc {
		rw, err := s.MatchOpen(com, serialx.DefaultBaud)
		if err == nil {
			var a *slcan.Adapter
			if a, err = slcan.Open(rw, slcan.DefaultBitrate); err == nil {
				can = append(can, &ptmatch.CAN{COM: com, A: a})
				continue
			}
			_ = rw.Close()
		}
		skipped = append(skipped, skippedPort{COM: com, Why: m("go.match.skip.open_failed", "detail", err)})
	}
	return serial, can, skipped, nil
}

// bindSerialPeer binds a serial adapter as a link port's far end and
// remembers it, as the row's Connect does (handleLink).
func (s *Server) bindSerialPeer(boardPort, com string, baud int) (*linkPort, error) {
	s.unbindLink(boardPort)
	l, err := s.bindLink(boardPort, com, baud)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.links == nil {
		s.links = map[string]*linkPort{}
	}
	s.links[boardPort] = l
	p, _ := s.caps.Port(boardPort)
	s.mu.Unlock()
	// Bound after the session started: take up its mode (PORTTOOL-FLOW.md B.9).
	if p.Running {
		l.setMode(p.Values["mode"])
	}
	portmap.SetPeer(boardPort, com, l.Baud)
	s.say(m("go.link.serial_up", "port", boardPort, "com", com, "baud", l.Baud))
	return l, nil
}

// handleMatchControl finds the control port: every candidate is asked for
// pt.caps; exactly one fixture answer is the control port.
func (s *Server) handleMatchControl(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	connected := s.board != nil
	s.mu.Unlock()
	if connected {
		writeJSON(w, 200, map[string]any{"error": m("go.match.ctl.connected")})
		return
	}
	if s.MatchPorts == nil || s.MatchOpen == nil {
		writeJSON(w, 200, map[string]any{"error": m("go.match.ctl.off")})
		return
	}
	serial, canable, skipped, err := s.candidatePorts("", nil)
	if err != nil {
		writeJSON(w, 200, map[string]any{"error": m("go.ports.list_failed", "detail", err)})
		return
	}
	for _, com := range canable {
		skipped = append(skipped, skippedPort{COM: com, Why: m("go.match.skip.canable")})
	}
	found, tried := ptmatch.FindControl(serial, s.MatchOpen, matchControlWait)

	list := make([]skippedPort, 0, len(tried)+len(skipped))
	for _, t := range tried {
		why := m("go.match.ctl.no_answer")
		switch t.Why {
		case "answered":
			why = m("go.match.ctl.answered", "firmware", t.Detail)
		case "open_failed":
			why = m("go.match.skip.open_failed", "detail", errors.New(t.Detail))
		}
		list = append(list, skippedPort{COM: t.COM, Why: why})
	}
	list = append(list, skipped...)
	out := map[string]any{"tried": list}
	switch len(found) {
	case 1:
		out["found"] = found[0].COM
		out["firmware"] = found[0].Firmware
		s.say(m("go.match.ctl.found", "com", found[0].COM, "firmware", found[0].Firmware))
	case 0:
		out["error"] = m("go.match.ctl.none", "n", len(tried))
	default:
		coms := make([]string, len(found))
		for i, f := range found {
			coms[i] = f.COM
		}
		out["error"] = m("go.match.ctl.many", "coms", strings.Join(coms, ", "))
	}
	writeJSON(w, 200, out)
}
