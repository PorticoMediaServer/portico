package ownedcapture

import (
	"encoding/hex"
	"testing"
)

// FX-249: the persisted capture id is lower-case hex-64. uniqueID is its only
// producer, so it must satisfy that contract and stay unpredictable without
// changing the shared identity digest, which other purposes use.
func TestUniqueIDMatchesThePersistedCaptureContract(t *testing.T) {
	seen := make(map[string]bool, 128)
	for range 128 {
		id := uniqueID()
		if !captureID.MatchString(id) {
			t.Fatalf("capture id does not match the persisted contract: %q", id)
		}
		if len(id) != 64 {
			t.Fatalf("capture id length = %d, want 64", len(id))
		}
		raw, err := hex.DecodeString(id)
		if err != nil || len(raw) != 32 {
			t.Fatalf("capture id is not 32 bytes of lower-case hex: %q (%v)", id, err)
		}
		if seen[id] {
			t.Fatalf("capture id repeated: %q", id)
		}
		seen[id] = true
	}
}
