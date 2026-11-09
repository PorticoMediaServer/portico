// Package bookseries defines the canonical accepted local book-series cohort
// bytes shared by the IA writer projection and the read-side verification.
// It is pure data: no SQL, connection, registration or disclosure authority.
// A cohort is one complete ordered set of exact name/position pairs for one
// book under one accepted source head.
package bookseries

import (
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"unicode/utf8"
)

const (
	// MaxPairs bounds one complete cohort. The audiobook accept carries at most
	// one local pair today; the projection still stores a complete ordered set
	// rather than silently truncating a longer accepted cohort.
	MaxPairs = 16
	// MaxNameRunes and MaxPositionRunes match the writer's title policy. The
	// exact bytes are retained: no trimming, folding or normalization.
	MaxNameRunes     = 1000
	MaxPositionRunes = 1000
)

// cohortDomain separates this canonical encoding from every other digest.
const cohortDomain = "portico.book_series.cohort.v1\x00"

// Pair is one exact series name and its exact position bytes. Position may be
// empty; a name may not.
type Pair struct {
	Name     string
	Position string
}

// Error is a closed validation refusal. Field is "pairs", "name", "position"
// or "duplicate"; Limit is non-zero only for a length overflow.
type Error struct {
	Field string
	Limit int
}

func (e *Error) Error() string { return "book series " + e.Field + " is invalid" }

func invalid(field string) error { return &Error{Field: field} }

func capError(field string, limit int) error { return &Error{Field: field, Limit: limit} }

// Validate proves the complete cohort before any write or disclosure: bounded
// pair count, exact UTF-8 text without NUL, a non-blank name and no repeated
// name. It never rewrites the retained bytes.
func Validate(pairs []Pair) error {
	if len(pairs) > MaxPairs {
		return capError("pairs", MaxPairs)
	}
	seen := make(map[string]bool, len(pairs))
	for _, pair := range pairs {
		if err := validateText("name", pair.Name, MaxNameRunes, true); err != nil {
			return err
		}
		if err := validateText("position", pair.Position, MaxPositionRunes, false); err != nil {
			return err
		}
		if seen[pair.Name] {
			return invalid("duplicate")
		}
		seen[pair.Name] = true
	}
	return nil
}

func validateText(field, value string, max int, required bool) error {
	if required && strings.TrimSpace(value) == "" {
		return invalid(field)
	}
	if !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return invalid(field)
	}
	if utf8.RuneCountInString(value) > max {
		return capError(field, max)
	}
	return nil
}

// Cohort returns the exact member count, retained payload bytes and canonical
// digest of the complete ordered cohort. The digest covers the domain prefix
// and every pair with its ordinal and both byte lengths, so reordering,
// truncation or a changed position is a different digest.
func Cohort(pairs []Pair) (count, retained int64, digest [32]byte, err error) {
	if err = Validate(pairs); err != nil {
		return 0, 0, digest, err
	}
	h := sha256.New()
	h.Write([]byte(cohortDomain))
	var word [8]byte
	for i, pair := range pairs {
		binary.BigEndian.PutUint64(word[:], uint64(i))
		h.Write(word[:])
		retained += 8
		binary.BigEndian.PutUint64(word[:], uint64(len(pair.Name)))
		h.Write(word[:])
		h.Write([]byte(pair.Name))
		retained += 8 + int64(len(pair.Name))
		binary.BigEndian.PutUint64(word[:], uint64(len(pair.Position)))
		h.Write(word[:])
		h.Write([]byte(pair.Position))
		retained += 8 + int64(len(pair.Position))
	}
	copy(digest[:], h.Sum(nil))
	return int64(len(pairs)), retained, digest, nil
}

// EmptyDigest is the canonical digest of an accepted empty cohort. A head with
// zero members must carry exactly this digest.
func EmptyDigest() [32]byte {
	_, _, digest, _ := Cohort(nil)
	return digest
}
