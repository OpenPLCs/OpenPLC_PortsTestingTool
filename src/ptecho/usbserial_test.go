package ptecho

import "testing"

func TestUSBSerial(t *testing.T) {
	// word0 0x00390039 + word2 0x39393938 = 0x39723971; word1 0x3133510F -> 3133.
	got, ok := USBSerial("003900393133510F39393938")
	if !ok || got != "397239713133" {
		t.Fatalf("got %q %v", got, ok)
	}
	if _, ok := USBSerial("xyz"); ok {
		t.Fatal("short uid accepted")
	}
}
