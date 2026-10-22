package simboard

// The PC's adapters when the board is the simulated one (PORTTOOL-FLOW.md
// C.3.6, automatic adapter matching): two serial adapters and a CANable that
// exist only in the simulator. A token "sent" on one becomes sim.tok on the
// simulator's input, and the simulator's own wiring (sim.wiring) decides which
// terminal hears it, so no real serial port on this PC is ever written to.

import (
	"errors"
	"io"

	"PortTool/src/ptmatch"
)

// The simulated adapters' names, as sim_main.c knows them.
const (
	MatchSerialA = "SIM-A"
	MatchSerialB = "SIM-B"
	MatchCAN     = "SIM-CAN"
)

// TokenAdapter is one simulated adapter; it has ptmatch.Adapter's methods.
type TokenAdapter struct {
	w    *wired
	name string
}

func (a *TokenAdapter) Name() string { return a.name }

// SendToken hands the simulator what the adapter would put on its wire.
func (a *TokenAdapter) SendToken(n int) error {
	a.w.inject("sim.tok " + a.name + " " + ptmatch.Token(n))
	return nil
}

// Close does nothing: there is no port behind it.
func (a *TokenAdapter) Close() error { return nil }

// MatchAdapters returns the simulated serial adapters and CANable of a
// simulated board opened by OpenPort.
func MatchAdapters(port io.ReadWriteCloser) (serial []*TokenAdapter, can *TokenAdapter, err error) {
	w, ok := port.(*wired)
	if !ok {
		return nil, nil, errors.New("not the simulated board")
	}
	return []*TokenAdapter{{w, MatchSerialA}, {w, MatchSerialB}}, &TokenAdapter{w, MatchCAN}, nil
}
