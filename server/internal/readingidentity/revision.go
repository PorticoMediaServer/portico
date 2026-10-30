package readingidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Revision preserves the existing ordered physical observation fence. Callers
// supply entity, asset, format, token, size, mtime, root id/generation/incarnation,
// root identity, relative path, physical object identity, inventory revision and
// evidence. Inventory row id is deliberately not substituted for file identity.
func Revision(fields ...any) string {
	if len(fields) != 14 {
		return ""
	}
	b, err := json.Marshal(fields)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(append([]byte("portico.reading.observation.v1\x00"), b...))
	return hex.EncodeToString(h[:])
}
