package catalog

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// SmallSelectionOrder is detached, volatile work for a COMPLETE selection of
// at most 200 identities. Callers must obtain that completeness proof from the
// prepared selection, not from a limited ordinary page. It carries no grants.
// Integration with the shared readprep store owns lifetime and delivery leases.
type SmallSelectionOrder struct {
	ids, ordered []int64
	basis        string
	done         bool
	group        *smallGroupOrder
	forYou       *smallForYouOrder
}

// SmallSelectionOrderLimits is separate from the active P2-47b reducer's
// options so these helpers can be reviewed against the current main pin.
type SmallSelectionOrderLimits struct {
	Rows, Bytes, RetainedBytes int
	ReserveTransient           func(int64) error
}

func newSmallSelectionOrder(ids []int64, basis string) (*SmallSelectionOrder, error) {
	if basis == "" {
		return nil, ErrSelectionBatchStale
	}
	if len(ids) > BrowseSelectionConversionLimit {
		return nil, ErrSelectionBatchCapacity
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return nil, ErrSelectionBatchUnsupported
		}
		seen[id] = true
	}
	return &SmallSelectionOrder{ids: slices.Clone(ids), basis: basis}, nil
}

func (p *SmallSelectionOrder) Complete() bool { return p != nil && p.done }

// IDs returns a copy only after EOF. The caller must still validate basis and
// authority in its fresh delivery/accepting transaction.
func (p *SmallSelectionOrder) IDs() ([]int64, error) {
	if !p.Complete() {
		return nil, ErrSelectionBatchIncomplete
	}
	return slices.Clone(p.ordered), nil
}

func (p *SmallSelectionOrder) Bytes() int {
	if p == nil {
		return 0
	}
	n := int(unsafe.Sizeof(*p)) + len(p.basis) + 8*(cap(p.ids)+cap(p.ordered))
	if p.group != nil {
		n += p.group.bytes()
	}
	if p.forYou != nil {
		n += p.forYou.bytes()
	}
	return n
}

func (p *SmallSelectionOrder) clone() *SmallSelectionOrder {
	n := *p
	n.ids, n.ordered = slices.Clone(p.ids), slices.Clone(p.ordered)
	if p.group != nil {
		n.group = p.group.clone()
	}
	if p.forYou != nil {
		n.forYou = p.forYou.clone()
	}
	return &n
}

// StepSmallSelectionOrder advances bounded raw source units in one fresh
// background snapshot. As in DB002, a scalar is indivisible: its complete
// transient reservation precedes decoding, and crossing the byte quantum ends
// the turn. Capacity/drift discard the clone; no partial order is published.
func (s *Service) StepSmallSelectionOrder(ctx context.Context, previous *SmallSelectionOrder, witness SelectionBatchWitness, limits SmallSelectionOrderLimits) (*SmallSelectionOrder, SelectionBatchWork, error) {
	var work SelectionBatchWork
	if previous == nil || witness == nil || previous.basis == "" {
		return nil, work, ErrSelectionBatchStale
	}
	if dbwork.Snapshot(ctx) != nil || s.readTx != nil {
		return nil, work, sql.ErrTxDone
	}
	rows, bytes, retained := limits.Rows, limits.Bytes, limits.RetainedBytes
	if rows <= 0 || rows > SelectionBatchMaximumRows {
		rows = SelectionBatchMaximumRows
	}
	if bytes <= 0 || bytes > SelectionBatchMaximumBytes {
		bytes = SelectionBatchMaximumBytes
	}
	if retained <= 0 || retained > SelectionBatchMaximumBytes {
		retained = SelectionBatchMaximumBytes
	}
	if previous.Bytes() > retained {
		return nil, work, ErrSelectionBatchCapacity
	}
	if limits.ReserveTransient != nil {
		if err := limits.ReserveTransient(int64(2 * previous.Bytes())); err != nil {
			return nil, work, err
		}
	}
	next := previous.clone()
	started := time.Now()
	err := dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), s.db, func(inner context.Context) error {
		read := s.WithContext(inner)
		check := func() error {
			current, err := witness(read)
			if err != nil {
				return err
			}
			if current == "" || current != next.basis {
				return ErrSelectionBatchStale
			}
			return nil
		}
		if err := check(); err != nil {
			return err
		}
		reserve := func(n int64) error {
			// Negative internal requests reserve Go allocation only. Positive
			// requests disclose actual source scalar bytes, including rejects.
			// Transient source work must not consume the retained-state budget.
			allocation := n < 0
			if allocation {
				n = -n
			}
			peak := int64(previous.Bytes()+next.Bytes()) + 4*n + 65536
			if n < 0 || peak > 32<<20 {
				return ErrSelectionBatchCapacity
			}
			if !allocation {
				work.DecodedBytes += int(n)
				work.ExaminedBytes += int(n)
				work.TransientScalarBytes = max(work.TransientScalarBytes, int(n))
			}
			if limits.ReserveTransient != nil {
				return limits.ReserveTransient(peak)
			}
			return nil
		}
		if next.forYou != nil {
			if rows < 2 && next.forYou.profile != "" {
				return ErrSelectionBatchCapacity
			}
			fence, validation, err := read.smallForYouActivityFence(next.forYou.profile, next.forYou.now, max(1, rows-1), bytes, func(n int64) error {
				peak := int64(previous.Bytes()+next.Bytes()) + n
				if peak > 32<<20 {
					return ErrSelectionBatchCapacity
				}
				if limits.ReserveTransient != nil {
					return limits.ReserveTransient(peak)
				}
				return nil
			})
			work.Rows += validation.Rows
			work.ExaminedBytes += validation.ExaminedBytes
			work.DecodedBytes += validation.DecodedBytes
			work.TransientScalarBytes = max(work.TransientScalarBytes, validation.TransientScalarBytes)
			if err != nil {
				return err
			}
			if next.forYou.activityFence != "" && next.forYou.activityFence != fence {
				return ErrSelectionBatchStale
			}
			next.forYou.activityFence = fence
			if work.Rows >= rows || work.ExaminedBytes+selectionBatchProbeBytes > bytes {
				return ErrSelectionBatchCapacity
			}
		}
		for !next.done && work.Rows < rows && work.ExaminedBytes < bytes {
			if err := inner.Err(); err != nil {
				return err
			}
			work.Rows++
			work.ExaminedBytes += selectionBatchProbeBytes
			var err error
			switch {
			case next.group != nil:
				err = read.smallGroupUnit(next, reserve)
			case next.forYou != nil:
				err = read.smallForYouUnit(next, reserve)
			default:
				err = ErrSelectionBatchUnsupported
			}
			if err != nil {
				return err
			}
			work.Bytes = next.Bytes()
			if work.Bytes > retained {
				return ErrSelectionBatchCapacity
			}
			if time.Since(started) >= selectionBatchTime {
				break
			}
		}
		return check()
	})
	if err != nil {
		return nil, work, err
	}
	return next, work, nil
}

// All variable source text passes this length probe before SQL/driver copies
// are allocated. query must select one text scalar by a pinned native key.
func (s *Service) smallOrderText(query string, args []any, reserve func(int64) error) (string, bool, error) {
	var size int64
	err := s.read().QueryRow(`SELECT length(CAST(v AS BLOB)) FROM (`+query+`)`, args...).Scan(&size)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if err = reserve(size); err != nil {
		return "", false, err
	}
	var value string
	err = s.read().QueryRow(query, args...).Scan(&value)
	return value, err == nil, err
}
