package read

import (
	"math"
	"sort"
)

// rbAuthorOrderRow is a preparation value, not an eligibility or exhaustion
// witness. Position must come from CAST(NULLIF(series_position,”) AS REAL)
// in an admitted source snapshot; nil preserves SQL NULL before numeric zero.
// TitleKey is the selected accepted article-policy key, not a derived title.
type rbAuthorOrderRow struct {
	Book, Part       int64
	Series, TitleKey string
	Position         *float64
	Disc, Number     int64 // source COALESCE(disc_number,1), COALESCE(part_number,0)
}

// rbAuthorOrderDraft sorts one bounded preparation fragment. Sorting fragments
// does NOT establish global author order: the root must complete membership,
// deduplicate books, merge all fragments, and reauthorize publication. It must
// also lift parts into requested browse entities with their earliest position.
func rbAuthorOrderDraft(rows []rbAuthorOrderRow, descending bool) ([]rbAuthorOrderRow, error) {
	if len(rows) > MaxRaw {
		return nil, ErrNotReady
	}
	out := append([]rbAuthorOrderRow(nil), rows...)
	// Copy nullable numeric values so a caller cannot mutate retained keys.
	for i := range out {
		if out[i].Position != nil {
			v := *out[i].Position
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, ErrUnqualified
			}
			out[i].Position = &v
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if descending {
			a, b = b, a
		}
		return rbAuthorOrderLess(a, b)
	})
	return out, nil
}
func rbAuthorOrderLess(a, b rbAuthorOrderRow) bool {
	if (a.Series == "") != (b.Series == "") {
		return a.Series != ""
	}
	sa, sb := rbRelationshipFold(a.Series), rbRelationshipFold(b.Series)
	if sa != sb {
		return sa < sb
	}
	if (a.Position == nil) != (b.Position == nil) {
		return a.Position == nil
	}
	if a.Position != nil && *a.Position != *b.Position {
		return *a.Position < *b.Position
	}
	ta, tb := rbRelationshipFold(a.TitleKey), rbRelationshipFold(b.TitleKey)
	if ta != tb {
		return ta < tb
	}
	if a.Book != b.Book {
		return a.Book < b.Book
	}
	if a.Disc != b.Disc {
		return a.Disc < b.Disc
	}
	if a.Number != b.Number {
		return a.Number < b.Number
	}
	return a.Part < b.Part
}
