package ptpanel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Templates are named, versioned copies of the fixture configuration; every
// station tests with the one in use, and a board keeps the version it started
// with (PORTTOOL-FLOW.md C.3.10 「模板」). The template window edits the
// configuration file (porttool_stagecfg.json) as its draft.

// TemplatesDir overrides where templates are kept; tests set it.
var TemplatesDir string

var tplMu sync.Mutex

func templatesDir() string {
	if TemplatesDir != "" {
		return TemplatesDir
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "templates")
	}
	return "templates"
}

// tplRef names one version of one template.
type tplRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Ver  int    `json:"ver"`
}

type tplMeta struct {
	Name string `json:"name"`
}

var tplIDRe = regexp.MustCompile(`^t[0-9]+$`)
var tplVerRe = regexp.MustCompile(`^v([0-9]+)\.json$`)

// defaultTplName is the first template's name; the page shows it in its own language.
const defaultTplName = "default"

func tplDir(id string) string { return filepath.Join(templatesDir(), id) }

func tplLatest(id string) int {
	ents, _ := os.ReadDir(tplDir(id))
	n := 0
	for _, e := range ents {
		if mm := tplVerRe.FindStringSubmatch(e.Name()); mm != nil {
			if v, _ := strconv.Atoi(mm[1]); v > n {
				n = v
			}
		}
	}
	return n
}

func tplName(id string) string {
	var m tplMeta
	if b, err := os.ReadFile(filepath.Join(tplDir(id), "meta.json")); err == nil && json.Unmarshal(b, &m) == nil {
		return m.Name
	}
	return id
}

func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// tplList is every template with its latest version, oldest first. The first
// call makes the default one from the configuration as it stands.
func tplList() []tplRef {
	ents, _ := os.ReadDir(templatesDir())
	out := []tplRef{}
	for _, e := range ents {
		if e.IsDir() && tplIDRe.MatchString(e.Name()) {
			if v := tplLatest(e.Name()); v > 0 {
				out = append(out, tplRef{ID: e.Name(), Name: tplName(e.Name()), Ver: v})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := strconv.Atoi(out[i].ID[1:])
		b, _ := strconv.Atoi(out[j].ID[1:])
		return a < b
	})
	if len(out) == 0 {
		if r, err := tplCreate(defaultTplName, loadStageCfgs()); err == nil {
			out = append(out, r)
		}
	}
	return out
}

func tplCreate(name string, cfg stageCfgs) (tplRef, error) {
	ents, _ := os.ReadDir(templatesDir())
	n := 1
	for _, e := range ents {
		if tplIDRe.MatchString(e.Name()) {
			if k, _ := strconv.Atoi(e.Name()[1:]); k >= n {
				n = k + 1
			}
		}
	}
	id := "t" + strconv.Itoa(n)
	if err := writeJSONFile(filepath.Join(tplDir(id), "meta.json"), tplMeta{Name: name}); err != nil {
		return tplRef{}, err
	}
	if err := writeJSONFile(filepath.Join(tplDir(id), "v1.json"), cfg); err != nil {
		return tplRef{}, err
	}
	return tplRef{ID: id, Name: name, Ver: 1}, nil
}

// tplLoad reads one version.
func tplLoad(r tplRef) (stageCfgs, error) {
	var f stageCfgs
	if !tplIDRe.MatchString(r.ID) {
		return f, fmt.Errorf("no template %q", r.ID)
	}
	b, err := os.ReadFile(filepath.Join(tplDir(r.ID), "v"+strconv.Itoa(r.Ver)+".json"))
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, err
	}
	f.Ranges = cfgRanges(f.Ranges)
	return f, nil
}

func tplCurrentPath() string { return filepath.Join(templatesDir(), "current.json") }

// tplCurrent is the template in use at its latest version; the first one when
// none was chosen or the chosen one is gone.
func tplCurrent() tplRef {
	list := tplList()
	var c tplRef
	if b, err := os.ReadFile(tplCurrentPath()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	for _, r := range list {
		if r.ID == c.ID {
			return r
		}
	}
	if len(list) > 0 {
		return list[0]
	}
	return tplRef{}
}

func (st *Stations) handleTemplates(w http.ResponseWriter, r *http.Request) {
	tplMu.Lock()
	defer tplMu.Unlock()
	if r.Method == http.MethodPost {
		var b struct {
			Act  string `json:"act"` // edit, save, new, use
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			writeErr(w, 400, m("go.station.bad_request"))
			return
		}
		var err error
		switch b.Act {
		case "new":
			var nr tplRef
			if nr, err = tplCreate(strings.TrimSpace(b.Name), stageCfgs{}); err == nil {
				b.ID = nr.ID
				err = saveStageCfgs(loadStageCfgsFrom(stageCfgs{}))
			}
		case "edit": // the draft becomes this template's latest version
			var f stageCfgs
			if f, err = tplLoad(tplRef{ID: b.ID, Ver: tplLatest(b.ID)}); err == nil {
				err = saveStageCfgs(f)
			}
		case "save": // the draft becomes the next version, under the name given
			if !tplIDRe.MatchString(b.ID) || tplLatest(b.ID) == 0 {
				err = fmt.Errorf("no template %q", b.ID)
				break
			}
			if name := strings.TrimSpace(b.Name); name != "" {
				err = writeJSONFile(filepath.Join(tplDir(b.ID), "meta.json"), tplMeta{Name: name})
			}
			// An unchanged draft keeps the version: only a change makes a new one.
			if last, lerr := tplLoad(tplRef{ID: b.ID, Ver: tplLatest(b.ID)}); err == nil && (lerr != nil || !sameCfg(last, loadStageCfgs())) {
				err = writeJSONFile(filepath.Join(tplDir(b.ID), "v"+strconv.Itoa(tplLatest(b.ID)+1)+".json"), loadStageCfgs())
			}
		case "use":
			if !tplIDRe.MatchString(b.ID) || tplLatest(b.ID) == 0 {
				err = fmt.Errorf("no template %q", b.ID)
				break
			}
			err = writeJSONFile(tplCurrentPath(), tplRef{ID: b.ID})
		default:
			writeErr(w, 400, m("go.station.bad_request"))
			return
		}
		if err != nil {
			writeJSON(w, 200, map[string]any{"error": m("go.tpl.failed", "detail", err.Error()), "list": tplList(), "using": tplCurrent()})
			return
		}
		writeJSON(w, 200, map[string]any{"list": tplList(), "using": tplCurrent(), "id": b.ID})
		return
	}
	writeJSON(w, 200, map[string]any{"list": tplList(), "using": tplCurrent()})
}

// loadStageCfgsFrom is f with the defaults loadStageCfgs fills in.
func loadStageCfgsFrom(f stageCfgs) stageCfgs {
	f.Ranges = cfgRanges(f.Ranges)
	for st, d := range cfgStages {
		if c := f.of(st); !durationOffered(st, c.Duration) {
			c.Duration = d
		}
	}
	return f
}

func sameCfg(a, b stageCfgs) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
