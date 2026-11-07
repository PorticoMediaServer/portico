package read

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"strings"
)

// Compiled adapter only: actual lengths precede the unchanged whole-binding
// payload. Optional assets are direct PK byte points, avoiding a joined scan in
// tiny analyzed scopes. No caller SQL, proof flag or permission callback exists.
func homeFixedBindingRows(s *savedViewSession, q browseQuery, name string, args ...any) ([][]any, error) {
	part := strings.TrimPrefix(name, "rb.home.binding.")
	var sizes, ids [][]any
	var e error
	switch part {
	case "item", "objects":
		sizes, e = q("rb.home.fixed.binding."+part+".bytes", args...)
	case "links":
		if len(args) != 3 {
			return nil, ErrInput
		}
		ids, e = q("rb.home.fixed.binding.links.ids", args[1], args[2])
		if e != nil {
			return nil, e
		}
		for _, id := range ids {
			point, e := q("rb.home.fixed.asset.bytes", id[0], args[0])
			if e != nil {
				return nil, e
			}
			if len(point) > 1 {
				return nil, ErrUnqualified
			}
			var n any
			if len(point) == 1 {
				n = point[0][0]
			}
			sizes = append(sizes, []any{n})
		}
	default:
		return nil, ErrInput
	}
	if e != nil {
		return nil, e
	}
	var total int64
	for _, row := range sizes {
		for _, v := range row {
			if v == nil {
				continue
			}
			n, ok := v.(int64)
			if !ok || n < 0 || n > s.nativeLimit-total {
				return nil, governor.ErrSnapshotBudget
			}
			total += n
		}
	}
	if 4096+3*total > s.copyLimit-s.copied {
		return nil, governor.ErrSnapshotBudget
	}
	payload := name
	if part == "item" {
		payload = "rb.home.fixed.binding.item.value"
	}
	rows, e := q(payload, args...)
	if e != nil {
		return rows, e
	}
	if len(rows) != len(sizes) {
		return nil, ErrStale
	}
	for n, row := range rows {
		switch part {
		case "item":
			if !homeFixedByteMatches(sizes[n][0], row[8]) {
				return nil, ErrStale
			}
		case "links":
			if row[0] != ids[n][0] || !homeFixedByteMatches(sizes[n][0], row[14]) {
				return nil, ErrStale
			}
		case "objects":
			for k, pos := range []int{0, 1, 2, 3, 15, 16} {
				if !homeFixedByteMatches(sizes[n][k+1], row[pos]) {
					return nil, ErrStale
				}
			}
		}
	}
	return rows, nil
}
func homeFixedByteMatches(size, value any) bool {
	if size == nil {
		return value == nil
	}
	n, ok := size.(int64)
	if !ok || n < 0 {
		return false
	}
	switch v := value.(type) {
	case string:
		return int64(len(v)) == n
	case []byte:
		return int64(len(v)) == n
	}
	return false
}
