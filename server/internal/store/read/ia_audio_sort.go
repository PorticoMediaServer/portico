package read

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Match RS's complete listening-cohort ceiling. Actual native/copy custody
// still applies independently; the ceiling never grants a larger allocation.
const IAAudioListeningChildCap = 512
const iaAudioListeningOrderVersion = 1

type iaAudioListeningRow struct {
	row []any
	key string
}

// Read the complete primitive membership before sorting. Each variable key
// comes through the existing same-snapshot registered entity octet/payload
// pair; no unmeasured variable-column membership query is introduced.
func (s *iaAudioSnapshots) listeningRows(ctx context.Context, q browseQuery, parent, library, kind, outputBytes int64) ([]iaAudioListeningRow, error) {
	name := "album"
	if kind == 9 {
		name = "book"
	}
	raw, err := q("foreground.ia.audio."+name+".first", parent, int64(IAAudioListeningChildCap+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > IAAudioListeningChildCap {
		return nil, ErrUnqualified
	}
	// Quarter-output custody covers the retained row/key slice, in-place sort
	// workspace and key/cursor overlap. Charge before allocating or retaining.
	charge, limit := int64(512+128*len(raw)), outputBytes/4
	if charge > limit {
		return nil, governor.ErrSnapshotBudget
	}
	rows := make([]iaAudioListeningRow, 0, len(raw))
	for _, row := range raw {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if row[2] != library {
			return nil, ErrDenied
		}
		value, err := s.point("entity", []int{1, 2, 3, 14, 18}, row[0], library)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrDenied
		}
		if err != nil {
			return nil, err
		}
		key := value[1].(string)
		if int64(len(key)) > (limit-charge)/2 {
			return nil, governor.ErrSnapshotBudget
		}
		charge += 2 * int64(len(key))
		rows = append(rows, iaAudioListeningRow{row: row, key: key})
	}
	slices.SortFunc(rows, func(a, b iaAudioListeningRow) int {
		return iaAudioListeningCompare(a.row[12].(int64), a.row[13].(int64), "", a.row[0].(int64),
			b.row[12].(int64), b.row[13].(int64), "", b.row[0].(int64))
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func iaAudioListeningCompare(disc, position int64, key string, id, otherDisc, otherPosition int64, otherKey string, otherID int64) int {
	if c := cmp.Compare(disc, otherDisc); c != 0 {
		return c
	}
	if c := cmp.Compare(position, otherPosition); c != 0 {
		return c
	}
	if c := iaAudioListeningKeyCompare(key, otherKey); c != 0 {
		return c
	}
	return cmp.Compare(id, otherID)
}

// Same native NOCASE ordering as RS #1430: ASCII folds only; embedded NUL
// stops byte comparison, then the original byte lengths break the tie.
func iaAudioListeningKeyCompare(a, b string) int {
	for n := 0; n < min(len(a), len(b)); n++ {
		x, y := a[n], b[n]
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}
		if y >= 'A' && y <= 'Z' {
			y += 'a' - 'A'
		}
		if c := cmp.Compare(x, y); c != 0 {
			return c
		}
		if x == 0 {
			break
		}
	}
	return cmp.Compare(len(a), len(b))
}
