package ptpanel

import (
	"errors"
	"io"
	"path/filepath"
	"testing"

	"PortTool/src/portmap"
)

// SD transfer over USB opens the port chosen on the left only when asked, and
// waits for it: the board's USB port appears after pt.start usb (PORTTOOL-FLOW.md
// B.11, decision 168).
func TestSDOverUSBOpensTheChosenPortOnceItAppears(t *testing.T) {
	portmap.File = filepath.Join(t.TempDir(), "ports.json")
	defer func() { portmap.File = "" }()
	portmap.SetPeer("usb", "COM42", 115200)

	tries := 0
	s := &Server{Open: func(name string, _ int) (io.ReadWriteCloser, error) {
		if name != "COM42" {
			t.Fatalf("opened %s, want the chosen COM42", name)
		}
		tries++
		if tries < 3 {
			return nil, errors.New("The system cannot find the file specified")
		}
		return newFakePort(), nil
	}}
	open, why, ok := s.sdfOpener("usb")
	if !ok {
		t.Fatalf("refused before the session started: %v", why)
	}
	if tries != 0 {
		t.Fatal("the port was opened before the session started")
	}
	rw, err := open()
	if err != nil {
		t.Fatalf("the port that appeared late was not opened: %v", err)
	}
	_ = rw.Close()
}
