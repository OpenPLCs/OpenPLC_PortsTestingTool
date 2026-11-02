package ptpanel

import (
	"errors"
	"io"
	"path/filepath"
	"sync"
	"testing"

	"PortTool/src/portmap"
)

// exclusivePorts opens each name at most once at a time, as Windows does.
type exclusivePorts struct {
	mu   sync.Mutex
	open map[string]bool
}

type exclusivePort struct {
	*fakePort
	ports *exclusivePorts
	name  string
}

func (p *exclusivePort) Close() error {
	p.ports.mu.Lock()
	delete(p.ports.open, p.name)
	p.ports.mu.Unlock()
	return p.fakePort.Close()
}

func (e *exclusivePorts) Open(name string, _ int) (io.ReadWriteCloser, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.open[name] {
		return nil, errors.New("Serial port busy")
	}
	e.open[name] = true
	return &exclusivePort{fakePort: newFakePort(), ports: e, name: name}, nil
}

// A run's peer on an adapter bound in the connection block borrows it and
// binds it again when done (PORTTOOL-FLOW.md C.3.6 「连接区」).
func TestRunPeerBorrowsABoundLink(t *testing.T) {
	portmap.File = filepath.Join(t.TempDir(), "ports.json")
	defer func() { portmap.File = "" }()
	ports := &exclusivePorts{open: map[string]bool{}}
	s := &Server{Open: ports.Open}
	if _, err := s.bindSerialPeer("usb", "COM11", 0); err != nil {
		t.Fatal(err)
	}
	defer s.unbindAllLinks()

	rw, err := s.openRunPeer("serial", "COM11", 115200)
	if err != nil {
		t.Fatalf("the run could not open the bound adapter: %v", err)
	}
	if s.linkOn("COM11") != nil {
		t.Error("the link still holds COM11 while the run has it")
	}
	_ = rw.Close()
	if l := s.linkOn("COM11"); l == nil || l.Board != "usb" {
		t.Error("COM11 is not bound to usb again after the run closed its peer")
	}
}
