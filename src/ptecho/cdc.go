package ptecho

import (
	"fmt"
	"strconv"
	"strings"

	"PortTool/src/serialx"
)

// The board's CDC pipe enumerates as ST's STM32 Virtual ComPort, the same
// VID:PID station 6's plan checks. ⚠️ The PID matters: the ST-Link's own
// virtual COM port is 0483:3754, and echoing into it looks like a dead pipe.
const (
	cdcVID = "0483"
	cdcPID = "5740"
)

// FindCDC names the board's CDC port, or says why it cannot.
func FindCDC() (string, error) {
	return serialx.FindUSB(cdcVID, cdcPID,
		fmt.Errorf("no %s:%s port found - is the usb session started "+
			"and the cable in? The port only appears once pt.start usb runs, "+
			"because that is when the board initialises its USB stack",
			cdcVID, cdcPID),
		// Two boards on one PC. Naming both and refusing beats picking one.
		func(names string) error {
			return fmt.Errorf("more than one %s:%s port (%s) - name the one you "+
				"mean with --com", cdcVID, cdcPID, names)
		})
}

// USBSerial is the USB serial number a board with this pt.id UID enumerates
// with: word0+word2 as 8 hex digits, then the top 16 bits of word1. Same in the
// bootloader, the fixture and the board package (usbd_desc.c Get_SerialNum).
func USBSerial(uid string) (string, bool) {
	if len(uid) != 24 {
		return "", false
	}
	var w [3]uint32
	for i := range w {
		v, err := strconv.ParseUint(uid[i*8:i*8+8], 16, 32)
		if err != nil {
			return "", false
		}
		w[i] = uint32(v)
	}
	s0 := w[0] + w[2]
	if s0 == 0 {
		return "", false
	}
	return fmt.Sprintf("%08X%04X", s0, w[1]>>16), true
}

// FindCDCFor names the CDC port of the board with this UID, so one PC can
// carry several boards (decision 190). An unknown UID falls back to FindCDC.
func FindCDCFor(uid string) (string, error) {
	want, ok := USBSerial(uid)
	if !ok {
		return FindCDC()
	}
	ports, err := serialx.List()
	if err != nil {
		return "", err
	}
	for _, p := range ports {
		if p.IsUSB && strings.EqualFold(p.VID, cdcVID) && strings.EqualFold(p.PID, cdcPID) &&
			strings.EqualFold(p.Serial, want) {
			return p.Name, nil
		}
	}
	return "", fmt.Errorf("no %s:%s port with serial %s (board UID %s) - is the usb "+
		"session started and the cable in?", cdcVID, cdcPID, want, uid)
}
