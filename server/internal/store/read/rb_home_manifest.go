package read

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

const homeManifestLibraryCapacity = 16
const homeManifestQueries = 1024

type HomeManifestReader struct{ native *SavedViewReader }

func NewHomeManifestReader(pool *governor.Snapshots) (*HomeManifestReader, error) {
	r, e := NewSavedViewReader(pool)
	if e != nil {
		return nil, e
	}
	return &HomeManifestReader{native: r}, nil
}

type HomeManifestInput struct{ View BrowseInput }
type HomeManifestLayout struct {
	Revision               int64
	Present                bool
	RowOrder, HiddenRowIDs []string
}
type HomeManifestLibrary struct {
	ID, Name, Kind, CatalogueState                                                    string
	Position, NativeID, DataRevision, OrderRevision, MetadataRevision, ViewerRevision int64
}
type HomeManifestDescriptor struct {
	PrivacySensitivity, Family, TargetKind                           string
	Platforms                                                        []string
	ID, Title, TitleCode, Kind, ArtworkShape, LibraryID              string
	PolicyState, ReadState, Reason                                   string
	Required, Hideable, Reorderable, Critical, CursorCapable, Hidden bool
	Priority, CacheTTLMs                                             int
	ResourceID                                                       string
	ResourceRevision                                                 int64
	defaultHidden                                                    bool
}
type HomeManifestDocument struct {
	Layout    HomeManifestLayout
	Libraries []HomeManifestLibrary
	// Catalogue keeps explicit unsupported/policy states. LayoutRows excludes
	// owner-disabled/unresolved policy; Rows also applies saved visibility and
	// the public 64-row cap, preserving required rows. None contains content.
	Catalogue, LayoutRows, Rows                []HomeManifestDescriptor
	Pending, SourceExhausted                   bool
	RawLibraryPositions                        int
	SpotlightState, GeneratedAt, PendingReason string
	context                                    *homeManifestFrame
}
type homeManifestLibraryFact struct {
	ID                     string
	Row, Catalogue, Viewer [][]any
}
type homeManifestViewFact struct {
	ID, State string
	Resource  savedViewResource
	Fence     browseFence
}
type homeManifestFrame struct {
	Principal                                                   browseFence
	Scope, OrderKey, HiddenKey, OrderPositions, HiddenPositions [][]any
	Community, CommunitySource, SettingsRevision                [][]any
	Layout                                                      HomeManifestLayout
	LibraryIDs                                                  []string
	LibraryFacts                                                []homeManifestLibraryFact
	Libraries                                                   []HomeManifestLibrary
	Views                                                       []homeManifestViewFact
	ViewDescriptors                                             []HomeManifestDescriptor
	Pending                                                     bool
}

func homeManifestArgs(f browseFence) []any {
	return []any{"local", f.account, f.profile, "profile-server", ""}
}
func homeManifestReadList(s *savedViewSession, q browseQuery, f browseFence, key string) ([]string, [][]any, [][]any, error) {
	args := append(homeManifestArgs(f), key)
	info, e := q("rb.home.manifest.key", args...)
	if e != nil {
		return nil, nil, nil, e
	}
	if len(info) > 1 {
		return nil, nil, nil, ErrUnqualified
	}
	if len(info) == 0 {
		return []string{}, info, nil, nil
	}
	if len(info[0]) != 3 || info[0][0] != nil || info[0][1] != nil || info[0][2] != nil {
		return nil, nil, nil, ErrUnqualified
	}
	positions, e := q("rb.home.manifest.positions", args...)
	if e != nil {
		return nil, nil, nil, e
	}
	if len(positions) > 64 {
		return nil, nil, nil, ErrUnqualified
	}
	values := make([]string, 0, len(positions))
	seen := make(map[string]bool, len(positions))
	for _, row := range positions {
		pos, ok := row[0].(int64)
		if !ok || pos < 0 || pos >= 64 {
			return nil, nil, nil, ErrUnqualified
		}
		point := append(append([]any{}, args...), pos)
		sizes, e := q("rb.home.manifest.list.bytes", point...)
		if e != nil {
			return nil, nil, nil, e
		}
		if len(sizes) != 1 {
			return nil, nil, nil, ErrStale
		}
		n, ok := sizes[0][0].(int64)
		// The actual byte point precedes hydration; 128 Unicode row-id runes need
		// at most 512 UTF-8 bytes. This is a validated field domain, not a row forecast.
		if !ok || n < 1 || n > 512 {
			return nil, nil, nil, ErrUnqualified
		}
		if 96+2*n > s.copyLimit-s.copied {
			return nil, nil, nil, governor.ErrSnapshotBudget
		}
		value, e := q("rb.home.manifest.list.value", point...)
		if e != nil {
			return nil, nil, nil, e
		}
		if len(value) != 1 {
			return nil, nil, nil, ErrStale
		}
		v, ok := value[0][0].(string)
		if !ok || int64(len(v)) != n || !utf8.ValidString(v) || utf8.RuneCountInString(v) > 128 || seen[v] {
			return nil, nil, nil, ErrUnqualified
		}
		seen[v] = true
		values = append(values, v)
	}
	return values, info, positions, nil
}
func homeManifestAllowedLibraries(s *savedViewSession, q browseQuery, f browseFence) ([]string, bool, error) {
	if e := s.reserve(3 * int64(len(f.memberLibraries)+len(f.profileLibraries))); e != nil {
		return nil, false, e
	}
	var member, profile []string
	if json.Unmarshal([]byte(f.memberLibraries), &member) != nil {
		return nil, false, ErrUnqualified
	}
	if f.profileRestricted && json.Unmarshal([]byte(f.profileLibraries), &profile) != nil {
		return nil, false, ErrUnqualified
	}
	var ids []string
	if f.role == "owner" && !f.profileRestricted {
		rows, e := q("rb.home.manifest.libraries", "")
		if e != nil {
			return nil, false, e
		}
		if len(rows) > homeManifestLibraryCapacity+1 {
			return nil, false, ErrUnqualified
		}
		for _, row := range rows {
			ids = append(ids, row[0].(string))
		}
		return ids, len(ids) > homeManifestLibraryCapacity, nil
	}
	if f.role == "owner" {
		member = profile
	}
	allowed := map[string]bool{}
	for _, id := range profile {
		allowed[id] = true
	}
	seen := map[string]bool{}
	for _, id := range member {
		if seen[id] || f.profileRestricted && !allowed[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, len(ids) > homeManifestLibraryCapacity, nil
}
func homeManifestLibraryRead(s *savedViewSession, q browseQuery, f browseFence, id string) (homeManifestLibraryFact, *HomeManifestLibrary, error) {
	fact := homeManifestLibraryFact{ID: id}
	sizes, e := q("rb.home.manifest.library.bytes", id)
	if e != nil {
		return fact, nil, e
	}
	if len(sizes) == 0 {
		return fact, nil, nil
	}
	if len(sizes) != 1 || len(sizes[0]) != 2 {
		return fact, nil, ErrUnqualified
	}
	var total int64
	for _, v := range sizes[0] {
		n, ok := v.(int64)
		if !ok || n < 0 || n > s.nativeLimit-total {
			return fact, nil, governor.ErrSnapshotBudget
		}
		total += n
	}
	if 480+2*total > s.copyLimit-s.copied {
		return fact, nil, governor.ErrSnapshotBudget
	}
	rows, e := q("rb.home.manifest.library", id)
	if e != nil {
		return fact, nil, e
	}
	if len(rows) != 1 || len(rows[0]) != 13 {
		return fact, nil, ErrStale
	}
	fact.Row = rows
	row := rows[0]
	for n := 0; n < 2; n++ {
		v, ok := row[n].(string)
		if !ok || int64(len(v)) != sizes[0][n].(int64) || !utf8.ValidString(v) {
			return fact, nil, ErrUnqualified
		}
	}
	fact.Catalogue, e = q("rb.home.manifest.catalogue", id)
	if e != nil {
		return fact, nil, e
	}
	if len(fact.Catalogue) > 1 {
		return fact, nil, ErrUnqualified
	}
	fact.Viewer, e = q("rb.home.manifest.viewer", f.profile, id)
	if e != nil {
		return fact, nil, e
	}
	if len(fact.Viewer) > 1 {
		return fact, nil, ErrUnqualified
	}
	out := &HomeManifestLibrary{ID: id, Name: row[0].(string), Kind: row[1].(string), Position: row[2].(int64), DataRevision: row[3].(int64), OrderRevision: row[4].(int64), MetadataRevision: row[5].(int64), CatalogueState: "unprepared"}
	if len(fact.Viewer) == 1 {
		out.ViewerRevision = fact.Viewer[0][0].(int64)
	}
	if len(fact.Catalogue) == 1 && fact.Catalogue[0][1] == int64(0) {
		out.NativeID = fact.Catalogue[0][0].(int64)
		out.CatalogueState = "current"
		if row[6] != row[7] || row[8] != int64(0) {
			out.CatalogueState = "age_pending"
		}
	}
	return fact, out, nil
}
func homeManifestFrameRead(s *savedViewSession, q browseQuery, in BrowseInput) (homeManifestFrame, error) {
	var frame homeManifestFrame
	f, e := readSavedViewPrincipal(q, s.input(in))
	if e != nil {
		return frame, e
	}
	frame.Principal = f
	args := homeManifestArgs(f)
	frame.Scope, e = q("rb.home.manifest.scope", args...)
	if e != nil {
		return frame, e
	}
	if len(frame.Scope) > 1 {
		return frame, ErrUnqualified
	}
	frame.Layout = HomeManifestLayout{Revision: 1, RowOrder: []string{}, HiddenRowIDs: []string{}}
	if len(frame.Scope) == 1 {
		frame.Layout.Present = true
		frame.Layout.Revision = frame.Scope[0][0].(int64)
	}
	frame.Layout.RowOrder, frame.OrderKey, frame.OrderPositions, e = homeManifestReadList(s, q, f, "home.rowOrder")
	if e != nil {
		return frame, e
	}
	frame.Layout.HiddenRowIDs, frame.HiddenKey, frame.HiddenPositions, e = homeManifestReadList(s, q, f, "home.hiddenRowIds")
	if e != nil {
		return frame, e
	}
	frame.Community, e = q("rb.home.manifest.community", int64(1))
	if e != nil {
		return frame, e
	}
	frame.CommunitySource, e = q("rb.home.manifest.community.source", "home.communityActivityEnabled")
	if e != nil {
		return frame, e
	}
	frame.SettingsRevision, e = q("rb.home.manifest.settings.revision", int64(1))
	if e != nil {
		return frame, e
	}
	if len(frame.Community) > 1 || len(frame.CommunitySource) > 1 || len(frame.SettingsRevision) > 1 {
		return frame, ErrUnqualified
	}
	frame.LibraryIDs, frame.Pending, e = homeManifestAllowedLibraries(s, q, f)
	if e != nil || frame.Pending {
		return frame, e
	}
	for _, id := range frame.LibraryIDs {
		fact, library, e := homeManifestLibraryRead(s, q, f, id)
		if e != nil {
			return frame, e
		}
		frame.LibraryFacts = append(frame.LibraryFacts, fact)
		if library != nil {
			frame.Libraries = append(frame.Libraries, *library)
		}
	}
	sort.Slice(frame.Libraries, func(a, b int) bool {
		x, y := frame.Libraries[a], frame.Libraries[b]
		if x.Position != y.Position {
			return x.Position < y.Position
		}
		if x.Name != y.Name {
			return x.Name < y.Name
		}
		return x.ID < y.ID
	})
	libraries := map[string]HomeManifestLibrary{}
	for _, l := range frame.Libraries {
		libraries[l.ID] = l
	}
	for n, id := range frame.Layout.RowOrder {
		if !strings.HasPrefix(id, "view:") {
			continue
		}
		resourceID := strings.TrimPrefix(id, "view:")
		if resourceID == "" {
			continue
		}
		r, e := s.resource(q, f, resourceID)
		if errors.Is(e, ErrDenied) {
			frame.Views = append(frame.Views, homeManifestViewFact{ID: id, State: "denied"})
			continue
		}
		if e != nil {
			return frame, e
		}
		fact := homeManifestViewFact{ID: id, Resource: r, State: "unsupported"}
		definition, e := savedViewStoredDefinition(r)
		if e != nil {
			frame.Views = append(frame.Views, fact)
			continue
		}
		library, allowed := libraries[definition.LibraryID]
		if !allowed {
			fact.State = "library_denied"
			frame.Views = append(frame.Views, fact)
			continue
		}
		d := HomeManifestDescriptor{ID: id, Title: r.name, Kind: "saved_view", ArtworkShape: homeManifestArtwork(library.Kind), LibraryID: library.ID, PolicyState: "available", ReadState: "unsupported", Reason: "stored_view_recipe_unsupported", Hideable: true, Reorderable: true, CursorCapable: true, PrivacySensitivity: "personal", TargetKind: "saved_view", Priority: 90 + n, CacheTTLMs: 60000, ResourceID: r.id, ResourceRevision: r.revision}
		if library.Kind == "movie" && library.NativeID > 0 && library.CatalogueState == "current" {
			target := in
			target.Library = library.NativeID
			target.Kind = 1
			target.After = nil
			target.Descending = false
			target.KeepArticles = false
			actual, e := readBrowseFence(q, s.input(target))
			if e != nil && !errors.Is(e, ErrDenied) && !errors.Is(e, ErrUnqualified) {
				return frame, e
			}
			if e == nil {
				fact.Fence = actual
				bound := r
				bound.fence = actual
				if _, _, e = savedViewExecutionPlan(bound, target); e == nil {
					d.ReadState = "deferred"
					d.Reason = ""
					fact.State = "current"
				}
			}
		}
		frame.Views = append(frame.Views, fact)
		frame.ViewDescriptors = append(frame.ViewDescriptors, d)
	}
	return frame, nil
}

// Read is descriptor/layout publication, NEVER a successful empty row preview.
// Recommendations and live content stay explicit unsupported sources; no caller
// permissions, fabricated viewer, legacy service, settings write or provider call.
func (r *HomeManifestReader) Read(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in HomeManifestInput) (HomeManifestDocument, BrowseReadResult, error) {
	if r == nil || r.native == nil || in.View.After != nil {
		return HomeManifestDocument{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, out, r.native.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (HomeManifestDocument, BrowseReadResult, error) {
		in.View.Now = at
		s, e := newSavedViewSession(r.native, ctx, request, budget, out, in.View)
		if e != nil {
			return HomeManifestDocument{}, BrowseReadResult{}, e
		}
		// One bounded scratch hold covers the shared closed AST decoder, reused
		// synchronously across descriptors/snapshots, never retained as row content.
		if e = s.reserve(1 << 20); e != nil {
			return HomeManifestDocument{}, s.result(), e
		}
		for attempt := 0; attempt < 2; attempt++ {
			s.cost.Attempts = attempt + 1
			var first homeManifestFrame
			e = s.window(false, homeManifestQueries, func(q browseQuery) error { var e error; first, e = homeManifestFrameRead(s, q, in.View); return e })
			if e == nil {
				var doc HomeManifestDocument
				if e = s.reserve(2048 * int64(12+len(first.ViewDescriptors))); e == nil {
					doc = homeManifestProject(first, s.input(in.View).Now)
				}
				prior := e
				final := s.window(true, homeManifestQueries, func(q browseQuery) error {
					now, e := homeManifestFrameRead(s, q, in.View)
					if e != nil {
						return e
					}
					if !reflect.DeepEqual(first, now) {
						return ErrStale
					}
					return nil
				})
				e = s.finish(first.Principal, prior, final)
				if e == nil {
					return doc, s.result(), nil
				}
			}
			if !errors.Is(e, ErrStale) || ctx.Err() != nil || !time.Now().Before(s.deadline) {
				break
			}
		}
		if errors.Is(e, ErrStale) {
			s.cost.Refusal = &governor.Refusal{Code: governor.Conflict}
			e = errors.Join(e, governor.ErrBusy)
		}
		return HomeManifestDocument{}, s.result(), e
	})
}
