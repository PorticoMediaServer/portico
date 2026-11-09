package read

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Cross-library ready composition.
//
// A cross-library page is a disjoint merge of the SAME exact order sources used
// by one library: title, accepted chronology, or a landed scalar order. Each
// participating authorized library keeps its own source cursor and its own
// mapping/revision/root fence; the page cursor binds the complete authorized
// scope digest. No filter, group, recommendation or personal order is invented
// here: those sources name their packet dependency instead.
//
// The shared call budget leaves four stream reads in the first window (after
// the principal/permission fence and the same-snapshot scope proof) and eight
// in the second, so a FIRST page over twelve authorized libraries fits exactly
// two windows. A CONTINUATION walks cursor positions two at a time in the first
// window and four per later window, so twelve libraries take four windows
// (2+4+4+2), not two. A larger scope is explicit pending preparation, never a
// partial page.
const (
	rbAcrossPolicy         readPolicyID = 32511
	rbAcrossFirstLibraries              = 4
	rbAcrossMaxLibraries                = rbAcrossFirstLibraries + 8
	rbAcrossScopePage                   = 65
	rbAcrossScopePages                  = 1
)

type rbAcrossWork struct{}

func (*rbAcrossWork) readPolicyState() {}

var rbAcrossPurpose = &rbAcrossWork{}

func init() {
	registerReadPolicy(readPolicyRecipe{
		id: rbAcrossPolicy, domain: "rb-across", kinds: []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		stateful: true, scope: readPrincipalScope,
		validate: func(q browseQuery, in BrowseInput, f browseFence) (string, error) {
			if in.policy.state != rbAcrossPurpose || in.policy.subject != 0 || f.library != "" {
				return "", ErrInput
			}
			_, digest, _, err := readRBDirectoryScope(q, f.profile, rbAcrossScopePages)
			if err != nil {
				return "", err
			}
			return rbDirectoryDigestText(f.profile, digest), nil
		},
	})
}

// RBAcrossSort is one closed same-sort source family, not an SQL identifier.
type RBAcrossSort string

const (
	RBAcrossTitle        RBAcrossSort = "title"
	RBAcrossAdded        RBAcrossSort = "added"
	RBAcrossContentAdded RBAcrossSort = "contentAdded"
)

// RBAcrossInput names one exact sort over the viewer's authorized libraries.
// After is a private continuation; View.After, View.Library and any caller
// allowed-library list are not part of cross-library authority.
type RBAcrossInput struct {
	View  BrowseInput
	Sort  RBAcrossSort
	After *RBAcrossCursor
}

type rbAcrossPosition struct {
	key     any
	null    bool
	id      int64
	started bool
}

type RBAcrossCursor struct {
	fence browseFence
	scope [32]byte
	sort  RBAcrossSort
	kind  int64
	desc  bool
	keep  bool
	limit int
	pos   []rbAcrossPosition
}

// RBAcrossPage is an exact merged page, or an explicit pending scope. Pending is
// empty and non-exhausted; it is never a successful zero-title answer.
type RBAcrossPage struct {
	Items         []BrowseItem
	Next          *RBAcrossCursor
	Pending       bool
	PendingSource RBSourceGap
	Exhausted     bool
	RawRows       int
	RawPositions  int
	Statements    int
}

type rbAcrossDraft struct {
	page      RBAcrossPage
	scope     []rbDirectoryLibraryScope
	allowed   []rbDirectoryLibraryScope
	digest    [32]byte
	per       int
	fetched   [][][]any
	exhausted []bool
	pos       []rbAcrossPosition
	pending   bool
}

// AcrossWindow publishes one bounded same-sort page across the viewer's
// libraries. It never accepts a caller allowed-library list and never reads a
// library outside the derived principal scope.
// rbWideSnapshots raises the compiled-statement bound for the cross-library
// merge. One window fetches the fence plus each authorized library's next
// bounded legs; the recipe stays finite and is never caller-supplied.
type rbWideSnapshots struct{ browseSnapshots }

const rbWideMaxQueries = 32

func (w rbWideSnapshots) Read(ctx context.Context, r governor.Request, budget time.Duration, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	maxQueries := int(r.Budget.RawRows)
	if maxQueries > rbWideMaxQueries {
		maxQueries = rbWideMaxQueries
	}
	if wide, ok := w.browseSnapshots.(*governor.Snapshots); ok && maxQueries > 0 {
		return wide.ReadForecasted(ctx, r, budget, governor.SnapshotForecast{NativeRowBytes: wide.NativeRowReservation(), MaxQueries: uint16(maxQueries)}, read)
	}
	return w.browseSnapshots.Read(ctx, r, budget, read)
}

func (r *BrowseReader) AcrossWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RBAcrossInput) (RBAcrossPage, BrowseReadResult, error) {
	if r == nil {
		return RBAcrossPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (RBAcrossPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.acrossWindow(ctx, request, budget, output, in)
	})
}

func (r *BrowseReader) acrossWindow(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in RBAcrossInput) (RBAcrossPage, BrowseReadResult, error) {
	if r == nil || in.View.After != nil || request.Budget.RawRows < MaxRaw+5 {
		return RBAcrossPage{}, BrowseReadResult{}, ErrInput
	}
	view := in.View
	view.Library = 0
	view.After = nil
	view.policy = readPolicyBinding{recipe: rbAcrossPolicy, state: rbAcrossPurpose}
	if view.Limit < 1 || view.Limit > MaxPage {
		return RBAcrossPage{}, BrowseReadResult{}, ErrInput
	}
	spec, err := rbAcrossSpecFor(in.Sort, view.Kind, view.KeepArticles)
	if err != nil {
		return RBAcrossPage{}, BrowseReadResult{}, err
	}
	if in.After != nil {
		if in.After.sort != in.Sort || in.After.kind != view.Kind || in.After.desc != view.Descending || in.After.keep != view.KeepArticles || in.After.limit != view.Limit {
			return RBAcrossPage{}, BrowseReadResult{}, ErrStale
		}
	}
	steps := rbAcrossSteps(in, spec)
	draft, result, err := rbPrincipalPublish(ctx, rbWideSnapshots{r.snapshots}, request, budget, output, view, steps)
	if err != nil {
		return RBAcrossPage{}, result, err
	}
	page := draft.page
	page.Statements = result.LookupStatements + result.PublicationStatements
	return page, result, nil
}

// rbAcrossSteps sizes the finite two-leg stream recipe for this page. A first
// page has unstarted sub-cursors (one call each): four in the first window and
// eight in the second. A continuation uses the equality-tail/next-key/null
// recipe (two calls each): two in the first window and four per later window.
// A continuation over twelve libraries therefore takes four windows
// (2+4+4+2), not the two windows a first page takes. The cap is explicit
// pending preparation, never a partial merge.
func rbAcrossSteps(in RBAcrossInput, spec rbAcrossSpec) []readStep[rbAcrossDraft] {
	chunks := [][2]int{{0, 4}, {4, 12}}
	if in.After != nil {
		n := len(in.After.pos)
		chunks = [][2]int{{0, min(2, n)}}
		for start := 2; start < n; start += 4 {
			chunks = append(chunks, [2]int{start, min(start+4, n)})
		}
	}
	steps := make([]readStep[rbAcrossDraft], len(chunks))
	for index := range chunks {
		index, start, end := index, chunks[index][0], chunks[index][1]
		steps[index] = func(ctx context.Context, q browseQuery, f browseFence, d *rbAcrossDraft) error {
			if index == 0 {
				if err := rbAcrossScopeStep(ctx, q, f, in, spec, d); err != nil {
					return err
				}
			}
			if !d.pending {
				for i := start; i < end && i < len(d.allowed); i++ {
					if err := rbAcrossFetch(ctx, q, in, spec, d, i); err != nil {
						return err
					}
				}
			}
			if index == len(chunks)-1 {
				return rbAcrossMerge(ctx, f, in, d)
			}
			return nil
		}
	}
	return steps
}

// rbAcrossSpecFor selects only a landed same-sort source. A pending family is a
// named refusal, never a silent title fallback.
func rbAcrossSpecFor(sort RBAcrossSort, kind int64, keep bool) (rbAcrossSpec, error) {
	if kind < 1 || kind > 10 {
		return rbAcrossSpec{}, rbSourceGap(RBGapDirectoryScope)
	}
	switch sort {
	case RBAcrossTitle:
		spec := rbAcrossSpec{sort: sort, name: "title.ignore", column: "sort_key", index: "catalog_entities_title", typ: stmt.Text, title: true}
		if keep {
			spec.name, spec.column, spec.index = "title.keep", "sort_key_keep_articles", "catalog_entities_title_keep_articles"
		}
		return spec, nil
	case RBAcrossAdded:
		return rbAcrossSpec{sort: sort, name: "added", column: "added_at", index: "catalog_entities_added_v1", typ: stmt.Text, nullable: true}, nil
	case RBAcrossContentAdded:
		if kind != 2 && kind != 3 {
			return rbAcrossSpec{sort: RBAcrossAdded, name: "added", column: "added_at", index: "catalog_entities_added_v1", typ: stmt.Text, nullable: true}, nil
		}
		return rbAcrossSpec{sort: sort, name: "contentAdded", column: "last_content_added_at", index: "catalog_entities_content_added", typ: stmt.Text, nullable: true}, nil
	}
	order, ok := rbScalarSpec(ScalarSort(sort))
	if !ok || !order.supports(kind) {
		if source, pending := rbAcrossPendingSort(sort); pending {
			return rbAcrossSpec{}, rbSourceGap(source)
		}
		return rbAcrossSpec{}, ErrInput
	}
	return rbAcrossSpec{sort: sort, name: "scalar." + string(sort), column: order.column, index: order.index, predicate: order.predicate, typ: order.typ, nullable: true}, nil
}

type rbAcrossSpec struct {
	sort                RBAcrossSort
	name, column, index string
	predicate           string
	typ                 stmt.Type
	title, nullable     bool
}

func rbAcrossOrderName(spec rbAcrossSpec, desc bool, phase string) string {
	direction := "asc."
	if desc {
		direction = "desc."
	}
	return "rb.across." + spec.name + "." + direction + phase
}

// rbAcrossScopeStep derives the exact scope and starts the first stream chunk.
// It binds the continuation to the whole scope digest and the exact sort, kind,
// direction, article policy and page size.
func rbAcrossScopeStep(ctx context.Context, q browseQuery, f browseFence, in RBAcrossInput, spec rbAcrossSpec, d *rbAcrossDraft) error {
	scope, digest, pending, err := readRBDirectoryScope(q, f.profile, rbAcrossScopePages)
	if err != nil {
		return err
	}
	d.scope, d.digest = scope, digest
	if in.After != nil && (in.After.fence != f || in.After.scope != digest) {
		return ErrStale
	}
	allowed, err := rbDirectoryAllowed(f)
	if err != nil {
		return err
	}
	for _, lib := range scope {
		if allowed != nil && !allowed[lib.id] {
			continue
		}
		if lib.bridgeRoot != lib.root {
			d.pending = true
			d.page.PendingSource = "rb.directory.root"
			continue
		}
		d.allowed = append(d.allowed, lib)
	}
	if spec.name == "scalar.fileSize" && f.role != "owner" {
		return ErrDenied
	}
	if pending || d.pending || len(d.allowed) > rbAcrossMaxLibraries {
		d.pending = true
		if d.page.PendingSource == "" {
			d.page.PendingSource = RBGapDirectoryScope
		}
		d.page.Pending, d.page.Exhausted = true, false
		return nil
	}
	d.pos = make([]rbAcrossPosition, len(d.allowed))
	if in.After != nil {
		if len(in.After.pos) != len(d.allowed) {
			return ErrStale
		}
		copy(d.pos, in.After.pos)
	}
	d.fetched = make([][][]any, len(d.allowed))
	d.exhausted = make([]bool, len(d.allowed))
	if len(d.allowed) == 0 {
		return nil
	}
	d.per = (MaxRaw - len(d.allowed)) / len(d.allowed)
	if d.per < 1 {
		d.per = 1
	}
	return nil
}

// rbAcrossFetch reads one library's next bounded window through the exact
// source legs: first, equality tail, next key, and the chronology undated
// bucket. It binds the remaining bound plus one and refuses a longer answer; a
// fully short set is the only EOF.
func rbAcrossFetch(ctx context.Context, q browseQuery, in RBAcrossInput, spec rbAcrossSpec, d *rbAcrossDraft, i int) error {
	pos := d.pos[i]
	remaining := d.per + 1
	var raw [][]any
	attempted := true
	leg := func(name string, args ...any) error {
		rows, err := q(name, append(args, int64(remaining))...)
		if err != nil {
			return err
		}
		if len(rows) > remaining {
			return ErrUnqualified
		}
		raw = append(raw, rows...)
		remaining -= len(rows)
		if remaining == 0 {
			attempted = false
		}
		return nil
	}
	base := []any{d.allowed[i].catalogID, in.View.Kind}
	switch {
	case !pos.started:
		if err := leg(rbAcrossOrderName(spec, in.View.Descending, "first"), base...); err != nil {
			return err
		}
	case pos.null:
		if err := leg(rbAcrossOrderName(spec, in.View.Descending, "null_tail"), append(base, pos.id)...); err != nil {
			return err
		}
		if remaining > 0 && !in.View.Descending && spec.nullable {
			if err := leg(rbAcrossOrderName(spec, in.View.Descending, "values"), base...); err != nil {
				return err
			}
		}
	default:
		if err := leg(rbAcrossOrderName(spec, in.View.Descending, "tail"), append(base, pos.key, pos.id)...); err != nil {
			return err
		}
		if remaining > 0 {
			if err := leg(rbAcrossOrderName(spec, in.View.Descending, "next"), append(base, pos.key)...); err != nil {
				return err
			}
		}
		if remaining > 0 && in.View.Descending && spec.nullable {
			if err := leg(rbAcrossOrderName(spec, in.View.Descending, "nulls"), base...); err != nil {
				return err
			}
		}
	}
	if len(raw) > d.per {
		raw = raw[:d.per]
	}
	for _, row := range raw {
		if err := rbAcrossRow(row, spec); err != nil {
			return err
		}
	}
	d.exhausted[i] = attempted && len(raw) < d.per
	d.fetched[i] = raw
	return nil
}

func rbAcrossRow(row []any, spec rbAcrossSpec) error {
	if len(row) != 7 {
		return ErrUnqualified
	}
	id, ok := row[0].(int64)
	if !ok || id <= 0 {
		return ErrUnqualified
	}
	if row[1] == nil {
		if !spec.nullable {
			return ErrUnqualified
		}
	} else {
		switch spec.typ {
		case stmt.Text:
			if _, ok := row[1].(string); !ok {
				return ErrUnqualified
			}
		case stmt.Integer:
			if _, ok := row[1].(int64); !ok {
				return ErrUnqualified
			}
		case stmt.Real:
			v, ok := row[1].(float64)
			if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
				return ErrUnqualified
			}
		default:
			return ErrUnqualified
		}
	}
	public, ok := row[2].([]byte)
	if !ok || len(public) != 16 {
		return ErrUnqualified
	}
	if _, ok := row[3].(string); !ok {
		return ErrUnqualified
	}
	if _, ok := row[4].(int64); !ok {
		return ErrUnqualified
	}
	if _, ok := row[5].(int64); !ok {
		return ErrUnqualified
	}
	if row[6] != nil {
		if _, ok := row[6].(int64); !ok {
			return ErrUnqualified
		}
	}
	return nil
}

// rbAcrossMerge is the exact disjoint stream merge. Equal keys across libraries
// fall back to the global entity identity, matching the source order's id tie.
//
// INVARIANT: every published page is an exact prefix of the global merged
// order, even though it may be shorter than the requested limit. A library that
// is not exhausted but whose buffer is now empty has an unknown next key: that
// key may sort before any remaining buffered head, so the next global row cannot
// be chosen. The merge therefore stops immediately and publishes Next; the
// continuation refetches from every per-library cursor (buffered but unpublished
// rows are simply read again, never skipped or duplicated).
//
// FORWARD PROGRESS: a non-exhausted library always buffers exactly d.per >= 1
// rows after a fetch, and an exhausted library is safe to drain to EOF. So if
// the first fetch of every library returned at least one row, at least one row
// is emitted before any stop. A page only returns zero rows when no data
// remains.
func rbAcrossMerge(ctx context.Context, f browseFence, in RBAcrossInput, d *rbAcrossDraft) error {
	page := RBAcrossPage{Pending: d.pending, PendingSource: d.page.PendingSource}
	if d.pending {
		d.page = page
		return nil
	}
	for i := range d.fetched {
		page.RawRows += len(d.fetched[i])
	}
	index := make([]int, len(d.fetched))
	for page.RawPositions < MaxRaw {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		best, stopped := -1, false
		for i := range d.fetched {
			if index[i] >= len(d.fetched[i]) {
				if !d.exhausted[i] {
					// A non-exhausted library with an empty buffer means the
					// next global row is unknown: its unfetched key may sort
					// before every buffered head. Stop and continue from the
					// cursors rather than emit out-of-order rows.
					stopped = true
				}
				continue
			}
			if best < 0 || rbAcrossBefore(d.fetched[i][index[i]], d.fetched[best][index[best]], in.Sort, in.View.Descending) {
				best = i
			}
		}
		if stopped || best < 0 {
			break
		}
		row := d.fetched[best][index[best]]
		index[best]++
		page.RawPositions++
		d.pos[best] = rbAcrossPosition{key: row[1], null: row[1] == nil, id: row[0].(int64), started: true}
		if !admittedItem(row, f) {
			continue
		}
		item, err := itemValue(row)
		if err != nil {
			return err
		}
		page.Items = append(page.Items, item)
		if len(page.Items) == in.View.Limit {
			break
		}
	}
	exhausted := true
	for i := range d.exhausted {
		if !d.exhausted[i] || index[i] < len(d.fetched[i]) {
			exhausted = false
		}
	}
	page.Exhausted = exhausted
	if !exhausted {
		next := &RBAcrossCursor{fence: f, scope: d.digest, sort: in.Sort, kind: in.View.Kind, desc: in.View.Descending, keep: in.View.KeepArticles, limit: in.View.Limit, pos: make([]rbAcrossPosition, len(d.pos))}
		copy(next.pos, d.pos)
		page.Next = next
	}
	d.page = page
	return nil
}

func rbAcrossBefore(a, b []any, sort RBAcrossSort, desc bool) bool {
	cmp := rbAcrossKeyCompare(sort, a[1], b[1])
	if cmp == 0 {
		cmp = compareInt64(a[0].(int64), b[0].(int64))
	}
	if desc {
		return cmp > 0
	}
	return cmp < 0
}

func rbAcrossKeyCompare(sort RBAcrossSort, a, b any) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	switch x := a.(type) {
	case string:
		y, ok := b.(string)
		if !ok {
			return 1
		}
		if sort == RBAcrossTitle {
			x, y = rbAcrossFold(x), rbAcrossFold(y)
		}
		return strings.Compare(x, y)
	case int64:
		y, ok := b.(int64)
		if !ok {
			return 1
		}
		return compareInt64(x, y)
	case float64:
		y, ok := b.(float64)
		if !ok {
			return 1
		}
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	}
	return 1
}

// rbAcrossFold reproduces SQLite NOCASE exactly: ASCII only, never Unicode.
func rbAcrossFold(s string) string {
	var out []byte
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			if out == nil {
				out = []byte(s)
			}
			out[i] = s[i] + ('a' - 'A')
		}
	}
	if out == nil {
		return s
	}
	return string(out)
}

// RBAcrossCapability declares one published order ceiling. Pending orders name
// the missing source and are refused by the reader rather than approximated.
type RBAcrossCapability struct {
	Sort       RBAcrossSort
	Kinds      []int64
	Directions []string
	OwnerOnly  bool
	State      RBDirectoryState
	Source     RBSourceGap
}

func RBAcrossCapabilities() []RBAcrossCapability {
	var out []RBAcrossCapability
	add := func(sort RBAcrossSort, kinds []int64, owner bool) {
		out = append(out, RBAcrossCapability{Sort: sort, Kinds: append([]int64(nil), kinds...), Directions: []string{"asc", "desc"}, OwnerOnly: owner, State: RBDirectorySupported})
	}
	all := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	add(RBAcrossTitle, all, false)
	add(RBAcrossAdded, all, false)
	add(RBAcrossContentAdded, []int64{2, 3}, false)
	for _, order := range rbScalarOrders() {
		add(RBAcrossSort(order.id), order.kinds, order.id == SortFileSize)
	}
	pending := []struct {
		sort  RBAcrossSort
		kinds []int64
		gap   RBSourceGap
	}{
		{RBAcrossSort("random"), all, RBGapRandom},
		{RBAcrossSort("resolution"), []int64{1, 2, 3, 4, 10}, RBGapResolution},
		{RBAcrossSort("forYou"), []int64{1, 2, 6, 8}, RBGapForYou},
		{RBAcrossSort("groupOrder"), all, RBGapGroupLift},
		{RBAcrossSort("lastPlayed"), all, RBGapPersonalPrepare},
		{RBAcrossSort("playCount"), all, RBGapPersonalPrepare},
		{RBAcrossSort("personalRating"), all, RBGapPersonalPrepare},
		{RBAcrossSort("album"), []int64{7}, RBGapGroupRelationship},
		{RBAcrossSort("artist"), []int64{6, 7}, RBGapGroupRelationship},
	}
	for _, p := range pending {
		out = append(out, RBAcrossCapability{Sort: p.sort, Kinds: append([]int64(nil), p.kinds...), Directions: []string{"asc", "desc"}, State: RBDirectoryPending, Source: p.gap})
	}
	return out
}

func rbAcrossPendingSort(sort RBAcrossSort) (RBSourceGap, bool) {
	for _, capability := range RBAcrossCapabilities() {
		if capability.Sort == sort && capability.State == RBDirectoryPending {
			return capability.Source, true
		}
	}
	return "", false
}

// rbAcrossSupportedSortIDs is the finite per-kind public sort vocabulary this
// composition actually serves. It is derived from the same compiled specs the
// reader uses, so the directory cannot advertise an unsupported order.
func rbAcrossSupportedSortIDs(kind int64) []string {
	ids := []string{}
	for _, id := range []RBAcrossSort{RBAcrossTitle, RBAcrossAdded, RBAcrossContentAdded} {
		if id == RBAcrossContentAdded && kind != 2 && kind != 3 {
			continue
		}
		if _, err := rbAcrossSpecFor(id, kind, false); err == nil {
			ids = append(ids, string(id))
		}
	}
	for _, order := range rbScalarOrders() {
		if order.supports(kind) {
			ids = append(ids, string(order.id))
		}
	}
	return ids
}
