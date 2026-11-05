package writer

import (
	"encoding/binary"
	"hash"
	"strings"
	"time"
	"unicode/utf8"
)

// EvidenceInteger preserves SQL NULL separately from zero. An absent value must
// have Value zero, so an ignored field cannot create two request encodings.
type EvidenceInteger struct {
	Value   int64
	Present bool
}

// EvidenceText preserves SQL NULL separately from the empty string.
type EvidenceText struct {
	Value   string
	Present bool
}

// EvidenceHeaderInput is the typed common observation in metadata_evidence.
// SubjectID is a native metadata_provider_subjects key; ProviderID stays opaque.
// ObservationDigest identifies the complete source observation, including any
// separately staged children. It is NOT a verified seal or command digest.
type EvidenceHeaderInput struct {
	ID, ParserVersion, Provider, SubjectKind, ProviderID string
	SubjectID                                            int64
	Language, Region, RequestShape                       string
	ObservationDigest                                    [32]byte
	ObservedAt, SourceURL, ETag, LastModified            string
	Title, OriginalTitle, Overview, Tagline              string
	ContentRating, Studio, Network, Country              string
	ReleaseDate, Artist                                  string
	Year                                                 int64
	LengthMillis, BeginYear, EndYear                     EvidenceInteger
	ArtistType                                           EvidenceText
	WikidataID, Biography, BiographySource               string
	BiographySourceURL, Disambiguation                   string
	ReleaseGroupID, GroupTitle, PrimaryType, Barcode     string
}

// EvidenceHeader is an immutable copied observation, not executing authority.
// Only the closed EvidenceStage command may persist it, unsealed, through W.
type EvidenceHeader struct {
	input     EvidenceHeaderInput
	textBytes uint64
	valid     bool
}

// This is an acquisition/preparation envelope, not a product metadata limit or
// an admitted SQL recipe. The complete command may refuse a smaller byte/work
// budget; a legal header is never silently truncated to make it fit.
const maxEvidenceHeaderTextBytes = 4 << 20

func NewEvidenceHeader(input EvidenceHeaderInput) (EvidenceHeader, error) {
	if input.SubjectID <= 0 || input.ID == "" || input.ParserVersion == "" || input.Provider == "" || input.ProviderID == "" || input.RequestShape == "" {
		return EvidenceHeader{}, invalid("evidence_header_identity")
	}
	switch input.SubjectKind {
	case "movie", "series", "season", "episode", "person", "artist", "release", "recording":
	default:
		return EvidenceHeader{}, invalid("evidence_subject_kind")
	}
	if (!input.ArtistType.Present && input.ArtistType.Value != "") ||
		(!input.LengthMillis.Present && input.LengthMillis.Value != 0) ||
		(!input.BeginYear.Present && input.BeginYear.Value != 0) ||
		(!input.EndYear.Present && input.EndYear.Value != 0) ||
		(input.LengthMillis.Present && input.LengthMillis.Value < 0) {
		return EvidenceHeader{}, invalid("evidence_nullable_value")
	}
	fields := evidenceHeaderStrings(&input)
	var size uint64
	for _, field := range fields {
		size += uint64(len(*field))
		if size > maxEvidenceHeaderTextBytes {
			return EvidenceHeader{}, invalid("evidence_header_bytes")
		}
	}
	for _, field := range fields {
		if !utf8.ValidString(*field) || strings.IndexByte(*field, 0) >= 0 {
			return EvidenceHeader{}, invalid("evidence_header_text")
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, input.ObservedAt); err != nil {
		return EvidenceHeader{}, invalid("evidence_observed_at")
	}
	// Clone every retained string, including the validated kind and optional
	// enum text. Short substrings must not keep an unbounded response alive.
	for _, field := range fields {
		*field = strings.Clone(*field)
	}
	return EvidenceHeader{input: input, textBytes: size, valid: true}, nil
}

func (h EvidenceHeader) ID() string        { return h.input.ID }
func (h EvidenceHeader) SubjectID() int64  { return h.input.SubjectID }
func (h EvidenceHeader) TextBytes() uint64 { return h.textBytes }

// The order is the common-header codec order, not reflection or map iteration.
// SQL binding order is separately explicit in the static statement definition.
func evidenceHeaderStrings(v *EvidenceHeaderInput) []*string {
	return []*string{
		&v.ID, &v.ParserVersion, &v.Provider, &v.SubjectKind, &v.ProviderID,
		&v.Language, &v.Region, &v.RequestShape,
		&v.ObservedAt, &v.SourceURL, &v.ETag, &v.LastModified,
		&v.Title, &v.OriginalTitle, &v.Overview, &v.Tagline,
		&v.ContentRating, &v.Studio, &v.Network, &v.Country,
		&v.ReleaseDate, &v.Artist, &v.ArtistType.Value,
		&v.WikidataID, &v.Biography, &v.BiographySource, &v.BiographySourceURL,
		&v.Disambiguation, &v.ReleaseGroupID, &v.GroupTitle, &v.PrimaryType, &v.Barcode,
	}
}

// evidenceHeaderDigest contributes an exact typed header to the W-computed
// request codec. It cannot establish a whole-evidence digest or seal, and it
// neither reuses nor relabels the credit/owner/PB manifest algorithms.
func evidenceHeaderDigest(h hash.Hash, input EvidenceHeader) error {
	if !input.valid {
		return invalid("evidence_header")
	}
	evidenceDigestText(h, "portico.evidence_header_fields.v1")
	for _, value := range evidenceHeaderStrings(&input.input) {
		evidenceDigestText(h, *value)
	}
	_, _ = h.Write(input.input.ObservationDigest[:])
	evidenceDigestInt(h, input.input.SubjectID)
	evidenceDigestInt(h, input.input.Year)
	for _, value := range []EvidenceInteger{input.input.LengthMillis, input.input.BeginYear, input.input.EndYear} {
		var tag byte
		if value.Present {
			tag = 1
		}
		_, _ = h.Write([]byte{tag})
		evidenceDigestInt(h, value.Value)
	}
	var tag byte
	if input.input.ArtistType.Present {
		tag = 1
	}
	_, _ = h.Write([]byte{tag})
	return nil
}

func evidenceDigestInt(h hash.Hash, value int64) {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], uint64(value))
	_, _ = h.Write(raw[:])
}

func evidenceDigestText(h hash.Hash, value string) {
	evidenceDigestInt(h, int64(len(value)))
	_, _ = h.Write([]byte(value))
}
