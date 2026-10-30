package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// This encoding belongs ONLY to leased in-memory readprep payloads. It never
// crosses an HTTP boundary or enters accepted command storage.
type groupCursorValue struct {
	Integer         int64
	Real            float64
	Text            string
	Type            uint8
	NoCase, Reverse bool
}
type groupCursorContent struct {
	ID     int64
	Values []groupCursorValue
}

func (c *GroupOrderWindowCursor) Bytes() int {
	if c == nil {
		return 0
	}
	n := int(unsafe.Sizeof(*c))
	for _, v := range c.key.values {
		n += len(v.s)
	}
	return n
}
func (c GroupOrderWindowCursor) MarshalJSON() ([]byte, error) {
	content := groupCursorContent{ID: c.id, Values: make([]groupCursorValue, c.key.n)}
	for i, v := range c.key.values[:c.key.n] {
		content.Values[i] = groupCursorValue{Integer: v.i, Real: v.f, Text: v.s, Type: v.typ, NoCase: v.nocase, Reverse: v.reverse}
	}
	return json.Marshal(content)
}
func (c *GroupOrderWindowCursor) UnmarshalJSON(raw []byte) error {
	if len(raw) > SelectionBatchMaximumBytes {
		return ErrSelectionBatchCapacity
	}
	var content groupCursorContent
	if err := json.Unmarshal(raw, &content); err != nil {
		return err
	}
	if content.ID <= 0 || len(content.Values) == 0 || len(content.Values) > 12 {
		return ErrSelectionBatchStale
	}
	next := GroupOrderWindowCursor{id: content.ID, key: smallOrderKey{n: len(content.Values), present: true}}
	for i, v := range content.Values {
		if v.Type > 3 {
			return ErrSelectionBatchStale
		}
		next.key.values[i] = smallOrderValue{i: v.Integer, f: v.Real, s: v.Text, typ: v.Type, nocase: v.NoCase, reverse: v.Reverse}
	}
	*c = next
	return nil
}

func (c *localQueueComputation) stepLocalGroup(ctx context.Context, b *readprep.Budget) (readprep.StepResult, error) {
	step := func(ctx context.Context, state *BrowseSelectionBatchState, witness SelectionBatchWitness, limits SmallSelectionOrderLimits) (*BrowseSelectionBatchState, SelectionBatchWork, error) {
		return c.service.StepBrowseSelectionBatch(ctx, state, witness, SelectionBatchLimits{Rows: limits.Rows, Bytes: limits.Bytes, RetainedBytes: limits.RetainedBytes, ReserveTransient: limits.ReserveTransient})
	}
	next, _, err := c.service.StepGroupOrderWindow(ctx, c.group, c.witness, step, SmallSelectionOrderLimits{ReserveTransient: func(n int64) error { return c.reserve(b, n) }})
	if err != nil {
		return readprep.StepResult{}, selectionPreparationError(err)
	}
	c.group = next
	if !next.Complete() {
		return readprep.StepResult{}, nil
	}
	ids, cursor, eof, err := next.Result()
	if err != nil {
		return readprep.StepResult{}, err
	}
	if err = c.reserve(b, int64(12*cursor.Bytes()+len(ids)*1024+65536)); err != nil {
		return readprep.StepResult{}, err
	}
	public := make([]string, 0, len(ids))
	err = dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassMaintenance), c.service.db, func(inner context.Context) error {
		read := c.service.WithContext(inner)
		basis, err := c.witness(read)
		if err != nil {
			return err
		}
		if basis != c.input.Basis {
			return ErrSelectionBatchStale
		}
		for _, id := range ids {
			var value string
			err = read.read().QueryRow(`SELECT pid(i.public_id) FROM catalog_entities i JOIN catalog_kinds k ON k.id=i.kind AND k.playable=1 WHERE i.id=? AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=i.id)`, id).Scan(&value)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			public = append(public, value)
		}
		return nil
	})
	if err != nil {
		return readprep.StepResult{}, selectionPreparationError(err)
	}
	c.payload, err = json.Marshal(localQueueOutput{IDs: public, EOF: eof, Cursor: localQueueCursor{Group: cursor}})
	if err != nil {
		return readprep.StepResult{}, err
	}
	return readprep.StepResult{Done: true, Payload: c.payload}, nil
}
