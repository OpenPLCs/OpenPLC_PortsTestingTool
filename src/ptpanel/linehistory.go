package ptpanel

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"PortTool/src/calstore"
	"PortTool/src/ptrecord"
	"PortTool/src/ptreport"
)

// What the big screen shows beside the tiles (PORTTOOL-FLOW.md C.3.10):
// boards away in the oven and the latest submitted records, both read from
// records/ so they survive a restart.

func recordsRoot() string { return filepath.Dir(ptrecord.BoardDir("_")) }

type ovenBoard struct {
	UID   string    `json:"uid"`
	Since time.Time `json:"since"` // when its aging task was written
}

type submission struct {
	UID     string    `json:"uid"`
	File    string    `json:"file"`
	Step    string    `json:"step"`
	Outcome string    `json:"outcome"`
	Station int       `json:"station,omitempty"`
	At      time.Time `json:"at"`
}

// lineHistory is every board whose ② passed and ③ has not, minus those on a
// tile now, and the newest records across all boards.
func lineHistory(onTiles map[string]bool, max int) ([]ovenBoard, []submission) {
	oven := []ovenBoard{}
	subs := []submission{}
	dirs, _ := os.ReadDir(recordsRoot())
	for _, d := range dirs {
		uid := d.Name()
		if !d.IsDir() {
			continue
		}
		if _, err := calstore.ParseUID(uid); err != nil {
			continue
		}
		passed, recs, _ := loadLine(uid)
		if passed[2] && !passed[3] && !onTiles[uid] {
			b := ovenBoard{UID: uid}
			if st, err := os.Stat(filepath.Join(ptrecord.BoardDir(uid), recs[2])); err == nil && recs[2] != "" {
				b.Since = st.ModTime()
			}
			oven = append(oven, b)
		}
		files, _ := os.ReadDir(ptrecord.BoardDir(uid))
		for _, f := range files {
			n := f.Name()
			if !strings.HasSuffix(n, ".json") || n == "line.json" || n == "progress.json" {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			subs = append(subs, submission{UID: uid, File: n, At: info.ModTime()})
		}
	}
	sort.Slice(subs, func(i, j int) bool { return subs[i].At.After(subs[j].At) })
	if len(subs) > max {
		subs = subs[:max]
	}
	for i := range subs {
		if r, err := readRecord(subs[i].UID, subs[i].File); err == nil && len(r.Steps) > 0 {
			subs[i].Step, subs[i].Outcome, subs[i].Station = r.Steps[0].ID, string(r.Steps[0].Outcome), r.Station
		}
	}
	sort.Slice(oven, func(i, j int) bool { return oven[i].Since.Before(oven[j].Since) })
	return oven, subs
}

func readRecord(uid, file string) (ptreport.Report, error) {
	var r ptreport.Report
	if _, err := calstore.ParseUID(uid); err != nil {
		return r, err
	}
	if file != filepath.Base(file) || !strings.HasSuffix(file, ".json") {
		return r, os.ErrNotExist
	}
	b, err := os.ReadFile(filepath.Join(ptrecord.BoardDir(uid), file))
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal(b, &r)
}

func (st *Stations) handleLineHistory(w http.ResponseWriter, r *http.Request) {
	on := map[string]bool{}
	st.mu.Lock()
	for _, x := range st.list {
		if t := x.line.view(); t.UID != "" {
			on[t.UID] = true
		}
	}
	st.mu.Unlock()
	oven, subs := lineHistory(on, 40)
	writeJSON(w, 200, map[string]any{"oven": oven, "subs": subs, "now": time.Now()})
}

// handleLineRecord is one record's steps and numbered items, for the board page.
func (st *Stations) handleLineRecord(w http.ResponseWriter, r *http.Request) {
	rep, err := readRecord(r.URL.Query().Get("uid"), r.URL.Query().Get("file"))
	if err != nil {
		writeErr(w, 404, m("go.station.bad_request"))
		return
	}
	writeJSON(w, 200, map[string]any{"record": rep})
}
