package enrich

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// SubjectKind is the canonical provider namespace, independent of adapter names.
// For example, an adapter's "show" maps to Series before preparation.
type SubjectKind string

const (
	Movie     SubjectKind = "movie"
	Series    SubjectKind = "series"
	Season    SubjectKind = "season"
	Episode   SubjectKind = "episode"
	Person    SubjectKind = "person"
	Artist    SubjectKind = "artist"
	Release   SubjectKind = "release"
	Recording SubjectKind = "recording"
)

// ProviderRef keeps all three R-12 namespace components; IDs are opaque strings.
// W rechecks these against the physical composite foreign keys at acceptance.
type ProviderRef struct {
	Provider   string
	Kind       SubjectKind
	ExternalID string
}

func (r ProviderRef) Validate() error {
	switch r.Kind {
	case Movie, Series, Season, Episode, Person, Artist, Release, Recording:
	default:
		return errors.New("unknown canonical provider kind")
	}
	if !boundedText(r.Provider, 128) || r.Provider == "" || strings.TrimSpace(r.Provider) != r.Provider || !boundedText(r.ExternalID, 1024) || r.ExternalID == "" || strings.TrimSpace(r.ExternalID) != r.ExternalID {
		return errors.New("invalid provider namespace")
	}
	return nil
}

// Credit classification and selection policy have one owner: writer.
type CreditClass = writer.CreditClass

const (
	Cast = writer.Cast
	Crew = writer.Crew
)

// ScreenCredit is adapter-normalized input. Billing is the provider's cast
// billing rank, SourceOrdinal the stable source position BEFORE trimming.
// Role identities are preserved, including multiple roles for the same person.
// Music artist/join-phrase facts use a separate typed preparation path.
type ScreenCredit struct {
	Class                            CreditClass
	Person                           ProviderRef
	CreditID, Name, Role, Department string
	Billing, SourceOrdinal           int64
}

// PreparedScreenCredits contains only the selected accepted document. There is
// no raw response, discarded-row list, attachment payload or mutable slice view.
// Values copied out still require W's current policy/identity/lock/CAS admission.
type PreparedScreenCredits struct {
	subject ProviderRef
	rows    []ScreenCredit
	digest  [32]byte
}

func (p PreparedScreenCredits) Subject() ProviderRef { return p.subject }
func (p PreparedScreenCredits) Count() int           { return len(p.rows) }
func (p PreparedScreenCredits) Digest() [32]byte     { return p.digest }
func (p PreparedScreenCredits) Rows() []ScreenCredit { return slices.Clone(p.rows) }

// Chunk produces one private credit stage of at most16 rows. The final150-credit
// screen cohort has nine16-row chunks and one6-row chunk. Out-of-range is empty.
func (p PreparedScreenCredits) Chunk(after int) []ScreenCredit {
	if after < 0 || after >= len(p.rows) {
		return nil
	}
	return slices.Clone(p.rows[after:min(after+16, len(p.rows))])
}

// PrepareScreenCredits selects top100 cast by billing and top50 crew by the
// frozen keyCrewRank policy before any evidence/build/attachment/person write.
// Selected crew return to source order. Only the retained cohort is hashed.
func PrepareScreenCredits(subject ProviderRef, input []ScreenCredit) (PreparedScreenCredits, error) {
	if err := subject.Validate(); err != nil {
		return PreparedScreenCredits{}, err
	}
	switch subject.Kind {
	case Movie, Series, Season, Episode:
	default:
		return PreparedScreenCredits{}, errors.New("screen credits require a screen subject")
	}
	// Acquisition/decode safety bound, not a persisted cast/crew cap. Oversized
	// responses must be refused before decoding by the provider transport too.
	if len(input) > writer.MaxScreenCreditCandidates {
		return PreparedScreenCredits{}, errors.New("screen credit input budget exceeded")
	}
	candidates := make([]writer.ScreenCreditCandidate, 0, len(input))
	bytes := 0
	for _, c := range input {
		if err := c.Person.Validate(); err != nil {
			return PreparedScreenCredits{}, err
		}
		if c.Person.Provider != subject.Provider || c.Person.Kind != Person {
			return PreparedScreenCredits{}, errors.New("credit person namespace differs from screen subject")
		}
		if !boundedText(c.CreditID, 1024) || !boundedText(c.Name, 1200) || strings.TrimSpace(c.Name) == "" || !boundedText(c.Role, 2000) || !boundedText(c.Department, 800) {
			return PreparedScreenCredits{}, errors.New("invalid normalized credit text")
		}
		bytes += len(c.Person.ExternalID) + len(c.CreditID) + len(c.Name) + len(c.Role) + len(c.Department)
		if bytes > 4<<20 {
			return PreparedScreenCredits{}, errors.New("screen credit input byte budget exceeded")
		}
		candidates = append(candidates, writer.ScreenCreditCandidate{Class: c.Class, Billing: c.Billing, SourceOrdinal: c.SourceOrdinal, CreditID: c.CreditID, Role: c.Role, Department: c.Department})
	}
	indices, err := writer.SelectScreenCredits(candidates)
	if err != nil {
		return PreparedScreenCredits{}, err
	}
	retained := make([]writer.ScreenCreditCandidate, 0, len(indices))
	for _, index := range indices {
		retained = append(retained, candidates[index])
	}
	if err := writer.ValidateRetainedCreditShape(retained); err != nil {
		return PreparedScreenCredits{}, err
	}
	// Copy just the chosen rows; no backing array retains discarded strings.
	rows := make([]ScreenCredit, 0, len(indices))
	for _, index := range indices {
		row := input[index]
		row.Person.Provider = strings.Clone(row.Person.Provider)
		row.Person.ExternalID = strings.Clone(row.Person.ExternalID)
		row.CreditID = strings.Clone(row.CreditID)
		row.Name = strings.Clone(row.Name)
		row.Role = strings.Clone(row.Role)
		row.Department = strings.Clone(row.Department)
		rows = append(rows, row)
	}
	h := sha256.New()
	digestText(h, "portico.screen-credits.v1")
	digestRef(h, subject)
	for _, c := range rows {
		digestText(h, string(c.Class))
		digestRef(h, c.Person)
		for _, s := range []string{c.CreditID, c.Name, c.Role, c.Department} {
			digestText(h, s)
		}
		digestInt(h, uint64(c.Billing))
		digestInt(h, uint64(c.SourceOrdinal))
	}
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return PreparedScreenCredits{subject: subject, rows: rows, digest: digest}, nil
}
func boundedText(s string, limit int) bool {
	if len(s) > limit || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func digestInt(h hash.Hash, n uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	_, _ = h.Write(b[:])
}
func digestText(h hash.Hash, s string) { digestInt(h, uint64(len(s))); _, _ = h.Write([]byte(s)) }
func digestRef(h hash.Hash, r ProviderRef) {
	digestText(h, r.Provider)
	digestText(h, string(r.Kind))
	digestText(h, r.ExternalID)
}
