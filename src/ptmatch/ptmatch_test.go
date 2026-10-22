package ptmatch

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"PortTool/src/ptboard"
	"PortTool/src/serialx"
	"PortTool/src/slcan"
)

// Fakes only: nothing here opens a serial port on this PC.

func TestClassifyKeepsObviousNonCandidatesOut(t *testing.T) {
	cases := []struct {
		p    serialx.PortInfo
		want Skip
	}{
		{serialx.PortInfo{Name: "COM18", Product: "蓝牙链接上的标准串行 (COM18)"}, SkipBluetooth},
		{serialx.PortInfo{Name: "COM4", Product: "Standard Serial over Bluetooth link (COM4)"}, SkipBluetooth},
		{serialx.PortInfo{Name: "COM5", Product: "Standardmäßige serielle Verbindung über Bluetooth (COM5)"}, SkipBluetooth},
		{serialx.PortInfo{Name: "/dev/rfcomm0"}, SkipBluetooth},
		{serialx.PortInfo{Name: "COM9", IsUSB: true, VID: "0483", PID: "374B", Product: "STMicroelectronics STLink Virtual COM Port"}, SkipSTLink},
		{serialx.PortInfo{Name: "COM17", IsUSB: true, VID: "0483", PID: "5740"}, SkipBoardUSB},
		{serialx.PortInfo{Name: "COM11", IsUSB: true, VID: "16d0", PID: "117e"}, SkipCANable},
		{serialx.PortInfo{Name: "COM12", IsUSB: true, VID: "0403", PID: "6001", Product: "USB Serial Port"}, SkipNone},
		{serialx.PortInfo{Name: "COM13", IsUSB: true, VID: "1A86", PID: "7523"}, SkipNone},
		{serialx.PortInfo{Name: "COM1", Product: "ELTIMA Virtual Serial Port (COM1->COM2)"}, SkipNone},
	}
	for _, c := range cases {
		if got := Classify(c.p); got != c.want {
			t.Errorf("%s %q: got %q, want %q", c.p.Name, c.p.Product, got, c.want)
		}
	}
}

func TestParseReply(t *testing.T) {
	r, err := ParseReply([]string{"OK match busy=0 ms=3000 baud=115200 bps=500000 rs485=2,5 rs485_ok=1 rs485_bytes=27 rs485_junk=1 can=7 can_ok=1 can_frames=2 can_junk=1 can_lec=7"})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(r.RS485) != "[2 5]" || fmt.Sprint(r.CAN) != "[7]" || !r.RS485OK || !r.CANOK ||
		r.RS485Junk != 1 || r.CANJunk != 1 || r.CANFrames != 2 || r.CANLec != 7 || r.Busy {
		t.Errorf("parsed %+v", r)
	}
	r, err = ParseReply([]string{"OK match busy=0 rs485=- rs485_ok=0 can=- can_ok=1"})
	if err != nil || r.RS485 != nil || r.CAN != nil || r.RS485OK {
		t.Errorf("empty lists: %+v %v", r, err)
	}
	if r, err := ParseReply([]string{"OK match busy=1"}); err != nil || !r.Busy {
		t.Errorf("busy: %+v %v", r, err)
	}
	if _, err := ParseReply([]string{"OK rtc.read init=1"}); err == nil {
		t.Error("a reply with no OK match line must be an error")
	}
}

func TestDecideEveryOutcome(t *testing.T) {
	src := map[int]Source{1: {Name: "COM5"}, 2: {Name: "COM12"}, 3: {Name: "COM11", CAN: true}}
	ok := Reply{RS485OK: true, CANOK: true}
	with := func(f func(*Reply)) Reply { r := ok; f(&r); return r }

	cases := []struct {
		name       string
		r          Reply
		src        map[int]Source
		rs485, can Outcome
		wantUnseen []string
	}{
		{"both matched", with(func(r *Reply) { r.RS485 = []int{1}; r.CAN = []int{3} }), src,
			Outcome{OK: true, COM: "COM5", Why: WhyMatched}, Outcome{OK: true, COM: "COM11", Why: WhyMatched}, []string{"COM12"}},
		{"nothing at all", ok, src,
			Outcome{Why: WhySilent}, Outcome{Why: WhySilent}, []string{"COM11", "COM12", "COM5"}},
		{"bytes but no token, CAN bus errors", with(func(r *Reply) { r.RS485Junk = 3; r.CANLec = 2 }), src,
			Outcome{Why: WhyGarbage}, Outcome{Why: WhyGarbage}, []string{"COM11", "COM12", "COM5"}},
		{"LEC 7 is not an error", with(func(r *Reply) { r.CANLec = 7 }), src,
			Outcome{Why: WhySilent}, Outcome{Why: WhySilent}, []string{"COM11", "COM12", "COM5"}},
		{"two adapters on one pair", with(func(r *Reply) { r.RS485 = []int{2, 1} }), src,
			Outcome{Why: WhySeveral, COMs: []string{"COM12", "COM5"}}, Outcome{Why: WhySilent}, []string{"COM11"}},
		// The wording the plan gives: RS485 heard nothing, COM12's token at CAN.
		{"serial token at the CAN terminals", with(func(r *Reply) { r.CAN = []int{2} }), src,
			Outcome{Why: WhySilent}, Outcome{Why: WhyCrossed, COMs: []string{"COM12"}}, []string{"COM11", "COM5"}},
		{"CAN token at the RS485 terminals", with(func(r *Reply) { r.RS485 = []int{3} }), src,
			Outcome{Why: WhyCrossed, COMs: []string{"COM11"}}, Outcome{Why: WhySilent}, []string{"COM12", "COM5"}},
		{"a terminal that did not start", with(func(r *Reply) { r.RS485OK = false; r.CAN = []int{3} }), src,
			Outcome{Why: WhyNotUp}, Outcome{OK: true, COM: "COM11", Why: WhyMatched}, []string{"COM12", "COM5"}},
		{"no adapter of either kind", ok, map[int]Source{},
			Outcome{Why: WhyNoAdapter}, Outcome{Why: WhyNoAdapter}, nil},
		{"a number this run did not hand out", with(func(r *Reply) { r.RS485 = []int{9} }), src,
			Outcome{Why: WhyGarbage}, Outcome{Why: WhySilent}, []string{"COM11", "COM12", "COM5"}},
	}
	for _, c := range cases {
		got, unseen := Decide(c.r, c.src)
		if fmt.Sprint(got[TermRS485]) != fmt.Sprint(c.rs485) {
			t.Errorf("%s: rs485 %+v, want %+v", c.name, got[TermRS485], c.rs485)
		}
		if fmt.Sprint(got[TermCAN]) != fmt.Sprint(c.can) {
			t.Errorf("%s: can %+v, want %+v", c.name, got[TermCAN], c.can)
		}
		if fmt.Sprint(unseen) != fmt.Sprint(c.wantUnseen) {
			t.Errorf("%s: unseen %v, want %v", c.name, unseen, c.wantUnseen)
		}
	}
}

// ---- FindControl ----

// capsPort answers pt.caps the way its script says, or never.
type capsPort struct {
	answer string // what one pt.caps gets back; "" = silence
	mu     sync.Mutex
	in     bytes.Buffer
	out    chan []byte
	once   sync.Once
}

func newCapsPort(answer string) *capsPort {
	return &capsPort{answer: answer, out: make(chan []byte, 8)}
}

func (p *capsPort) Read(b []byte) (int, error) {
	d, ok := <-p.out
	if !ok {
		return 0, io.EOF
	}
	return copy(b, d), nil
}

func (p *capsPort) Write(b []byte) (int, error) {
	p.mu.Lock()
	p.in.Write(b)
	asked := strings.Contains(p.in.String(), "pt.caps\r\n")
	p.mu.Unlock()
	if asked && p.answer != "" {
		p.once.Do(func() { p.out <- []byte(p.answer) })
	}
	return len(b), nil
}

func (p *capsPort) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.out:
	default:
	}
	defer func() { _ = recover() }()
	close(p.out)
	return nil
}

func TestFindControlTakesOnlyTheFixtureAnswer(t *testing.T) {
	ports := map[string]*capsPort{
		"COM3": newCapsPort("\r\nOK porttool=0.18.3 ports=18 lines=55\r\nOK port=din ...\r\n"),
		"COM4": newCapsPort(""),                        // nobody there
		"COM6": newCapsPort("ERR unknown command\r\n"), // something that is not the fixture
	}
	open := func(name string, baud int) (io.ReadWriteCloser, error) {
		if name == "COM8" {
			return nil, errors.New("access denied")
		}
		return ports[name], nil
	}
	found, tried := FindControl([]string{"COM3", "COM4", "COM6", "COM8"}, open, 300*time.Millisecond)
	if len(found) != 1 || found[0].COM != "COM3" || found[0].Firmware != "0.18.3" {
		t.Fatalf("found %+v", found)
	}
	why := map[string]string{}
	for _, tr := range tried {
		why[tr.COM] = tr.Why
	}
	want := map[string]string{"COM3": "answered", "COM4": "no_answer", "COM6": "no_answer", "COM8": "open_failed"}
	if fmt.Sprint(why) != fmt.Sprint(want) {
		t.Errorf("tried %v, want %v", why, want)
	}
	if !strings.Contains(ports["COM4"].in.String(), "pt.caps") {
		t.Error("every candidate is asked")
	}
}

func TestFindControlReportsTwoBoards(t *testing.T) {
	open := func(name string, baud int) (io.ReadWriteCloser, error) {
		return newCapsPort("OK porttool=0.18.3 ports=18 lines=55\r\n"), nil
	}
	found, _ := FindControl([]string{"COM3", "COM7"}, open, 300*time.Millisecond)
	if len(found) != 2 {
		t.Errorf("both answers are reported, so the caller can refuse to pick: %+v", found)
	}
}

// ---- Listen ----

// matchBoard is a board that runs pt.run match against the fake adapters it
// is wired to: what each terminal hears is whatever the adapters on it sent.
type matchBoard struct {
	out    chan []byte
	rs485  []*recAdapter // adapters wired to RS485
	can    []*recAdapter // to CAN
	window time.Duration
	mu     sync.Mutex
	line   []byte
}

func (b *matchBoard) Read(p []byte) (int, error) {
	d, ok := <-b.out
	if !ok {
		return 0, io.EOF
	}
	return copy(p, d), nil
}

func (b *matchBoard) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.line = append(b.line, p...)
	l := string(b.line)
	b.mu.Unlock()
	if strings.Contains(l, "pt.run match") && strings.HasSuffix(l, "\n") {
		b.mu.Lock()
		b.line = nil
		b.mu.Unlock()
		go func() {
			b.out <- []byte("MATCH_TEST: listening on RS485 at 115200 and CAN at 500000 for 3000 ms\r\n")
			time.Sleep(b.window)
			b.out <- []byte(fmt.Sprintf("OK match busy=0 ms=3000 baud=115200 bps=500000 rs485=%s rs485_ok=1 rs485_bytes=0 rs485_junk=0 can=%s can_ok=1 can_frames=0 can_junk=0 can_lec=7\r\n",
				heard(b.rs485), heard(b.can)))
		}()
	} else if strings.HasSuffix(l, "\n") {
		b.mu.Lock()
		b.line = nil
		b.mu.Unlock()
	}
	return len(p), nil
}

func (b *matchBoard) Close() error { return nil }

var tokRe = regexp.MustCompile(`OPLC-(\d+)`)

func heard(as []*recAdapter) string {
	seen := map[string]bool{}
	var out []string
	for _, a := range as {
		for _, m := range tokRe.FindAllStringSubmatch(a.sent(), -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	if len(out) == 0 {
		return "-"
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// recAdapter records what its port was given.
type recAdapter struct {
	inner Adapter
	port  *recPort
}

func (r *recAdapter) sent() string {
	r.port.mu.Lock()
	defer r.port.mu.Unlock()
	return r.port.buf.String()
}

type recPort struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (p *recPort) Read([]byte) (int, error) { select {} }
func (p *recPort) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.buf.Write(b)
	return len(b), nil
}
func (p *recPort) Close() error { return nil }

// recCAN records the frames a CAN adapter sent, in SLCAN text.
type recCAN struct{ port *recPort }

func (c recCAN) Send(f slcan.Frame) error {
	s, err := slcan.Encode(f)
	if err != nil {
		return err
	}
	_, _ = c.port.Write([]byte(s + "\r" + string(f.Data) + "\n"))
	return nil
}
func (c recCAN) Close() error { return nil }

func TestListenSendsEachAdapterItsOwnToken(t *testing.T) {
	RoundEvery, StaggerEvery = 50*time.Millisecond, time.Millisecond

	a := &recAdapter{port: &recPort{}}
	a.inner = &Serial{COM: "COM5", RW: a.port}
	bb := &recAdapter{port: &recPort{}}
	bb.inner = &Serial{COM: "COM12", RW: bb.port}
	c := &recAdapter{port: &recPort{}}
	c.inner = &CAN{COM: "COM11", A: recCAN{c.port}}

	// COM5 on RS485, COM11 on CAN, COM12 on nothing.
	fb := &matchBoard{out: make(chan []byte, 16), rs485: []*recAdapter{a}, can: []*recAdapter{c}, window: 300 * time.Millisecond}
	b := ptboard.New(fb, 0)
	defer b.Close()

	r, err := Listen(b, []Adapter{a.inner, bb.inner, c.inner}, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.sent(), "OPLC-1\r\n") || !strings.Contains(bb.sent(), "OPLC-2\r\n") {
		t.Errorf("serial tokens: COM5 %q, COM12 %q", a.sent(), bb.sent())
	}
	if !strings.Contains(c.sent(), "t5A56") || !strings.Contains(c.sent(), "OPLC-3") {
		t.Errorf("the CAN token is one frame on 0x5A5 carrying OPLC-3: %q", c.sent())
	}
	if strings.Count(a.sent(), "OPLC-1") < 2 {
		t.Errorf("tokens are repeated every round while the board listens: %q", a.sent())
	}
	src := map[int]Source{1: {Name: "COM5"}, 2: {Name: "COM12"}, 3: {Name: "COM11", CAN: true}}
	out, unseen := Decide(r, src)
	if !out[TermRS485].OK || out[TermRS485].COM != "COM5" || !out[TermCAN].OK || out[TermCAN].COM != "COM11" {
		t.Errorf("decided %+v", out)
	}
	if fmt.Sprint(unseen) != "[COM12]" {
		t.Errorf("unseen %v", unseen)
	}
}
