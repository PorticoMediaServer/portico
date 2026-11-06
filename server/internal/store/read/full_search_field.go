package read

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// This ordinary-table sibling step verifies a raw accepted-publication key
// supplied by the future native bank adapter. It mints NO accepted-head,
// profile/slot attestation or viewer permission. Every physical sibling is
// charged; physical ordinal never becomes the projection's logical ordinal0.
func readLogicalFieldStep(ctx context.Context, query browseQuery, walk *logicalFieldWalk, after int64, limit int) (next int64, exhausted bool, err error) {
	defer func() {
		if err != nil && walk != nil {
			walk.failed = true
		}
	}()
	if ctx == nil || query == nil || walk == nil || after < -1 || limit < 1 || limit > maxOwnerPositions {
		return after, false, ErrInput
	}
	if e := ctx.Err(); e != nil {
		return after, false, e
	}
	if after != walk.after || walk.nativeComplete {
		walk.failed = true
		return after, false, search.ErrFieldContinuation
	}
	walk.native = true
	key := walk.key
	name := "full.owner.field.first"
	args := []any{key.PublicationID, int64(key.Field)}
	if after >= 0 {
		name = "full.owner.field.next"
		args = append(args, after)
	}
	rows, err := query(name, append(args, int64(limit))...)
	if err != nil {
		return after, false, err
	}
	if len(rows) > limit {
		walk.failed = true
		return after, false, ErrUnqualified
	}
	next = after
	for _, row := range rows {
		ordinal := row[0].(int64)
		if ordinal <= next || row[4] != key.Revision || row[5] != key.InputGeneration || row[8] != key.BankID {
			walk.failed = true
			return after, false, search.ErrFieldContinuation
		}
		owner, other := row[6], row[7]
		if key.Subject == search.ManifestPerson {
			owner, other = other, owner
		}
		if owner != key.SubjectID || other != nil {
			walk.failed = true
			return after, false, search.ErrFieldContinuation
		}
		if err = walk.observe(ctx, key, row[2].(string), row[3].(string)); err != nil {
			return after, false, err
		}
		next = ordinal
	}
	walk.after = next
	walk.nativeComplete = len(rows) < limit
	return next, walk.nativeComplete, nil
}
