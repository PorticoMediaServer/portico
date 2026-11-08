package read

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// MP reading resume display is a bounded, typed decode of one viewer's latest
// saved Reading position. It owns no personal write, source lease, file handle
// or permission: the source observation is display evidence for RS, never
// authority to open a publication.
const (
	mpReadingResumeNativePolicy     readPolicyID = 702
	mpReadingResumeConversionPolicy readPolicyID = 703
	mpReadingResumeMaxRevision      int64        = 9007199254740991
	mpReadingResumeMaxObserved      int64        = 9999999999999
	mpReadingResumeMaxCFI                        = 4096
	mpReadingResumeMaxSection                    = 1024
	mpReadingResumeMaxPage          int64        = 1000000
)

func init() {
	for _, recipe := range []struct {
		id         readPolicyID
		conversion bool
	}{{mpReadingResumeNativePolicy, false}, {mpReadingResumeConversionPolicy, true}} {
		conversion := recipe.conversion
		registerReadPolicy(readPolicyRecipe{id: recipe.id, domain: fmt.Sprint("mp-reading-resume-conversion-", conversion), kinds: []int64{20, 21}, validate: func(q browseQuery, in BrowseInput, f browseFence) (string, error) {
			if in.policy.subject <= 0 {
				return "", ErrInput
			}
			resume, err := mpReadingResumeSelect(q, in.Library, in.Kind, in.policy.subject, f.library, f.profile, conversion)
			if err != nil {
				return "", err
			}
			return mpReadingResumeWitness(resume), nil
		}})
	}
}

// MPReadingResumeInput selects one currently visible Reading title for the
// profile named by the current viewing token. The profile is never a caller
// field: it comes from the same native principal fence as the library.
type MPReadingResumeInput struct {
	View BrowseInput
	Item int64
}

// MPReadingLocation is the latest saved presentation coordinate for one source.
// Format is the saved presentation format (epub for a converted MOBI/AZW3);
// SourceToken is the public Reading SourceID, the selected asset token.
// SourceRevision is the saved historical revision. For a native publication it
// is the exact 14-field source revision; for a converted EPUB it is the
// converter's immutable provenance revision, which MP cannot re-derive.
type MPReadingLocation struct {
	AssetID        int64
	SourceToken    string
	SourceRevision string
	Format         string
	CFI            string
	Section        string
	PageIndex      *int64
	Progression    float64
	Revision       int64
	ObservedMS     int64
}

// MPReadingResume is the typed display result for one title.
//
// Location is nil when the viewer has no saved version, not when a saved
// version is completed or stale: those stay truthful history. Completed reports
// that the latest saved version is finished. Stale reports a proven mismatch of
// the saved token/format or native source revision, or that the current source
// is not obtainable; there is never a fallback to an older version. Converted
// reports a saved EPUB over a native MOBI/AZW3: MP never compares the saved
// conversion revision to the native revision, so RS compares
// Location.SourceRevision with its actual converter provenance.
type MPReadingResume struct {
	Item             int64
	PublicID         [16]byte
	PersonalRevision int64
	ReadingRevision  int64
	Location         *MPReadingLocation
	Source           *MPReadingSource
	Completed        bool
	Stale            bool
	Converted        bool
}

// MPReadingResumeReader selects the current native source and the viewer's
// latest saved version through the shared two-view publication protocol. Its
// pool and exact requests come from trusted composition.
type MPReadingResumeReader struct {
	snapshots  browseSnapshots
	pool       *governor.Snapshots
	conversion bool
}

func NewMPReadingResumeReader(s *governor.Snapshots) (*MPReadingResumeReader, error) {
	if s == nil {
		return nil, ErrInput
	}
	return &MPReadingResumeReader{snapshots: s, pool: s}, nil
}

// Conversion is private trusted composition, never a request option. The
// phase4 adapter must derive it from the existing configured converter.
func newMPReadingResumeReaderWithConversion(s *governor.Snapshots) (*MPReadingResumeReader, error) {
	r, err := NewMPReadingResumeReader(s)
	if err == nil {
		r.conversion = true
	}
	return r, err
}

func (r *MPReadingResumeReader) Select(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in MPReadingResumeInput) (MPReadingResume, BrowseReadResult, error) {
	if r == nil {
		return MPReadingResume{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (MPReadingResume, BrowseReadResult, error) {
		in.View.Now = at
		return r.selectResume(ctx, request, budget, output, in)
	})
}

func (r *MPReadingResumeReader) selectResume(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in MPReadingResumeInput) (MPReadingResume, BrowseReadResult, error) {
	if r == nil || in.Item <= 0 || in.View.Kind != 20 && in.View.Kind != 21 || in.View.After != nil || in.View.Descending || in.View.KeepArticles {
		return MPReadingResume{}, BrowseReadResult{}, ErrInput
	}
	in.View.policy = readPolicyBinding{recipe: mpReadingResumeNativePolicy, subject: in.Item}
	if r.conversion {
		in.View.policy.recipe = mpReadingResumeConversionPolicy
	}
	// The shared fence validates this recipe in both snapshots, so personal,
	// child and source facts are compared at final publication as well. The
	// extra scalar points stay under this bounded call recipe; payload bytes are
	// charged by their own octet points.
	windows := &rsByteWindow{source: r.snapshots, preflightCalls: 48}
	return publishRead(ctx, windows, request, budget, output, in.View, []readStep[MPReadingResume]{func(ctx context.Context, q browseQuery, f browseFence, out *MPReadingResume) error {
		got, err := mpReadingResumeSelect(q, in.View.Library, in.View.Kind, in.Item, f.library, f.profile, r.conversion)
		if err != nil {
			return err
		}
		*out = got
		return ctx.Err()
	}})
}

func mpReadingResumeSelect(q browseQuery, library, kind, item int64, libraryName, profile string, conversion bool) (MPReadingResume, error) {
	if item <= 0 || library <= 0 || kind != 20 && kind != 21 || profile == "" || len(profile) > 256 || !mpReadingIdentifier(profile, 256) {
		return MPReadingResume{}, ErrInput
	}
	// The library kind is part of Reading eligibility even when the viewer has
	// no saved version, so it is proved before the personal point.
	rows, err := q("mp.reading.library", libraryName)
	if err != nil {
		return MPReadingResume{}, err
	}
	if len(rows) != 1 || len(rows[0]) != 1 {
		return MPReadingResume{}, ErrDenied
	}
	if kind == 20 && rows[0][0] != "book" || kind == 21 && rows[0][0] != "comic" {
		return MPReadingResume{}, ErrDenied
	}
	rows, err = q("mp.reading.item", item, library, kind)
	if err != nil {
		return MPReadingResume{}, err
	}
	if len(rows) != 1 || len(rows[0]) != 3 {
		return MPReadingResume{}, ErrDenied
	}
	public, ok := rows[0][0].([]byte)
	if !ok || len(public) != 16 || rows[0][1].(int64) != kind {
		return MPReadingResume{}, ErrUnqualified
	}
	if rows[0][2].(int64) != 0 {
		return MPReadingResume{}, ErrDenied
	}
	resume := MPReadingResume{Item: item}
	copy(resume.PublicID[:], public)

	rows, err = q("mp.reading.resume.personal", profile, item)
	if err != nil {
		return MPReadingResume{}, err
	}
	if len(rows) > 1 {
		return MPReadingResume{}, ErrUnqualified
	}
	if len(rows) == 0 {
		return resume, nil
	}
	if len(rows[0]) != 4 {
		return MPReadingResume{}, ErrUnqualified
	}
	personal, ok := rows[0][0].(int64)
	if !ok || personal <= 0 {
		return MPReadingResume{}, ErrUnqualified
	}
	// A retained personal row cannot attach to another public identity merely
	// because a native item id happens to match.
	personalPublic, ok := rows[0][1].([]byte)
	if !ok || len(personalPublic) != 16 || !bytes.Equal(public, personalPublic) {
		return MPReadingResume{}, ErrUnqualified
	}
	personalRevision, ok := rows[0][2].(int64)
	if !ok || personalRevision < 1 {
		return MPReadingResume{}, ErrUnqualified
	}
	resume.PersonalRevision = personalRevision
	if rows[0][3] != nil {
		readingRevision, ok := rows[0][3].(int64)
		if !ok || readingRevision < 0 || readingRevision > mpReadingResumeMaxRevision {
			return MPReadingResume{}, ErrUnqualified
		}
		resume.ReadingRevision = readingRevision
	}

	// Latest (observed_ms DESC,asset_id DESC) is chosen first. Completion and
	// current-source eligibility are tested only after this choice.
	rows, err = q("mp.reading.resume.latest", personal)
	if err != nil {
		return MPReadingResume{}, err
	}
	if len(rows) > 1 {
		return MPReadingResume{}, ErrUnqualified
	}
	if len(rows) == 0 {
		return resume, nil
	}
	if len(rows[0]) != 4 {
		return MPReadingResume{}, ErrUnqualified
	}
	asset, ok := rows[0][0].(int64)
	if !ok || asset <= 0 {
		return MPReadingResume{}, ErrUnqualified
	}
	observed, ok := rows[0][1].(int64)
	if !ok || observed < 0 || observed > mpReadingResumeMaxObserved {
		return MPReadingResume{}, ErrUnqualified
	}
	childRevision, ok := rows[0][2].(int64)
	if !ok || childRevision < 1 || childRevision > mpReadingResumeMaxRevision {
		return MPReadingResume{}, ErrUnqualified
	}
	latestProgression, ok := rows[0][3].(float64)
	if !ok || !mpReadingResumeProgression(latestProgression) {
		return MPReadingResume{}, ErrUnqualified
	}

	// The selected child is read by its complete native key, with its variable
	// bytes charged before the payload is copied in this same snapshot.
	rows, err = q("mp.reading.resume.child", personal, asset)
	if err != nil {
		return MPReadingResume{}, err
	}
	if len(rows) != 1 {
		return MPReadingResume{}, ErrStale
	}
	location, err := decodeMPReadingLocation(asset, rows[0])
	if err != nil {
		return MPReadingResume{}, err
	}
	if location.ObservedMS != observed || location.Revision != childRevision || location.Progression != latestProgression {
		return MPReadingResume{}, ErrUnqualified
	}
	resume.Location = &location
	resume.Completed = location.Progression >= 1

	// The saved source is only an observation. Current disclosure and the exact
	// canonical object are re-proved by the MP-01 typed decoder; a missing or
	// unavailable source marks this saved version stale instead of denying the
	// whole title or falling back to an older version.
	source, err := mpReadingSourceSelect(q, library, kind, item, asset, libraryName, conversion)
	if err != nil {
		if err == ErrDenied {
			resume.Stale = true
			return resume, nil
		}
		return MPReadingResume{}, err
	}
	resume.Converted = source.Converted
	resume.Stale = source.Token != location.SourceToken || source.PresentationFormat != location.Format
	if !source.Converted && location.SourceRevision != source.Revision() {
		// A native saved revision must be the exact current observation. The
		// converted provenance revision is deliberately never compared with the
		// native one.
		resume.Stale = true
	}
	if !resume.Stale {
		resume.Source = &source
	}
	return resume, nil
}

// decodeMPReadingLocation validates one complete selected child row. The stored
// CHECK constraints are not authority: a torn or non-native row refuses here.
func decodeMPReadingLocation(asset int64, row []any) (MPReadingLocation, error) {
	if asset <= 0 || len(row) != 9 {
		return MPReadingLocation{}, ErrUnqualified
	}
	token, ok := row[0].(string)
	if !ok || !mpReadingIdentifier(token, 512) {
		return MPReadingLocation{}, ErrUnqualified
	}
	revision, ok := row[1].(string)
	if !ok || len(revision) != 64 {
		return MPReadingLocation{}, ErrUnqualified
	}
	if _, err := hex.DecodeString(revision); err != nil || strings.ToLower(revision) != revision {
		return MPReadingLocation{}, ErrUnqualified
	}
	format, ok := row[2].(string)
	if !ok || !mpReadingResumeFormat(format) {
		return MPReadingLocation{}, ErrUnqualified
	}
	location := MPReadingLocation{AssetID: asset, SourceToken: token, SourceRevision: revision, Format: format}
	if row[3] != nil {
		cfi, ok := row[3].(string)
		if !ok || len(cfi) == 0 || len(cfi) > mpReadingResumeMaxCFI || !utf8.ValidString(cfi) || strings.IndexFunc(cfi, mpReadingResumeControl) >= 0 {
			return MPReadingLocation{}, ErrUnqualified
		}
		location.CFI = cfi
	}
	if row[4] != nil {
		section, ok := row[4].(string)
		if !ok || len(section) > mpReadingResumeMaxSection || !utf8.ValidString(section) || strings.IndexFunc(section, mpReadingResumeControl) >= 0 {
			return MPReadingLocation{}, ErrUnqualified
		}
		location.Section = section
	}
	var page *int64
	if row[5] != nil {
		value, ok := row[5].(int64)
		if !ok || value < 0 || value > mpReadingResumeMaxPage {
			return MPReadingLocation{}, ErrUnqualified
		}
		page = &value
	}
	location.PageIndex = page
	progression, ok := row[6].(float64)
	if !ok || !mpReadingResumeProgression(progression) {
		return MPReadingLocation{}, ErrUnqualified
	}
	location.Progression = progression
	revisionNumber, ok := row[7].(int64)
	if !ok || revisionNumber < 1 || revisionNumber > mpReadingResumeMaxRevision {
		return MPReadingLocation{}, ErrUnqualified
	}
	location.Revision = revisionNumber
	observed, ok := row[8].(int64)
	if !ok || observed < 0 || observed > mpReadingResumeMaxObserved {
		return MPReadingLocation{}, ErrUnqualified
	}
	location.ObservedMS = observed
	// The stored shape is one location per presentation format: EPUB keeps CFI
	// (and optional section), paged formats keep one page number.
	if format == "epub" {
		if location.CFI == "" || page != nil {
			return MPReadingLocation{}, ErrUnqualified
		}
	} else if location.CFI != "" || location.Section != "" || page == nil {
		return MPReadingLocation{}, ErrUnqualified
	}
	return location, nil
}

func mpReadingResumeFormat(format string) bool {
	switch format {
	case "epub", "pdf", "cbz", "cb7", "cbr":
		return true
	}
	return false
}

func mpReadingResumeProgression(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

func mpReadingResumeControl(r rune) bool { return r == 0 || r < 32 || r == 127 }

// mpReadingResumeWitness is the bounded comparable value that joins the shared
// continuation/publication fence. It covers the personal row, the selected
// latest child, the decoded location and the current typed source.
func mpReadingResumeWitness(r MPReadingResume) string {
	location := "-"
	if r.Location != nil {
		page := "-"
		if r.Location.PageIndex != nil {
			page = fmt.Sprint(*r.Location.PageIndex)
		}
		location = fmt.Sprintf("%d|%s|%s|%s|%s|%s|%s|%x|%d|%d", r.Location.AssetID, r.Location.SourceToken, r.Location.SourceRevision, r.Location.Format, r.Location.CFI, r.Location.Section, page, math.Float64bits(r.Location.Progression), r.Location.Revision, r.Location.ObservedMS)
	}
	source := "-"
	if r.Source != nil {
		source = mpReadingSourceWitness(*r.Source)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%x|%d|%d|%s|%s|%t|%t|%t", r.Item, r.PublicID, r.PersonalRevision, r.ReadingRevision, location, source, r.Completed, r.Stale, r.Converted)))
	return hex.EncodeToString(sum[:])
}
