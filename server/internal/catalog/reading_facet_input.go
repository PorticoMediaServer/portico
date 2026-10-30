package catalog

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

var errReadingFacetIndex = errors.New("Reading facet ordered member index is not ready")

// readingFacetSQLInput has independent cursors for raw members, publications,
// archive objects, memberships and attributes. A rejected row advances its raw
// cursor too. No LIMIT is placed outside a query predicate/join/GROUP BY.
type readingFacetSQLInput struct {
	scope                              readingBrowseScope
	library, field, clause, relativeAt string
	args                               []any
	restrictions                       identity.ContentRestrictions
	after                              readingFacetMember
	current                            readingFacetMember
	phase                              string
	asset                              int64
	token, object                      string
	member, memberSource, term         int64
	hadMember, recognized, blocked     bool
}

// Construct in the admission snapshot, with the same evaluation instant used
// in the canonical preparation key. The caller supplies the captured complete
// witness to newReadingFacetReduction; neither function invents a partial key.
func (s *Service) newReadingFacetSQLInput(viewer Viewer, request FacetRequest, instant time.Time) (*readingFacetSQLInput, error) {
	if dbwork.Snapshot(s.Context()) == nil {
		return nil, ErrReadingProjectionChanged
	}
	if !viewer.AllowsLibrary(request.Library) {
		return nil, sql.ErrNoRows
	}
	scope, err := s.readingBrowseScope(request.Library)
	if err != nil {
		return nil, err
	}
	if scope == nil {
		return nil, ErrBrowsePivot
	}
	if request.Field != "author" && request.Field != "series" {
		return nil, browseIssue("field", "Reading counts author and series facets")
	}
	if request.Pivot != "" {
		if _, ok := pivotForKind(scope.kind, request.Pivot); !ok {
			return nil, ErrBrowsePivot
		}
	}
	// Refuse the old (key,item) index. Tests must use the actual baseline; adding
	// a fixture-only index would conceal a missing production prerequisite.
	rows, err := s.read().Query(`PRAGMA index_info(reading_attribution_` + request.Field + `_facet)`)
	if err != nil {
		return nil, err
	}
	var columns []string
	for rows.Next() {
		var seq, cid int
		var name string
		if err = rows.Scan(&seq, &cid, &name); err != nil {
			break
		}
		columns = append(columns, name)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	if !slices.Equal(columns, []string{"library_id", "generation", request.Field + "_key", request.Field + "_value", "item_id"}) {
		return nil, errReadingFacetIndex
	}
	c := &browseCompiler{reading: scope, now: instant, profile: viewer.Profile, restrictions: viewer.EffectiveRestrictions()}
	leaves := 0
	clause, err := compileReadingFacetNode(c, browseAnd(request.Group, request.Query), 0, &leaves)
	if err != nil {
		return nil, err
	}
	policy := viewer.EffectiveRestrictions()
	policy.BlockedLabels = slices.Clone(policy.BlockedLabels)
	policy.MemberDeniedLabels = slices.Clone(policy.MemberDeniedLabels)
	if policy.MaximumAge != nil {
		age := *policy.MaximumAge
		policy.MaximumAge = &age
	}
	relativeAt := ""
	if browseRelative(browseAnd(request.Group, request.Query)) {
		relativeAt = instant.UTC().Format(time.RFC3339Nano)
	}
	return &readingFacetSQLInput{scope: *scope, library: request.Library, field: request.Field, clause: clause, relativeAt: relativeAt, args: c.args, restrictions: policy}, nil
}
func (in *readingFacetSQLInput) fence() string { return in.relativeAt }
func (in *readingFacetSQLInput) discard()      { *in = readingFacetSQLInput{} }
func (in *readingFacetSQLInput) reset() {
	in.after = readingFacetMember{}
	in.current = readingFacetMember{}
	in.phase = ""
	in.asset = 0
	in.token = ""
	in.object = ""
	in.member = 0
	in.memberSource = -1
	in.term = 0
	in.hadMember = false
	in.recognized = false
	in.blocked = false
}
func (in *readingFacetSQLInput) bytes() int {
	n := int(unsafe.Sizeof(*in)) + len(in.library) + len(in.field) + len(in.clause) + len(in.relativeAt) + in.after.bytes() + in.current.bytes() + len(in.token) + len(in.object)
	for _, a := range in.args {
		n += int(unsafe.Sizeof(a))
		if v, ok := a.(string); ok {
			n += len(v)
		}
	}
	n += (cap(in.args) - len(in.args)) * int(unsafe.Sizeof(any(nil)))
	for _, v := range in.restrictions.BlockedLabels {
		n += int(unsafe.Sizeof(v)) + len(v)
	}
	for _, v := range in.restrictions.MemberDeniedLabels {
		n += int(unsafe.Sizeof(v)) + len(v)
	}
	n += (cap(in.restrictions.BlockedLabels) - len(in.restrictions.BlockedLabels) + cap(in.restrictions.MemberDeniedLabels) - len(in.restrictions.MemberDeniedLabels)) * int(unsafe.Sizeof(""))
	if in.restrictions.MaximumAge != nil {
		n += int(unsafe.Sizeof(*in.restrictions.MaximumAge))
	}
	return n
}
func (in *readingFacetSQLInput) scalarBytes() int {
	return max(len(in.current.Key)+len(in.current.Label)+len(in.current.Source), len(in.after.Key)+len(in.after.Label)+len(in.after.Source), len(in.token)+len(in.object))
}
func (in *readingFacetSQLInput) finish(eligible bool) (readingFacetMember, bool, bool, bool, error) {
	m := in.current
	in.current = readingFacetMember{}
	in.token = ""
	in.object = ""
	return m, eligible, true, false, nil
}
func (in *readingFacetSQLInput) next(s *Service, b *readingFacetBudget) (readingFacetMember, bool, bool, bool, error) {
	empty := readingFacetMember{}
	if dbwork.Snapshot(s.Context()) == nil {
		return empty, false, false, false, ErrReadingProjectionChanged
	}
	for b.available() {
		if err := s.Context().Err(); err != nil {
			return empty, false, false, false, err
		}
		switch in.phase {
		case "":
			query, args := in.memberQuery()
			var m readingFacetMember
			err := s.read().QueryRow(query, args...).Scan(&m.Key, &m.Label, &m.Source, &m.Item)
			b.charge(m.bytes())
			if errors.Is(err, sql.ErrNoRows) {
				return empty, false, false, true, nil
			}
			if err != nil {
				return empty, false, false, false, err
			}
			in.after, in.current = m, m
			in.phase = "scalar"
		case "scalar":
			// All joins are PK lookups. Reading query leaves compile to row
			// scalars below; no generic nested SQL is executed here.
			var matches, live bool
			var scalarBytes int
			kind := 20
			if in.scope.kind == "comic" {
				kind = 21
			}
			query := `SELECT COALESCE((` + in.clause + `),0),(ce.library_id=? AND ce.kind=? AND ce.retired=0),length(CAST(e.title AS BLOB))+COALESCE(length(CAST(e.added_text AS BLOB)),0)+COALESCE(length(CAST(cm.author_key AS BLOB)),0)+COALESCE(length(CAST(cm.series_key AS BLOB)),0) FROM catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id JOIN reading_attribution_members cm ON cm.library_id=e.library_id AND cm.generation=` + fmt.Sprint(in.scope.generation.Ready) + ` AND cm.item_id=e.entity_id WHERE e.entity_id=? AND e.library_id=?`
			args := append(append([]any{}, in.args...), in.scope.library, kind, in.current.Item, in.scope.library)
			err := s.read().QueryRow(query, args...).Scan(&matches, &live, &scalarBytes)
			b.charge(32 + scalarBytes)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return empty, false, false, false, err
			}
			if errors.Is(err, sql.ErrNoRows) || !matches || !live {
				in.phase = ""
				return in.finish(false)
			}
			in.asset = 0
			in.phase = "publication"
		case "publication":
			// Existence equals the old primary-choice predicate: reading_publications
			// has FK(item_id,asset_id)->catalog_asset_links, so each admitted candidate
			// has its link; the primary selects one iff the admitted set is nonempty.
			// Availability changes which primary, never the existence of that set.
			var asset int64
			var format, token string
			err := s.read().QueryRow(`SELECT p.asset_id,p.format,a.token FROM reading_publications p LEFT JOIN catalog_assets a ON a.id=p.asset_id WHERE p.item_id=? AND p.asset_id>? ORDER BY p.asset_id LIMIT 1`, in.current.Item, in.asset).Scan(&asset, &format, &token)
			b.charge(32 + len(format) + len(token))
			if errors.Is(err, sql.ErrNoRows) {
				in.phase = ""
				return in.finish(false)
			}
			if err != nil {
				return empty, false, false, false, err
			}
			in.asset = asset
			if !slices.Contains(in.scope.sourceFormats(), format) {
				continue
			}
			if format == "cb7" || format == "cbr" {
				in.token = token
				in.object = ""
				in.phase = "object"
			} else {
				in.beginRestrictions()
			}
		case "object":
			var object string
			var local bool
			// Raw asset/object index, including unavailable/rejected objects. Joins
			// after that one raw row are source/entity/asset/library PK lookups only.
			err := s.read().QueryRow(`SELECT o.id,COALESCE(o.retired=0 AND o.root_incarnation=source.incarnation AND o.size=a.size AND o.modified_ns=a.modified_ns AND source.kind='local' AND source.library_id=?,0)
 FROM inventory_objects o INDEXED BY inventory_objects_reading_asset_object
 LEFT JOIN library_sources source ON source.id=o.source_id LEFT JOIN catalog_assets a ON a.id=?
 WHERE o.asset_id=? AND o.id>? ORDER BY o.id LIMIT 1`, in.library, in.asset, in.token, in.object).Scan(&object, &local)
			b.charge(16 + len(object))
			if errors.Is(err, sql.ErrNoRows) {
				in.phase = "publication"
				in.token = ""
				in.object = ""
				continue
			}
			if err != nil {
				return empty, false, false, false, err
			}
			in.object = object
			if local {
				in.token = ""
				in.object = ""
				in.beginRestrictions()
			}
		case "accept":
			in.phase = ""
			return in.finish(true)
		case "membership":
			var member, source int64
			err := s.read().QueryRow(`SELECT item_id,source FROM catalog_browse_memberships WHERE entity_id=? AND (item_id,source)>(?,?) ORDER BY item_id,source LIMIT 1`, in.current.Item, in.member, in.memberSource).Scan(&member, &source)
			b.charge(16)
			if errors.Is(err, sql.ErrNoRows) {
				in.phase = ""
				return in.finish(!in.hadMember)
			}
			if err != nil {
				return empty, false, false, false, err
			}
			in.member, in.memberSource = member, source
			in.hadMember = true
			in.term = 0
			in.blocked = false
			in.recognized = false
			in.phase = "member-live"
		case "member-live":
			var exists bool
			err := s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_entities ci JOIN catalog_libraries lib ON lib.id=ci.library_id WHERE ci.id=? AND lib.retired=0)`, in.member).Scan(&exists)
			b.charge(8)
			if err != nil {
				return empty, false, false, false, err
			}
			if !exists {
				in.phase = "membership"
			} else {
				in.phase = "attribute"
			}
		case "attribute":
			var term, field int64
			var key, source string
			var age sql.NullInt64
			err := s.read().QueryRow(`SELECT ae.term_id,term.field_id,term.value_key,lower(ae.source_value),ra.minimum_age
 FROM catalog_item_attribute_edges ae JOIN catalog_attribute_terms term ON term.id=ae.term_id LEFT JOIN content_rating_ages ra ON ra.value_key=term.value_key
 WHERE ae.item_id=? AND ae.term_id>? ORDER BY ae.term_id LIMIT 1`, in.member, in.term).Scan(&term, &field, &key, &source, &age)
			b.charge(32 + len(key) + len(source))
			if errors.Is(err, sql.ErrNoRows) {
				if !in.blocked && (!in.restrictions.BlockUnrated || in.recognized) {
					in.phase = ""
					return in.finish(true)
				}
				in.phase = "membership"
				continue
			}
			if err != nil {
				return empty, false, false, false, err
			}
			in.term = term
			if field == 1 && age.Valid {
				if age.Int64 >= 0 {
					in.recognized = true
				}
				if in.restrictions.MaximumAge != nil && age.Int64 > int64(*in.restrictions.MaximumAge) {
					in.blocked = true
				}
			}
			if field == 2 {
				for _, v := range in.restrictions.BlockedLabels {
					if key == strings.ToLower(v) {
						in.blocked = true
					}
				}
			}
			if field == 2 || field == 3 {
				for _, v := range in.restrictions.MemberDeniedLabels {
					if source == strings.ToLower(v) {
						in.blocked = true
					}
				}
			}
			if in.blocked {
				in.phase = "membership"
			}
		default:
			return empty, false, false, false, fmt.Errorf("unknown Reading facet input phase %q", in.phase)
		}
	}
	return empty, false, false, false, nil
}
func (in *readingFacetSQLInput) beginRestrictions() {
	if !in.restrictions.Active() {
		in.phase = "accept"
		return
	}
	in.member, in.memberSource = 0, -1
	in.hadMember = false
	in.phase = "membership"
}

// compileReadingFacetNode closes the entire Reading compiler vocabulary.
// title/year/dateAdded use the existing scalar compiler (count/ids are nil,
// so no candidate or trigram driver runs). Attribution reads the joined ready
// member cm directly. The foreground compiler remains unchanged. In particular
// contains means key equality, missing negative predicates are true, and
// contains-all requires the one accepted key to equal each operand.
func compileReadingFacetNode(c *browseCompiler, n *BrowseNode, depth int, leaves *int) (string, error) {
	if n == nil {
		return "1=1", nil
	}
	if depth > BrowseMaximumDepth+1 {
		return "", browseIssue("query", "Reading facet query is too deep")
	}
	shapes := 0
	if n.All != nil {
		shapes++
	}
	if n.Any != nil {
		shapes++
	}
	if n.Not != nil {
		shapes++
	}
	if n.Field != "" || n.Operator != "" {
		shapes++
	}
	if shapes != 1 {
		return "", browseIssue("query", "query must have exactly one group or predicate")
	}
	if (n.All != nil || n.Any != nil || n.Not != nil) && n.Value != nil {
		return "", browseIssue("query", "group cannot have a value")
	}
	switch {
	case n.All != nil || n.Any != nil:
		children, glue := n.All, " AND "
		if n.Any != nil {
			children, glue = n.Any, " OR "
		}
		if len(children) == 0 || len(children) > 2*BrowseMaximumClauses {
			return "", browseIssue("query", "Reading facet group is invalid")
		}
		parts := make([]string, 0, len(children))
		for i := range children {
			part, err := compileReadingFacetNode(c, &children[i], depth+1, leaves)
			if err != nil {
				return "", err
			}
			parts = append(parts, part)
		}
		return "(" + strings.Join(parts, glue) + ")", nil
	case n.Not != nil:
		part, err := compileReadingFacetNode(c, n.Not, depth+1, leaves)
		return "NOT(" + part + ")", err
	}
	*leaves++
	if *leaves > 2*BrowseMaximumClauses {
		return "", browseIssue("query", "Reading facet query has too many predicates")
	}
	if !readingBrowseField(n.Field) {
		return "", browseIssue("query.field", "field is not published for Reading")
	}
	field, ok := browseFieldByID(n.Field)
	if !ok || !containsString(field.Operators, n.Operator) {
		return "", browseIssue("query.operator", "operator is not valid for this field")
	}
	value, err := validateBrowseValue(field, n.Operator, n.Value, "query.value")
	if err != nil {
		return "", err
	}
	copy := *n
	copy.Value = value
	n = &copy
	if n.Field != "author" && n.Field != "series" {
		return c.predicate(*n)
	}
	column := "cm." + n.Field + "_key"
	normalize := func(v any) (string, error) {
		text, ok := v.(string)
		if !ok || len(text) > BrowseMaximumBytes {
			return "", browseIssue("query.value", "Reading attribution requires bounded text")
		}
		return compactcatalog.ReadingValueKey(text), nil
	}
	bind := func(v any) (string, error) {
		key, err := normalize(v)
		if err == nil {
			c.args = append(c.args, key)
		}
		return key, err
	}
	var condition string
	negative := false
	switch n.Operator {
	case "is-present":
		return column + " IS NOT NULL", nil
	case "is-missing":
		return column + " IS NULL", nil
	case "equals", "contains", "not-equals", "starts-with":
		key, err := bind(n.Value)
		if err != nil {
			return "", err
		}
		if n.Operator == "starts-with" {
			c.args = append(c.args, key)
			condition = "substr(" + column + ",1,length(?))=? COLLATE BINARY"
		} else {
			condition = column + "=? COLLATE BINARY"
		}
		negative = n.Operator == "not-equals"
	case "in", "not-in", "contains-any", "contains-all":
		values, ok := n.Value.([]any)
		if !ok || len(values) == 0 || len(values) > BrowseMaximumValues {
			return "", browseIssue("query.value", "Reading attribution requires a bounded value list")
		}
		for _, v := range values {
			if _, err := bind(v); err != nil {
				return "", err
			}
		}
		if n.Operator == "contains-all" {
			parts := make([]string, len(values))
			for i := range parts {
				parts[i] = column + "=? COLLATE BINARY"
			}
			condition = strings.Join(parts, " AND ")
		} else {
			condition = column + " IN(" + placeholders(len(values)) + ")"
		}
		negative = n.Operator == "not-in"
	default:
		return "", browseIssue("query.operator", "operator is not supported for Reading attribution")
	}
	condition = "COALESCE((" + condition + "),0)"
	if negative {
		condition = "NOT " + condition
	}
	return condition, nil
}

// memberQuery is also used by the production-plan qualification test.
func (in *readingFacetSQLInput) memberQuery() (string, []any) {
	f := in.field
	query := `SELECT m.` + f + `_key,m.` + f + `_value,m.` + f + `_source,m.item_id FROM reading_attribution_members m INDEXED BY reading_attribution_` + f + `_facet WHERE m.library_id=? AND m.generation=? AND m.` + f + `_key IS NOT NULL`
	args := []any{in.scope.library, in.scope.generation.Ready}
	if in.after.Item != 0 {
		query += ` AND (m.` + f + `_key COLLATE BINARY,m.` + f + `_value COLLATE BINARY,m.item_id)>(?,?,?)`
		args = append(args, in.after.Key, in.after.Label, in.after.Item)
	}
	query += ` ORDER BY m.` + f + `_key COLLATE BINARY,m.` + f + `_value COLLATE BINARY,m.item_id LIMIT 1`
	return query, args
}
