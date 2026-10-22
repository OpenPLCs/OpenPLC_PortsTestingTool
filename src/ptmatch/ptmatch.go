// Package ptmatch tells which of this PC's adapters is wired to which board
// terminal (decision 146): the control port is found by asking each candidate
// for pt.caps; the RS485 and CAN adapters each send their own token while the
// board's pt.run match listens, and the board says what each terminal heard.
// $PROD/docs/modules/M4/PORTTOOL-FLOW.md A.3 (match) and C.3.6 (automatic
// adapter matching).
package ptmatch

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"PortTool/src/ptboard"
	"PortTool/src/ptproto"
	"PortTool/src/serialx"
	"PortTool/src/slcan"
)

// Skip says why a port is never written to; "" means it is a candidate.
type Skip string

const (
	SkipNone      Skip = ""
	SkipBluetooth Skip = "bluetooth"
	SkipSTLink    Skip = "stlink"
	SkipBoardUSB  Skip = "board_usb"
	SkipCANable   Skip = "canable"
)

// The ST USB vendor id: ST-Link virtual COM ports and the board's own CDC port.
const stVID = "0483"

// The board's own USB CDC port (boards.txt and the fixture alike).
const boardPID = "5740"

// bluetoothRe matches what Windows (in any of the panel's languages), Linux
// and macOS call a Bluetooth serial port; the system gives no better handle.
var bluetoothRe = regexp.MustCompile(`(?i)bluetooth|蓝牙|藍牙|rfcomm`)

// Classify says whether a port is a candidate adapter.
func Classify(p serialx.PortInfo) Skip {
	switch {
	case bluetoothRe.MatchString(p.Name + " " + p.Product):
		return SkipBluetooth
	case p.IsUSB && strings.EqualFold(p.VID, slcan.VID) && strings.EqualFold(p.PID, slcan.PID):
		return SkipCANable
	case p.IsUSB && strings.EqualFold(p.VID, stVID) && strings.EqualFold(p.PID, boardPID):
		return SkipBoardUSB
	case p.IsUSB && strings.EqualFold(p.VID, stVID):
		return SkipSTLink
	}
	return SkipNone
}

// TokenPrefix starts every token; the number after it is the adapter's.
const TokenPrefix = "OPLC-"

// TokenCANID is the identifier the CAN token frame goes out on; the firmware
// takes any.
const TokenCANID = 0x5A5

// Token is adapter n's token.
func Token(n int) string { return TokenPrefix + strconv.Itoa(n) }

// Adapter is one of this PC's ends that can send a token.
type Adapter interface {
	Name() string
	SendToken(n int) error
	Close() error
}

// Serial is a serial adapter: the token goes out as one line.
type Serial struct {
	COM string
	RW  io.ReadWriteCloser
}

func (s *Serial) Name() string { return s.COM }

func (s *Serial) SendToken(n int) error {
	_, err := io.WriteString(s.RW, Token(n)+"\r\n")
	return err
}

func (s *Serial) Close() error { return s.RW.Close() }

// CANSender is what a CAN adapter needs: slcan.Adapter has it.
type CANSender interface {
	Send(slcan.Frame) error
	Close() error
}

// CAN is a CAN adapter: the token is one frame's data.
type CAN struct {
	COM string
	A   CANSender
}

func (c *CAN) Name() string { return c.COM }

func (c *CAN) SendToken(n int) error {
	return c.A.Send(slcan.Frame{ID: TokenCANID, Data: []byte(Token(n))})
}

func (c *CAN) Close() error { return c.A.Close() }

// Reply is the board's OK match line.
type Reply struct {
	Busy       bool
	RS485      []int
	RS485OK    bool
	RS485Bytes int
	RS485Junk  int
	CAN        []int
	CANOK      bool
	CANFrames  int
	CANJunk    int
	CANLec     int
}

// ParseReply reads the OK match line out of a reply.
func ParseReply(lines []string) (Reply, error) {
	for _, l := range lines {
		kind, body := ptproto.Classify(l)
		if kind != ptproto.LineOK || !strings.HasPrefix(body, "match ") {
			continue
		}
		f := ptproto.Fields(body)
		get := func(k string) string { v, _ := ptproto.Get(f, k); return v }
		num := func(k string) int { n, _ := strconv.Atoi(get(k)); return n }
		r := Reply{Busy: get("busy") == "1"}
		if r.Busy {
			return r, nil
		}
		if get("busy") != "0" {
			return Reply{}, fmt.Errorf("no busy= in %q", l)
		}
		var err error
		if r.RS485, err = tokens(get("rs485")); err != nil {
			return Reply{}, err
		}
		if r.CAN, err = tokens(get("can")); err != nil {
			return Reply{}, err
		}
		r.RS485OK, r.CANOK = get("rs485_ok") == "1", get("can_ok") == "1"
		r.RS485Bytes, r.RS485Junk = num("rs485_bytes"), num("rs485_junk")
		r.CANFrames, r.CANJunk, r.CANLec = num("can_frames"), num("can_junk"), num("can_lec")
		return r, nil
	}
	return Reply{}, fmt.Errorf("no OK match line in %q", lines)
}

func tokens(v string) ([]int, error) {
	if v == "" || v == "-" {
		return nil, nil
	}
	var out []int
	for _, s := range strings.Split(v, ",") {
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, fmt.Errorf("token list %q: %w", v, err)
		}
		out = append(out, n)
	}
	return out, nil
}

// The two terminals pt.run match listens on.
const (
	TermRS485 = "rs485"
	TermCAN   = "can"
)

// Source is the adapter behind a token number.
type Source struct {
	Name string
	CAN  bool
}

// Why a terminal was not matched; each is one sentence on the panel.
const (
	WhyMatched   = "matched"
	WhySilent    = "silent"     // nothing arrived at all
	WhyGarbage   = "garbage"    // something arrived, not a token
	WhySeveral   = "several"    // tokens of two or more adapters of the right kind
	WhyCrossed   = "crossed"    // a token of the other kind of adapter
	WhyNotUp     = "not_up"     // the board could not open the terminal
	WhyNoAdapter = "no_adapter" // this PC has no adapter of that kind to try
)

// Outcome is one terminal's result.
type Outcome struct {
	OK   bool
	COM  string   // the adapter, when OK
	Why  string   // a Why* code
	COMs []string // the adapters a Why is about (several, crossed)
}

// Decide turns the board's reply into one outcome per terminal, and lists the
// adapters whose token reached neither terminal.
func Decide(r Reply, src map[int]Source) (map[string]Outcome, []string) {
	haveSerial, haveCAN := false, false
	for _, s := range src {
		if s.CAN {
			haveCAN = true
		} else {
			haveSerial = true
		}
	}
	seen := map[int]bool{}
	judge := func(heard []int, ok bool, wantCAN bool, have bool, junk int, errSeen bool) Outcome {
		var right, wrong []string
		for _, n := range heard {
			s, known := src[n]
			if !known {
				junk++ // not a number this run handed out
				continue
			}
			seen[n] = true
			if s.CAN == wantCAN {
				right = append(right, s.Name)
			} else {
				wrong = append(wrong, s.Name)
			}
		}
		switch {
		case !ok:
			return Outcome{Why: WhyNotUp}
		case len(wrong) > 0:
			return Outcome{Why: WhyCrossed, COMs: wrong}
		case len(right) == 1:
			return Outcome{OK: true, COM: right[0], Why: WhyMatched}
		case len(right) > 1:
			return Outcome{Why: WhySeveral, COMs: right}
		case !have:
			return Outcome{Why: WhyNoAdapter}
		case junk > 0 || errSeen:
			return Outcome{Why: WhyGarbage}
		}
		return Outcome{Why: WhySilent}
	}
	// PSR.LEC 1-6 are bus errors; 0 is none and 7 "nothing since the last read".
	canErr := r.CANLec >= 1 && r.CANLec <= 6
	out := map[string]Outcome{
		TermRS485: judge(r.RS485, r.RS485OK, false, haveSerial, r.RS485Junk, false),
		TermCAN:   judge(r.CAN, r.CANOK, true, haveCAN, r.CANJunk, canErr),
	}
	var unseen []string
	for n, s := range src {
		if !seen[n] {
			unseen = append(unseen, s.Name)
		}
	}
	sort.Strings(unseen)
	return out, unseen
}

// The firmware's prose line that opens the listening window.
const listeningPrefix = "MATCH_TEST: listening"

// Timing of the token rounds (PORTTOOL-FLOW.md C.3.6): a round every 300 ms,
// the serial adapters 15 ms apart so two on one RS485 pair do not collide.
var (
	RoundEvery   = 300 * time.Millisecond
	StaggerEvery = 15 * time.Millisecond
	// StartWait bounds the wait for the listening line; an older or quieter
	// firmware still gets its tokens.
	StartWait = time.Second
)

// Listen runs pt.run match for ms on the board while every adapter sends its
// token, numbered from 1 in the order given, and returns the parsed reply.
// It does not close the adapters.
func Listen(b *ptboard.Board, adapters []Adapter, ms int) (Reply, error) {
	events, unsubscribe := b.Subscribe(64)
	defer unsubscribe()

	type result struct {
		lines []string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		lines, err := b.Send(fmt.Sprintf("pt.run match ms=%d", ms), ptboard.ExpectOne,
			time.Duration(ms)*time.Millisecond+5*time.Second)
		done <- result{lines, err}
	}()

	start := time.NewTimer(StartWait)
	defer start.Stop()
	var res *result
wait:
	for {
		select {
		case ev, ok := <-events:
			if ok && strings.HasPrefix(ev.Line, listeningPrefix) {
				break wait
			}
		case <-start.C:
			break wait
		case r := <-done:
			res = &r
			break wait
		}
	}

	for res == nil {
		sendRound(adapters)
		select {
		case r := <-done:
			res = &r
		case <-time.After(RoundEvery):
		}
	}
	if res.err != nil {
		return Reply{}, res.err
	}
	return ParseReply(res.lines)
}

func sendRound(adapters []Adapter) {
	for i, a := range adapters {
		_ = a.SendToken(i + 1) // an adapter that will not take it is simply not heard
		if i+1 < len(adapters) {
			time.Sleep(StaggerEvery)
		}
	}
}

// Answer is a port that answered pt.caps with the fixture protocol.
type Answer struct {
	COM      string
	Firmware string
}

// Tried is what one candidate did when asked.
type Tried struct {
	COM    string
	Why    string // "answered", "no_answer", "open_failed"
	Detail string
}

// FindControl asks every candidate for pt.caps at once and reports the ones
// that answered as the fixture firmware does, within wait.
func FindControl(cands []string, open func(name string, baud int) (io.ReadWriteCloser, error), wait time.Duration) ([]Answer, []Tried) {
	tried := make([]Tried, len(cands))
	var wg sync.WaitGroup
	for i, com := range cands {
		wg.Add(1)
		go func(i int, com string) {
			defer wg.Done()
			tried[i] = askCaps(com, open, wait)
		}(i, com)
	}
	wg.Wait()
	var found []Answer
	for _, t := range tried {
		if t.Why == "answered" {
			found = append(found, Answer{COM: t.COM, Firmware: t.Detail})
		}
	}
	return found, tried
}

func askCaps(com string, open func(string, int) (io.ReadWriteCloser, error), wait time.Duration) Tried {
	type opened struct {
		rw  io.ReadWriteCloser
		err error
	}
	oc := make(chan opened, 1)
	go func() {
		rw, err := open(com, serialx.DefaultBaud)
		oc <- opened{rw, err}
	}()
	deadline := time.NewTimer(wait)
	defer deadline.Stop()

	var rw io.ReadWriteCloser
	select {
	case o := <-oc:
		if o.err != nil {
			return Tried{COM: com, Why: "open_failed", Detail: o.err.Error()}
		}
		rw = o.rw
	case <-deadline.C:
		// A port that takes this long to open is not the one; close it if it ever does.
		go func() {
			if o := <-oc; o.rw != nil {
				_ = o.rw.Close()
			}
		}()
		return Tried{COM: com, Why: "no_answer"}
	}

	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 256)
		var line []byte
		for {
			n, err := rw.Read(buf)
			for _, c := range buf[:n] {
				if c != '\n' {
					line = append(line, c)
					continue
				}
				l := strings.TrimRight(string(line), "\r")
				line = line[:0]
				if v, ok := strings.CutPrefix(l, "OK porttool="); ok {
					got <- strings.Fields(v + " ")[0]
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	// The leading line ending clears anything half-typed on the board's line.
	_, werr := io.WriteString(rw, "\r\npt.caps\r\n")
	defer rw.Close() // also ends the reader above
	if werr != nil {
		return Tried{COM: com, Why: "open_failed", Detail: werr.Error()}
	}
	select {
	case v := <-got:
		return Tried{COM: com, Why: "answered", Detail: v}
	case <-deadline.C:
		return Tried{COM: com, Why: "no_answer"}
	}
}
