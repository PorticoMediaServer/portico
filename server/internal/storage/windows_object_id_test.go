package storage

import "testing"

func TestObservedWindowsIDKeepsAllFileIDBits(t *testing.T) {
	first, second := [16]byte{1}, [16]byte{1}
	second[15] = 1
	a, _ := observedWindowsID(7, first)
	b, _ := observedWindowsID(7, second)
	if a == b {
		t.Fatal("128-bit file identities collapsed")
	}
	c, _ := observedWindowsID(8, first)
	if a == c {
		t.Fatal("volume identity lost")
	}
}

func TestObservedWindowsIDRejectsZeroFileID(t *testing.T) {
	if id, err := observedWindowsID(7, [16]byte{}); err == nil || id != "" {
		t.Fatal("zero file identity accepted")
	}
}
