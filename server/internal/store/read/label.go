package read

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var ErrLabel = errors.New("invalid or over-budget restriction label")

type LabelPolicy uint8

const (
	ProfileLabel LabelPolicy = iota + 1
	AccountLabel
)

// LabelKey validates a raw profile blocked label or account denied value, then
// applies the v1 equality recipe. Validate BEFORE trimming/normalization:
// both profile and account values admit120 UTF8 bytes. The caller also bounds
// complete policy lists at64/256 respectively. Account byte admission supersedes
// the former120-scalar proposal.
//
// Trim Unicode White_Space, NFC, then Unicode DEFAULT case folding, in that
// order. No post-fold normalization, compatibility folding, accent removal,
// search tokens or SQLite collation participates. Internal whitespace and '|'
// are literal. Default U+0130 folds to U+0069 U+0307, distinct from plain 'i'.
//
// This is a values-only helper. It supplies neither current authority nor a
// complete candidate eligibility proof. Browse still refuses label policies.
// The later candidate verifier must validate catalogue values against THEIR
// own source contract before using this same comparison recipe; a policy's
// input limit is not permission to shorten a catalogue value or omit its proof.
func LabelKey(raw string, policy LabelPolicy) (string, error) {
	// Bound caller-owned bytes before even walking UTF8 or counting scalars.
	if policy != ProfileLabel && policy != AccountLabel || len(raw) > 120 {
		return "", ErrLabel
	}
	if !utf8.ValidString(raw) {
		return "", ErrLabel
	}
	switch policy {
	case ProfileLabel:
		if len(raw) > 120 || strings.ContainsAny(raw, "\r\n\t\x00") {
			return "", ErrLabel
		}
	case AccountLabel:
		for _, r := range raw {
			if r < 0x20 || r == 0x7f || unicode.Is(unicode.Cf, r) {
				return "", ErrLabel
			}
		}
	default:
		return "", ErrLabel
	}
	raw = strings.TrimFunc(raw, func(r rune) bool { return unicode.Is(unicode.White_Space, r) })
	if raw == "" {
		return "", ErrLabel
	}
	return cases.Fold().String(norm.NFC.String(raw)), nil
}
