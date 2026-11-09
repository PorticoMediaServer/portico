package read

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// ErrMembershipHomeRowSource is the named refusal for the Home row predicate.
// A row's current titles are a Home preparation result, not an enumerated
// catalogue fact; this recipe has no stable exact RB Home membership API, so it
// refuses rather than interpreting a sampled preview as the full row.
var ErrMembershipHomeRowSource = errors.New("rb filter membership needs exact RB Home row membership")

// ErrMembershipSearchSource is the named refusal for the search predicate. The
// exact matching identities come from RS search's own source; this recipe does
// not substitute a title substring scan for that membership.
var ErrMembershipSearchSource = errors.New("rb filter membership needs exact RS search membership")

// ErrMembershipEpisodeRange names the episode-group range refusal. A range is
// defined by the show's authoritative episode rank (RB-14's show workspace
// source); this recipe reads only the hierarchy's stable group facts.
var ErrMembershipEpisodeRange = errors.New("rb filter membership needs the authoritative show rank source")

// MembershipFilterInput is a bounded raw-title source window for the closed
// membership and contextual-source vocabulary: collection, memberOf,
// savedCollection, list, album, artist and episodeGroup. It is a single-source
// primitive, not a general filter endpoint, exact total, facet or
// broad-intersection composer. homeRow and search refuse with named errors
// until their exact sources are attached.
type MembershipFilterInput struct {
	View  BrowseInput
	Query json.RawMessage
	After *MembershipFilterCursor
}
type MembershipFilterCursor struct {
	fence browseFence
	query [32]byte
	key   string
	id    int64
}

const (
	// Fixed per-window recipes: the raw title walk plus the bounded fact
	// families below. The final window repeats the fence and every fact.
	rbMembershipLookupQueries = 32
	rbMembershipFinalQueries  = 40
)

type MembershipFilterPage struct {
	Items                 []BrowseItem
	After                 *MembershipFilterCursor
	RawRows, RawPositions int
	Exhausted             bool
}

// rbMembershipFilter is the values-only predicate kernel. A caller must
// separately prove the item, group authority and current publication.
type rbMembershipFilter struct{ node *rbFilterNode }

// rbMembershipFacts is one title's proved fact values. Values are opaque
// identities (collection/memberOf public ids, saved resource ids, playlist
// tokens, episode-group values) or related text (album/artist titles).
type rbMembershipFacts struct {
	values map[string][]string
	has    map[string]bool
}

func newRBMembershipFacts() *rbMembershipFacts {
	return &rbMembershipFacts{values: map[string][]string{}, has: map[string]bool{}}
}

func (f *rbMembershipFacts) add(field, value string) {
	for _, existing := range f.values[field] {
		if existing == value {
			return
		}
	}
	f.values[field] = append(f.values[field], value)
	f.has[field] = true
}

func compileRBFilterMembership(raw json.RawMessage, kind int64) (rbMembershipFilter, error) {
	n, err := parseRBClosedNode(raw, rbParseMembershipPredicate)
	if err != nil {
		return rbMembershipFilter{}, err
	}
	count := 0
	var visit func(*rbFilterNode, int) error
	visit = func(n *rbFilterNode, depth int) error {
		if n == nil {
			return nil
		}
		count++
		if depth > 8 || count > 128 {
			return ErrInput
		}
		if n.Field != "" {
			operators := rbMembershipFieldOperators(n.Field)
			if operators == nil {
				return ErrUnqualified
			}
			ok := false
			for _, op := range operators {
				if op == n.Operator {
					ok = true
				}
			}
			if !ok {
				return ErrUnqualified
			}
			if !rbMembershipKindApplies(n.Field, kind) {
				return ErrUnqualified
			}
			switch n.Field {
			case "homeRow":
				return ErrMembershipHomeRowSource
			case "search":
				return ErrMembershipSearchSource
			case "episodeGroup":
				show, group, valid := splitRBMembershipEpisodeGroup(n.Value)
				if !valid || show == "" {
					return ErrInput
				}
				if strings.HasPrefix(group, "range:") {
					return ErrMembershipEpisodeRange
				}
			}
			return nil
		}
		for i := range n.All {
			if err := visit(&n.All[i], depth+1); err != nil {
				return err
			}
		}
		for i := range n.Any {
			if err := visit(&n.Any[i], depth+1); err != nil {
				return err
			}
		}
		return visit(n.Not, depth+1)
	}
	if err = visit(n, 0); err != nil {
		return rbMembershipFilter{}, err
	}
	return rbMembershipFilter{n}, nil
}

func rbMembershipFieldOperators(field string) []string {
	switch field {
	case "collection":
		// An identity set: contains/contains-any are the legacy aliases for
		// equals/in. Presence is proved by the collection-presence source.
		return []string{"contains", "contains-any", "contains-all", "equals", "in", "not-equals", "not-in", "is-present", "is-missing"}
	case "memberOf", "savedCollection", "list":
		return []string{"equals", "in", "not-in"}
	case "album", "artist":
		return []string{"contains", "contains-any", "contains-all", "equals", "not-equals", "starts-with", "in", "not-in", "is-present", "is-missing"}
	case "episodeGroup", "homeRow":
		return []string{"equals"}
	case "search":
		return []string{"contains"}
	}
	return nil
}

func rbMembershipFieldApplicable(field string, kind int64) bool {
	kinds := rbMembershipFieldKinds(field)
	for _, candidate := range kinds {
		if candidate == kind {
			return true
		}
	}
	return false
}

func rbMembershipKindApplies(field string, kind int64) bool {
	return kind >= 1 && kind <= 10 && rbMembershipFieldApplicable(field, kind)
}

func rbMembershipFieldKinds(field string) []int64 {
	switch field {
	case "collection":
		// collectedKinds, excluding the collection pivot itself.
		return []int64{1, 2, 3, 4, 5, 6, 7, 8, 9}
	case "memberOf", "savedCollection", "list", "homeRow", "search":
		return []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	case "album", "artist":
		return []int64{5, 6, 7}
	case "episodeGroup":
		return []int64{4}
	}
	return nil
}

func rbParseMembershipPredicate(field, op string, v any) (*rbFilterNode, error) {
	operators := rbMembershipFieldOperators(field)
	if operators == nil {
		return nil, ErrUnqualified
	}
	allowed := false
	for _, candidate := range operators {
		if candidate == op {
			allowed = true
		}
	}
	if !allowed {
		return nil, ErrInput
	}
	if op == "is-present" || op == "is-missing" {
		if v != nil {
			return nil, ErrInput
		}
		return &rbFilterNode{Field: field, Operator: op}, nil
	}
	listOp := op == "in" || op == "not-in" || op == "contains-any" || op == "contains-all"
	list, isList := v.([]any)
	if listOp != isList {
		return nil, ErrInput
	}
	if isList {
		if len(list) == 0 || len(list) > rbMembershipValueSlot {
			return nil, ErrInput
		}
		values := make([]any, len(list))
		for i, x := range list {
			parsed, err := rbParseScalarValue(field, x)
			if err != nil {
				return nil, err
			}
			values[i] = parsed
		}
		v = values
	} else {
		parsed, err := rbParseScalarValue(field, v)
		if err != nil {
			return nil, err
		}
		v = parsed
	}
	return &rbFilterNode{Field: field, Operator: op, Value: v}, nil
}

// splitRBMembershipEpisodeGroup reads "<show public id>/<group>" exactly as the
// public show workspace names a group.
func splitRBMembershipEpisodeGroup(value any) (string, string, bool) {
	text, ok := value.(string)
	if !ok {
		return "", "", false
	}
	at := strings.LastIndex(text, "/")
	if at <= 0 || at == len(text)-1 {
		return "", "", false
	}
	show, group := text[:at], text[at+1:]
	switch group {
	case "all", "specials", "unassigned_absolute":
		return show, group, true
	}
	if strings.HasPrefix(group, "range:") {
		start := strings.TrimPrefix(group, "range:")
		n := 0
		if start == "" {
			return "", "", false
		}
		for _, r := range start {
			if r < '0' || r > '9' {
				return "", "", false
			}
			n = n*10 + int(r-'0')
			if n > 1000000 {
				return "", "", false
			}
		}
		if n%100 != 0 || n >= 1000000 {
			return "", "", false
		}
		return show, group, true
	}
	return "", "", false
}

func (p rbMembershipFilter) matches(f *rbMembershipFacts) bool { return rbEvalMembership(p.node, f) }

func rbEvalMembership(n *rbFilterNode, f *rbMembershipFacts) bool {
	if n == nil {
		return true
	}
	if n.Not != nil {
		return !rbEvalMembership(n.Not, f)
	}
	if len(n.All) != 0 {
		for i := range n.All {
			if !rbEvalMembership(&n.All[i], f) {
				return false
			}
		}
		return true
	}
	if len(n.Any) != 0 {
		for i := range n.Any {
			if rbEvalMembership(&n.Any[i], f) {
				return true
			}
		}
		return false
	}
	if f == nil {
		return false
	}
	switch n.Field {
	case "collection", "memberOf", "savedCollection", "list", "episodeGroup":
		return rbMembershipIdentityEval(n, f)
	case "album", "artist":
		return rbMembershipTextEval(n, f.values[n.Field])
	}
	return false
}

func rbMembershipIdentityEval(n *rbFilterNode, f *rbMembershipFacts) bool {
	values := f.values[n.Field]
	switch n.Operator {
	case "is-present":
		return f.has[n.Field]
	case "is-missing":
		return !f.has[n.Field]
	}
	targets := rbMembershipIdentityTargets(n)
	switch n.Operator {
	case "not-equals", "not-in":
		return !rbMembershipAnyValue(values, targets)
	case "contains-all":
		for _, target := range targets {
			if !rbMembershipAnyValue(values, []string{target}) {
				return false
			}
		}
		return true
	}
	return rbMembershipAnyValue(values, targets)
}

func rbMembershipIdentityTargets(n *rbFilterNode) []string {
	raw := rbMembershipNodeValues(n.Value)
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		text, ok := value.(string)
		if !ok {
			continue
		}
		if n.Field == "collection" || n.Field == "memberOf" {
			// The legacy contract compares pid_blob(public id): malformed ids
			// never match a positive fact and never exclude a title.
			if decoded, valid := entityid.Decode(text); valid {
				out = append(out, string(decoded))
			}
			continue
		}
		out = append(out, text)
	}
	return out
}

func rbMembershipNodeValues(value any) []any {
	if list, ok := value.([]any); ok {
		return list
	}
	return []any{value}
}

func rbMembershipAnyValue(values, targets []string) bool {
	for _, value := range values {
		for _, target := range targets {
			if value == target {
				return true
			}
		}
	}
	return false
}

func rbMembershipTextEval(n *rbFilterNode, values []string) bool {
	switch n.Operator {
	case "is-present":
		return len(values) > 0
	case "is-missing":
		return len(values) == 0
	case "not-equals":
		return !rbMembershipTextPositive(values, "equals", n.Value)
	case "not-in":
		return !rbMembershipTextPositive(values, "in", n.Value)
	case "contains-all":
		list := rbMembershipNodeValues(n.Value)
		for _, value := range list {
			if !rbMembershipTextPositive(values, "equals", value) {
				return false
			}
		}
		return true
	}
	return rbMembershipTextPositive(values, n.Operator, n.Value)
}

// rbMembershipTextPositive mirrors the public EXISTS forms for one positive
// atom over related text. Album and artist are value sets, so contains is exact
// member equality and contains-any is in; text is folded exactly as
// portico_fold_text folds it.
func rbMembershipTextPositive(values []string, op string, value any) bool {
	switch op {
	case "contains":
		op = "equals"
	case "contains-any":
		op = "in"
	}
	switch op {
	case "equals", "in":
		targets := []any{value}
		if op == "in" {
			list, ok := value.([]any)
			if !ok {
				return false
			}
			targets = list
		}
		folded := make([]string, 0, len(targets))
		for _, target := range targets {
			text, ok := target.(string)
			if !ok {
				return false
			}
			folded = append(folded, sorttext.Fold(text))
		}
		for _, candidate := range values {
			text := sorttext.Fold(candidate)
			for _, target := range folded {
				if text == target {
					return true
				}
			}
		}
		return false
	case "starts-with":
		prefix, ok := value.(string)
		if !ok {
			return false
		}
		prefix = sorttext.Fold(prefix)
		for _, candidate := range values {
			if strings.HasPrefix(sorttext.Fold(candidate), prefix) {
				return true
			}
		}
		return false
	}
	return false
}

// MembershipFilterWindow is an unwired bounded source window. It returns at
// most 40 items from at most 64 raw title positions; rejected rows and
// non-matching items advance the opaque cursor. Empty full batches stay
// non-exhausted work.
func (r *BrowseReader) MembershipFilterWindow(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in MembershipFilterInput) (MembershipFilterPage, BrowseReadResult, error) {
	if r == nil {
		return MembershipFilterPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, out, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (MembershipFilterPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.membershipFilterWindow(ctx, request, budget, out, in)
	})
}

func (r *BrowseReader) membershipFilterWindow(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in MembershipFilterInput) (MembershipFilterPage, BrowseReadResult, error) {
	if in.View.policy != (readPolicyBinding{}) || request.Budget.RawRows < 2*MaxRaw+5 || len(in.Query) > 64<<10 || !utf8.Valid(in.Query) {
		return MembershipFilterPage{}, BrowseReadResult{}, ErrInput
	}
	if in.View.Kind < 1 || in.View.Kind > 10 {
		return MembershipFilterPage{}, BrowseReadResult{}, ErrUnqualified
	}
	s, err := newSavedViewSession(&SavedViewReader{snapshots: r.snapshots, pool: r.pool}, ctx, request, budget, out, in.View)
	if err != nil {
		return MembershipFilterPage{}, BrowseReadResult{}, err
	}
	if err = s.reserve((1 << 20) + (32 << 10) + 16*int64(len(in.Query))); err != nil {
		return MembershipFilterPage{}, s.result(), err
	}
	body := bytes.Clone(in.Query)
	var first browseFence
	var filter rbMembershipFilter
	var hash [32]byte
	ready := false
	err = s.window(false, 0, func(q browseQuery) error {
		var e error
		first, e = readBrowseFence(q, s.input(in.View))
		if e != nil {
			return e
		}
		filter, e = compileRBFilterMembership(body, in.View.Kind)
		if e != nil {
			return e
		}
		canonical, e := json.Marshal(filter.node)
		if e != nil {
			return e
		}
		hash = sha256.Sum256(canonical)
		if in.After != nil && (in.After.fence != first || in.After.query != hash) {
			return ErrStale
		}
		ready = true
		return nil
	})
	var rows [][]any
	var facts map[int64]*rbMembershipFacts
	var page MembershipFilterPage
	if err == nil {
		err = s.window(false, rbMembershipLookupQueries, func(q browseQuery) error {
			var e error
			rows, e = readRBMembershipTitleRows(s, q, in, first)
			if e != nil {
				return e
			}
			facts, e = readRBMembershipFacts(s, q, in, first, rows, filter)
			if e != nil {
				return e
			}
			page, e = rbMembershipPage(ctx, in, first, filter, hash, rows, facts)
			return e
		})
	}
	if ready {
		prior := err
		final := s.window(true, rbMembershipFinalQueries, func(q browseQuery) error {
			fresh, e := readBrowseFence(q, s.input(in.View))
			if e != nil {
				return e
			}
			if fresh != first {
				return ErrStale
			}
			current, e := readRBMembershipTitleRows(s, q, in, fresh)
			if e != nil {
				return e
			}
			if !reflect.DeepEqual(rows, current) {
				return ErrStale
			}
			currentFacts, e := readRBMembershipFacts(s, q, in, fresh, current, filter)
			if e != nil {
				return e
			}
			if !reflect.DeepEqual(facts, currentFacts) {
				return ErrStale
			}
			return nil
		})
		err = s.finish(first, prior, final)
	}
	if err != nil {
		return MembershipFilterPage{}, s.result(), err
	}
	return page, s.result(), nil
}

// readRBMembershipTitleRows reserves the exact title source bytes before the
// wider row, exactly like the personal and terms windows' raw walk.
func readRBMembershipTitleRows(s *savedViewSession, q browseQuery, in MembershipFilterInput, f browseFence) ([][]any, error) {
	name := "rb.filter_membership.entity.ignore.asc."
	if in.View.KeepArticles {
		name = "rb.filter_membership.entity.keep.asc."
	}
	if in.View.Descending {
		name = "rb.filter_membership.entity.ignore.desc."
		if in.View.KeepArticles {
			name = "rb.filter_membership.entity.keep.desc."
		}
	}
	leg := func(part string, args ...any) ([][]any, error) {
		sizes, e := q(name+part+".bytes", args...)
		if e != nil {
			return nil, e
		}
		if len(sizes) > MaxRaw {
			return nil, ErrUnqualified
		}
		if len(sizes) == 0 {
			return nil, nil
		}
		var total int64
		for _, row := range sizes {
			if len(row) != 3 {
				return nil, ErrUnqualified
			}
			for _, v := range row[1:] {
				n, ok := v.(int64)
				if !ok || n < 0 || n > min(s.nativeLimit, s.copyLimit/2)-total {
					return nil, governor.ErrSnapshotBudget
				}
				total += n
			}
		}
		if 8192+int64(len(sizes))*1024+2*total > s.copyLimit-s.copied {
			return nil, governor.ErrSnapshotBudget
		}
		rows, e := q(name+part, args...)
		if e != nil {
			return nil, e
		}
		if len(rows) != len(sizes) {
			return nil, ErrStale
		}
		for i, row := range rows {
			if len(row) != 13 || row[0] != sizes[i][0] {
				return nil, ErrStale
			}
			for k, pos := range []int{1, 3} {
				v, ok := row[pos].(string)
				if !ok || int64(len(v)) != sizes[i][k+1] {
					return nil, ErrStale
				}
			}
		}
		return rows, nil
	}
	if in.After == nil {
		return leg("first", in.View.Library, in.View.Kind, int64(MaxRaw))
	}
	rows, e := leg("tail", in.View.Library, in.View.Kind, in.After.key, in.After.id, int64(MaxRaw))
	if e != nil {
		return nil, e
	}
	if len(rows) < MaxRaw {
		more, e := leg("next", in.View.Library, in.View.Kind, in.After.key, int64(MaxRaw-len(rows)))
		if e != nil {
			return nil, e
		}
		rows = append(rows, more...)
	}
	return rows, nil
}

func rbMembershipPage(ctx context.Context, in MembershipFilterInput, f browseFence, filter rbMembershipFilter, hash [32]byte, rows [][]any, facts map[int64]*rbMembershipFacts) (MembershipFilterPage, error) {
	page := MembershipFilterPage{RawRows: len(rows)}
	for _, row := range rows {
		if e := ctx.Err(); e != nil {
			return MembershipFilterPage{}, e
		}
		page.RawPositions++
		page.After = &MembershipFilterCursor{f, hash, row[1].(string), row[0].(int64)}
		if !admittedItem(row, f) {
			continue
		}
		if !filter.matches(facts[row[0].(int64)]) {
			continue
		}
		item, e := itemValue(row)
		if e != nil {
			return MembershipFilterPage{}, e
		}
		page.Items = append(page.Items, item)
		if len(page.Items) == in.View.Limit {
			break
		}
	}
	page.Exhausted = page.RawPositions == len(rows) && len(rows) < MaxRaw
	if page.Exhausted {
		page.After = nil
	}
	return page, nil
}

// readRBMembershipFacts proves one page's fact values in the same snapshot as
// the title bytes. Every family is complete or refuses; no family samples.
func readRBMembershipFacts(s *savedViewSession, q browseQuery, in MembershipFilterInput, f browseFence, rows [][]any, filter rbMembershipFilter) (map[int64]*rbMembershipFacts, error) {
	items := make([]int64, 0, len(rows))
	seen := map[int64]bool{}
	for _, row := range rows {
		if !admittedItem(row, f) {
			continue
		}
		id := row[0].(int64)
		if seen[id] {
			continue
		}
		seen[id] = true
		items = append(items, id)
	}
	sort.Slice(items, func(i, j int) bool { return items[i] < items[j] })
	out := map[int64]*rbMembershipFacts{}
	for _, id := range items {
		out[id] = newRBMembershipFacts()
	}
	if len(items) == 0 {
		return out, nil
	}
	fields := rbMembershipUsedFields(filter.node)
	titles := items

	addRows := func(name string, args []any, field string) error {
		rows, e := rbMembershipFactRows(s, q, name, args)
		if e != nil {
			return e
		}
		for _, row := range rows {
			if len(row) != 2 {
				return ErrUnqualified
			}
			id, ok := row[0].(int64)
			if !ok || out[id] == nil {
				return ErrUnqualified
			}
			value, ok := rbMembershipValueString(row[1])
			if !ok {
				return ErrUnqualified
			}
			out[id].add(field, value)
		}
		return nil
	}
	admission := rbMembershipAdmissionArgs(f)

	if fields["collection"] {
		if rbMembershipNeedsPositive(filter.node, "collection") {
			blobs := rbMembershipPublicValues(filter.node, "collection")
			if len(blobs) > rbMembershipValueSlot {
				return nil, ErrUnqualified
			}
			if len(blobs) > 0 {
				if err := addRows("collection", rbMembershipCollectionArgs(blobs, titles, admission), "collection"); err != nil {
					return nil, err
				}
			}
		}
		if rbMembershipNeedsPresence(filter.node, "collection") {
			if err := addRows("collection.presence", rbMembershipPresenceArgs(titles, admission), "collection"); err != nil {
				return nil, err
			}
		}
	}
	if fields["memberOf"] && rbMembershipNeedsPositive(filter.node, "memberOf") {
		blobs := rbMembershipPublicValues(filter.node, "memberOf")
		if len(blobs) > rbMembershipValueSlot {
			return nil, ErrUnqualified
		}
		if len(blobs) > 0 {
			if err := addRows("parent", rbMembershipCollectionArgs(blobs, titles, admission), "memberOf"); err != nil {
				return nil, err
			}
		}
	}
	if fields["savedCollection"] && rbMembershipNeedsPositive(filter.node, "savedCollection") {
		values := rbMembershipStringValues(filter.node, "savedCollection")
		if len(values) > rbMembershipValueSlot {
			return nil, ErrUnqualified
		}
		if len(values) > 0 {
			for _, name := range []string{"saved.direct", "saved.held", "saved.parts"} {
				if err := addRows(name, rbMembershipSavedArgs(name, values, titles, admission, f), "savedCollection"); err != nil {
					return nil, err
				}
			}
		}
	}
	if fields["list"] && rbMembershipNeedsPositive(filter.node, "list") {
		values := rbMembershipStringValues(filter.node, "list")
		if len(values) > rbMembershipValueSlot {
			return nil, ErrUnqualified
		}
		if len(values) > 0 {
			if err := addRows("playlist", rbMembershipPlaylistArgs(values, titles, admission, f), "list"); err != nil {
				return nil, err
			}
		}
	}
	if fields["album"] {
		if err := addRows("album", rbMembershipSourceArgs(titles, admission), "album"); err != nil {
			return nil, err
		}
	}
	if fields["artist"] {
		if err := addRows("artist", rbMembershipSourceArgs(titles, admission), "artist"); err != nil {
			return nil, err
		}
	}
	if fields["episodeGroup"] {
		values := rbMembershipStringValues(filter.node, "episodeGroup")
		for _, value := range values {
			show, group, valid := splitRBMembershipEpisodeGroup(value)
			if !valid || strings.HasPrefix(group, "range:") {
				return nil, ErrMembershipEpisodeRange
			}
			blob, ok := entityid.Decode(show)
			if !ok {
				continue
			}
			name := map[string]string{"all": "episode.all", "specials": "episode.specials", "unassigned_absolute": "episode.unassigned"}[group]
			rows, e := rbMembershipPointRows(s, q, name, rbMembershipEpisodeArgs(blob, titles))
			if e != nil {
				return nil, e
			}
			for _, row := range rows {
				id, ok := row[0].(int64)
				if !ok || out[id] == nil {
					return nil, ErrUnqualified
				}
				out[id].add("episodeGroup", value)
			}
		}
	}
	for _, fact := range out {
		for field, values := range fact.values {
			if len(values) > rbMembershipTitleValues {
				return nil, ErrNotReady
			}
			sort.Strings(values)
			fact.values[field] = values
		}
	}
	return out, nil
}

// rbMembershipFactRows runs the exact octet preflight then the payload for one
// fact statement. The preflight binds cap+1; more than cap rows is incomplete
// evidence and refuses. Payload values must match the reserved byte multiset.
func rbMembershipFactRows(s *savedViewSession, q browseQuery, name string, args []any) ([][]any, error) {
	sizes, e := q("rb.filter_membership."+name+".bytes", args...)
	if e != nil {
		return nil, e
	}
	if len(sizes) > rbMembershipFactLimit {
		return nil, ErrNotReady
	}
	var total int64
	for _, row := range sizes {
		if len(row) != 2 {
			return nil, ErrUnqualified
		}
		n, ok := row[1].(int64)
		if !ok || n < 0 || n > min(s.nativeLimit, s.copyLimit/2)-total {
			return nil, governor.ErrSnapshotBudget
		}
		if _, ok := row[0].(int64); !ok {
			return nil, ErrUnqualified
		}
		total += n
	}
	if 4096+int64(len(sizes))*256+2*total > s.copyLimit-s.copied {
		return nil, governor.ErrSnapshotBudget
	}
	rows, e := q("rb.filter_membership."+name, args...)
	if e != nil {
		return nil, e
	}
	if len(rows) != len(sizes) {
		return nil, ErrStale
	}
	counts := make(map[string]int, len(sizes))
	for _, row := range sizes {
		counts[fmt.Sprintf("%d:%d", row[0].(int64), row[1].(int64))]++
	}
	for _, row := range rows {
		if len(row) != 2 {
			return nil, ErrUnqualified
		}
		id, ok := row[0].(int64)
		if !ok {
			return nil, ErrUnqualified
		}
		n, ok := rbMembershipValueBytes(row[1])
		if !ok {
			return nil, ErrUnqualified
		}
		key := fmt.Sprintf("%d:%d", id, n)
		counts[key]--
		if counts[key] < 0 {
			return nil, ErrStale
		}
	}
	for _, n := range counts {
		if n != 0 {
			return nil, ErrStale
		}
	}
	return rows, nil
}

func rbMembershipPointRows(s *savedViewSession, q browseQuery, name string, args []any) ([][]any, error) {
	rows, e := q("rb.filter_membership."+name, args...)
	if e != nil {
		return nil, e
	}
	if len(rows) > rbMembershipFactLimit {
		return nil, ErrNotReady
	}
	return rows, nil
}

func rbMembershipValueString(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case []byte:
		if len(v) != entityid.Size {
			return "", false
		}
		return string(v), true
	}
	return "", false
}

func rbMembershipValueBytes(value any) (int, bool) {
	switch v := value.(type) {
	case string:
		return len(v), true
	case []byte:
		return len(v), true
	}
	return 0, false
}

func rbMembershipUsedFields(n *rbFilterNode) map[string]bool {
	out := map[string]bool{}
	var visit func(*rbFilterNode)
	visit = func(n *rbFilterNode) {
		if n == nil {
			return
		}
		if n.Field != "" {
			out[n.Field] = true
		}
		for i := range n.All {
			visit(&n.All[i])
		}
		for i := range n.Any {
			visit(&n.Any[i])
		}
		visit(n.Not)
	}
	visit(n)
	return out
}

func rbMembershipNeedsPositive(n *rbFilterNode, field string) bool {
	found := false
	var visit func(*rbFilterNode)
	visit = func(n *rbFilterNode) {
		if n == nil || found {
			return
		}
		if n.Field == field && n.Operator != "is-present" && n.Operator != "is-missing" {
			found = true
			return
		}
		for i := range n.All {
			visit(&n.All[i])
		}
		for i := range n.Any {
			visit(&n.Any[i])
		}
		visit(n.Not)
	}
	visit(n)
	return found
}

func rbMembershipNeedsPresence(n *rbFilterNode, field string) bool {
	found := false
	var visit func(*rbFilterNode)
	visit = func(n *rbFilterNode) {
		if n == nil || found {
			return
		}
		if n.Field == field && (n.Operator == "is-present" || n.Operator == "is-missing") {
			found = true
			return
		}
		for i := range n.All {
			visit(&n.All[i])
		}
		for i := range n.Any {
			visit(&n.Any[i])
		}
		visit(n.Not)
	}
	visit(n)
	return found
}

func rbMembershipStringValues(n *rbFilterNode, field string) []string {
	set := map[string]bool{}
	var visit func(*rbFilterNode)
	visit = func(n *rbFilterNode) {
		if n == nil {
			return
		}
		if n.Field == field && n.Operator != "is-present" && n.Operator != "is-missing" {
			for _, value := range rbMembershipNodeValues(n.Value) {
				if text, ok := value.(string); ok {
					set[text] = true
				}
			}
		}
		for i := range n.All {
			visit(&n.All[i])
		}
		for i := range n.Any {
			visit(&n.Any[i])
		}
		visit(n.Not)
	}
	visit(n)
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func rbMembershipPublicValues(n *rbFilterNode, field string) [][]byte {
	set := map[string][]byte{}
	var visit func(*rbFilterNode)
	visit = func(n *rbFilterNode) {
		if n == nil {
			return
		}
		if n.Field == field && n.Operator != "is-present" && n.Operator != "is-missing" {
			for _, value := range rbMembershipNodeValues(n.Value) {
				text, ok := value.(string)
				if !ok {
					continue
				}
				if raw, valid := entityid.Decode(text); valid {
					set[string(raw)] = raw
				}
			}
		}
		for i := range n.All {
			visit(&n.All[i])
		}
		for i := range n.Any {
			visit(&n.Any[i])
		}
		visit(n.Not)
	}
	visit(n)
	out := make([][]byte, 0, len(set))
	for _, value := range set {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i], out[j]) < 0 })
	return out
}

func rbMembershipAdmissionArgs(f browseFence) []any {
	allow := int64(1)
	if f.allowUnrated == 0 {
		allow = 0
	}
	return []any{allow, f.maximumAge, f.maximumAge}
}

func rbMembershipCollectionArgs(values [][]byte, titles []int64, admission []any) []any {
	args := make([]any, 0, rbMembershipValueSlot+rbMembershipSlot+len(admission)+1)
	args = append(args, rbMembershipBlobArgs(values)...)
	args = append(args, rbMembershipTitleArgs(titles)...)
	args = append(args, admission...)
	return append(args, int64(rbMembershipFactLimit+1))
}

func rbMembershipPresenceArgs(titles []int64, admission []any) []any {
	args := make([]any, 0, rbMembershipSlot+len(admission)+1)
	args = append(args, rbMembershipTitleArgs(titles)...)
	args = append(args, admission...)
	return append(args, int64(rbMembershipFactLimit+1))
}

func rbMembershipSourceArgs(titles []int64, admission []any) []any {
	args := make([]any, 0, rbMembershipSlot+len(admission)+1)
	args = append(args, rbMembershipTitleArgs(titles)...)
	args = append(args, admission...)
	return append(args, int64(rbMembershipFactLimit+1))
}

func rbMembershipSavedArgs(name string, values []string, titles []int64, admission []any, f browseFence) []any {
	owner := rbMembershipOwnerKey(f)
	args := make([]any, 0, 4+rbMembershipValueSlot+rbMembershipSlot+len(admission)+1)
	args = append(args, "local", f.account, f.profile)
	args = append(args, owner)
	args = append(args, rbMembershipTextArgs(values)...)
	args = append(args, rbMembershipTitleArgs(titles)...)
	if name == "saved.held" {
		args = append(args, admission...)
	}
	return append(args, int64(rbMembershipFactLimit+1))
}

func rbMembershipPlaylistArgs(values []string, titles []int64, admission []any, f browseFence) []any {
	args := make([]any, 0, 6+rbMembershipValueSlot+rbMembershipSlot+len(admission)+1)
	args = append(args, "local", f.account, f.profile)
	args = append(args, "local", f.account, f.profile)
	args = append(args, rbMembershipTextArgs(values)...)
	args = append(args, rbMembershipTitleArgs(titles)...)
	args = append(args, admission...)
	return append(args, int64(rbMembershipFactLimit+1))
}

func rbMembershipEpisodeArgs(show []byte, titles []int64) []any {
	args := make([]any, 0, 1+rbMembershipSlot+1)
	args = append(args, show)
	args = append(args, rbMembershipTitleArgs(titles)...)
	return append(args, int64(rbMembershipFactLimit+1))
}

func rbMembershipTitleArgs(titles []int64) []any {
	out := make([]any, rbMembershipSlot)
	for n := range out {
		out[n] = int64(-1)
	}
	for i, id := range titles {
		out[i] = id
	}
	return out
}

func rbMembershipBlobArgs(values [][]byte) []any {
	out := make([]any, rbMembershipValueSlot)
	for n := range out {
		out[n] = []byte{}
	}
	for i, value := range values {
		out[i] = value
	}
	return out
}

func rbMembershipTextArgs(values []string) []any {
	out := make([]any, rbMembershipValueSlot)
	for i, value := range values {
		out[i] = value
	}
	for n := len(values); n < len(out); n++ {
		out[n] = ""
	}
	return out
}

// rbMembershipOwnerKey is the legacy personal owner key for this live principal.
func rbMembershipOwnerKey(f browseFence) string {
	raw, _ := json.Marshal([3]string{"local", f.account, f.profile})
	return "viewer:" + base64.RawURLEncoding.EncodeToString(raw)
}
