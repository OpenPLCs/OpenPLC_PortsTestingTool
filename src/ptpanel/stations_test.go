package ptpanel

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func newTestStations(t *testing.T) *Stations {
	t.Helper()
	StationsFile = filepath.Join(t.TempDir(), "stations.json")
	t.Cleanup(func() { StationsFile = "" })
	st := &Stations{New: func(int) *Server { return New() }, NoLine: true}
	st.Load()
	t.Cleanup(st.Close)
	return st
}

func stCall(t *testing.T, h http.Handler, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, rd))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func stationNs(t *testing.T, h http.Handler) []int {
	_, out := stCall(t, h, "GET", "/api/stations", nil)
	var ns []int
	for _, x := range out["stations"].([]any) {
		ns = append(ns, int(x.(map[string]any)["n"].(float64)))
	}
	return ns
}

func TestStationsAddRemoveKeep(t *testing.T) {
	st := newTestStations(t)
	h := st.Handler()
	if ns := stationNs(t, h); len(ns) != 1 || ns[0] != 1 {
		t.Fatalf("fresh list = %v, want [1]", ns)
	}
	if _, out := stCall(t, h, "POST", "/api/stations/add", nil); out["n"] != 2.0 {
		t.Fatalf("add gave %v, want n=2", out)
	}
	stCall(t, h, "POST", "/api/stations/add", nil)
	if code, _ := stCall(t, h, "POST", "/api/stations/remove", stationBody{N: 2}); code != 200 {
		t.Fatalf("remove 2: %d", code)
	}
	// The lowest free number is reused.
	if _, out := stCall(t, h, "POST", "/api/stations/add", nil); out["n"] != 2.0 {
		t.Fatalf("re-add gave %v, want n=2", out)
	}
	if code, _ := stCall(t, h, "POST", "/api/stations/remove", stationBody{N: 1}); code != 409 {
		t.Fatalf("removing station 1 gave %d, want 409", code)
	}
	if code, _ := stCall(t, h, "POST", "/api/stations/remove", stationBody{N: 7}); code != 404 {
		t.Fatalf("removing a missing station gave %d, want 404", code)
	}
	if st.list[2].srv.Ports == nil || st.list[1].srv.Ports != nil {
		t.Fatal("station 2 must have its own port map, station 1 the default file")
	}
}

func TestStationsSTLinkPersists(t *testing.T) {
	st := newTestStations(t)
	h := st.Handler()
	stCall(t, h, "POST", "/api/stations/add", nil)
	if code, _ := stCall(t, h, "POST", "/api/stations/set", map[string]any{"n": 2, "sn": " 0670FF48 "}); code != 200 {
		t.Fatalf("stlink: %d", code)
	}
	again := &Stations{New: func(int) *Server { return New() }, NoLine: true}
	again.Load()
	defer again.Close()
	if got := again.list[2]; got == nil || got.srv.STLinkSN != "0670FF48" {
		t.Fatalf("reloaded station 2 = %+v, want ST-Link 0670FF48", got)
	}
}

func TestStationsRouting(t *testing.T) {
	st := newTestStations(t)
	h := st.Handler()
	stCall(t, h, "POST", "/api/stations/add", nil)
	for _, c := range []struct {
		path string
		code int
	}{
		{"/st/2/", 200},
		{"/st/2/api/uid", 200},
		{"/st/2", 301},
		{"/st/9/", 404},
		{"/st/x/", 404},
		{"/", 200},
	} {
		if code, _ := stCall(t, h, "GET", c.path, nil); code != c.code {
			t.Errorf("GET %s = %d, want %d", c.path, code, c.code)
		}
	}
}
