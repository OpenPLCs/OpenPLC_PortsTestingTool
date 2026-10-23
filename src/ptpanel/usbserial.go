package ptpanel

// The board's own USB port, told apart by its USB serial number (decision 163,
// PORTTOOL-FLOW.md C.3.6 「连接区」「板子 USB」).

import (
	"fmt"
	"strconv"
	"strings"

	"PortTool/src/serialx"
	"PortTool/src/simboard"
)

// boardUSBSerial is the USB serial number a board enumerates with, from its
// UID as pt.id reports it (words 0, 1, 2 as %08X each): word0 + word2 as 8 hex
// digits, then the top 16 bits of word1. Mirror 22 in
// $PROD/docs/repo/ARCHITECTURE.md (Get_SerialNum() in usbd_desc.c). "" when uid
// is not that shape.
func boardUSBSerial(uid string) string {
	if len(uid) != 24 {
		return ""
	}
	var w [3]uint64
	for i := range w {
		x, err := strconv.ParseUint(uid[i*8:i*8+8], 16, 32)
		if err != nil {
			return ""
		}
		w[i] = x
	}
	return fmt.Sprintf("%08X%04X", uint32(w[0]+w[2]), w[1]>>16)
}

// uidTail is how a board is named on screen: the last 6 digits of its UID.
func uidTail(uid string) string {
	if len(uid) > 6 {
		return uid[len(uid)-6:]
	}
	return uid
}

// connUIDs are the UIDs of the board under test and the reference board, ""
// for one not connected or not answering. The board's is asked once per
// connection and kept in s.uid, as /api/uid does.
func (s *Server) connUIDs() (board, ref string) {
	s.mu.Lock()
	b, uid := s.board, s.uid
	s.mu.Unlock()
	s.conn.mu.Lock()
	ref = s.conn.ref.uid
	asked := s.conn.uidAsked == b
	s.conn.mu.Unlock()
	if b == nil || uid != "" || asked {
		return uid, ref
	}
	s.conn.mu.Lock()
	s.conn.uidAsked = b
	s.conn.mu.Unlock()
	if u, err := uidOf(b); err == nil {
		s.mu.Lock()
		if s.board == b {
			s.uid = u
		}
		s.mu.Unlock()
		uid = u
	}
	return uid, ref
}

// uidNow is the board under test's UID, "" when unknown.
func (s *Server) uidNow() string {
	board, _ := s.connUIDs()
	return board
}

// usbOwnerBad refuses a COM port whose USB serial is the other board's:
// other is that board's UID. A port with no serial, or the simulated board,
// stays selectable.
func usbOwnerBad(com, other string) *msg {
	want := boardUSBSerial(other)
	if want == "" || simboard.IsSim(com) {
		return nil
	}
	ports, err := serialx.List()
	if err != nil {
		return nil
	}
	for _, p := range ports {
		if strings.EqualFold(p.Name, com) && p.Serial != "" && strings.EqualFold(p.Serial, want) {
			return ref(m("go.link.usb_other_board", "com", com, "uid", uidTail(other)))
		}
	}
	return nil
}

// usbJSON is what the connection area needs to group its rows and pick the
// board's own USB port.
func (s *Server) usbJSON() map[string]any {
	board, ref := s.connUIDs()
	return map[string]any{
		"board_uid": board, "board_serial": boardUSBSerial(board),
		"ref_uid": ref, "ref_serial": boardUSBSerial(ref),
	}
}
