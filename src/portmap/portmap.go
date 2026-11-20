// Package portmap remembers which COM port is which between runs.
//
// VID/PID cannot tell the adapters apart (a CH340 RS485 adapter and a CANable2
// look alike), so a person tells the panel once and both the panel and
// `porttool run` read the answer back.
//
// Its own small file beside the executable, porttool_ports.json.
package portmap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"PortTool/src/simboard"
)

type portMap struct {
	Control string             `json:"control"`
	Peers   map[string]peerRef `json:"peers"`
}

type peerRef struct {
	COM  string `json:"com"`
	Baud int    `json:"baud,omitempty"`
}

// File overrides where the mapping is kept; empty means beside the executable.
// Tests set it, so a test run never rewrites a bench's real mapping.
var File string

var mu sync.Mutex

// Map is one station's mapping (decision 190: each fixture station keeps its
// own). A nil *Map is the default file, the one the package functions use.
type Map struct {
	Path string
}

// ForStation is station n's mapping: station 1 keeps porttool_ports.json,
// station n > 1 has stations/<n>/porttool_ports.json beside the executable.
func ForStation(n int) *Map {
	if n <= 1 {
		return nil
	}
	return &Map{Path: filepath.Join(exeDir(), "stations", strconv.Itoa(n), "porttool_ports.json")}
}

func exeDir() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	return "."
}

func (mp *Map) path() string {
	if mp != nil && mp.Path != "" {
		return mp.Path
	}
	if File != "" {
		return File
	}
	return filepath.Join(exeDir(), "porttool_ports.json")
}

// load never fails in a way the caller has to handle: a missing or unreadable
// file means nothing is remembered yet, which is the first-run state.
func (mp *Map) load() portMap {
	mu.Lock()
	defer mu.Unlock()
	var m portMap
	b, err := os.ReadFile(mp.path())
	if err != nil || json.Unmarshal(b, &m) != nil {
		return portMap{Peers: map[string]peerRef{}}
	}
	if m.Peers == nil {
		m.Peers = map[string]peerRef{}
	}
	return m
}

func (mp *Map) save(m portMap) {
	mu.Lock()
	defer mu.Unlock()
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	// A write failure costs the convenience of being remembered and nothing
	// else; refusing to run over a read-only preferences file would be worse.
	_ = os.MkdirAll(filepath.Dir(mp.path()), 0o755)
	_ = os.WriteFile(mp.path(), append(b, '\n'), 0o644)
}

// SetControl records the control port after a connect succeeded. The
// simulated board is never remembered, or a test run against sim would
// overwrite a bench's mapping.
func (mp *Map) SetControl(com string) {
	if simboard.IsSim(com) {
		return
	}
	m := mp.load()
	if m.Control == com {
		return
	}
	m.Control = com
	mp.save(m)
}

// SetPeer records a link peer once it is bound, keyed by the board port it
// answers for: the question a person answers is "which adapter is on RS485".
func (mp *Map) SetPeer(boardPort, com string, baud int) {
	m := mp.load()
	if cur, ok := m.Peers[boardPort]; ok && cur.COM == com && cur.Baud == baud {
		return
	}
	m.Peers[boardPort] = peerRef{COM: com, Baud: baud}
	mp.save(m)
}

// Peer is the adapter recorded for a board port, for a plan step whose peer is
// "serial" - in the panel and in `porttool run` alike.
func (mp *Map) Peer(boardPort string) (com string, baud int, ok bool) {
	p, ok := mp.load().Peers[boardPort]
	if !ok || p.COM == "" {
		return "", 0, false
	}
	return p.COM, p.Baud, true
}

// ForgetPeer drops a binding when it is unbound, so an adapter somebody
// deliberately took away is not offered again.
func (mp *Map) ForgetPeer(boardPort string) {
	m := mp.load()
	if _, ok := m.Peers[boardPort]; !ok {
		return
	}
	delete(m.Peers, boardPort)
	mp.save(m)
}

// Saved is what the page reads to prefill its choices.
func (mp *Map) Saved() map[string]any {
	m := mp.load()
	peers := map[string]any{}
	for k, v := range m.Peers {
		peers[k] = map[string]any{"com": v.COM, "baud": v.Baud}
	}
	return map[string]any{"control": m.Control, "peers": peers}
}

// The package functions are the default file's, for `porttool run` and the
// panel's station 1.
func SetControl(com string)                                 { (*Map)(nil).SetControl(com) }
func SetPeer(boardPort, com string, baud int)               { (*Map)(nil).SetPeer(boardPort, com, baud) }
func Peer(boardPort string) (com string, baud int, ok bool) { return (*Map)(nil).Peer(boardPort) }
func ForgetPeer(boardPort string)                           { (*Map)(nil).ForgetPeer(boardPort) }
func Saved() map[string]any                                 { return (*Map)(nil).Saved() }
