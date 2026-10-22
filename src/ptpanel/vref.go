package ptpanel

// The AI card's analog reference switch: pt.run vref src=int|ext, the
// measured VREF+ it reports, and the selection kept for this server's
// lifetime and sent again when the board connects (decision 158,
// PORTTOOL-FLOW.md C.3.6 「AI / AO 卡片」 「基准」).

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"PortTool/src/ptboard"
	"PortTool/src/ptproto"
)

// vrefErrKey words the board's error= tokens (A.3 pt.run vref).
var vrefErrKey = map[string]string{
	"src_must_be_int_or_ext": "go.vref.err.src_must_be_int_or_ext",
	"vrefbuf_not_ready":      "go.vref.err.vrefbuf_not_ready",
	"adc_not_ready":          "go.vref.err.adc_not_ready",
}

// vrefReply is what the board said: "OK vref src=ext mv=2493 ok=1 [error=...]".
type vrefReply struct {
	Src string  `json:"src"`
	MV  float64 `json:"mv"`
	OK  bool    `json:"ok"`
	Err string  `json:"-"`
}

func parseVref(lines []string) (vrefReply, bool) {
	for _, l := range lines {
		kind, body := ptproto.Classify(l)
		if kind != ptproto.LineOK || !strings.HasPrefix(body, "vref ") {
			continue
		}
		f := ptproto.Fields(body)
		var r vrefReply
		r.Src, _ = ptproto.Get(f, "src")
		mv, _ := ptproto.Get(f, "mv")
		r.MV, _ = strconv.ParseFloat(mv, 64)
		ok, _ := ptproto.Get(f, "ok")
		r.OK = ok == "1"
		r.Err, _ = ptproto.Get(f, "error")
		return r, true
	}
	return vrefReply{}, false
}

// sendVref sends pt.run vref with src ("" only reports).
func sendVref(b *ptboard.Board, src string) (vrefReply, *msg) {
	cmd := "pt.run vref"
	if src != "" {
		cmd += " src=" + src
	}
	lines, err := b.Send(cmd, ptboard.ExpectOne, ptboard.TimeoutFor(cmd))
	if err != nil {
		return vrefReply{}, ref(m("go.vref.failed", "detail", err.Error()))
	}
	r, ok := parseVref(lines)
	if !ok {
		return vrefReply{}, ref(m("go.vref.failed", "detail", strings.Join(lines, " | ")))
	}
	if r.Err != "" {
		if k, ok := vrefErrKey[r.Err]; ok {
			return r, ref(m(k))
		}
		return r, ref(m("go.vref.err.other", "token", r.Err))
	}
	return r, nil
}

// handleVref: GET reports, POST {"src": "int"|"ext"} switches. The selection
// follows what the board reports after a switch.
func (s *Server) handleVref(w http.ResponseWriter, r *http.Request) {
	src := ""
	if r.Method == http.MethodPost {
		var body struct {
			Src string `json:"src"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || (body.Src != "int" && body.Src != "ext") {
			writeErr(w, 400, m("go.vref.bad_src"))
			return
		}
		src = body.Src
	}
	s.mu.Lock()
	b := s.board
	s.mu.Unlock()
	out := map[string]any{}
	if b == nil {
		if src != "" {
			writeErr(w, 409, m("go.cmd.no_board"))
			return
		}
	} else {
		rep, why := sendVref(b, src)
		if rep.Src != "" {
			out["src"], out["mv"], out["ok"] = rep.Src, rep.MV, rep.OK
			if src != "" {
				s.mu.Lock()
				s.vrefExt = rep.Src == "ext"
				s.mu.Unlock()
			}
		}
		if why != nil {
			out["error"] = why
		}
	}
	s.mu.Lock()
	out["chosen"] = map[bool]string{false: "int", true: "ext"}[s.vrefExt]
	s.mu.Unlock()
	writeJSON(w, 200, out)
}

// resendVref puts a freshly connected board back on the external reference
// when that was chosen: a reset brings it up on the internal one. Called
// before the panel hands the board to anything that starts a session.
func (s *Server) resendVref(b *ptboard.Board) {
	s.mu.Lock()
	ext := s.vrefExt
	s.mu.Unlock()
	if !ext {
		return
	}
	if _, why := sendVref(b, "ext"); why != nil {
		s.say(m("go.vref.resend_failed", "detail", *why))
		return
	}
	s.say(m("go.vref.resent"))
}
