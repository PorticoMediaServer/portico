package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// The reducer visits the maintained count-order index, not holders. It keeps
// only <=201 page identities, <=4096 complete seed identities and an exact total.
// A larger match set drops the seed, keeping the bounded page. Cold windows are
// O(V+label bytes), including deep pages; they are not advertised as O(page).
// Matching precomputed sorttext.Fold bytes via BLOB instr is exactly Go's
// strings.Contains, including a one-byte needle and embedded NUL bytes.
// No query writes, maps of all values, GROUP BY, FTS or foreground sorts.
type facetWindowScan struct {
	db             *sql.DB
	base           int64
	spec           facetWindowSpec
	bytes          int64
	ids            []int64
	cursor         int64
	total          int64
	earlier, later bool
	prefix         facetWindowPrefix
}

func (s *facetWindowScan) AccountedBytes() int64 { return s.bytes }

type facetWindowReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const facetWindowOrder = `b.negative_count,b.label_order,b.value_order,b.value_id`
const facetWindowTuple = `(` + facetWindowOrder + `)`
const facetWindowAnchor = `(SELECT negative_count,label_order,value_order,value_id FROM catalog_facet_base_rows WHERE base_id=? AND value_id=?)`

func (s *facetWindowScan) Step(ctx context.Context, budget *readprep.Budget) (readprep.StepResult, error) {
	if err := budget.Resize(s.bytes); err != nil {
		return readprep.StepResult{}, err
	}
	ready, err := s.preparePrefix(ctx, budget)
	if err != nil || !ready {
		return readprep.StepResult{}, err
	}
	done := false
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	// Each SELECT owns one short implicit read snapshot, released by Rows.Close.
	// COMPLETE inputs are immutable, so before/after basis checks suffice; an
	// explicit multi-statement snapshot would impose a hard 500ms scalar cap.
	err = func() error {
		tx := dbwork.ReadHandle(ctx, s.db)
		if _, err := facetBaseSnapshot(ctx, tx, s.base, true); err != nil {
			return readprep.ErrStale
		}
		if s.spec.Needle == "" {
			var err error
			done, err = s.empty(ctx, tx)
			return err
		}
		if s.prefix.seeded {
			var err error
			done, err = s.candidateBatch(ctx, tx)
			return err
		}
		eligible, args := s.eligibility()
		query := `SELECT b.value_id,length(CAST(x.folded AS BLOB))+length(CAST(b.label_order AS BLOB))+length(CAST(b.value_order AS BLOB)),instr(CAST(x.folded AS BLOB),CAST(? AS BLOB))>0,` + eligible + `
 FROM catalog_facet_base_rows b INDEXED BY catalog_facet_base_order JOIN catalog_facet_base_texts x ON x.id=b.text_id WHERE b.base_id=?`
		args = append(args, s.base)
		if s.cursor > 0 {
			query += ` AND ` + facetWindowTuple + `>` + facetWindowAnchor
			args = append(args, s.base, s.cursor)
		}
		query += ` ORDER BY ` + facetWindowOrder + ` LIMIT 4096`
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		started := time.Now()
		n := 0
		bytes := int64(0)
		for rows.Next() {
			var id, width int64
			var match, accept bool
			if err = rows.Scan(&id, &width, &match, &accept); err != nil {
				return err
			}
			n++
			bytes += width
			s.cursor = id
			if match {
				s.match(id, accept)
			}
			// Soft yield: do not cancel a successfully processed row or silently lose
			// its cursor. A single legal scalar is the explicit O(B) exception.
			if bytes >= 4<<20 || time.Since(started) >= 8*time.Millisecond {
				return nil
			}
		}
		if err = rows.Err(); err != nil {
			return err
		}
		done = n < 4096
		return nil
	}()
	if err == nil {
		if _, checkErr := facetBaseSnapshot(ctx, dbwork.ReadHandle(ctx, s.db), s.base, true); checkErr != nil {
			err = readprep.ErrStale
		}
	}
	if err != nil {
		return readprep.StepResult{}, err
	}
	if !done {
		return readprep.StepResult{}, nil
	}
	earlier, later := s.earlier, s.later
	if len(s.ids) > s.spec.Limit {
		if s.spec.Direction == 2 {
			s.ids = s.ids[1:]
			earlier = true
		} else {
			s.ids = s.ids[:s.spec.Limit]
			later = true
		}
	}
	sum := facetWindowSummary{Spec: s.spec, IDs: s.ids, Total: s.total, Earlier: earlier, Later: later, CompleteMatchSet: s.prefix.initialized && !s.prefix.overflow, CompleteMatchIDs: s.prefix.matches}
	// Bounded payload: page IDs + <=4096 COMPLETE candidate IDs + folded query.
	// Charge encoder growth/overlap and the returned payload before allocation.
	encodeBytes := int64(32768)
	if sum.CompleteMatchSet {
		encodeBytes = facetWindowPrefixEncodeBytes
	}
	ok, err := budget.TryResize(s.bytes + encodeBytes)
	if err != nil {
		return readprep.StepResult{}, err
	}
	if !ok {
		// The exact page/total is already complete. Discard the optional seed
		// when its encoder peak cannot fit; never refuse only to cache a seed.
		sum.CompleteMatchSet, sum.CompleteMatchIDs = false, nil
		s.prefix.matches, s.prefix.seed = nil, nil
		encodeBytes = 32768
		if err = budget.Resize(s.bytes + encodeBytes); err != nil {
			return readprep.StepResult{}, err
		}
	}
	payload, err := json.Marshal(sum)
	if err != nil {
		return readprep.StepResult{}, err
	}
	s.bytes += encodeBytes
	return readprep.StepResult{Done: true, Payload: payload}, nil
}

// empty uses the ready index and maintained exact distinct-value header. There
// is no all-value scan even at a cold initial, before, after or inclusive reload.
func (s *facetWindowScan) empty(ctx context.Context, tx facetWindowReader) (bool, error) {
	where := "b.base_id=?"
	args := []any{s.base}
	order := facetWindowOrder
	if s.spec.Anchor > 0 {
		op := ">"
		if s.spec.Direction == 2 {
			op = "<"
			order = `b.negative_count DESC,b.label_order DESC,b.value_order DESC,b.value_id DESC`
		}
		if s.spec.Direction == 3 {
			op = ">="
		}
		where += ` AND ` + facetWindowTuple + op + facetWindowAnchor
		args = append(args, s.base, s.spec.Anchor)
	}
	args = append(args, s.spec.Limit+1)
	rows, err := tx.QueryContext(ctx, `SELECT b.value_id FROM catalog_facet_base_rows b INDEXED BY catalog_facet_base_order WHERE `+where+` ORDER BY `+order+` LIMIT ?`, args...)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return false, err
		}
		s.ids = append(s.ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	if s.spec.Direction == 2 {
		s.later = true
		if len(s.ids) > s.spec.Limit {
			s.earlier = true
			s.ids = s.ids[:s.spec.Limit]
		}
		for i, j := 0, len(s.ids)-1; i < j; i, j = i+1, j-1 {
			s.ids[i], s.ids[j] = s.ids[j], s.ids[i]
		}
	}
	if s.spec.Direction == 1 {
		s.earlier = true
	}
	if s.spec.Direction == 3 {
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_facet_base_rows b INDEXED BY catalog_facet_base_order WHERE b.base_id=? AND `+facetWindowTuple+`<`+facetWindowAnchor+`)`, s.base, s.base, s.spec.Anchor).Scan(&s.earlier)
	}
	return true, err
}
