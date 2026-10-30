package selectionactions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// TargetIdentity includes the stable public/library/kind/parent tuple. Lifetime
// is checked separately because catalogue added_ms can legitimately be NULL.
func TargetIdentity(publicID, libraryID, kind string, parent any) string {
	raw, _ := json.Marshal([]any{publicID, libraryID, kind, parent})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
