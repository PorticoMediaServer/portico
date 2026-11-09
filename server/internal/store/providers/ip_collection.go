package providers

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/enrich"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// ErrCollectionObservation is the closed provider-collection refusal. A
// partial, duplicate or foreign membership is refused rather than repaired.
var ErrCollectionObservation = errors.New("invalid collection observation")

// CollectionKind is the exact provider-collection subject kind from the IP
// source contract. It is deliberately local: enrich.SubjectKind and
// writer.NewEvidenceHeader do not admit "collection" yet (P2-IP-CONTRACT item
// 5). Native metadata_evidence.subject_kind is already TEXT, so no DDL enum
// change is requested; only the Go admission list is missing.
const CollectionKind enrich.SubjectKind = "collection"

// The collection source is TMDB collection detail with its complete declared
// "parts" membership array. It grants no catalogue membership ownership: each
// member stays a provider movie reference, and a member without a local entity
// is not created, hidden or removed by this adapter.
const (
	IPCollectionParser = "tmdb.collection.header-complete-parts.v1"
	IPCollectionShape  = "tmdb.collection.detail.header-complete-parts.v1"
)

const (
	// maxIPCollectionText bounds one declared header text. It is an acquisition
	// safety bound, not a product metadata limit.
	maxIPCollectionText = 64 << 10
	// maxIPCollectionMembers bounds the declared membership array so a hostile
	// response cannot retain an unbounded family. Real collections are orders of
	// magnitude smaller; this is not a product cap.
	maxIPCollectionMembers = 4096
	maxIPCollectionName    = 4096
)

// CollectionObservation captures one recorded TMDB collection acquisition. The
// numeric provider ID is a collection namespace, never a movie ID, even when
// both happen to be the same number. SubjectID is a catalogue entity key.
type CollectionObservation struct {
	EvidenceID                                string
	SubjectID                                 int64
	ProviderID                                string
	Language, Region                          string
	ObservedAt, SourceURL, ETag, LastModified string
}

// CollectionFields is the declared collection header presence mask. Membership
// is the separate declared family, never a header bit.
type CollectionFields uint8

const (
	CollectionName CollectionFields = 1 << iota
	CollectionOverview
)

// CollectionMember is one exact provider membership entry in the declared
// array order. It names a provider movie reference and its reported title; it is
// not a catalogue entity, ownership row or collection identity.
type CollectionMember struct {
	Ref     enrich.ProviderRef
	Name    string
	Ordinal int64
}

// CollectionHeader is the complete common-evidence input for the collection
// subject namespace. The fields mirror metadata_evidence exactly so the later
// collection admission can compose its row without reinterpreting provider data.
type CollectionHeader struct {
	EvidenceID, Provider, SubjectKind, ProviderID string
	ParserVersion, RequestShape, Language, Region string
	SubjectID                                     int64
	ObservationDigest                             [32]byte
	ObservedAt, SourceURL, ETag, LastModified     string
	Name, Overview                                string
}

// CollectionDocument is one prepared, immutable provider collection. It neither
// authorizes publication nor writes a catalogue identity or membership.
type CollectionDocument struct {
	input         writer.EvidenceHeaderInput
	fields, nulls CollectionFields
	members       []CollectionMember
	memberDigest  [32]byte
}

// Header returns the complete typed collection header derived from the vetted
// input, so the two views cannot drift.
func (d CollectionDocument) Header() CollectionHeader {
	return CollectionHeader{EvidenceID: d.input.ID, SubjectID: d.input.SubjectID, Provider: d.input.Provider,
		SubjectKind: d.input.SubjectKind, ProviderID: d.input.ProviderID, ParserVersion: d.input.ParserVersion,
		RequestShape: d.input.RequestShape, Language: d.input.Language, Region: d.input.Region,
		ObservationDigest: d.input.ObservationDigest, ObservedAt: d.input.ObservedAt, SourceURL: d.input.SourceURL,
		ETag: d.input.ETag, LastModified: d.input.LastModified, Name: d.input.Title, Overview: d.input.Overview}
}

// EvidenceHeaderInput returns the vetted common evidence input. The later
// collection admission must extend writer.NewEvidenceHeader's closed subject
// kind list before this input can become an EvidenceHeader; native
// subject_kind is already TEXT, so no schema change is involved.
func (d CollectionDocument) EvidenceHeaderInput() writer.EvidenceHeaderInput { return d.input }

// Fields is the source-presence mask for the declared header scalars.
func (d CollectionDocument) Fields() CollectionFields { return d.fields }

// Nulls is the supplied subset whose JSON value was null.
func (d CollectionDocument) Nulls() CollectionFields { return d.nulls }

// Members is the complete declared membership in provider order as a copy.
func (d CollectionDocument) Members() []CollectionMember {
	return append([]CollectionMember(nil), d.members...)
}

// MemberCount is the declared membership size, including a declared empty set.
func (d CollectionDocument) MemberCount() int { return len(d.members) }

// MemberDigest is the ordered provider membership identity. It changes with a
// member, its order, its title or the owning collection namespace.
func (d CollectionDocument) MemberDigest() [32]byte { return d.memberDigest }

// Complete reports that preparation only ever returns a declared complete set.
// A declared empty "parts" array is complete; a missing or null one is refused,
// so a prepared document with the required header is always complete.
func (d CollectionDocument) Complete() bool { return d.input.ParserVersion != "" }

// PrepareTMDBCollection requires an explicit existing TMDB preparation policy,
// freezes the body before validation/hash/decode and preserves the original
// acquisition time/validators. It never calls a provider and never invents a
// collection member's catalogue ownership.
func PrepareTMDBCollection(policy enrich.TMDBPolicy, observation CollectionObservation, body []byte) (CollectionDocument, error) {
	limits, err := policy.Limits()
	if err != nil {
		return CollectionDocument{}, err
	}
	if int64(len(body)) > limits.ResponseBytes {
		return CollectionDocument{}, &enrich.ResponseSizeError{LimitBytes: limits.ResponseBytes, ObservedBytes: int64(len(body))}
	}
	remaining := limits.ResponseBytes
	for _, text := range []string{string(CollectionKind), observation.ProviderID, observation.EvidenceID,
		observation.Language, observation.Region, observation.ObservedAt, observation.SourceURL,
		observation.ETag, observation.LastModified} {
		if int64(len(text)) > remaining || !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 {
			return CollectionDocument{}, ErrCollectionObservation
		}
		remaining -= int64(len(text))
	}
	// The common header constructor, which normally checks these, cannot admit
	// the collection kind yet; the same identity and clock fences apply here.
	if !canonicalPositiveID(observation.ProviderID) || observation.SubjectID <= 0 || observation.EvidenceID == "" {
		return CollectionDocument{}, ErrCollectionObservation
	}
	if _, err := time.Parse(time.RFC3339Nano, observation.ObservedAt); err != nil {
		return CollectionDocument{}, ErrCollectionObservation
	}
	ref := enrich.ProviderRef{Provider: "tmdb", Kind: CollectionKind, ExternalID: observation.ProviderID}
	path := "/3/collection/" + observation.ProviderID
	input := writer.EvidenceHeaderInput{ID: observation.EvidenceID, SubjectID: observation.SubjectID,
		Provider: "tmdb", SubjectKind: string(CollectionKind), ProviderID: observation.ProviderID,
		ParserVersion: IPCollectionParser, RequestShape: IPCollectionShape,
		Language: observation.Language, Region: observation.Region, ObservedAt: observation.ObservedAt,
		ETag: observation.ETag, LastModified: observation.LastModified}
	// Check control occurrences before allocating/copying body or retained keys.
	// This is the same bounded control check the common header uses; only the
	// subject-kind admission differs.
	if _, err := writer.CheckEvidenceStageControls(input, 0, 0); err != nil {
		return CollectionDocument{}, errors.Join(ErrCollectionObservation, err)
	}
	u, err := url.Parse(observation.SourceURL)
	if err != nil || u.Opaque != "" || u.Scheme != "https" ||
		!strings.EqualFold(u.Hostname(), "api.themoviedb.org") ||
		(u.Port() != "" && u.Port() != "443") || u.Path != path {
		return CollectionDocument{}, ErrCollectionObservation
	}
	input.SourceURL = "https://api.themoviedb.org" + path
	frozen := bytes.Clone(body)
	if !enrich.ValidTMDBObservationJSON(frozen) {
		return CollectionDocument{}, ErrCollectionObservation
	}
	decoded, err := decodeCollection(frozen, observation.ProviderID)
	if err != nil {
		return CollectionDocument{}, err
	}
	input.ObservationDigest = sha256.Sum256(frozen)
	input.Title = strings.Clone(*decoded.name)
	if decoded.overview != nil {
		input.Overview = strings.Clone(*decoded.overview)
	}
	return CollectionDocument{input: input, fields: decoded.fields, nulls: decoded.nulls,
		members: decoded.parts, memberDigest: collectionMemberDigest(ref, decoded.parts)}, nil
}

type collectionScalars struct {
	id             *int64
	name, overview *string
	fields, nulls  CollectionFields
	parts          []CollectionMember
	partsPresent   bool
}

func decodeCollection(body []byte, providerID string) (collectionScalars, error) {
	var out collectionScalars
	d := json.NewDecoder(bytes.NewReader(body))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return collectionScalars{}, ErrCollectionObservation
	}
	// Only fixed selected keys consume the duplicate set. Unknown provider
	// fields remain part of the raw observation digest but grant no declared
	// fact and no membership.
	seen := make(map[string]bool, 4)
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return collectionScalars{}, ErrCollectionObservation
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return collectionScalars{}, ErrCollectionObservation
		}
		seen[key] = true
		switch key {
		case "id":
			if d.Decode(&out.id) != nil {
				return collectionScalars{}, ErrCollectionObservation
			}
		case "name":
			if d.Decode(&out.name) != nil {
				return collectionScalars{}, ErrCollectionObservation
			}
			out.fields = out.fields | CollectionName
			if out.name == nil {
				out.nulls |= CollectionName
			}
		case "overview":
			if d.Decode(&out.overview) != nil {
				return collectionScalars{}, ErrCollectionObservation
			}
			out.fields = out.fields | CollectionOverview
			if out.overview == nil {
				out.nulls |= CollectionOverview
			}
		case "parts":
			members, err := decodeCollectionParts(d)
			if err != nil {
				return collectionScalars{}, err
			}
			out.parts, out.partsPresent = members, true
		default:
			var discard json.RawMessage
			if d.Decode(&discard) != nil {
				return collectionScalars{}, ErrCollectionObservation
			}
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return collectionScalars{}, ErrCollectionObservation
	}
	var trailing json.RawMessage
	if d.Decode(&trailing) != io.EOF || out.id == nil || strconv.FormatInt(*out.id, 10) != providerID ||
		out.name == nil || strings.TrimSpace(*out.name) == "" || out.fields&CollectionName != CollectionName ||
		!out.partsPresent {
		return collectionScalars{}, ErrCollectionObservation
	}
	for _, text := range []*string{out.name, out.overview} {
		if text != nil && (len(*text) > maxIPCollectionText || !utf8.ValidString(*text) ||
			strings.IndexByte(*text, 0) >= 0) {
			return collectionScalars{}, ErrCollectionObservation
		}
	}
	return out, nil
}

// decodeCollectionParts reads the complete declared membership array from the
// already open top-level object. It never fills in an absent part, accepts a
// duplicate provider member or treats a missing array as an empty one.
func decodeCollectionParts(d *json.Decoder) ([]CollectionMember, error) {
	if token, err := d.Token(); err != nil || token != json.Delim('[') {
		return nil, ErrCollectionObservation
	}
	members := make([]CollectionMember, 0)
	seen := make(map[string]bool)
	for d.More() {
		if len(members) >= maxIPCollectionMembers {
			return nil, ErrCollectionObservation
		}
		token, err := d.Token()
		if err != nil || token != json.Delim('{') {
			return nil, ErrCollectionObservation
		}
		var id *int64
		var title *string
		partKeys := make(map[string]bool, 2)
		for d.More() {
			token, err := d.Token()
			if err != nil {
				return nil, ErrCollectionObservation
			}
			key, ok := token.(string)
			if !ok || partKeys[key] {
				return nil, ErrCollectionObservation
			}
			partKeys[key] = true
			switch key {
			case "id":
				if d.Decode(&id) != nil {
					return nil, ErrCollectionObservation
				}
			case "title":
				if d.Decode(&title) != nil {
					return nil, ErrCollectionObservation
				}
			default:
				var discard json.RawMessage
				if d.Decode(&discard) != nil {
					return nil, ErrCollectionObservation
				}
			}
		}
		if token, err := d.Token(); err != nil || token != json.Delim('}') {
			return nil, ErrCollectionObservation
		}
		if id == nil || *id <= 0 || title == nil || strings.TrimSpace(*title) == "" ||
			len(*title) > maxIPCollectionName || !utf8.ValidString(*title) ||
			strings.IndexByte(*title, 0) >= 0 {
			return nil, ErrCollectionObservation
		}
		externalID := strconv.FormatInt(*id, 10)
		if seen[externalID] {
			return nil, ErrCollectionObservation
		}
		seen[externalID] = true
		ref := enrich.ProviderRef{Provider: "tmdb", Kind: enrich.Movie, ExternalID: externalID}
		if err := ref.Validate(); err != nil {
			return nil, ErrCollectionObservation
		}
		members = append(members, CollectionMember{Ref: ref, Name: strings.Clone(*title), Ordinal: int64(len(members))})
	}
	if token, err := d.Token(); err != nil || token != json.Delim(']') {
		return nil, ErrCollectionObservation
	}
	return members, nil
}

// collectionMemberDigest binds the owning provider collection namespace and the
// complete ordered membership. It is inert provider data, not a writer request,
// result or seal digest.
func collectionMemberDigest(owner enrich.ProviderRef, members []CollectionMember) [32]byte {
	h := sha256.New()
	text := func(value string) {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(value))
	}
	number := func(value int64) {
		var raw [8]byte
		binary.BigEndian.PutUint64(raw[:], uint64(value))
		_, _ = h.Write(raw[:])
	}
	text("portico.collection_members.v1")
	text(owner.Provider)
	text(string(owner.Kind))
	text(owner.ExternalID)
	number(int64(len(members)))
	for _, member := range members {
		number(member.Ordinal)
		text(member.Ref.Provider)
		text(string(member.Ref.Kind))
		text(member.Ref.ExternalID)
		text(member.Name)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
