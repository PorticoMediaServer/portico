package catalog

import (
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
)

// A playlist or collection page can list its occurrences in a temporary order
// or narrowed to the viewer's own watch state. "position" is the resource's own
// order, the only order a move means; the others are display orders over the
// same occurrences and are never stored. A hidden occurrence (an item this
// viewer may not see) has no facts: a display order lists hidden occurrences
// last, by occurrence, and a filter other than "all" never matches one.
//
// Every query below reads one resource's occurrences as `e` with the item, when
// the viewer may see it, as `i` (NULL otherwise) and the viewer's personal key
// as `viewer.profile_id`.
type ResourceContentQuery struct {
	Sort, Direction, Filter string
}

type resourceContentSort struct {
	id, label, key string
	numeric        bool
	directions     []string
}

var resourceContentSorts = []resourceContentSort{
	{"position", "sort.position", "", false, []string{"asc"}},
	{"title", "sort.title", `lower(i.title)`, false, []string{"asc", "desc"}},
	{"year", "sort.year", `CAST(COALESCE(i.year,0) AS REAL)`, true, []string{"desc", "asc"}},
	{"duration", "sort.duration", `COALESCE((SELECT duration FROM catalog_browse_item_metrics WHERE item_id=i.id),0.0)`, true, []string{"asc", "desc"}},
}

var resourceContentFilterIDs = []string{"all", "unwatched", "inProgress", "watched"}

func resourceContentFilterSQL(filter string) string {
	watched := personalstate.CompactSQL("viewer.profile_id", "i.id")
	switch filter {
	case "unwatched":
		return `i.id IS NOT NULL AND ` + watched + `=0`
	case "inProgress":
		return `i.id IS NOT NULL AND ` + watched + `=0 AND ((i.kind NOT IN(20,21) AND COALESCE((SELECT g.position FROM progress g WHERE g.profile_id=viewer.profile_id AND g.item_id=i.id),0)>0) OR (i.kind IN(20,21) AND ` + savedReadingInProgressSQL("viewer.profile_id") + `))`
	case "watched":
		return `i.id IS NOT NULL AND ` + watched + `=1`
	}
	return `1`
}

// normalize applies the defaults (the resource's own order, everything) and
// refuses a sort, direction or filter the resource does not publish.
func (q ResourceContentQuery) normalize() (ResourceContentQuery, resourceContentSort, error) {
	if q.Sort == "" {
		q.Sort = "position"
	}
	if q.Filter == "" {
		q.Filter = "all"
	}
	known := false
	for _, f := range resourceContentFilterIDs {
		known = known || f == q.Filter
	}
	for _, sort := range resourceContentSorts {
		if sort.id != q.Sort {
			continue
		}
		if q.Direction == "" {
			q.Direction = sort.directions[0]
		}
		for _, d := range sort.directions {
			if d == q.Direction && known {
				return q, sort, nil
			}
		}
	}
	return q, resourceContentSort{}, requestinput.New("invalid resource content query")
}

// manual is the resource's own order over every occurrence: the only page a
// move can be made from, and the only one that publishes order witnesses.
func (q ResourceContentQuery) manual() bool {
	return q.Sort == "position" && q.Filter == "all"
}

func resourceContentSortOptions() []ContentSort {
	out := make([]ContentSort, 0, len(resourceContentSorts))
	for _, sort := range resourceContentSorts {
		out = append(out, ContentSort{sort.id, sort.label, sort.directions})
	}
	return out
}

// resourceContentCountsSQL counts every filter option in one pass.
func resourceContentCountsSQL() string {
	var b strings.Builder
	b.WriteString(`SELECT count(*)`)
	for _, f := range resourceContentFilterIDs[1:] {
		b.WriteString(`,COALESCE(sum(` + resourceContentFilterSQL(f) + `),0)`)
	}
	return b.String()
}

func resourceContentFilterOptions(counts []int) []ContentFilter {
	labels := map[string]string{"all": "All", "unwatched": "Unwatched", "inProgress": "In progress", "watched": "Watched"}
	options := make([]ContentFilterOption, 0, len(resourceContentFilterIDs))
	for n, f := range resourceContentFilterIDs {
		options = append(options, ContentFilterOption{ID: f, Label: labels[f], Count: counts[n]})
	}
	return []ContentFilter{{ID: "filter", LabelKey: "saved.filter", Options: options}}
}

// resourceContentOrder is the page order and its keyset. The resource's own
// order seeks its (order_key, id) index; a display order sorts hidden
// occurrences last in either direction, then by its key, then by occurrence.
type resourceContentOrder struct {
	columns []string // the keyset, as SQL, in comparison order
	numeric []bool
	orderBy string
	op      string
}

// tie is the occurrence's unique id column; tieNumeric when it is an integer.
func newResourceContentOrder(q ResourceContentQuery, sort resourceContentSort, tie string, tieNumeric bool) resourceContentOrder {
	dir, op := " ASC", ">"
	if q.Direction == "desc" {
		dir, op = " DESC", "<"
	}
	if sort.key == "" {
		return resourceContentOrder{columns: []string{"e.order_key", tie}, numeric: []bool{false, tieNumeric}, orderBy: "e.order_key" + dir + "," + tie + dir, op: op}
	}
	// Hidden last: ascending ranks hidden 1, descending ranks it 0.
	rank, empty := `(i.id IS NULL)`, `''`
	if q.Direction == "desc" {
		rank = `(i.id IS NOT NULL)`
	}
	if sort.numeric {
		empty = `0.0`
	}
	key := `CASE WHEN i.id IS NULL THEN ` + empty + ` ELSE COALESCE(` + sort.key + `,` + empty + `) END`
	return resourceContentOrder{columns: []string{rank, key, tie}, numeric: []bool{true, sort.numeric, tieNumeric}, orderBy: rank + dir + "," + key + dir + "," + tie + dir, op: op}
}

// select lists the keyset columns as k0..kn for the page's continuation.
func (o resourceContentOrder) selectSQL() string {
	parts := make([]string, len(o.columns))
	for n, c := range o.columns {
		parts[n] = c + ` AS k` + strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}

// after is the row-value seek past a continuation's keys. Numbers are bound as
// numbers (SQLite ranks every INTEGER or REAL below any TEXT), so a page never
// skips the rows that tie with its last one.
func (o resourceContentOrder) after(keys []string) (string, []any, error) {
	if len(keys) != len(o.columns) {
		return "", nil, ErrCursor
	}
	args := make([]any, len(keys))
	for n, key := range keys {
		if !o.numeric[n] {
			args[n] = key
			continue
		}
		if i, e := strconv.ParseInt(key, 10, 64); e == nil {
			args[n] = i
			continue
		}
		f, e := strconv.ParseFloat(key, 64)
		if e != nil {
			return "", nil, ErrCursor
		}
		args[n] = f
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
	return `(` + strings.Join(o.columns, ",") + `)` + o.op + `(` + marks + `)`, args, nil
}

// resourceOrderKeys renders scanned keyset values for a cursor exactly, so the
// seek resumes at the same value it stopped at.
func resourceOrderKeys(values []any) []string {
	out := make([]string, len(values))
	for n, v := range values {
		switch value := v.(type) {
		case int64:
			out[n] = strconv.FormatInt(value, 10)
		case float64:
			out[n] = strconv.FormatFloat(value, 'g', -1, 64)
		default:
			out[n] = formatOrderValue(v)
		}
	}
	return out
}
