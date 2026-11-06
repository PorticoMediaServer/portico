package writer

import (
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// EvidenceCreditInput is the draft009 persisted FIRST movie credit projection.
// Ordinal is retained child position. SourceOrdinal is the independent raw
// pretrim position; Billing is present for cast, including0, and NULL for crew.
type EvidenceCreditInput struct {
	Ordinal, SourceOrdinal                   int64
	Billing                                  EvidenceInteger
	Class                                    CreditClass
	ProviderPersonID, Name, Role, Department string
	CreditID                                 string
}

// EvidenceStagePayload owns immutable preparation data, NOT admission, source
// proof, a command/result digest or a seal. W's complete command still verifies
// original authority/receipts and prospective009 proof-reference/resource cost.
type EvidenceStagePayload struct {
	header    EvidenceHeader
	credits   []EvidenceCreditInput
	baseBytes uint64
	valid     bool
}

func (p EvidenceStagePayload) Header() EvidenceHeader         { return p.header }
func (p EvidenceStagePayload) CreditCount() int               { return len(p.credits) }
func (p EvidenceStagePayload) Credits() []EvidenceCreditInput { return slices.Clone(p.credits) }

// BaseContentBytes excludes future proof-reference bytes. It is a native
// TEXT/BLOB forecast, not an admitted Q/debt/RAM/WAL allowance.
func (p EvidenceStagePayload) BaseContentBytes() uint64 { return p.baseBytes }

// PrepareEvidenceStagePayload freezes the native FIRST projection. It neither
// trims nor reorders: W's sole selector must run before this construction.
// The32MiB guard is preparation safety; trusted command policy independently
// enforces the adopted151-row/6MiB prospective CONTENT bound before allocation.
func PrepareEvidenceStagePayload(header EvidenceHeader, input []EvidenceCreditInput) (EvidenceStagePayload, error) {
	if !firstMovieEvidenceHeader(header) || len(input) > MaxCastCredits+MaxCrewCredits {
		return EvidenceStagePayload{}, invalid("evidence_stage_payload")
	}
	size := header.textBytes + 64 // actual digest storage is TEXT64, not BLOB32
	candidates := make([]ScreenCreditCandidate, len(input))
	for i, row := range input {
		if row.Ordinal != int64(i) || row.SourceOrdinal < 0 || strings.TrimSpace(row.Name) == "" {
			return EvidenceStagePayload{}, invalid("evidence_credit_order_name")
		}
		billing := int64(0)
		switch row.Class {
		case Cast:
			if !row.Billing.Present || row.Billing.Value < 0 {
				return EvidenceStagePayload{}, invalid("evidence_cast_billing")
			}
			billing = row.Billing.Value
		case Crew:
			if row.Billing.Present || row.Billing.Value != 0 {
				return EvidenceStagePayload{}, invalid("evidence_crew_billing")
			}
		default:
			return EvidenceStagePayload{}, invalid("evidence_credit_class")
		}
		id, err := strconv.ParseInt(row.ProviderPersonID, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != row.ProviderPersonID {
			return EvidenceStagePayload{}, invalid("evidence_credit_person")
		}
		for _, s := range []string{header.input.Provider, header.input.SubjectKind, header.input.ID, string(row.Class), row.ProviderPersonID, row.Name, row.Role, row.Department, row.CreditID} {
			if uint64(len(s)) > maxCanonicalRequestBytes-size || !utf8.ValidString(s) || strings.IndexByte(s, 0) >= 0 {
				return EvidenceStagePayload{}, invalid("evidence_credit_text")
			}
			size += uint64(len(s))
		}
		candidates[i] = ScreenCreditCandidate{row.Class, billing, row.SourceOrdinal, row.CreditID, row.Role, row.Department}
	}
	if err := ValidateRetainedCreditShape(candidates); err != nil {
		return EvidenceStagePayload{}, err
	}
	rows := slices.Clone(input)
	for i := range rows {
		row := &rows[i]
		if row.Class == Cast {
			row.Class = Cast
		} else {
			row.Class = Crew
		}
		row.ProviderPersonID, row.Name = strings.Clone(row.ProviderPersonID), strings.Clone(row.Name)
		row.Role, row.Department, row.CreditID = strings.Clone(row.Role), strings.Clone(row.Department), strings.Clone(row.CreditID)
	}
	return EvidenceStagePayload{header, rows, size, true}, nil
}

func firstMovieEvidenceHeader(header EvidenceHeader) bool {
	h := header.input
	if !header.valid || h.Provider != "tmdb" || h.SubjectKind != "movie" ||
		h.ParserVersion != "tmdb.movie.header-selected-credits.v1" || h.RequestShape != "tmdb.movie.detail+credits.header-selected-credits.v1" ||
		strings.TrimSpace(h.Title) == "" || h.ArtistType.Present || h.BeginYear.Present || h.EndYear.Present {
		return false
	}
	id, err := strconv.ParseInt(h.ProviderID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != h.ProviderID || h.SourceURL != "https://api.themoviedb.org/3/movie/"+h.ProviderID {
		return false
	}
	for _, s := range []string{h.ContentRating, h.Studio, h.Network, h.Country, h.Artist, h.WikidataID, h.Biography, h.BiographySource, h.BiographySourceURL, h.Disambiguation, h.ReleaseGroupID, h.GroupTitle, h.PrimaryType, h.Barcode} {
		if s != "" {
			return false
		}
	}
	if h.ReleaseDate == "" {
		return h.Year == 0
	}
	date, err := time.Parse("2006-01-02", h.ReleaseDate)
	return err == nil && h.Year == int64(date.Year())
}
