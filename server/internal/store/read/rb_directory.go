package read

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Navigation directory and cross-library ready composition.
//
// The directory is a principal-scope read: it derives the viewer's authorized
// libraries from the SAME-SNAPSHOT principal (role plus account/profile library
// shares) and the actual library mapping. Callers supply no allowed-library
// list, no catalogue selector and no pivot choice. The cross-library reader
// then merges the exact per-library order sources of those same authorized
// libraries; it does not reinterpret a page, sample or caller scope.
//
// Sources that are not landed yet stay explicit named refusals. This file
// introduces no substitute predicate, no fallback to the legacy catalogue and no
// second authority over library shares.

// RBSourceGap names a packet dependency that has not landed. It is a compiled
// literal, never a request field. ErrUnqualified wraps it so ordinary refusal
// handling is unchanged.
type RBSourceGap string

const (
	RBGapDirectoryScope      RBSourceGap = "rb.directory.scope (bounded inventory)"
	RBGapFacetContext        RBSourceGap = "rb.facet_context (RB-08)"
	RBGapFilterMembership    RBSourceGap = "rb.filter_membership (RB-06)"
	RBGapGroupPrepare        RBSourceGap = "rb.group_prepare (RB-12)"
	RBGapGroupLift           RBSourceGap = "rb.group_lift (RB-15)"
	RBGapGroupRelationship   RBSourceGap = "rb.relationship_prepare (RB-11)"
	RBGapTVWorkspace         RBSourceGap = "rb.tv_workspace (RB-14)"
	RBGapHomeRecommendations RBSourceGap = "rb.home_recommendations (RB-22)"
	RBGapReading             RBSourceGap = "rs.reading_browse (RS)"
	RBGapRandom              RBSourceGap = "rb.random (RB-17 rotation cross composition)"
	RBGapResolution          RBSourceGap = "rb.resolution (RB-16 cross composition)"
	RBGapPersonalPrepare     RBSourceGap = "rb.personal_prepare (RB-10 cross composition)"
	RBGapForYou              RBSourceGap = "rs.rec_browse (RS)"
)

func rbSourceGap(name RBSourceGap) error {
	return fmt.Errorf("%w: %s", ErrUnqualified, name)
}

// Navigation states are the only published alternatives. A pending pivot names
// the source that must land; it is never rendered as an exact empty pivot.
type RBDirectoryState string

const (
	RBDirectorySupported RBDirectoryState = "supported"
	RBDirectoryPending   RBDirectoryState = "pending"
)

const (
	// rbDirectoryScopeCapacity bounds one mapped-library inventory window.
	rbDirectoryScopePage = 65
	// rbDirectoryScopePages bounds the complete cold fallback to metadata rows.
	// Directory pages and cross composition are distinct declared recipes: this
	// is not permission to enlarge entity readers.
	rbDirectoryScopePages = 3
	// rbDirectoryCapacity is the complete mapped-library proof ceiling. Above it
	// the directory reports pending preparation instead of a truncated list.
	rbDirectoryCapacity = rbDirectoryScopePages * (rbDirectoryScopePage - 1)
)

const rbDirectoryPolicy readPolicyID = 32510

type rbDirectoryWork struct{}

func (*rbDirectoryWork) readPolicyState() {}

var rbDirectoryPurpose = &rbDirectoryWork{}

func init() {
	registerReadPolicy(readPolicyRecipe{
		id: rbDirectoryPolicy, domain: "rb-directory", kinds: []int64{1},
		stateful: true, scope: readPrincipalScope,
		validate: func(q browseQuery, in BrowseInput, f browseFence) (string, error) {
			if in.policy.state != rbDirectoryPurpose || in.policy.subject != 0 || f.library != "" {
				return "", ErrInput
			}
			_, digest, _, err := readRBDirectoryScope(q, f.profile, rbDirectoryScopePages)
			if err != nil {
				return "", err
			}
			// The exact authorized mapping (including kinds, roots, revisions and
			// each library's viewer revision) joins the shared continuation fence,
			// so a first/final library or scope change cannot publish a torn page.
			return rbDirectoryDigestText(f.profile, digest), nil
		},
	})
}

// RBDirectoryInput is one page of the viewer's library navigation. After is a
// private values-only continuation from this reader; a caller anchor or an
// allowed-library list is not part of the input.
type RBDirectoryInput struct {
	View  BrowseInput
	After *RBDirectoryCursor
}

type RBDirectoryCursor struct {
	fence browseFence
	scope [32]byte
	limit int
	id    int64
}

// RBDirectoryLibrary is one authorized library and its published pivot facts.
// Pending pivots carry a named source gap; they are not successful empty tabs.
type RBDirectoryLibrary struct {
	ID            string
	Name          string
	Kind          string
	Position      int64
	Revision      int64
	OrderRevision int64
	State         RBDirectoryState
	Source        RBSourceGap
	Pivots        []RBDirectoryPivot
}

type RBDirectoryPivot struct {
	ID             string
	LabelKey       string
	EntityKinds    []string
	SupportedViews []string
	DefaultSort    []string
	Aggregate      string
	Browsable      bool
	Members        bool
	State          RBDirectoryState
	Source         RBSourceGap
	Sorts          []string
	OwnerOnlySorts []string
	Paging         string
}

// RBDirectoryPage is either a complete bounded slice of authorized libraries or
// an explicit pending inventory. Pending is empty and non-exhausted; it is never
// a zero-library answer.
type RBDirectoryPage struct {
	Libraries     []RBDirectoryLibrary
	CrossPivots   []RBDirectoryPivot
	Next          *RBDirectoryCursor
	Pending       bool
	PendingSource RBSourceGap
	Exhausted     bool
	RawRows       int
	RawPositions  int
	Statements    int
}

// rbDirectoryLibraryScope is the raw same-snapshot library mapping row. The two
// roots are compared rather than substituted: a torn mapping is not a library.
type rbDirectoryLibraryScope struct {
	catalogID                                     int64
	id, name, kind                                string
	position                                      int64
	bridgeRoot, root                              string
	dataRevision, orderRevision, metadataRevision int64
	viewerRevision                                int64
	ageInput, agePublished, agePending            int64
	origin                                        [16]byte
	originRevision                                int64
}

type rbDirectoryDraft struct {
	page RBDirectoryPage
}

// Directory publishes authorized library/pivot/capability facts only. Every
// window binds its inventory bound plus one and refuses a longer answer; an
// over-cap mapping is explicit pending, never a partial navigation list.
func (r *BrowseReader) Directory(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RBDirectoryInput) (RBDirectoryPage, BrowseReadResult, error) {
	if r == nil {
		return RBDirectoryPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (RBDirectoryPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.directoryWindow(ctx, request, budget, output, in)
	})
}

func (r *BrowseReader) directoryWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RBDirectoryInput) (RBDirectoryPage, BrowseReadResult, error) {
	if r == nil || in.View.After != nil {
		return RBDirectoryPage{}, BrowseReadResult{}, ErrInput
	}
	view := in.View
	view.Library, view.Kind = 0, 1
	view.Descending, view.KeepArticles, view.After = false, false, nil
	view.policy = readPolicyBinding{recipe: rbDirectoryPolicy, state: rbDirectoryPurpose}
	if view.Limit < 1 || view.Limit > MaxPage {
		return RBDirectoryPage{}, BrowseReadResult{}, ErrInput
	}
	draft, result, err := rbPrincipalPublish(ctx, r.snapshots, request, budget, output, view, []readStep[rbDirectoryDraft]{func(ctx context.Context, q browseQuery, f browseFence, d *rbDirectoryDraft) error {
		return readRBDirectoryPage(ctx, q, f, in, d)
	}})
	if err != nil {
		return RBDirectoryPage{}, result, err
	}
	draft.page.Statements = result.LookupStatements + result.PublicationStatements
	return draft.page, result, nil
}

func readRBDirectoryPage(ctx context.Context, q browseQuery, f browseFence, in RBDirectoryInput, d *rbDirectoryDraft) error {
	scope, digest, pending, err := readRBDirectoryScope(q, f.profile, rbDirectoryScopePages)
	if err != nil {
		return err
	}
	if in.After != nil && (in.After.fence != f || in.After.scope != digest || in.After.limit != in.View.Limit) {
		return ErrStale
	}
	page := RBDirectoryPage{RawRows: len(scope), RawPositions: len(scope)}
	if pending {
		if in.After != nil {
			return ErrStale
		}
		page.Pending, page.PendingSource = true, RBGapDirectoryScope
		d.page = page
		return nil
	}
	allowed, err := rbDirectoryAllowed(f)
	if err != nil {
		return err
	}
	after := int64(0)
	if in.After != nil {
		after = in.After.id
	}
	first := in.After == nil
	lastID := int64(0)
	more := false
	for _, lib := range scope {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !(allowed == nil || allowed[lib.id]) || lib.catalogID <= after {
			continue
		}
		if len(page.Libraries) == in.View.Limit {
			more = true
			break
		}
		page.Libraries = append(page.Libraries, rbDirectoryLibraryFact(lib))
		lastID = lib.catalogID
	}
	if more && len(page.Libraries) > 0 {
		// The cursor holds the actual catalogue mapping ID, never a display
		// selector, caller position or allowed-library list.
		page.Next = &RBDirectoryCursor{fence: f, scope: digest, limit: in.View.Limit, id: lastID}
	} else {
		page.Exhausted = true
	}
	if first {
		page.CrossPivots = rbDirectoryCrossPivots()
	}
	d.page = page
	return nil
}

func rbDirectoryLibraryFact(lib rbDirectoryLibraryScope) RBDirectoryLibrary {
	item := RBDirectoryLibrary{ID: lib.id, Name: lib.name, Kind: lib.kind, Position: lib.position, Revision: lib.dataRevision, OrderRevision: lib.orderRevision}
	if lib.bridgeRoot != lib.root {
		item.State, item.Source = RBDirectoryPending, "rb.directory.root"
		return item
	}
	pivots, ok := rbDirectoryPivots(lib.kind)
	if !ok {
		item.State, item.Source = RBDirectoryPending, "rb.directory.kind"
		return item
	}
	item.State, item.Pivots = RBDirectorySupported, pivots
	return item
}

// rbDirectoryPivots is the finite compiled navigation inventory. A pivot either
// has a complete source in this composition (supported) or names the missing
// packet source (pending). Nothing is inferred from a request.
func rbDirectoryPivots(kind string) ([]RBDirectoryPivot, bool) {
	specs, ok := rbDirectoryPivotSpecs[kind]
	if !ok {
		return nil, false
	}
	out := make([]RBDirectoryPivot, 0, len(specs))
	for _, spec := range specs {
		pivot := RBDirectoryPivot{ID: spec.id, LabelKey: spec.label, EntityKinds: append([]string(nil), spec.kinds...), SupportedViews: append([]string(nil), spec.views...), DefaultSort: append([]string(nil), spec.defaultSort...), Aggregate: spec.aggregate, Browsable: spec.browsable, Members: spec.members, State: RBDirectorySupported, Paging: "keyset"}
		if spec.gap != "" {
			pivot.State, pivot.Source, pivot.Paging = RBDirectoryPending, spec.gap, ""
		} else {
			pivot.Sorts = rbDirectoryPivotSorts(spec.kinds)
			if slices.Contains(pivot.Sorts, string(SortFileSize)) {
				pivot.OwnerOnlySorts = []string{string(SortFileSize)}
			}
		}
		out = append(out, pivot)
	}
	return out, true
}

func rbDirectoryPivotSorts(kinds []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, name := range kinds {
		kind, ok := rbDirectoryKind(name)
		if !ok {
			continue
		}
		for _, id := range rbAcrossSupportedSortIDs(kind) {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

func rbDirectoryKind(name string) (int64, bool) {
	switch name {
	case "movie":
		return 1, true
	case "show":
		return 2, true
	case "season":
		return 3, true
	case "episode":
		return 4, true
	case "artist":
		return 5, true
	case "album":
		return 6, true
	case "song":
		return 7, true
	case "book":
		return 8, true
	case "audiobook_file":
		return 9, true
	case "collection":
		return 10, true
	case "author":
		return 13, true
	}
	return 0, false
}

type rbDirectoryPivotSpec struct {
	id, label, aggregate string
	kinds, views         []string
	defaultSort          []string
	browsable, members   bool
	gap                  RBSourceGap
}

var rbDirectoryPivotSpecs = map[string][]rbDirectoryPivotSpec{
	"movie": {
		{id: "discover", label: "library.discover", kinds: []string{"movie"}, views: []string{"rail"}, defaultSort: []string{"title"}, gap: RBGapHomeRecommendations},
		{id: "movies", label: "library.movies", kinds: []string{"movie"}, views: []string{"grid", "list"}, defaultSort: []string{"title"}, browsable: true},
		{id: "collections", label: "library.collections", kinds: []string{"collection"}, views: []string{"grid", "list"}, defaultSort: []string{"title"}, browsable: true, gap: RBGapGroupLift},
		{id: "categories", label: "library.categories", kinds: []string{"category"}, views: []string{"grid"}, defaultSort: []string{"title"}, aggregate: "decade", gap: RBGapGroupPrepare},
	},
	"tv": {
		{id: "discover", label: "library.discover", kinds: []string{"show"}, views: []string{"rail"}, defaultSort: []string{"title"}, gap: RBGapHomeRecommendations},
		{id: "shows", label: "library.shows", kinds: []string{"show"}, views: []string{"grid", "list"}, defaultSort: []string{"title"}, browsable: true},
		{id: "episodes", label: "library.episodes", kinds: []string{"episode"}, views: []string{"list", "grid"}, defaultSort: []string{"title"}, browsable: true, gap: RBGapTVWorkspace},
		{id: "collections", label: "library.collections", kinds: []string{"collection"}, views: []string{"grid", "list"}, defaultSort: []string{"title"}, browsable: true, gap: RBGapGroupLift},
		{id: "categories", label: "library.categories", kinds: []string{"category"}, views: []string{"grid"}, defaultSort: []string{"title"}, aggregate: "decade", gap: RBGapGroupPrepare},
	},
	"anime": {
		{id: "discover", label: "library.discover", kinds: []string{"show"}, views: []string{"rail"}, defaultSort: []string{"title"}, gap: RBGapHomeRecommendations},
		{id: "shows", label: "library.shows", kinds: []string{"show"}, views: []string{"grid", "list"}, defaultSort: []string{"title"}, browsable: true},
		{id: "episodes", label: "library.episodes", kinds: []string{"episode"}, views: []string{"list", "grid"}, defaultSort: []string{"title"}, browsable: true, gap: RBGapTVWorkspace},
		{id: "collections", label: "library.collections", kinds: []string{"collection"}, views: []string{"grid", "list"}, defaultSort: []string{"title"}, browsable: true, gap: RBGapGroupLift},
		{id: "categories", label: "library.categories", kinds: []string{"category"}, views: []string{"grid"}, defaultSort: []string{"title"}, aggregate: "decade", gap: RBGapGroupPrepare},
	},
	"music": {
		{id: "discover", label: "library.discover", kinds: []string{"artist"}, views: []string{"rail"}, defaultSort: []string{"title"}, gap: RBGapHomeRecommendations},
		{id: "artists", label: "library.artists", kinds: []string{"artist"}, views: []string{"grid", "list"}, defaultSort: []string{"title"}, browsable: true},
		{id: "albums", label: "library.albums", kinds: []string{"album"}, views: []string{"grid", "list"}, defaultSort: []string{"title"}, browsable: true},
		{id: "songs", label: "library.songs", kinds: []string{"song"}, views: []string{"list"}, defaultSort: []string{"title"}, browsable: true},
		{id: "genres", label: "library.genres", kinds: []string{"category"}, views: []string{"grid"}, defaultSort: []string{"title"}, aggregate: "genre", gap: RBGapGroupPrepare},
		{id: "playlists", label: "library.tab.playlists", kinds: []string{"playlist"}, views: []string{"grid"}, defaultSort: []string{"title"}, browsable: true, gap: RBGapGroupLift},
	},
	"audiobook": {
		{id: "discover", label: "library.discover", kinds: []string{"book"}, views: []string{"rail"}, defaultSort: []string{"title"}, gap: RBGapHomeRecommendations},
		{id: "authors", label: "library.authors", kinds: []string{"author"}, views: []string{"grid", "list"}, defaultSort: []string{"title"}, browsable: true},
		{id: "books", label: "library.books", kinds: []string{"book"}, views: []string{"grid", "list"}, defaultSort: []string{"title"}, browsable: true},
		{id: "series", label: "library.series", kinds: []string{"book_series"}, views: []string{"grid"}, defaultSort: []string{"title"}, aggregate: "series", gap: RBGapGroupPrepare},
		{id: "collections", label: "library.collections", kinds: []string{"collection"}, views: []string{"grid", "list"}, defaultSort: []string{"title"}, browsable: true, gap: RBGapGroupLift},
		{id: "parts", label: "library.parts", kinds: []string{"audiobook_file"}, views: []string{"list"}, defaultSort: []string{"title"}, browsable: true, members: true, gap: RBGapGroupLift},
	},
	"book": {
		{id: "books", label: "library.tab.books", kinds: []string{"ebook"}, views: []string{"grid", "list"}, defaultSort: []string{"title"}, browsable: true, gap: RBGapReading},
		{id: "authors", label: "library.tab.authors", kinds: []string{"author"}, views: []string{"list"}, defaultSort: []string{"title"}, gap: RBGapReading},
		{id: "series", label: "library.tab.series", kinds: []string{"book_series"}, views: []string{"list"}, defaultSort: []string{"title"}, gap: RBGapReading},
	},
	"comic": {
		{id: "comics", label: "library.tab.volumesAndIssues", kinds: []string{"comic"}, views: []string{"grid", "list"}, defaultSort: []string{"title"}, browsable: true, gap: RBGapReading},
		{id: "series", label: "library.tab.series", kinds: []string{"book_series"}, views: []string{"list"}, defaultSort: []string{"title"}, gap: RBGapReading},
		{id: "authors", label: "library.tab.authors", kinds: []string{"author"}, views: []string{"list"}, defaultSort: []string{"title"}, gap: RBGapReading},
	},
}

// rbDirectoryCrossPivots declares the published cross-library surfaces. Their
// exact source is this task's same-sort merge; filters and remaining orders name
// their missing packet sources.
func rbDirectoryCrossPivots() []RBDirectoryPivot {
	return []RBDirectoryPivot{
		{ID: "titles", LabelKey: "library.titles", EntityKinds: []string{"movie", "show", "album", "book"}, SupportedViews: []string{"grid", "list"}, DefaultSort: []string{"title"}, Browsable: true, State: RBDirectorySupported, Sorts: rbDirectoryPivotSorts([]string{"movie", "show", "album", "book"}), Paging: "keyset"},
		{ID: "video-titles", LabelKey: "library.titles", EntityKinds: []string{"movie", "show"}, SupportedViews: []string{"grid", "list"}, DefaultSort: []string{"title"}, Browsable: true, State: RBDirectorySupported, Sorts: rbDirectoryPivotSorts([]string{"movie", "show"}), Paging: "keyset"},
		{ID: "items", LabelKey: "library.items", EntityKinds: []string{"movie", "episode", "song", "audiobook_file"}, SupportedViews: []string{"list", "grid"}, DefaultSort: []string{"title"}, Browsable: true, State: RBDirectorySupported, Sorts: rbDirectoryPivotSorts([]string{"movie", "episode", "song", "audiobook_file"}), Paging: "keyset"},
	}
}

// rbDirectoryAllowed derives membership and profile share scope from the actual
// principal fence. An owner bypasses only the account library share, never a
// profile restriction; this mirrors the common permission fence exactly. A nil
// map means every mapped library (owner with no profile restriction).
func rbDirectoryAllowed(f browseFence) (map[string]bool, error) {
	var member, profile []string
	if err := json.Unmarshal([]byte(f.memberLibraries), &member); err != nil {
		return nil, ErrDenied
	}
	if f.profileRestricted {
		if err := json.Unmarshal([]byte(f.profileLibraries), &profile); err != nil {
			return nil, ErrDenied
		}
	}
	if f.role == "owner" && !f.profileRestricted {
		return nil, nil
	}
	allowed := make(map[string]bool, len(member)+len(profile))
	if f.role != "owner" {
		for _, id := range member {
			allowed[id] = true
		}
	}
	if !f.profileRestricted {
		return allowed, nil
	}
	narrowed := make(map[string]bool, len(profile))
	for _, id := range profile {
		if f.role == "owner" || allowed[id] {
			narrowed[id] = true
		}
	}
	return narrowed, nil
}

// readRBDirectoryScope streams the complete mapped-library inventory by exact
// integer mapping key. Every window binds page+1 and checks len > page; a short
// window is the only EOF. The principal's share check happens against the
// returned actual mapping in the caller.
func readRBDirectoryScope(q browseQuery, profile string, maxPages int) ([]rbDirectoryLibraryScope, [32]byte, bool, error) {
	var all []rbDirectoryLibraryScope
	after := int64(0)
	for page := 0; page < maxPages; page++ {
		rows, err := q("rb.directory.scope", profile, after, int64(rbDirectoryScopePage))
		if err != nil {
			return nil, [32]byte{}, false, err
		}
		if len(rows) > rbDirectoryScopePage {
			return nil, [32]byte{}, false, ErrUnqualified
		}
		for _, row := range rows {
			lib, err := rbDirectoryScopeRow(row)
			if err != nil {
				return nil, [32]byte{}, false, err
			}
			all = append(all, lib)
		}
		if len(rows) < rbDirectoryScopePage {
			return all, rbDirectoryScopeDigest(all), false, nil
		}
		after = rows[len(rows)-1][0].(int64)
	}
	return all, rbDirectoryScopeDigest(all), true, nil
}

func rbDirectoryScopeRow(row []any) (rbDirectoryLibraryScope, error) {
	var lib rbDirectoryLibraryScope
	if len(row) != 16 {
		return lib, ErrUnqualified
	}
	var ok bool
	if lib.catalogID, ok = row[0].(int64); !ok || lib.catalogID <= 0 {
		return lib, ErrUnqualified
	}
	if lib.id, ok = row[1].(string); !ok || lib.id == "" {
		return lib, ErrUnqualified
	}
	if lib.name, ok = row[2].(string); !ok {
		return lib, ErrUnqualified
	}
	if lib.kind, ok = row[3].(string); !ok || lib.kind == "" {
		return lib, ErrUnqualified
	}
	if lib.position, ok = row[4].(int64); !ok {
		return lib, ErrUnqualified
	}
	if lib.bridgeRoot, ok = row[5].(string); !ok || lib.bridgeRoot == "" {
		return lib, ErrUnqualified
	}
	if lib.root, ok = row[6].(string); !ok || lib.root == "" {
		return lib, ErrUnqualified
	}
	for n, target := range []*int64{&lib.dataRevision, &lib.orderRevision, &lib.metadataRevision, &lib.viewerRevision, &lib.ageInput, &lib.agePublished, &lib.agePending} {
		v, ok := row[7+n].(int64)
		if !ok {
			return lib, ErrUnqualified
		}
		*target = v
	}
	origin, ok := row[14].([]byte)
	if !ok || len(origin) != 16 {
		return lib, ErrUnqualified
	}
	copy(lib.origin[:], origin)
	if lib.originRevision, ok = row[15].(int64); !ok {
		return lib, ErrUnqualified
	}
	return lib, nil
}

func rbDirectoryScopeDigest(scope []rbDirectoryLibraryScope) [32]byte {
	h := sha256.New()
	var data [8]byte
	write := func(value any) {
		switch x := value.(type) {
		case nil:
			h.Write([]byte{'n'})
		case int64:
			h.Write([]byte{'i'})
			binary.BigEndian.PutUint64(data[:], uint64(x))
			h.Write(data[:])
		case []byte:
			h.Write([]byte{'b'})
			binary.BigEndian.PutUint64(data[:], uint64(len(x)))
			h.Write(data[:])
			h.Write(x)
		case string:
			h.Write([]byte{'s'})
			binary.BigEndian.PutUint64(data[:], uint64(len(x)))
			h.Write(data[:])
			h.Write([]byte(x))
		}
	}
	for _, lib := range scope {
		write(lib.catalogID)
		write(lib.id)
		write(lib.name)
		write(lib.kind)
		write(lib.position)
		write(lib.bridgeRoot)
		write(lib.root)
		write(lib.dataRevision)
		write(lib.orderRevision)
		write(lib.metadataRevision)
		write(lib.viewerRevision)
		write(lib.ageInput)
		write(lib.agePublished)
		write(lib.agePending)
		write(lib.origin[:])
		write(lib.originRevision)
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func rbDirectoryDigestText(profile string, digest [32]byte) string {
	return "rb-directory-v1:" + profile + ":" + fmt.Sprintf("%x", digest)
}

// rbPrincipalPublish is the shared two-window publication protocol for the two
// principal-scope readers in this file. It retains publishRead's exact first and
// fresh final shared fence plus one complete retry, and aggregates the evidence
// of both attempts. The scope digest is carried by the policy witness, so a
// library mapping, kind, root or revision change cannot publish a torn page.
func rbPrincipalPublish[T any](ctx context.Context, snapshots browseSnapshots, request governor.Request, budget time.Duration, output BrowseOutput, view BrowseInput, steps []readStep[T]) (T, BrowseReadResult, error) {
	var zero T
	started := time.Now()
	deadline := started.Add(min(budget, time.Second))
	if request.Deadline.Before(deadline) {
		deadline = request.Deadline
	}
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	if !time.Now().Before(deadline) {
		return zero, BrowseReadResult{}, context.DeadlineExceeded
	}
	var result BrowseReadResult
	var page T
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if !time.Now().Before(deadline) {
			return zero, result, context.DeadlineExceeded
		}
		var one BrowseReadResult
		page, one, err = publishRead(ctx, snapshots, request, time.Until(deadline), output, view, steps)
		result.Lookup = one.Lookup
		result.Publication = one.Publication
		result.Refusal = one.Refusal
		result.LookupStatements += one.LookupStatements
		result.PublicationStatements += one.PublicationStatements
		result.LookupWindows = append(result.LookupWindows, one.LookupWindows...)
		if !one.Publication.Started.IsZero() {
			result.PublicationWindows = append(result.PublicationWindows, one.Publication)
		}
		result.Attempts = attempt + 1
		if !errors.Is(err, ErrStale) || ctx.Err() != nil || !time.Now().Before(deadline) {
			break
		}
	}
	result.Elapsed = time.Since(started)
	if errors.Is(err, ErrStale) {
		result.Refusal = &governor.Refusal{Code: governor.Conflict}
		err = errors.Join(err, governor.ErrBusy)
	}
	return page, result, err
}
