package read

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// TermsFilterInput is a bounded raw-title source window for the closed term and
// credit vocabulary: genre, tag, label, contentRating, studio, network, actor,
// director, writer, person, cast and crew. It is a single-source primitive, not
// a general filter endpoint, exact total, facet or broad-intersection composer.
//
// Facts are read for the item and its work ancestry (the show for a season or
// episode) exactly as the legacy browse contract scopes a title's terms and
// credits. Credit facts come only from each entity's sealed current image. The
// raw walk is the existing accepted article-aware title order for the four video
// kinds. Unsupported fields, operators and kinds refuse, including beneath NOT.
type TermsFilterInput struct {
	View  BrowseInput
	Query json.RawMessage
	After *TermsFilterCursor
}
type TermsFilterCursor struct {
	fence browseFence
	query [32]byte
	key   string
	id    int64
}
type TermsFilterPage struct {
	Items                 []BrowseItem
	After                 *TermsFilterCursor
	RawRows, RawPositions int
	Exhausted             bool
}

// rbTermsFilter is the values-only predicate kernel. A caller must separately
// prove the item, ancestry, sealed images and current publication.
type rbTermsFilter struct{ node *rbFilterNode }

// rbTermsFacts is the already-proved value set per field. Duplicate reasons
// (one credit repeated, one term from two providers) collapse to one value.
type rbTermsFacts map[string][]string

const (
	rbTermsFactSlots = MaxRaw
	rbTermsMaxPerson = 2 * MaxRaw
	// Fixed per-window recipes: title preflight+payload plus the bounded fact
	// families below. The final window repeats the fence and every fact.
	rbTermsLookupQueries = 32
	rbTermsFinalQueries  = 40
)

func rbTermsSetOperators() []string {
	return []string{"contains", "contains-any", "contains-all", "equals", "not-equals", "starts-with", "in", "not-in", "is-present", "is-missing"}
}
func rbTermsStringOperators() []string {
	return []string{"equals", "not-equals", "contains", "starts-with", "in", "not-in", "is-present", "is-missing"}
}
func rbTermsIdentityOperators() []string { return []string{"equals", "in", "not-in"} }

func rbTermsFieldOperators(field string) []string {
	switch field {
	case "genre", "tag", "label", "actor", "director", "writer":
		return rbTermsSetOperators()
	case "contentRating", "studio", "network":
		return rbTermsStringOperators()
	case "person", "cast", "crew":
		return rbTermsIdentityOperators()
	}
	return nil
}

// rbTermsSetField reports a field whose values are a membership set, where the
// public contains operator means exact member equality.
func rbTermsSetField(field string) bool {
	switch field {
	case "genre", "tag", "label", "actor", "director", "writer":
		return true
	}
	return false
}

func rbTermsAttributeField(field string) bool {
	switch field {
	case "tag", "label", "contentRating", "studio", "network":
		return true
	}
	return false
}
func rbTermsCreditField(field string) bool {
	switch field {
	case "actor", "director", "writer", "person", "cast", "crew":
		return true
	}
	return false
}

func compileRBFilterTerms(raw json.RawMessage) (rbTermsFilter, error) {
	n, err := parseRBClosedNode(raw, rbParseTermsPredicate)
	if err != nil {
		return rbTermsFilter{}, err
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
			operators := rbTermsFieldOperators(n.Field)
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
		return rbTermsFilter{}, err
	}
	return rbTermsFilter{n}, nil
}

func rbParseTermsPredicate(field, op string, v any) (*rbFilterNode, error) {
	operators := rbTermsFieldOperators(field)
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
		if len(list) == 0 || len(list) > 100 {
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

func (p rbTermsFilter) matches(f rbTermsFacts) bool { return rbEvalTerms(p.node, f) }

func rbEvalTerms(n *rbFilterNode, f rbTermsFacts) bool {
	if n == nil {
		return true
	}
	if n.Not != nil {
		return !rbEvalTerms(n.Not, f)
	}
	if len(n.All) != 0 {
		for i := range n.All {
			if !rbEvalTerms(&n.All[i], f) {
				return false
			}
		}
		return true
	}
	if len(n.Any) != 0 {
		for i := range n.Any {
			if rbEvalTerms(&n.Any[i], f) {
				return true
			}
		}
		return false
	}
	values := f[n.Field]
	switch n.Operator {
	case "is-present":
		return len(values) > 0
	case "is-missing":
		return len(values) == 0
	case "not-equals":
		return !rbTermsPositive(values, rbTermsSetField(n.Field), "equals", n.Value)
	case "not-in":
		return !rbTermsPositive(values, rbTermsSetField(n.Field), "in", n.Value)
	case "contains-all":
		list, ok := n.Value.([]any)
		if !ok {
			return false
		}
		for _, value := range list {
			if !rbTermsPositive(values, true, "equals", value) {
				return false
			}
		}
		return true
	}
	return rbTermsPositive(values, rbTermsSetField(n.Field), n.Operator, n.Value)
}

// rbTermsPositive mirrors the public EXISTS forms for one positive atom.
func rbTermsPositive(values []string, set bool, op string, value any) bool {
	fold := func(v any) (string, bool) {
		text, ok := v.(string)
		if !ok {
			return "", false
		}
		return sorttext.Fold(text), true
	}
	switch op {
	case "contains":
		if set {
			op = "equals"
		}
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
			text, ok := fold(target)
			if !ok {
				return false
			}
			folded = append(folded, text)
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
		prefix, ok := fold(value)
		if !ok {
			return false
		}
		for _, candidate := range values {
			if strings.HasPrefix(sorttext.Fold(candidate), prefix) {
				return true
			}
		}
		return false
	}
	// String attribute contains is a folded substring search over the member.
	if op == "contains" && !set {
		needle, ok := fold(value)
		if !ok {
			return false
		}
		for _, candidate := range values {
			if strings.Contains(sorttext.Fold(candidate), needle) {
				return true
			}
		}
	}
	return false
}

// TermsFilterWindow is an unwired bounded source window. It returns at most 40
// items from at most 64 raw title positions; rejected rows and non-matching
// items advance the opaque cursor. Empty full batches stay non-exhausted work.
func (r *BrowseReader) TermsFilterWindow(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in TermsFilterInput) (TermsFilterPage, BrowseReadResult, error) {
	if r == nil {
		return TermsFilterPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, out, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (TermsFilterPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.termsFilterWindow(ctx, request, budget, out, in)
	})
}

func (r *BrowseReader) termsFilterWindow(ctx context.Context, request governor.Request, budget time.Duration, out BrowseOutput, in TermsFilterInput) (TermsFilterPage, BrowseReadResult, error) {
	if in.View.After != nil || in.View.policy != (readPolicyBinding{}) || request.Budget.RawRows < 2*MaxRaw+5 || len(in.Query) > 64<<10 || !utf8.Valid(in.Query) {
		return TermsFilterPage{}, BrowseReadResult{}, ErrInput
	}
	if in.View.Kind < 1 || in.View.Kind > 4 {
		return TermsFilterPage{}, BrowseReadResult{}, ErrUnqualified
	}
	s, err := newSavedViewSession(&SavedViewReader{snapshots: r.snapshots, pool: r.pool}, ctx, request, budget, out, in.View)
	if err != nil {
		return TermsFilterPage{}, BrowseReadResult{}, err
	}
	if err = s.reserve((1 << 20) + (32 << 10) + 16*int64(len(in.Query))); err != nil {
		return TermsFilterPage{}, s.result(), err
	}
	body := bytes.Clone(in.Query)
	var first browseFence
	var filter rbTermsFilter
	var hash [32]byte
	ready := false
	err = s.window(false, 0, func(q browseQuery) error {
		var e error
		first, e = readBrowseFence(q, s.input(in.View))
		if e != nil {
			return e
		}
		filter, e = compileRBFilterTerms(body)
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
	var facts map[int64]rbTermsFacts
	var page TermsFilterPage
	if err == nil {
		err = s.window(false, rbTermsLookupQueries, func(q browseQuery) error {
			var e error
			rows, e = readRBTermsTitleRows(s, q, in, first)
			if e != nil {
				return e
			}
			facts, e = readRBTermsFacts(s, q, in, first, rows, filter)
			if e != nil {
				return e
			}
			page, e = rbTermsPage(ctx, in, first, filter, hash, rows, facts)
			return e
		})
	}
	if ready {
		prior := err
		final := s.window(true, rbTermsFinalQueries, func(q browseQuery) error {
			fresh, e := readBrowseFence(q, s.input(in.View))
			if e != nil {
				return e
			}
			if fresh != first {
				return ErrStale
			}
			current, e := readRBTermsTitleRows(s, q, in, fresh)
			if e != nil {
				return e
			}
			if !reflect.DeepEqual(rows, current) {
				return ErrStale
			}
			currentFacts, e := readRBTermsFacts(s, q, in, fresh, current, filter)
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
		return TermsFilterPage{}, s.result(), err
	}
	return page, s.result(), nil
}

// readRBTermsTitleRows reserves the exact title source bytes before the wider
// row, exactly like the personal window's raw walk.
func readRBTermsTitleRows(s *savedViewSession, q browseQuery, in TermsFilterInput, f browseFence) ([][]any, error) {
	name := "rb.filter_terms.entity.ignore.asc."
	if in.View.KeepArticles {
		name = "rb.filter_terms.entity.keep.asc."
	}
	if in.View.Descending {
		name = "rb.filter_terms.entity.ignore.desc."
		if in.View.KeepArticles {
			name = "rb.filter_terms.entity.keep.desc."
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

func rbTermsPage(ctx context.Context, in TermsFilterInput, f browseFence, filter rbTermsFilter, hash [32]byte, rows [][]any, facts map[int64]rbTermsFacts) (TermsFilterPage, error) {
	page := TermsFilterPage{RawRows: len(rows)}
	for _, row := range rows {
		if e := ctx.Err(); e != nil {
			return TermsFilterPage{}, e
		}
		page.RawPositions++
		page.After = &TermsFilterCursor{f, hash, row[1].(string), row[0].(int64)}
		if !admittedItem(row, f) {
			continue
		}
		if !filter.matches(facts[row[0].(int64)]) {
			continue
		}
		item, e := itemValue(row)
		if e != nil {
			return TermsFilterPage{}, e
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

type rbTermsItem struct{ id, work int64 }

// readRBTermsFacts proves one page's fact values in the same snapshot as the
// title bytes. Every family is complete or refuses; no family samples.
func readRBTermsFacts(s *savedViewSession, q browseQuery, in TermsFilterInput, f browseFence, rows [][]any, filter rbTermsFilter) (map[int64]rbTermsFacts, error) {
	items := make([]rbTermsItem, 0, len(rows))
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
		work := id
		if row[8] != nil {
			work = row[8].(int64)
		}
		items = append(items, rbTermsItem{id: id, work: work})
	}
	facts := map[int64]rbTermsFacts{}
	for _, item := range items {
		facts[item.id] = rbTermsFacts{}
	}
	if len(items) == 0 {
		return facts, nil
	}
	fields := rbTermsUsedFields(filter.node)
	entities := map[int64]rbTermsFacts{}
	entity := func(id int64) rbTermsFacts {
		set := entities[id]
		if set == nil {
			set = rbTermsFacts{}
			entities[id] = set
		}
		return set
	}

	itemIDs := make([]int64, 0, len(items))
	ancestry := map[int64]bool{}
	for _, item := range items {
		itemIDs = append(itemIDs, item.id)
		ancestry[item.id] = true
		ancestry[item.work] = true
	}
	sort.Slice(itemIDs, func(i, j int) bool { return itemIDs[i] < itemIDs[j] })
	ancestryIDs := make([]int64, 0, len(ancestry))
	for id := range ancestry {
		ancestryIDs = append(ancestryIDs, id)
	}
	sort.Slice(ancestryIDs, func(i, j int) bool { return ancestryIDs[i] < ancestryIDs[j] })

	if rbTermsUsesAttribute(fields) {
		for _, chunk := range rbTermsChunks(itemIDs, rbTermsFactSlots) {
			args := append(rbTermsSlots(chunk), int64(rbTermsFactLimit))
			rows, e := rbTermsFactRows(s, q, "rb.filter_terms.edge", args, 2, rbTermsFactLimit)
			if e != nil {
				return nil, e
			}
			for _, row := range rows {
				field, ok := row[1].(string)
				value, valid := row[2].(string)
				if !ok || !valid || !rbTermsAttributeField(field) {
					return nil, ErrUnqualified
				}
				rbTermsAdd(entity(row[0].(int64)), field, value)
			}
		}
	}
	if fields["genre"] {
		for _, chunk := range rbTermsChunks(ancestryIDs, rbTermsFactSlots) {
			args := append(rbTermsSlots(chunk), in.View.Library, int64(rbTermsFactLimit))
			rows, e := rbTermsFactRows(s, q, "rb.filter_terms.term", args, 1, rbTermsFactLimit)
			if e != nil {
				return nil, e
			}
			for _, row := range rows {
				value, ok := row[1].(string)
				if !ok || value == "" {
					return nil, ErrUnqualified
				}
				rbTermsAdd(entity(row[0].(int64)), "genre", value)
			}
		}
	}
	if fields["actor"] || fields["director"] || fields["writer"] {
		labels, e := rbTermsCreditLabels(s, q, in)
		if e != nil {
			return nil, e
		}
		department := []any{int64(-1), int64(-1), int64(-1)}
		for n, name := range []string{"Acting", "Directing", "Writing"} {
			if id, ok := labels[name]; ok {
				department[n] = id
			}
		}
		role := []any{int64(-1), int64(-1), int64(-1)}
		for n, name := range []string{"Director", "Writer", "Screenplay"} {
			if id, ok := labels[name]; ok {
				role[n] = id
			}
		}
		if department[0] != int64(-1) || department[1] != int64(-1) || department[2] != int64(-1) {
			for _, chunk := range rbTermsChunks(ancestryIDs, rbTermsFactSlots) {
				args := append(append([]any(nil), department...), in.View.Library)
				args = append(args, rbTermsSlots(chunk)...)
				args = append(args, int64(rbTermsFactLimit))
				rows, e := rbTermsFactRows(s, q, "rb.filter_terms.credit.department", args, 3, rbTermsFactLimit)
				if e != nil {
					return nil, e
				}
				if e = rbTermsCreditRows(entities, rows, fields, false); e != nil {
					return nil, e
				}
			}
		}
		if role[0] != int64(-1) || role[1] != int64(-1) || role[2] != int64(-1) {
			for _, chunk := range rbTermsChunks(ancestryIDs, rbTermsFactSlots) {
				args := append(append([]any(nil), role...), in.View.Library)
				args = append(args, rbTermsSlots(chunk)...)
				args = append(args, int64(rbTermsFactLimit))
				rows, e := rbTermsFactRows(s, q, "rb.filter_terms.credit.role", args, 3, rbTermsFactLimit)
				if e != nil {
					return nil, e
				}
				if e = rbTermsCreditRows(entities, rows, fields, false); e != nil {
					return nil, e
				}
			}
		}
	}
	if fields["person"] || fields["cast"] || fields["crew"] {
		tokens, e := rbTermsPersonTokens(filter.node)
		if e != nil {
			return nil, e
		}
		for _, tokenChunk := range rbTermsChunks(tokens, rbTermsFactSlots) {
			for _, chunk := range rbTermsChunks(ancestryIDs, rbTermsFactSlots) {
				args := append(rbTermsTokenSlots(tokenChunk), in.View.Library)
				args = append(args, rbTermsSlots(chunk)...)
				args = append(args, int64(rbTermsFactLimit))
				rows, e := rbTermsFactRows(s, q, "rb.filter_terms.credit.person", args, 4, rbTermsFactLimit)
				if e != nil {
					return nil, e
				}
				if e = rbTermsCreditRows(entities, rows, fields, true); e != nil {
					return nil, e
				}
			}
		}
	}
	for _, item := range items {
		dst := facts[item.id]
		rbTermsMerge(dst, entities[item.id])
		if item.work != item.id {
			rbTermsMerge(dst, entities[item.work])
		}
	}
	return facts, nil
}

// rbTermsCreditRows classifies one credit row by its exact public labels. The
// credited name feeds actor/director/writer; the person token feeds
// person/cast/crew. A row never contributes a field family that did not fetch
// the complete class it belongs to.
func rbTermsCreditRows(entities map[int64]rbTermsFacts, rows [][]any, fields map[string]bool, hasToken bool) error {
	for _, row := range rows {
		if len(row) != 4 && !hasToken || len(row) != 5 && hasToken {
			return ErrUnqualified
		}
		id, ok := row[0].(int64)
		if !ok {
			return ErrUnqualified
		}
		name, okName := row[1].(string)
		role, okRole := row[2].(string)
		department, okDept := row[3].(string)
		if !okName || !okRole || !okDept {
			return ErrUnqualified
		}
		token := ""
		if hasToken && row[4] != nil {
			value, ok := row[4].(string)
			if !ok {
				return ErrUnqualified
			}
			token = value
		}
		set := entities[id]
		if set == nil {
			set = rbTermsFacts{}
			entities[id] = set
		}
		cast := strings.EqualFold(department, "Acting") || strings.EqualFold(department, "Cast")
		// Each family only consumes the statement that proved its complete
		// class: the class statements feed names, the person statement feeds
		// tokens. A partial class can never stand in for a complete one.
		if !hasToken {
			if fields["actor"] && department == "Acting" {
				rbTermsAdd(set, "actor", name)
			}
			if fields["director"] && (role == "Director" || department == "Directing") {
				rbTermsAdd(set, "director", name)
			}
			if fields["writer"] && (role == "Writer" || role == "Screenplay" || department == "Writing") {
				rbTermsAdd(set, "writer", name)
			}
		}
		if !hasToken || token == "" {
			continue
		}
		if fields["person"] {
			rbTermsAdd(set, "person", token)
		}
		if fields["cast"] && cast {
			rbTermsAdd(set, "cast", token)
		}
		if fields["crew"] && !cast {
			rbTermsAdd(set, "crew", token)
		}
	}
	return nil
}

func rbTermsAdd(set rbTermsFacts, field, value string) {
	for _, existing := range set[field] {
		if existing == value {
			return
		}
	}
	set[field] = append(set[field], value)
}
func rbTermsMerge(dst, src rbTermsFacts) {
	for field, values := range src {
		for _, value := range values {
			rbTermsAdd(dst, field, value)
		}
	}
}

// rbTermsCreditLabels resolves the exact role/department labels once per window.
// A missing label leaves that class empty; it is never substituted.
func rbTermsCreditLabels(s *savedViewSession, q browseQuery, in TermsFilterInput) (map[string]int64, error) {
	labels := []string{"Acting", "Director", "Directing", "Writer", "Screenplay", "Writing"}
	out := map[string]int64{}
	for _, label := range labels {
		rows, e := rbTermsFactRows(s, q, "rb.filter_terms.label", []any{label, int64(1)}, 1, 0)
		if e != nil {
			return nil, e
		}
		for _, row := range rows {
			id, ok := row[0].(int64)
			name, valid := row[1].(string)
			if !ok || !valid {
				return nil, ErrUnqualified
			}
			out[name] = id
		}
	}
	return out, nil
}

// rbTermsFactRows runs the exact octet preflight then the payload for one fact
// statement and proves every returned value against its reserved bytes.
func rbTermsFactRows(s *savedViewSession, q browseQuery, name string, args []any, strings int, limit int64) ([][]any, error) {
	sizes, e := q(name+".bytes", args...)
	if e != nil {
		return nil, e
	}
	if limit > 0 && int64(len(sizes)) >= limit {
		// A full fact page cannot prove the remainder.
		return nil, ErrNotReady
	}
	var total int64
	for _, row := range sizes {
		if len(row) != 1+strings {
			return nil, ErrUnqualified
		}
		for _, value := range row[1:] {
			if value == nil {
				continue
			}
			n, ok := value.(int64)
			if !ok || n < 0 || n > min(s.nativeLimit, s.copyLimit/2)-total {
				return nil, governor.ErrSnapshotBudget
			}
			total += n
		}
	}
	if 4096+int64(len(sizes))*256+2*total > s.copyLimit-s.copied {
		return nil, governor.ErrSnapshotBudget
	}
	rows, e := q(name, args...)
	if e != nil {
		return nil, e
	}
	if len(rows) != len(sizes) {
		return nil, ErrStale
	}
	for i, row := range rows {
		if len(row) != len(sizes[i]) || row[0] != sizes[i][0] {
			return nil, ErrStale
		}
		for j := 1; j < len(row); j++ {
			if row[j] == nil {
				if sizes[i][j] != nil {
					return nil, ErrStale
				}
				continue
			}
			text, ok := row[j].(string)
			if !ok || int64(len(text)) != sizes[i][j].(int64) {
				return nil, ErrStale
			}
		}
	}
	return rows, nil
}

func rbTermsSlots(ids []int64) []any {
	out := make([]any, rbTermsFactSlots)
	for i := range out {
		out[i] = int64(-1)
	}
	for i, id := range ids {
		out[i] = id
	}
	return out
}
func rbTermsTokenSlots(tokens []string) []any {
	out := make([]any, rbTermsFactSlots)
	for i := range out {
		out[i] = ""
	}
	for i, token := range tokens {
		out[i] = token
	}
	return out
}
func rbTermsChunks[T any](values []T, size int) [][]T {
	var out [][]T
	for len(values) > 0 {
		n := min(len(values), size)
		out = append(out, values[:n])
		values = values[n:]
	}
	return out
}

func rbTermsUsedFields(n *rbFilterNode) map[string]bool {
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
func rbTermsUsesAttribute(fields map[string]bool) bool {
	for field := range fields {
		if rbTermsAttributeField(field) {
			return true
		}
	}
	return false
}

// rbTermsPersonTokens collects the exact person tokens a person/cast/crew
// predicate names. The three identity fields allow only equals/in/not-in, so
// every needed credit belongs to one of these tokens.
func rbTermsPersonTokens(n *rbFilterNode) ([]string, error) {
	set := map[string]bool{}
	var visit func(*rbFilterNode) error
	visit = func(n *rbFilterNode) error {
		if n == nil {
			return nil
		}
		if n.Field != "" && (n.Field == "person" || n.Field == "cast" || n.Field == "crew") {
			switch value := n.Value.(type) {
			case string:
				set[value] = true
			case []any:
				for _, item := range value {
					text, ok := item.(string)
					if !ok {
						return ErrUnqualified
					}
					set[text] = true
				}
			default:
				return ErrUnqualified
			}
		}
		for i := range n.All {
			if err := visit(&n.All[i]); err != nil {
				return err
			}
		}
		for i := range n.Any {
			if err := visit(&n.Any[i]); err != nil {
				return err
			}
		}
		return visit(n.Not)
	}
	if err := visit(n); err != nil {
		return nil, err
	}
	if len(set) > rbTermsMaxPerson {
		return nil, ErrUnqualified
	}
	out := make([]string, 0, len(set))
	for token := range set {
		out = append(out, token)
	}
	sort.Strings(out)
	return out, nil
}
