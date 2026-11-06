package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

func wc008ProgressBucket(ctx context.Context, turn *commandTurn, q wc008ProgressCommand, grain string, bucket, credit int64, item bool) error {
	return wc008ProgressBucketDelta(ctx, turn, q, grain, bucket, 0, 0, credit, item)
}

// Finite same-turn point/read/CAS branches. Counter tuples retain other classes'
// values; neither source capture nor time drain overwrites the other's credits.
func wc008ProgressBucketDelta(ctx context.Context, turn *commandTurn, q wc008ProgressCommand, grain string, bucket, started, completed, credit int64, item bool) error {
	read, update, insert := wc008ProgressProfileBucketRead, wc008ProgressProfileBucketUpdate, wc008ProgressProfileBucket
	args := []any{q.profileKey, q.proof.activityGeneration, grain, bucket}
	if item {
		read, update, insert = wc008ProgressItemBucketRead, wc008ProgressItemBucketUpdate, wc008ProgressItemBucket
		args = append(args, q.subjectID)
	}
	row, err := wc008DraftQuery(ctx, turn, read, args...)
	if err != nil {
		return err
	}
	var old [3]int64
	err = row.Scan(&old[0], &old[1], &old[2])
	delta := [3]int64{started, completed, credit}
	var next [3]int64
	for i, n := range delta {
		if n < 0 || old[i] < 0 || old[i] > math.MaxInt64-n {
			return ErrResolveRequired
		}
		next[i] = old[i] + n
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		values := append(args, started, completed, credit)
		_, err = wc008DraftInteger(ctx, turn, insert, values...)
	case err != nil:
		return err
	default:
		values := append([]any{next[0], next[1], next[2]}, args...)
		values = append(values, old[0], old[1], old[2])
		var actual int64
		actual, err = wc008DraftInteger(ctx, turn, update, values...)
		if err == nil && actual != next[2] {
			return ErrResolveRequired
		}
	}
	return err
}
