package ptpanel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"PortTool/src/portmap"
	"PortTool/src/serialx"
)

// Adapter matching (decision 146) against a scripted board and fake adapters:
// nothing here opens a serial port on this PC. PORTTOOL-FLOW.md C.3.6,
// automatic adapter matching.

const matchCaps = "OK porttool=0.18.3 ports=3 lines=9\r\n" +
	"OK port=rs485 board=upper kind=session blk=C term=C10,C11 channels=1 loop=link params=baud,period running=0\r\n" +
	"OK vals=rs485 baud=115200 period=3000\r\n" +
	"OK terms=rs485 C10+C11\r\n" +
	"OK limits=rs485 baud:9600|19200|38400|57600|115200 period:50..\r\n" +
	"OK port=can board=upper kind=session blk=C term=C07,C08 channels=1 loop=link params=baud,mode,period running=0\r\n" +
	"OK vals=can baud=500000 mode=normal period=1000\r\n" +
	"OK terms=can C07+C08\r\n" +
	"OK limits=can baud:125000|250000|500000|1000000 mode:normal|listen|loopback|extloop|echo|bus period:50..\r\n" +
	"OK port=match board=upper kind=run blk=C term=C07,C08,C10,C11 channels=1 loop=none runs=match\r\n"

// scriptBoard answers pt.caps, rtc.read and pt.run match; reply builds the
// OK match body from what the adapters were given.
type scriptBoard struct {
	out   chan []byte
	reply func() string
	mu    sync.Mutex
	line  []byte
	runs  int
}

func newScriptBoard(reply func() string) *scriptBoard {
	return &scriptBoard{out: make(chan []byte, 64), reply: reply}
}

func (b *scriptBoard) Read(p []byte) (int, error) {
	d, ok := <-b.out
	if !ok {
		return 0, io.EOF
	}
	return copy(p, d), nil
}

func (b *scriptBoard) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.line = append(b.line, p...)
	var cmds []string
	for {
		i := bytes.IndexByte(b.line, '\n')
		if i < 0 {
			break
		}
		cmds = append(cmds, strings.TrimSpace(string(b.line[:i])))
		b.line = b.line[i+1:]
	}
	b.mu.Unlock()
	for _, c := range cmds {
		switch {
		case c == "":
		case c == "pt.caps":
			b.out <- []byte(matchCaps)
		case strings.HasPrefix(c, "pt.run rtc.read"):
			b.out <- []byte("OK rtc.read init=1 clk=lse date=26-10-23 time=10:00:00\r\n")
		case strings.HasPrefix(c, "pt.run match"):
			b.mu.Lock()
			b.runs++
			b.mu.Unlock()
			go func() {
				b.out <- []byte("MATCH_TEST: listening on RS485 at 115200 and CAN at 500000 for 3000 ms\r\n")
				time.Sleep(400 * time.Millisecond)
				b.out <- []byte("OK match busy=0 ms=3000 baud=115200 bps=500000 " + b.reply() + "\r\n")
			}()
		default:
			b.out <- []byte("ERR unknown command \"" + c + "\"\r\n")
		}
	}
	return len(p), nil
}

func (b *scriptBoard) Close() error { return nil }

// tokPort records what an adapter was given; a CANable also acknowledges
// every SLCAN command with CR.
type tokPort struct {
	slcan  bool
	mu     sync.Mutex
	buf    bytes.Buffer
	acks   chan []byte
	closed bool
}

func newTokPort(slcan bool) *tokPort { return &tokPort{slcan: slcan, acks: make(chan []byte, 64)} }

func (p *tokPort) Read(b []byte) (int, error) {
	d, ok := <-p.acks
	if !ok {
		return 0, io.EOF
	}
	return copy(b, d), nil
}

func (p *tokPort) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, io.ErrClosedPipe
	}
	p.buf.Write(b)
	if p.slcan && bytes.HasSuffix(b, []byte("\r")) {
		select {
		case p.acks <- []byte("\r"):
		default:
		}
	}
	return len(b), nil
}

func (p *tokPort) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		close(p.acks)
	}
	return nil
}

func (p *tokPort) text() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.buf.String()
}

var (
	serialTokRe = regexp.MustCompile(`OPLC-(\d+)\r\n`)
	// SLCAN "t5A5" + DLC + the data in hex: OPLC- is 4F504C432D.
	canTokRe = regexp.MustCompile(`t5A5\d4F504C432D((?:3[0-9])+)`)
)

func serialTokens(p *tokPort) string {
	if p == nil {
		return "-"
	}
	m := serialTokRe.FindStringSubmatch(p.text())
	if m == nil {
		return "-"
	}
	return m[1]
}

func canTokens(p *tokPort) string {
	m := canTokRe.FindStringSubmatch(p.text())
	if m == nil {
		return "-"
	}
	var n strings.Builder
	for i := 0; i < len(m[1]); i += 2 {
		n.WriteByte(m[1][i+1])
	}
	return n.String()
}

type matchBench struct {
	srv     *httptest.Server
	s       *Server
	board   *scriptBoard
	mu      sync.Mutex
	opened  []string
	ports   map[string]*tokPort
	onRS485 string // which COM the RS485 terminals are wired to
}

func newMatchBench(t *testing.T) *matchBench {
	t.Helper()
	old := portmap.File
	portmap.File = filepath.Join(t.TempDir(), "porttool_ports.json")
	t.Cleanup(func() { portmap.File = old })

	mb := &matchBench{onRS485: "COM5", ports: map[string]*tokPort{}}
	mb.board = newScriptBoard(func() string {
		mb.mu.Lock()
		rs := serialTokens(mb.ports[mb.onRS485])
		can := canTokens(mb.ports["COM11"])
		mb.mu.Unlock()
		return fmt.Sprintf("rs485=%s rs485_ok=1 rs485_bytes=9 rs485_junk=0 can=%s can_ok=1 can_frames=1 can_junk=0 can_lec=7", rs, can)
	})
	s := New()
	s.Open = func(name string, baud int) (io.ReadWriteCloser, error) {
		if name == "COM3" {
			return mb.board, nil
		}
		return newFakePort(), nil // the link bound after matching
	}
	s.MatchPorts = func() ([]serialx.PortInfo, error) {
		return []serialx.PortInfo{
			{Name: "COM3", IsUSB: true, VID: "067B", PID: "23A3"},
			{Name: "COM5", IsUSB: true, VID: "1A86", PID: "7523"},
			{Name: "COM12", IsUSB: true, VID: "0403", PID: "6001"},
			{Name: "COM7", Product: "Standard Serial over Bluetooth link (COM7)"},
			{Name: "COM9", IsUSB: true, VID: "0483", PID: "374B"},
			{Name: "COM17", IsUSB: true, VID: "0483", PID: "5740"},
			{Name: "COM11", IsUSB: true, VID: "16D0", PID: "117E"},
		}, nil
	}
	s.MatchOpen = func(name string, baud int) (io.ReadWriteCloser, error) {
		mb.mu.Lock()
		defer mb.mu.Unlock()
		mb.opened = append(mb.opened, name)
		p := newTokPort(name == "COM11")
		mb.ports[name] = p
		return p, nil
	}
	mb.s = s
	mb.srv = httptest.NewServer(s.Handler())
	t.Cleanup(func() { mb.srv.Close(); s.Close() })
	return mb
}

func (mb *matchBench) post(t *testing.T, path string, body any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(body)
	r, err := http.Post(mb.srv.URL+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(r.Body).Decode(&out)
	return out
}

// waitMatch waits for a result newer than after.
func (mb *matchBench) waitMatch(t *testing.T, after time.Time) matchResult {
	t.Helper()
	end := time.Now().Add(15 * time.Second)
	for time.Now().Before(end) {
		st := &mb.s.matchSt
		st.mu.Lock()
		res, running := st.matchResult, st.running
		st.mu.Unlock()
		if !running && res.at.After(after) {
			return res
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no matching result within 15 s")
	return matchResult{}
}

func TestMatchAfterConnectFillsTheRows(t *testing.T) {
	mb := newMatchBench(t)
	start := time.Now()
	if j := mb.post(t, "/api/connect", map[string]any{"port": "COM3"}); j["connected"] != true {
		t.Fatalf("connect: %v", j)
	}
	res := mb.waitMatch(t, start)

	rs, can := res.terms["rs485"], res.terms["can"]
	if !rs.OK || rs.COM != "COM5" || rs.Why.Key != "go.match.ok_rs485" {
		t.Errorf("rs485: %+v", rs)
	}
	if !can.OK || can.COM != "COM11" || can.Why.Key != "go.match.ok_can" {
		t.Errorf("can: %+v", can)
	}
	// Each adapter got its own token; the board heard COM5's and the CANable's.
	if serialTokens(mb.ports["COM5"]) != "1" || serialTokens(mb.ports["COM12"]) != "2" || canTokens(mb.ports["COM11"]) != "3" {
		t.Errorf("tokens: COM5 %q COM12 %q COM11 %q", mb.ports["COM5"].text(), mb.ports["COM12"].text(), mb.ports["COM11"].text())
	}
	if len(res.unseen) != 1 || res.unseen[0].Args["com"] != "COM12" {
		t.Errorf("unseen: %+v", res.unseen)
	}

	// Never written to: the control port, Bluetooth, ST-Link, the board's USB.
	mb.mu.Lock()
	opened := strings.Join(mb.opened, ",")
	mb.mu.Unlock()
	if opened != "COM5,COM12,COM11" {
		t.Errorf("opened %s, want COM5,COM12,COM11", opened)
	}
	skipped := map[string]string{}
	for _, sp := range res.skipped {
		skipped[sp.COM] = sp.Why.Key
	}
	want := map[string]string{"COM3": "go.match.skip.in_use", "COM7": "go.match.skip.bluetooth",
		"COM9": "go.match.skip.stlink", "COM17": "go.match.skip.board_usb"}
	if fmt.Sprint(skipped) != fmt.Sprint(want) {
		t.Errorf("skipped %v, want %v", skipped, want)
	}

	// RS485 is bound as a person's Connect would, and remembered for plans.
	mb.s.mu.Lock()
	l := mb.s.links["rs485"]
	mb.s.mu.Unlock()
	if l == nil || l.COM != "COM5" {
		t.Errorf("rs485 link: %+v", l)
	}
	if com, _, ok := portmap.Peer("rs485"); !ok || com != "COM5" {
		t.Errorf("porttool_ports.json rs485 = %q %v", com, ok)
	}
	if _, _, ok := portmap.Peer("can"); ok {
		t.Error("the CAN adapter is found by USB id each time, not stored")
	}

	// The page reads it with the once-a-second poll.
	r, err := http.Get(mb.srv.URL + "/api/conn")
	if err != nil {
		t.Fatal(err)
	}
	var conn map[string]any
	_ = json.NewDecoder(r.Body).Decode(&conn)
	r.Body.Close()
	m, _ := conn["match"].(map[string]any)
	terms, _ := m["terms"].(map[string]any)
	if rsj, _ := terms["rs485"].(map[string]any); rsj == nil || rsj["com"] != "COM5" || rsj["ok"] != true {
		t.Errorf("/api/conn match: %v", conn["match"])
	}

	// The RS485 adapter unplugged from the terminals: detect again says so
	// and leaves the row to a manual choice.
	mb.mu.Lock()
	mb.onRS485 = ""
	mb.mu.Unlock()
	again := time.Now()
	mb.post(t, "/api/match", map[string]any{})
	res = mb.waitMatch(t, again)
	if rs := res.terms["rs485"]; rs.OK || rs.Why.Key != "go.match.silent" || rs.Why.Args["pins"] != "C10,C11" {
		t.Errorf("rs485 after unplugging: %+v", rs)
	}
	mb.s.mu.Lock()
	_, still := mb.s.links["rs485"]
	mb.s.mu.Unlock()
	if still {
		t.Error("the old binding was released for the token and not rebound")
	}
}

func TestMatchSaysWhenARunningTestHoldsTheTerminals(t *testing.T) {
	mb := newMatchBench(t)
	start := time.Now()
	mb.post(t, "/api/connect", map[string]any{"port": "COM3"})
	mb.waitMatch(t, start)

	mb.s.mu.Lock()
	for i, p := range mb.s.caps.Ports {
		if p.Name == "can" {
			mb.s.caps.Ports[i].Running = true
		}
	}
	mb.s.mu.Unlock()
	mb.board.mu.Lock()
	runs := mb.board.runs
	mb.board.mu.Unlock()

	again := time.Now()
	mb.post(t, "/api/match", map[string]any{})
	res := mb.waitMatch(t, again)
	if res.terms["rs485"].Why.Key != "go.match.busy" || res.terms["can"].Why.Key != "go.match.busy" {
		t.Errorf("busy: %+v", res.terms)
	}
	mb.board.mu.Lock()
	defer mb.board.mu.Unlock()
	if mb.board.runs != runs {
		t.Error("nothing is sent to the board while its terminals are taken")
	}
}

func TestMatchStaysOffRealPortsWithoutAccess(t *testing.T) {
	// New() leaves MatchPorts / MatchOpen nil: a test board never gets pt.run match.
	board := newScriptBoard(func() string { return "rs485=- can=-" })
	s := New()
	s.Open = func(string, int) (io.ReadWriteCloser, error) { return board, nil }
	srv := httptest.NewServer(s.Handler())
	defer func() { srv.Close(); s.Close() }()
	raw, _ := json.Marshal(map[string]any{"port": "COM3"})
	r, err := http.Post(srv.URL+"/api/connect", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	time.Sleep(300 * time.Millisecond)
	board.mu.Lock()
	defer board.mu.Unlock()
	if board.runs != 0 {
		t.Error("pt.run match was sent with no access to this PC's ports")
	}
}

func TestFindControlPortConnectsToTheOneFixture(t *testing.T) {
	s := New()
	s.MatchPorts = func() ([]serialx.PortInfo, error) {
		return []serialx.PortInfo{
			{Name: "COM3", IsUSB: true, VID: "067B", PID: "23A3"},
			{Name: "COM4", IsUSB: true, VID: "0403", PID: "6001"},
			{Name: "COM7", Product: "蓝牙链接上的标准串行 (COM7)"},
			{Name: "COM8"},
		}, nil
	}
	var mu sync.Mutex
	var opened []string
	s.MatchOpen = func(name string, baud int) (io.ReadWriteCloser, error) {
		mu.Lock()
		opened = append(opened, name)
		mu.Unlock()
		switch name {
		case "COM3":
			p := newTokPort(false)
			go func() { p.acks <- []byte("OK porttool=0.18.3 ports=18 lines=55\r\n") }()
			return p, nil
		case "COM8":
			return nil, errors.New("access denied")
		}
		return newTokPort(false), nil
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	raw, _ := json.Marshal(map[string]any{})
	r, err := http.Post(srv.URL+"/api/match/control", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.NewDecoder(r.Body).Decode(&out)
	r.Body.Close()
	if out["found"] != "COM3" || out["firmware"] != "0.18.3" {
		t.Errorf("found: %v", out)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, n := range opened {
		if n == "COM7" {
			t.Error("a Bluetooth port was opened")
		}
	}
	tried, _ := out["tried"].([]any)
	if len(tried) != 4 {
		t.Errorf("every port is listed with what it did: %v", tried)
	}
}
