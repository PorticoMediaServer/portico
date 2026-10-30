package catalog

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// GroupOrderWindowCursor is a process-local continuation, not an HTTP or
// durable command cursor. Accepted command targets remain under their existing
// receipt rules. A lost cursor restarts from those accepted targets.
type GroupOrderWindowCursor struct {
	key smallOrderKey
	id  int64
}
type groupOrderWindowHit struct {
	id  int64
	key smallOrderKey
}

// GroupOrderWindow finds the next <=128 matches in exact group order using
// capped memory. It scans raw identities, evaluates the existing singleton
// reducer, then retains only the best next window's native minimum tuples.
// A complete raw pass proves the window, even if almost every row is rejected.
// Repeated pages may repeat raw passes; no 1M/5M throughput claim is made.
type GroupOrderWindow struct {
	template, candidate             *BrowseSelectionBatchState
	order                           *SmallSelectionOrder
	target                          BrowseNode
	direction, basis                string
	after                           GroupOrderWindowCursor
	limit, stage                    int
	rawAfter, current, previous, id int64
	more                            bool
	done                            bool
	hits                            []groupOrderWindowHit
}

// GroupOrderCandidateStep must use the DB002 singleton evaluator with its
// native scalar/token reservation callback. The final P2-47b adapter supplies
// that callback through SelectionBatchLimits.ReserveTransient. Keeping this
// adapter explicit lets independent code compile on the lead's older base.
type GroupOrderCandidateStep func(context.Context, *BrowseSelectionBatchState, SelectionBatchWitness, SmallSelectionOrderLimits) (*BrowseSelectionBatchState, SelectionBatchWork, error)

func NewGroupOrderWindow(template *BrowseSelectionBatchState, target *BrowseNode, direction string, limit int, after *GroupOrderWindowCursor) (*GroupOrderWindow, error) {
	if template == nil || template.plan == nil || target == nil || limit < 1 || limit > 128 {
		return nil, ErrSelectionBatchUnsupported
	}
	// Validate the native source shape without running any query.
	probe, err := NewSmallGroupOrder(nil, target, direction, template.plan.witness)
	if err != nil {
		return nil, err
	}
	w := &GroupOrderWindow{template: &BrowseSelectionBatchState{plan: template.plan}, target: BrowseNode{Field: target.Field, Operator: target.Operator, Value: probe.group.value}, direction: direction, basis: template.plan.witness, limit: limit, hits: make([]groupOrderWindowHit, 0, limit)}
	if after != nil {
		w.after = *after
	}
	if w.Bytes() > SelectionBatchMaximumBytes {
		return nil, ErrSelectionBatchCapacity
	}
	return w, nil
}
func (w *GroupOrderWindow) Bytes() int {
	if w == nil {
		return 0
	}
	n := int(unsafe.Sizeof(*w)) + w.template.Bytes() + w.candidate.Bytes() + w.order.Bytes() + len(w.direction) + len(w.basis) + len(w.target.Field) + len(w.target.Operator) + cap(w.hits)*int(unsafe.Sizeof(groupOrderWindowHit{}))
	if v, ok := w.target.Value.(string); ok {
		n += len(v)
	}
	for _, v := range w.after.key.values {
		n += len(v.s)
	}
	for _, h := range w.hits {
		for _, v := range h.key.values {
			n += len(v.s)
		}
	}
	return n
}
func (w *GroupOrderWindow) clone() *GroupOrderWindow {
	n := *w
	n.hits = slices.Clone(w.hits)
	if w.candidate != nil {
		n.candidate = w.candidate.Clone()
	}
	if w.order != nil {
		n.order = w.order.clone()
	}
	return &n
}
func (w *GroupOrderWindow) Complete() bool { return w != nil && w.done }
func (w *GroupOrderWindow) Result() ([]int64, *GroupOrderWindowCursor, bool, error) {
	if !w.Complete() {
		return nil, nil, false, ErrSelectionBatchIncomplete
	}
	ids := make([]int64, len(w.hits))
	for i, h := range w.hits {
		ids[i] = h.id
	}
	var cursor *GroupOrderWindowCursor
	if len(w.hits) > 0 {
		last := w.hits[len(w.hits)-1]
		cursor = &GroupOrderWindowCursor{key: last.key, id: last.id}
	}
	return ids, cursor, !w.more, nil
}
func (w *GroupOrderWindow) compare(a, b groupOrderWindowHit) int {
	c := smallOrderCompare(a.key, b.key)
	if c == 0 {
		if a.id < b.id {
			c = -1
		} else if a.id > b.id {
			c = 1
		}
	}
	if w.direction == "desc" {
		c = -c
	}
	return c
}

// StepGroupOrderWindow performs a raw unit or one delegated predicate quantum,
// never a post-filter SELECT LIMIT. Its reservation includes the full template,
// its clone, both nested states, keys and the worker's source/native preloads.
func (s *Service) StepGroupOrderWindow(ctx context.Context, previous *GroupOrderWindow, witness SelectionBatchWitness, step GroupOrderCandidateStep, limits SmallSelectionOrderLimits) (*GroupOrderWindow, SelectionBatchWork, error) {
	var work SelectionBatchWork
	if previous == nil || witness == nil || step == nil {
		return nil, work, ErrSelectionBatchStale
	}
	retained := limits.RetainedBytes
	if retained <= 0 || retained > SelectionBatchMaximumBytes {
		retained = SelectionBatchMaximumBytes
	}
	if previous.Bytes() > retained {
		return nil, work, ErrSelectionBatchCapacity
	}
	reserve := func(n int64) error {
		if limits.ReserveTransient != nil {
			return limits.ReserveTransient(int64(2*previous.Bytes()) + n + 65536)
		}
		return nil
	}
	if err := reserve(SelectionBatchMaximumBytes); err != nil {
		return nil, work, err
	}
	next := previous.clone()
	innerLimits := limits
	innerLimits.Rows = 1
	innerLimits.ReserveTransient = reserve
	if next.candidate != nil {
		candidate, used, err := step(ctx, next.candidate, witness, innerLimits)
		work = used
		if err != nil {
			return nil, work, err
		}
		next.candidate = candidate
		if candidate.candidate.stage == 0 {
			matched := false
			for _, count := range candidate.counts {
				matched = matched || count[0] > 0
			}
			next.candidate = nil
			if matched {
				next.order, err = NewSmallGroupOrder([]int64{next.id}, &next.target, next.direction, next.basis)
				if err != nil {
					return nil, work, err
				}
			}
		}
	} else if next.order != nil {
		order, used, err := s.StepSmallSelectionOrder(ctx, next.order, witness, innerLimits)
		work = used
		if err != nil {
			return nil, work, err
		}
		next.order = order
		if order.Complete() {
			hit := groupOrderWindowHit{id: next.id, key: order.group.keys[0]}
			next.order = nil
			if next.after.id == 0 || next.compare(hit, groupOrderWindowHit{id: next.after.id, key: next.after.key}) > 0 {
				at, found := slices.BinarySearchFunc(next.hits, hit, next.compare)
				if found {
					return nil, work, ErrSelectionBatchStale
				}
				if len(next.hits) == next.limit {
					next.more = true
					if at < len(next.hits) {
						copy(next.hits[at+1:], next.hits[at:len(next.hits)-1])
						next.hits[at] = hit
					}
				} else {
					next.hits = slices.Insert(next.hits, at, hit)
				}
			}
		}
	} else {
		work.Rows = 1
		work.ExaminedBytes = selectionBatchProbeBytes
		err := dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassMaintenance), s.db, func(inner context.Context) error {
			read := s.WithContext(inner)
			current, err := witness(read)
			if err != nil {
				return err
			}
			if current != next.basis {
				return ErrSelectionBatchStale
			}
			if next.done {
				return nil
			}
			tx := dbwork.Snapshot(inner)
			switch next.stage {
			case 0:
				next.current, next.previous = 0, 0
				err = tx.QueryRowContext(inner, `SELECT entity_id FROM catalog_browse_rows WHERE entity_id>? ORDER BY entity_id LIMIT 1`, next.rawAfter).Scan(&next.current)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				next.stage = 1
			case 1:
				err = tx.QueryRowContext(inner, `SELECT entity_id FROM catalog_browse_rows_previous WHERE generation=(SELECT generation FROM catalog_generations WHERE singleton=1 AND retaining=1) AND entity_id>? ORDER BY entity_id LIMIT 1`, next.rawAfter).Scan(&next.previous)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				next.stage = 2
			case 2:
				next.id = next.current
				if next.previous > 0 && (next.id == 0 || next.previous < next.id) {
					next.id = next.previous
				}
				next.stage = 0
				if next.id == 0 {
					next.done = true
					return nil
				}
				// Both raw runs advance before any scope, visibility or query test.
				next.rawAfter = next.id
				next.candidate = &BrowseSelectionBatchState{plan: next.template.plan, candidate: selectionBatchCandidate{id: next.id, stage: 2}}
			default:
				return ErrSelectionBatchIncomplete
			}
			return nil
		})
		if err != nil {
			return nil, work, err
		}
	}
	work.Bytes = next.Bytes()
	if work.Bytes > retained {
		return nil, work, ErrSelectionBatchCapacity
	}
	return next, work, nil
}
