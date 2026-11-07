// Package artwork prepares copied artwork observations for the private writer.
// It performs no provider, filesystem, image-decoder or database work.
package artwork

import (
	"errors"
	"math"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/language"
)

var ErrSelection = errors.New("invalid artwork selection preparation")

// Role is a canonical artwork category, independent of the provider's names.
type Role string

const (
	Poster   Role = "poster"
	Backdrop Role = "backdrop"
	Still    Role = "still"
	Logo     Role = "logo"
	Square   Role = "square"
	Portrait Role = "portrait"
	Banner   Role = "banner"
	Cover    Role = "cover"
)

var roles = [...]Role{Poster, Backdrop, Still, Logo, Square, Portrait, Banner, Cover}

// Scope captures observation inputs, not permission to replace a selection.
// The writer must independently recheck the subject, provider policy, source,
// language and locks in its accepting transaction. Entity and person IDs are
// separate namespaces; Kind must be exactly "entity" or "person".
type Scope struct {
	Kind                           string
	ID                             int64
	Incarnation                    [16]byte
	SourceRevision, PolicyRevision int64
	Provider, PreferredLanguage    string
}

// Limits is a required preparation envelope, not a memory grant or a product
// cap. The caller reserves the complete input/temporary/output footprint before
// decoding. This helper checks logical text occurrences and candidate count;
// native memory, IO, retained history and SQL effects need separate admission.
type Limits struct {
	Candidates int
	TextBytes  int64
}

// Candidate is one adapter-normalized observation in a complete provider page.
// Ordinal is the original position within the role, before selection. Score is
// comparable only inside this provider's page. Origin carries no credentials;
// parsing it grants no fetch permission or SSRF exemption.
type Candidate struct {
	Role                                 Role
	ImageID, Origin, Locale, Attribution string
	Score                                float64
	Ordinal                              int64
}

// PreparedSelection retains at most one candidate per unlocked role. It owns
// its strings and never retains discarded candidates or the caller's slices.
// A prepared result is not accepted metadata, a seal, a receipt or authority.
type PreparedSelection struct {
	scope Scope
	rows  []Candidate
	valid bool
}

func (p PreparedSelection) Valid() bool       { return p.valid }
func (p PreparedSelection) Scope() Scope      { return p.scope }
func (p PreparedSelection) Rows() []Candidate { return slices.Clone(p.rows) }

// PrepareSelection validates the entire page, including candidates for locked
// roles and discarded candidates. It ranks preferred base language first,
// textless next, English next, then other languages; score descending and raw
// ordinal ascending break ties. Output has canonical role order, independent
// of input slice order. Empty locale means textless; "und" stays distinct.
// Locked roles produce no automatic replacement; existing locked art is read
// and checked by the writer, never passed through as an automatic candidate.
func PrepareSelection(scope Scope, input []Candidate, locked []Role, limits Limits) (PreparedSelection, error) {
	if limits.Candidates <= 0 || limits.TextBytes <= 0 || len(input) > limits.Candidates ||
		(scope.Kind != "entity" && scope.Kind != "person") || scope.ID <= 0 ||
		scope.Incarnation == [16]byte{} || scope.SourceRevision <= 0 || scope.PolicyRevision <= 0 ||
		scope.Provider == "" || strings.TrimSpace(scope.Provider) != scope.Provider || len(locked) > len(roles) {
		return PreparedSelection{}, ErrSelection
	}
	remaining := limits.TextBytes
	if !chargeText(&remaining, scope.Kind, scope.Provider, scope.PreferredLanguage) {
		return PreparedSelection{}, ErrSelection
	}
	preferred, ok := baseLanguage(scope.PreferredLanguage)
	if !ok {
		return PreparedSelection{}, ErrSelection
	}
	var blocked [len(roles)]bool
	for _, role := range locked {
		i := roleIndex(role)
		if i < 0 || blocked[i] {
			return PreparedSelection{}, ErrSelection
		}
		blocked[i] = true
	}
	// Validate and account before allocating maps or retaining candidate text.
	for _, row := range input {
		if roleIndex(row.Role) < 0 || row.ImageID == "" || row.Ordinal < 0 ||
			math.IsNaN(row.Score) || math.IsInf(row.Score, 0) ||
			!chargeText(&remaining, string(row.Role), row.ImageID, row.Origin, row.Locale, row.Attribution) {
			return PreparedSelection{}, ErrSelection
		}
		if _, ok := baseLanguage(row.Locale); !ok {
			return PreparedSelection{}, ErrSelection
		}
		if !safeOrigin(row.Origin) {
			return PreparedSelection{}, ErrSelection
		}
	}
	type identity struct {
		role Role
		id   string
	}
	type position struct {
		role Role
		n    int64
	}
	ids := make(map[identity]struct{}, len(input))
	ordinals := make(map[position]struct{}, len(input))
	var best [len(roles)]int
	var ranks [len(roles)]int
	for i := range best {
		best[i] = -1
	}
	for i, row := range input {
		key, ordinal := identity{row.Role, row.ImageID}, position{row.Role, row.Ordinal}
		_, duplicateID := ids[key]
		_, duplicateOrdinal := ordinals[ordinal]
		if duplicateID || duplicateOrdinal {
			return PreparedSelection{}, ErrSelection
		}
		ids[key], ordinals[ordinal] = struct{}{}, struct{}{}
		r := roleIndex(row.Role)
		if blocked[r] {
			continue
		}
		locale, _ := baseLanguage(row.Locale)
		rank := languageRank(locale, preferred)
		prior := best[r]
		if prior < 0 || rank < ranks[r] || (rank == ranks[r] &&
			(row.Score > input[prior].Score || (row.Score == input[prior].Score && row.Ordinal < input[prior].Ordinal))) {
			best[r], ranks[r] = i, rank
		}
	}
	rows := make([]Candidate, 0, len(roles))
	for _, i := range best {
		if i >= 0 {
			row := input[i]
			row.Role = Role(strings.Clone(string(row.Role)))
			row.ImageID, row.Origin = strings.Clone(row.ImageID), strings.Clone(row.Origin)
			row.Locale, row.Attribution = strings.Clone(row.Locale), strings.Clone(row.Attribution)
			rows = append(rows, row)
		}
	}
	scope.Kind, scope.Provider = strings.Clone(scope.Kind), strings.Clone(scope.Provider)
	scope.PreferredLanguage = strings.Clone(scope.PreferredLanguage)
	return PreparedSelection{scope: scope, rows: rows, valid: true}, nil
}

func roleIndex(role Role) int {
	return slices.Index(roles[:], role)
}

func chargeText(remaining *int64, values ...string) bool {
	for _, value := range values {
		if int64(len(value)) > *remaining || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
			return false
		}
		*remaining -= int64(len(value))
	}
	return true
}

func baseLanguage(value string) (string, bool) {
	if value == "" {
		return "", true
	}
	tag, err := language.Parse(value)
	if err != nil {
		return "", false
	}
	base, _, _ := tag.Raw()
	return base.String(), true
}

func languageRank(locale, preferred string) int {
	switch {
	case locale == preferred:
		return 0
	case locale == "":
		return 1
	case locale == "en":
		return 2
	default:
		return 3
	}
}

func safeOrigin(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Opaque == "" && u.Hostname() != "" &&
		u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
}
