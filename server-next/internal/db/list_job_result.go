package db

import (
	"context"
	"database/sql"
)

// ListJobResult counts committed additions for source titles the reader may see now.
// Added includes a playlist holder's expanded entries; Failed counts source titles.
type ListJobResult struct {
	Added          int64
	AlreadyPresent int64
	Failed         int64
}

// ListJobResult reads existing replay results and failures in one snapshot. It needs no
// separate counter writes: a cancelled or restarted job keeps exactly what it committed.
func (d *DB) ListJobResult(ctx context.Context, v Viewer, job Job) (ListJobResult, error) {
	var out ListJobResult
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	a, err := loadAccess(ctx, tx, v)
	if err != nil {
		return out, err
	}
	prefix := "list-view:" + job.PublicID + ":"
	// created_ref is the result's two decimal integers, "added already", written by
	// AddJobToList in the same transaction as the entries. It is never a human sentence.
	err = tx.QueryRowContext(ctx, `SELECT coalesce(sum(added), 0), coalesce(sum(already), 0), coalesce(sum(failed), 0) FROM (
		SELECT CAST(k.created_ref AS INTEGER) AS added,
			CAST(substr(k.created_ref, instr(k.created_ref, ' ') + 1) AS INTEGER) AS already, 0 AS failed
		FROM idempotency_keys k CROSS JOIN items i ON i.public_id = substr(k.key, @start)
		WHERE k.user_id = @owner AND k.key GLOB @keys AND `+a.visible("i")+`
		UNION ALL
		SELECT 0, 0, 1 FROM job_item_outcomes f CROSS JOIN items i ON i.id = f.item_id
		WHERE f.job_id = @job AND f.code <> '' AND `+a.visible("i")+`)`,
		append(v.args(), sql.Named("owner", job.OwnerID), sql.Named("keys", prefix+"*"),
			sql.Named("start", len(prefix)+1), sql.Named("job", job.ID))...).Scan(&out.Added, &out.AlreadyPresent, &out.Failed)
	return out, err
}
