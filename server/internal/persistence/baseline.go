package persistence

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
)

//go:embed migrations/0001_baseline.sql
var baselineSQL string

// BaselineSQL is the single schema declaration used by fresh installs and fixtures.
func BaselineSQL() string { return baselineSQL }

// BaselineDigest identifies the exact pre-release schema this binary installs.
func BaselineDigest() string {
	digest := sha256.Sum256([]byte(baselineSQL))
	return hex.EncodeToString(digest[:])
}
