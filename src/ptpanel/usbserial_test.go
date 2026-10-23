package ptpanel

import "testing"

// The board's USB serial from its pt.id UID (mirror 22, decision 163): words
// 0x00340041, 0x31355114, 0x39303538 give 0x00340041 + 0x39303538 and 0x3135.
func TestBoardUSBSerial(t *testing.T) {
	for uid, want := range map[string]string{
		"003400413135511439303538": "396435793135",
		"FFFFFFFF0000FFFF00000002": "000000010000", // the sum wraps at 32 bits
		"0034004131355114393035":   "",             // too short
		"00340041313551143930353G": "",             // not hex
		"":                         "",
	} {
		if got := boardUSBSerial(uid); got != want {
			t.Errorf("boardUSBSerial(%q) = %q, want %q", uid, got, want)
		}
	}
}
