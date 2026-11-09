package providers

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/enrich"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// MusicBrainz artist/release/recording preparation. One recorded lookup body is
// normalized into the exact nonmovie evidence families IP-06/IP-07 stage and
// seal: the closed header projection plus aliases (a verified MBID redirect),
// release-group secondary-type terms, media, tracks and artist credits. It never
// acquires an observation, writes a catalogue identity, accepts a source or
// matches a book. A release body without its complete declared media/tracks is
// refused rather than repaired into a complete source.
var (
	// ErrMBEvidence is the closed normalization refusal: a partial medium, a
	// duplicate track, a foreign identifier, an invalid date or a body outside
	// the acquisition envelope is refused.
	ErrMBEvidence = errors.New("invalid MusicBrainz observation")

	// ErrMBConsent refuses preparation without the library's explicit
	// MusicBrainz opt-in and its positive online-permission epoch. This is a
	// preparation envelope, not a physical grant; the writer rechecks the exact
	// current epoch at claim and acceptance.
	ErrMBConsent = errors.New("invalid MusicBrainz consent envelope")
)

const (
	musicBrainzOrigin = "https://musicbrainz.org"
	musicBrainzAPI    = "/ws/2/"

	// The acquisition envelope matches the retired MusicBrainz client budget.
	// It bounds one response; it is not the writer's page, row or charge budget.
	maxMBResponseBytes = 4 << 20
	maxMBText          = 4096
	maxMBNameText      = 2048
	maxMBShortText     = 256
	maxMBMedia         = 200
	maxMBTracks        = 1024
	maxMBArtistCredits = 128
	maxMBTerms         = 64
	maxMBLengthMillis  = 1_000_000
	maxMBJSONDepth     = 64

	// The release parser/shape is the pinned IP-READ-SOURCE-CONTRACT name under
	// the IP-06 "ip.<provider>." namespace every nonmovie header must carry. The
	// artist and recording shapes follow the merged TV header convention.
	mbReleaseParser = "ip.musicbrainz.release.header-complete-children.v1"
	mbReleaseShape  = "ip.musicbrainz.release.detail+artist-credits+recordings+release-groups.v1"

	// The exact vocabulary the read contract pins for the COMPLETE original
	// ordered release-group secondary-type array. It is not genre or a release
	// type alias.
	mbSecondaryVocabulary = "release_group_secondary_type"
)

// MBConsent is the MusicBrainz preparation envelope captured with the body:
// the runtime online lookups flag and the online-permission epoch. It authorizes
// offline preparation only.
type MBConsent struct {
	OnlineLookups int64
	LookupEpoch   int64
}

func (c MBConsent) check() error {
	if c.OnlineLookups != 1 || c.LookupEpoch < 1 {
		return ErrMBConsent
	}
	return nil
}

// MBRoute is the actual upstream coordinate. RequestedID is the MBID that was
// looked up; a verified merge redirect may make the canonical body MBID differ.
type MBRoute struct {
	Kind        enrich.SubjectKind
	RequestedID string
}

// MBObservation is the original acquisition envelope. ObservedAt and the
// validators are preserved as acquired; cache reuse never retimes them.
type MBObservation struct {
	Route                                     MBRoute
	EvidenceID                                string
	SubjectID                                 int64
	ObservedAt, SourceURL, ETag, LastModified string
}

// MBRedirect retains the original requested MBID next to the canonical one. A
// read must never infer identity adoption from this row; it only records that
// MusicBrainz answered the requested entity with the canonical MBID.
type MBRedirect struct {
	RequestedID, CanonicalID, Name string
}

// MBTerm is one release-group secondary type in its original array position.
type MBTerm struct {
	Ordinal           int64
	Vocabulary, Value string
}

// MBMedium is one declared medium. Format and Title are the provider's exact
// values; MusicBrainz leaves the title empty for an untitled medium.
type MBMedium struct {
	Position      int64
	Format, Title string
}

// MBTrack is one complete track coordinate. TrackID and RecordingID are
// provider MBIDs; the recording evidence ID is a separate prepared observation
// the worker resolves, not a provider fact.
type MBTrack struct {
	TrackID                  string
	MediumPosition, Position int64
	Number, Title, Artist    string
	LengthMillis             writer.EvidenceInteger
	RecordingID              string
}

// MBCredit is one artist credit of the evidence subject itself. Class is always
// "artist" and the scope is empty. IP-06's closed family order materializes the
// credit family before media/tracks, so a track-scoped credit could not satisfy
// metadata_evidence_credits' generated-track foreign key; a track's complete
// artist credit is retained exactly as the track row's joined display instead.
type MBCredit struct {
	Ordinal                                          int64
	Class, ProviderPersonID, Name                    string
	ArtistName, SortName, Disambiguation, JoinPhrase string
}

// MBReleaseClass is the album browse section from the read contract's ordered
// precedence. Unknown primary types classify as album; Detail keeps the raw
// primary type in the header.
type MBReleaseClass string

const (
	MBClassAlbum       MBReleaseClass = "album"
	MBClassEP          MBReleaseClass = "ep"
	MBClassSingle      MBReleaseClass = "single"
	MBClassCompilation MBReleaseClass = "compilation"
)

// MBEvidence is an immutable prepared nonmovie observation. It is preparation
// data only: it neither authorizes publication nor writes a catalogue identity.
type MBEvidence struct {
	header       writer.EvidenceHeader
	input        writer.EvidenceHeaderInput
	route        MBRoute
	lookupEpoch  int64
	redirects    []MBRedirect
	terms        []MBTerm
	media        []MBMedium
	tracks       []MBTrack
	credits      []MBCredit
	releaseClass MBReleaseClass
	digest       [32]byte
}

func (d MBEvidence) Header() writer.EvidenceHeader { return d.header }
func (d MBEvidence) Route() MBRoute                { return d.route }

// HeaderInput is the exact closed header projection the writer stages. It is
// returned as data, not as write authority.
func (d MBEvidence) HeaderInput() writer.EvidenceHeaderInput { return d.input }

// LookupEpoch is the online-permission epoch bound by preparation. The claim and
// acceptance bind it as the operation's consent revision.
func (d MBEvidence) LookupEpoch() int64 { return d.lookupEpoch }

// Digest is the complete ordered preparation identity, including the consent
// epoch and every normalized row. It is inert provider data, not a writer
// request or result digest.
func (d MBEvidence) Digest() [32]byte { return d.digest }

func (d MBEvidence) Redirects() []MBRedirect { return slices.Clone(d.redirects) }
func (d MBEvidence) Terms() []MBTerm         { return slices.Clone(d.terms) }
func (d MBEvidence) Media() []MBMedium       { return slices.Clone(d.media) }
func (d MBEvidence) Tracks() []MBTrack       { return slices.Clone(d.tracks) }
func (d MBEvidence) Credits() []MBCredit     { return slices.Clone(d.credits) }

// Class reports the album browse section of a release observation. Every other
// subject kind has no release classification.
func (d MBEvidence) Class() MBReleaseClass { return d.releaseClass }

func mbEntity(kind enrich.SubjectKind) (string, bool) {
	switch kind {
	case enrich.Artist:
		return "artist", true
	case enrich.Release:
		return "release", true
	case enrich.Recording:
		return "recording", true
	default:
		return "", false
	}
}

func mbParser(kind enrich.SubjectKind) string {
	if kind == enrich.Release {
		return mbReleaseParser
	}
	return "ip.musicbrainz." + string(kind) + ".header.v1"
}

func mbShape(kind enrich.SubjectKind) string {
	if kind == enrich.Release {
		return mbReleaseShape
	}
	return "ip.musicbrainz." + string(kind) + ".detail.header.v1"
}

// mbSourcePath is the only accepted source path for one route. Query, fragment
// and credentials never enter evidence.
func mbSourcePath(kind enrich.SubjectKind, id string) (string, bool) {
	entity, ok := mbEntity(kind)
	if !ok || !mbMBID(id) {
		return "", false
	}
	return musicBrainzAPI + entity + "/" + id, true
}

// mbMBID accepts only the canonical lowercase 8-4-4-4-12 hex form MusicBrainz
// returns. Upper case, braces and short forms are not the same identifier.
func mbMBID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i := 0; i < len(value); i++ {
		switch i {
		case 8, 13, 18, 23:
			if value[i] != '-' {
				return false
			}
		default:
			c := value[i]
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return false
			}
		}
	}
	return true
}

func mbText(value string, max int) bool {
	return len(value) <= max && utf8.ValidString(value) && strings.IndexByte(value, 0) < 0
}

func mbTitle(value string) bool {
	return mbText(value, maxMBText) && strings.TrimSpace(value) != ""
}

func mbName(value string) bool {
	return mbText(value, maxMBNameText) && strings.TrimSpace(value) != ""
}

func mbShort(value string) bool {
	return mbText(value, maxMBShortText)
}

func mbCountry(value string) bool {
	if value == "" {
		return true
	}
	if len(value) != 2 {
		return false
	}
	for i := 0; i < 2; i++ {
		if value[i] < 'A' || value[i] > 'Z' {
			return false
		}
	}
	return true
}

func mbBarcode(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 32 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

// mbYear accepts an exact, partial or full MusicBrainz date and returns its
// year. The provider date text is retained; it is never extended or retimed.
func mbYear(value string) (int64, bool) {
	var layout string
	switch len(value) {
	case 4:
		layout = "2006"
	case 7:
		layout = "2006-01"
	case 10:
		layout = "2006-01-02"
	default:
		return 0, false
	}
	parsed, err := time.Parse(layout, value)
	if err != nil {
		return 0, false
	}
	year := int64(parsed.Year())
	if year < 1 || year > 9999 {
		return 0, false
	}
	return year, true
}

// mbLifeYear preserves an absent life-span bound as absent rather than zero.
func mbLifeYear(value string) (int64, bool, bool) {
	if value == "" {
		return 0, false, true
	}
	year, ok := mbYear(value)
	if !ok {
		return 0, false, false
	}
	return year, true, true
}

func mbLength(value *int64) (int64, bool, bool) {
	if value == nil {
		return 0, false, true
	}
	if *value < 0 || *value > maxMBLengthMillis {
		return 0, false, false
	}
	return *value, true, true
}

// PrepareMusicBrainz normalizes one recorded MusicBrainz lookup body. It never
// calls a provider. The body must be the complete recorded response for the
// exact declared route; a partial release page is refused.
func PrepareMusicBrainz(consent MBConsent, o MBObservation, body []byte) (MBEvidence, error) {
	var zero MBEvidence
	if err := consent.check(); err != nil {
		return zero, err
	}
	entity, ok := mbEntity(o.Route.Kind)
	if !ok || !mbMBID(o.Route.RequestedID) || len(body) == 0 || len(body) > maxMBResponseBytes {
		return zero, ErrMBEvidence
	}
	// Bound the original metadata before URL parsing or descriptor allocation.
	remaining := int64(maxMBResponseBytes)
	for _, text := range []string{string(o.Route.Kind), o.Route.RequestedID, o.EvidenceID,
		o.ObservedAt, o.SourceURL, o.ETag, o.LastModified} {
		if int64(len(text)) > remaining || !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 {
			return zero, ErrMBEvidence
		}
		remaining -= int64(len(text))
	}
	path := musicBrainzAPI + entity + "/" + o.Route.RequestedID
	u, err := url.Parse(o.SourceURL)
	if err != nil || u.Opaque != "" || u.Scheme != "https" || u.User != nil ||
		!strings.EqualFold(u.Hostname(), "musicbrainz.org") ||
		(u.Port() != "" && u.Port() != "443") || u.Path != path {
		return zero, ErrMBEvidence
	}
	input := writer.EvidenceHeaderInput{ID: o.EvidenceID, SubjectID: o.SubjectID,
		Provider: "musicbrainz", SubjectKind: string(o.Route.Kind), ProviderID: o.Route.RequestedID,
		ParserVersion: mbParser(o.Route.Kind), RequestShape: mbShape(o.Route.Kind),
		ObservedAt: o.ObservedAt, ETag: o.ETag, LastModified: o.LastModified}
	if _, err := writer.CheckEvidenceStageControls(input, 0, 0); err != nil {
		return zero, errors.Join(ErrMBEvidence, err)
	}
	// Freeze the recorded body before validation/hash/decode.
	frozen := bytes.Clone(body)
	if !mbDuplicateFree(frozen) {
		return zero, ErrMBEvidence
	}
	input.ObservationDigest = sha256.Sum256(frozen)
	input.SourceURL = musicBrainzOrigin + path
	var canonical string
	var rows mbRows
	switch o.Route.Kind {
	case enrich.Artist:
		canonical, rows, err = mbParseArtist(frozen, &input)
	case enrich.Release:
		canonical, rows, err = mbParseRelease(frozen, &input)
	default:
		canonical, rows, err = mbParseRecording(frozen, &input)
	}
	if err != nil {
		return zero, err
	}
	if !mbMBID(canonical) {
		return zero, ErrMBEvidence
	}
	input.ProviderID = canonical
	header, err := writer.NewEvidenceHeader(input)
	if err != nil {
		return zero, errors.Join(ErrMBEvidence, err)
	}
	var redirects []MBRedirect
	if canonical != o.Route.RequestedID {
		redirects = []MBRedirect{{RequestedID: o.Route.RequestedID, CanonicalID: canonical,
			Name: input.Title}}
	}
	route := MBRoute{Kind: o.Route.Kind, RequestedID: o.Route.RequestedID}
	return MBEvidence{header: header, input: input, route: route, lookupEpoch: consent.LookupEpoch,
		redirects: redirects, terms: rows.terms, media: rows.media, tracks: rows.tracks,
		credits: rows.credits, releaseClass: rows.class,
		digest: mbEvidenceDigest(route, input, consent.LookupEpoch, redirects, rows)}, nil
}

// mbRows is one complete normalized body before it is sealed into MBEvidence.
type mbRows struct {
	terms   []MBTerm
	media   []MBMedium
	tracks  []MBTrack
	credits []MBCredit
	class   MBReleaseClass
}

type mbArtistJSON struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	SortName       string `json:"sort-name"`
	Disambiguation string `json:"disambiguation"`
	Type           string `json:"type"`
	Country        string `json:"country"`
	LifeSpan       struct {
		Begin string `json:"begin"`
		End   string `json:"end"`
		Ended bool   `json:"ended"`
	} `json:"life-span"`
}

type mbArtistCreditJSON struct {
	Name       string `json:"name"`
	JoinPhrase string `json:"joinphrase"`
	Artist     struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		SortName       string `json:"sort-name"`
		Disambiguation string `json:"disambiguation"`
	} `json:"artist"`
}

type mbRecordingJSON struct {
	ID             string               `json:"id"`
	Title          string               `json:"title"`
	Length         *int64               `json:"length"`
	Disambiguation string               `json:"disambiguation"`
	ArtistCredit   []mbArtistCreditJSON `json:"artist-credit"`
}

type mbReleaseGroupJSON struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	PrimaryType    string   `json:"primary-type"`
	SecondaryTypes []string `json:"secondary-types"`
}

type mbTrackJSON struct {
	ID           string               `json:"id"`
	Number       string               `json:"number"`
	Title        string               `json:"title"`
	Length       *int64               `json:"length"`
	Position     int64                `json:"position"`
	ArtistCredit []mbArtistCreditJSON `json:"artist-credit"`
	Recording    mbRecordingJSON      `json:"recording"`
}

type mbMediumJSON struct {
	Position   int64         `json:"position"`
	Format     string        `json:"format"`
	Title      string        `json:"title"`
	TrackCount int64         `json:"track-count"`
	Tracks     []mbTrackJSON `json:"tracks"`
}

type mbReleaseJSON struct {
	ID             string               `json:"id"`
	Title          string               `json:"title"`
	Date           string               `json:"date"`
	Country        string               `json:"country"`
	Barcode        string               `json:"barcode"`
	Disambiguation string               `json:"disambiguation"`
	ArtistCredit   []mbArtistCreditJSON `json:"artist-credit"`
	ReleaseGroup   mbReleaseGroupJSON   `json:"release-group"`
	Media          []mbMediumJSON       `json:"media"`
}

func mbParseArtist(body []byte, h *writer.EvidenceHeaderInput) (string, mbRows, error) {
	var raw mbArtistJSON
	if err := json.Unmarshal(body, &raw); err != nil || !mbMBID(raw.ID) || !mbName(raw.Name) ||
		!mbText(raw.SortName, maxMBNameText) || !mbText(raw.Disambiguation, maxMBNameText) ||
		!mbCountry(raw.Country) || (raw.Type != "" && !mbShort(raw.Type)) {
		return "", mbRows{}, ErrMBEvidence
	}
	begin, beginPresent, ok := mbLifeYear(raw.LifeSpan.Begin)
	if !ok {
		return "", mbRows{}, ErrMBEvidence
	}
	end, endPresent, ok := mbLifeYear(raw.LifeSpan.End)
	if !ok || (beginPresent && endPresent && begin > end) {
		return "", mbRows{}, ErrMBEvidence
	}
	h.Title = raw.Name
	h.Country = raw.Country
	h.Disambiguation = raw.Disambiguation
	h.ArtistType = writer.EvidenceText{Present: raw.Type != "", Value: raw.Type}
	h.BeginYear = writer.EvidenceInteger{Present: beginPresent, Value: begin}
	h.EndYear = writer.EvidenceInteger{Present: endPresent, Value: end}
	return raw.ID, mbRows{}, nil
}

func mbParseRecording(body []byte, h *writer.EvidenceHeaderInput) (string, mbRows, error) {
	var raw mbRecordingJSON
	if err := json.Unmarshal(body, &raw); err != nil || !mbMBID(raw.ID) || !mbTitle(raw.Title) ||
		!mbText(raw.Disambiguation, maxMBNameText) || len(raw.ArtistCredit) == 0 {
		return "", mbRows{}, ErrMBEvidence
	}
	length, present, ok := mbLength(raw.Length)
	if !ok {
		return "", mbRows{}, ErrMBEvidence
	}
	display, ok := mbCreditDisplay(raw.ArtistCredit)
	if !ok {
		return "", mbRows{}, ErrMBEvidence
	}
	credits := make([]MBCredit, 0, len(raw.ArtistCredit))
	next := int64(0)
	if err := mbAppendCredits(&credits, raw.ArtistCredit, &next); err != nil {
		return "", mbRows{}, err
	}
	h.Title = raw.Title
	h.Artist = display
	h.Disambiguation = raw.Disambiguation
	h.LengthMillis = writer.EvidenceInteger{Present: present, Value: length}
	return raw.ID, mbRows{credits: credits}, nil
}

func mbParseRelease(body []byte, h *writer.EvidenceHeaderInput) (string, mbRows, error) {
	var raw mbReleaseJSON
	if err := json.Unmarshal(body, &raw); err != nil || !mbMBID(raw.ID) || !mbTitle(raw.Title) ||
		!mbText(raw.Disambiguation, maxMBNameText) || !mbCountry(raw.Country) ||
		!mbBarcode(raw.Barcode) || len(raw.ArtistCredit) == 0 {
		return "", mbRows{}, ErrMBEvidence
	}
	year := int64(0)
	if raw.Date != "" {
		parsed, ok := mbYear(raw.Date)
		if !ok {
			return "", mbRows{}, ErrMBEvidence
		}
		year = parsed
	}
	group := raw.ReleaseGroup
	if !mbMBID(group.ID) || group.ID == raw.ID || !mbTitle(group.Title) ||
		!mbShort(group.PrimaryType) || strings.TrimSpace(group.PrimaryType) == "" ||
		len(group.SecondaryTypes) > maxMBTerms {
		return "", mbRows{}, ErrMBEvidence
	}
	terms := make([]MBTerm, 0, len(group.SecondaryTypes))
	for i, value := range group.SecondaryTypes {
		if !mbShort(value) || strings.TrimSpace(value) == "" {
			return "", mbRows{}, ErrMBEvidence
		}
		terms = append(terms, MBTerm{Ordinal: int64(i), Vocabulary: mbSecondaryVocabulary, Value: value})
	}
	display, ok := mbCreditDisplay(raw.ArtistCredit)
	if !ok {
		return "", mbRows{}, ErrMBEvidence
	}
	credits := make([]MBCredit, 0, len(raw.ArtistCredit))
	next := int64(0)
	if err := mbAppendCredits(&credits, raw.ArtistCredit, &next); err != nil {
		return "", mbRows{}, err
	}
	if len(raw.Media) == 0 || len(raw.Media) > maxMBMedia {
		return "", mbRows{}, ErrMBEvidence
	}
	media := make([]MBMedium, 0, len(raw.Media))
	tracks := make([]MBTrack, 0)
	seenTrack := make(map[string]bool)
	for i, medium := range raw.Media {
		if medium.Position != int64(i)+1 || !mbShort(medium.Format) || !mbText(medium.Title, maxMBText) ||
			medium.TrackCount <= 0 || int64(len(medium.Tracks)) != medium.TrackCount ||
			len(tracks)+len(medium.Tracks) > maxMBTracks {
			return "", mbRows{}, ErrMBEvidence
		}
		media = append(media, MBMedium{Position: medium.Position, Format: medium.Format, Title: medium.Title})
		for j, track := range medium.Tracks {
			length, present, ok := mbLength(track.Length)
			if !ok || track.Position != int64(j)+1 || !mbMBID(track.ID) || seenTrack[track.ID] ||
				!mbTitle(track.Number) || !mbTitle(track.Title) || !mbMBID(track.Recording.ID) {
				return "", mbRows{}, ErrMBEvidence
			}
			seenTrack[track.ID] = true
			if err := mbValidateCredits(track.ArtistCredit); err != nil {
				return "", mbRows{}, ErrMBEvidence
			}
			artist, ok := mbCreditDisplay(track.ArtistCredit)
			if !ok {
				return "", mbRows{}, ErrMBEvidence
			}
			tracks = append(tracks, MBTrack{TrackID: track.ID, MediumPosition: medium.Position,
				Position: track.Position, Number: track.Number, Title: track.Title, Artist: artist,
				LengthMillis: writer.EvidenceInteger{Present: present, Value: length},
				RecordingID:  track.Recording.ID})
		}
	}
	h.Title = raw.Title
	h.Artist = display
	h.ReleaseDate = raw.Date
	h.Year = year
	h.Country = raw.Country
	h.Barcode = raw.Barcode
	h.Disambiguation = raw.Disambiguation
	h.ReleaseGroupID = group.ID
	h.GroupTitle = group.Title
	h.PrimaryType = group.PrimaryType
	return raw.ID, mbRows{terms: terms, media: media, tracks: tracks, credits: credits,
		class: mbClassifyRelease(group.PrimaryType, group.SecondaryTypes)}, nil
}

// mbCreditName validates one complete artist credit and returns its credited
// name. A blank credited name falls back to the artist name; both blank is a
// refused partial credit.
func mbCreditName(value mbArtistCreditJSON) (string, bool) {
	name := value.Name
	if name == "" {
		name = value.Artist.Name
	}
	if !mbMBID(value.Artist.ID) || !mbName(name) ||
		!mbText(value.Artist.Name, maxMBNameText) || !mbText(value.Artist.SortName, maxMBNameText) ||
		!mbText(value.Artist.Disambiguation, maxMBNameText) ||
		!mbText(value.JoinPhrase, maxMBShortText) {
		return "", false
	}
	return name, true
}

// mbValidateCredits validates one complete declared credit array, including a
// track's own credits, even when only the joined display is persisted.
func mbValidateCredits(values []mbArtistCreditJSON) error {
	if len(values) > maxMBArtistCredits {
		return ErrMBEvidence
	}
	for _, value := range values {
		if _, ok := mbCreditName(value); !ok {
			return ErrMBEvidence
		}
	}
	return nil
}

// mbCreditDisplay joins one complete credit array in provider order. An empty
// array is a declared absence of a display credit, not an error; a blank name is.
func mbCreditDisplay(values []mbArtistCreditJSON) (string, bool) {
	if err := mbValidateCredits(values); err != nil {
		return "", false
	}
	var out strings.Builder
	for _, value := range values {
		name, _ := mbCreditName(value)
		if out.Len()+len(name)+len(value.JoinPhrase) > maxMBText {
			return "", false
		}
		out.WriteString(name)
		out.WriteString(value.JoinPhrase)
	}
	return out.String(), true
}

// mbAppendCredits materializes one complete subject credit array into the closed
// artist-credit family in provider order. Every ordinal is contiguous from zero.
func mbAppendCredits(out *[]MBCredit, values []mbArtistCreditJSON, next *int64) error {
	if err := mbValidateCredits(values); err != nil {
		return err
	}
	for _, value := range values {
		name, _ := mbCreditName(value)
		*out = append(*out, MBCredit{Ordinal: *next, Class: "artist",
			ProviderPersonID: value.Artist.ID, Name: name, ArtistName: value.Artist.Name,
			SortName: value.Artist.SortName, Disambiguation: value.Artist.Disambiguation,
			JoinPhrase: value.JoinPhrase})
		*next = *next + 1
	}
	return nil
}

// mbClassifyRelease applies the read contract's ordered precedence: a
// Compilation secondary type wins, then primary EP, then primary Single,
// otherwise album. Known labels compare ASCII case-insensitively.
func mbClassifyRelease(primary string, secondary []string) MBReleaseClass {
	for _, value := range secondary {
		if mbFoldASCII(value, "compilation") {
			return MBClassCompilation
		}
	}
	if mbFoldASCII(primary, "ep") {
		return MBClassEP
	}
	if mbFoldASCII(primary, "single") {
		return MBClassSingle
	}
	return MBClassAlbum
}

func mbFoldASCII(value, want string) bool {
	if len(value) != len(want) {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != want[i] {
			return false
		}
	}
	return true
}

// mbDuplicateFree refuses duplicate JSON member names anywhere in the bounded
// body and any trailing data or excessive nesting. MusicBrainz never repeats a
// member; silently accepting the last one could hide a declared array.
func mbDuplicateFree(body []byte) bool {
	d := json.NewDecoder(bytes.NewReader(body))
	var walk func(depth int) bool
	walk = func(depth int) bool {
		if depth > maxMBJSONDepth {
			return false
		}
		token, err := d.Token()
		if err != nil {
			return false
		}
		switch token {
		case json.Delim('{'):
			seen := make(map[string]bool, 16)
			for d.More() {
				keyToken, err := d.Token()
				if err != nil {
					return false
				}
				key, ok := keyToken.(string)
				if !ok || seen[key] {
					return false
				}
				seen[key] = true
				if !walk(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim('}')
		case json.Delim('['):
			for d.More() {
				if !walk(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim(']')
		default:
			return true
		}
	}
	if !walk(0) {
		return false
	}
	var trailing json.RawMessage
	return d.Decode(&trailing) == io.EOF
}

// mbEvidenceDigest binds the complete ordered preparation identity, including
// the consent epoch and the original observation digest. The writer recomputes
// its own seal; this digest is inert provider data.
func mbEvidenceDigest(route MBRoute, input writer.EvidenceHeaderInput, epoch int64,
	redirects []MBRedirect, rows mbRows) [32]byte {
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
	flag := func(value bool) {
		if value {
			_, _ = h.Write([]byte{1})
		} else {
			_, _ = h.Write([]byte{0})
		}
	}
	text("portico.mb_evidence.v1")
	text(string(route.Kind))
	text(input.ProviderID)
	text(input.ID)
	text(input.Title)
	text(input.Artist)
	text(input.Disambiguation)
	text(input.Country)
	text(input.ReleaseDate)
	number(input.Year)
	text(input.ReleaseGroupID)
	text(input.GroupTitle)
	text(input.PrimaryType)
	text(input.Barcode)
	flag(input.ArtistType.Present)
	text(input.ArtistType.Value)
	flag(input.BeginYear.Present)
	number(input.BeginYear.Value)
	flag(input.EndYear.Present)
	number(input.EndYear.Value)
	flag(input.LengthMillis.Present)
	number(input.LengthMillis.Value)
	_, _ = h.Write(input.ObservationDigest[:])
	number(epoch)
	number(int64(len(redirects)))
	for _, value := range redirects {
		text(value.RequestedID)
		text(value.CanonicalID)
		text(value.Name)
	}
	text(string(rows.class))
	number(int64(len(rows.terms)))
	for _, value := range rows.terms {
		number(value.Ordinal)
		text(value.Vocabulary)
		text(value.Value)
	}
	number(int64(len(rows.media)))
	for _, value := range rows.media {
		number(value.Position)
		text(value.Format)
		text(value.Title)
	}
	number(int64(len(rows.tracks)))
	for _, value := range rows.tracks {
		text(value.TrackID)
		number(value.MediumPosition)
		number(value.Position)
		text(value.Number)
		text(value.Title)
		text(value.Artist)
		flag(value.LengthMillis.Present)
		number(value.LengthMillis.Value)
		text(value.RecordingID)
	}
	number(int64(len(rows.credits)))
	for _, value := range rows.credits {
		number(value.Ordinal)
		text(value.Class)
		text(value.ProviderPersonID)
		text(value.Name)
		text(value.ArtistName)
		text(value.SortName)
		text(value.Disambiguation)
		text(value.JoinPhrase)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
