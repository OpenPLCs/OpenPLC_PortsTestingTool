package ptpanel

import (
	"net/http/httptest"
	"testing"
)

// The reference chosen on the AI card is the server's: a board that
// connects again (here a new simulated board, which starts on the internal
// reference like a reset one) is switched back to external (decision 158).
func TestVrefChoiceIsSentAgainOnConnect(t *testing.T) {
	needSim(t)
	s := New()
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	defer s.Close()

	connect := func() {
		if st := call(t, srv, "/api/connect", map[string]any{"port": "sim"}); st["connected"] != true {
			t.Fatalf("connect: %v", st)
		}
	}
	connect()
	if v := call(t, srv, "/api/analog/vref", nil); v["src"] != "int" || v["chosen"] != "int" {
		t.Fatalf("a fresh board reports %v, want the internal reference", v)
	}
	if v := call(t, srv, "/api/analog/vref", map[string]any{"src": "ext"}); v["src"] != "ext" || v["chosen"] != "ext" || v["ok"] != true || v["error"] != nil {
		t.Fatalf("switch to external: %v", v)
	}
	call(t, srv, "/api/disconnect", map[string]any{})
	connect()
	if v := call(t, srv, "/api/analog/vref", nil); v["src"] != "ext" {
		t.Fatalf("after connecting again the board reports %v, want external sent again", v)
	}
	if v := call(t, srv, "/api/analog/vref", map[string]any{"src": "int"}); v["src"] != "int" || v["chosen"] != "int" {
		t.Fatalf("switch back to internal: %v", v)
	}
	if v := call(t, srv, "/api/analog/vref", map[string]any{"src": "x"}); v["error"] == nil {
		t.Fatalf("a bad src is refused: %v", v)
	}
}
