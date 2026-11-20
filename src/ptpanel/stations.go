package ptpanel

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"PortTool/src/portmap"
)

// Stations is the fixture line's panels, one Server per station (decision 190,
// PORTTOOL-FLOW.md C.3.10 「多工位怎么实现」). Station 1 always exists and is
// also served at "/" until the big screen takes that address.
type Stations struct {
	// New builds station n's panel; Serve sets it so every station talks to
	// real ports, tests give fakes.
	New func(n int) *Server

	mu   sync.Mutex
	list map[int]*station
}

type station struct {
	srv *Server
	h   http.Handler
}

// stationRec is one row of stations.json.
type stationRec struct {
	N      int    `json:"n"`
	STLink string `json:"stlink,omitempty"`
}

// StationsFile overrides where the station list is kept; tests set it.
var StationsFile string

func stationsPath() string {
	if StationsFile != "" {
		return StationsFile
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "stations.json")
	}
	return "stations.json"
}

// Load opens every station in stations.json, and station 1 when the file is
// missing or does not name it.
func (st *Stations) Load() {
	var recs []stationRec
	if b, err := os.ReadFile(stationsPath()); err == nil {
		_ = json.Unmarshal(b, &recs)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.list = map[int]*station{}
	for _, r := range recs {
		if r.N >= 1 && st.list[r.N] == nil {
			st.open(r.N, r.STLink)
		}
	}
	if st.list[1] == nil {
		st.open(1, "")
	}
}

func (st *Stations) open(n int, sn string) {
	s := st.New(n)
	s.Ports = portmap.ForStation(n)
	s.STLinkSN = sn
	st.list[n] = &station{srv: s, h: s.Handler()}
}

// save writes the list; a failed write costs only the memory of it.
func (st *Stations) save() {
	recs := make([]stationRec, 0, len(st.list))
	for n, x := range st.list {
		recs = append(recs, stationRec{N: n, STLink: x.srv.STLinkSN})
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].N < recs[j].N })
	b, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(stationsPath(), append(b, '\n'), 0o644)
}

// Close drops every station's board connection.
func (st *Stations) Close() {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, x := range st.list {
		x.srv.Close()
	}
}

// Handler routes /api/stations to the list, /st/<n>/... to station n's panel
// with the prefix stripped, and everything else to station 1.
func (st *Stations) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/stations", st.handleList)
	mux.HandleFunc("/api/stations/add", st.handleAdd)
	mux.HandleFunc("/api/stations/remove", st.handleRemove)
	mux.HandleFunc("/api/stations/stlink", st.handleSTLink)
	mux.HandleFunc("/st/", st.handleStation)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		h := st.list[1].h
		st.mu.Unlock()
		h.ServeHTTP(w, r)
	})
	return mux
}

func (st *Stations) handleStation(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/st/")
	num, tail, _ := strings.Cut(rest, "/")
	n, err := strconv.Atoi(num)
	st.mu.Lock()
	x := st.list[n]
	st.mu.Unlock()
	if err != nil || x == nil {
		http.NotFound(w, r)
		return
	}
	if tail == "" && !strings.HasSuffix(r.URL.Path, "/") {
		// The page's relative js/ and css paths need the trailing slash.
		http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
		return
	}
	http.StripPrefix("/st/"+num, x.h).ServeHTTP(w, r)
}

// stationView is one station as the list reports it.
func stationView(n int, s *Server) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{"n": n, "stlink": s.STLinkSN, "connected": s.board != nil,
		"port": s.portNam, "uid": s.uid}
}

func (st *Stations) handleList(w http.ResponseWriter, r *http.Request) {
	st.mu.Lock()
	ns := make([]int, 0, len(st.list))
	for n := range st.list {
		ns = append(ns, n)
	}
	sort.Ints(ns)
	out := make([]map[string]any, 0, len(ns))
	for _, n := range ns {
		out = append(out, stationView(n, st.list[n].srv))
	}
	st.mu.Unlock()
	writeJSON(w, 200, map[string]any{"stations": out})
}

// handleAdd opens the lowest free station number.
func (st *Stations) handleAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, m("go.station.bad_request"))
		return
	}
	st.mu.Lock()
	n := 1
	for st.list[n] != nil {
		n++
	}
	st.open(n, "")
	st.save()
	st.mu.Unlock()
	writeJSON(w, 200, map[string]any{"n": n})
}

type stationBody struct {
	N  int    `json:"n"`
	SN string `json:"sn"`
}

func readStationBody(w http.ResponseWriter, r *http.Request) (stationBody, bool) {
	var b stationBody
	if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&b) != nil {
		writeErr(w, 400, m("go.station.bad_request"))
		return b, false
	}
	return b, true
}

// handleRemove drops an empty station: not station 1, no board connected.
func (st *Stations) handleRemove(w http.ResponseWriter, r *http.Request) {
	b, ok := readStationBody(w, r)
	if !ok {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	x := st.list[b.N]
	switch {
	case x == nil:
		writeErr(w, 404, m("go.station.none", "n", b.N))
		return
	case b.N == 1:
		writeErr(w, 409, m("go.station.keep_first"))
		return
	}
	x.srv.mu.Lock()
	busy := x.srv.board != nil
	x.srv.mu.Unlock()
	if busy {
		writeErr(w, 409, m("go.station.busy", "n", b.N))
		return
	}
	x.srv.Close()
	delete(st.list, b.N)
	st.save()
	writeJSON(w, 200, map[string]any{"ok": true})
}

// handleSTLink records which ST-Link belongs to a station; "" forgets it.
func (st *Stations) handleSTLink(w http.ResponseWriter, r *http.Request) {
	b, ok := readStationBody(w, r)
	if !ok {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	x := st.list[b.N]
	if x == nil {
		writeErr(w, 404, m("go.station.none", "n", b.N))
		return
	}
	x.srv.STLinkSN = strings.TrimSpace(b.SN)
	st.save()
	writeJSON(w, 200, map[string]any{"ok": true})
}
