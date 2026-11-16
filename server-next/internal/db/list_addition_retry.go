package db

import (
	"database/sql"
	"errors"
	"fmt"
)

// additionItems is shared by a new addition and a retry: direct hidden targets fail alike.
func (e *listEdit) additionItems(publicIDs []string) ([]int64, error) {
	var items []int64
	for _, publicID := range publicIDs {
		var id int64
		var kind ItemKind
		err := e.tx.QueryRowContext(e.ctx, `SELECT i.id, i.kind FROM items i WHERE i.public_id = @id AND `+e.acc.visible("i"),
			append(e.v.args(), sql.Named("id", publicID))...).Scan(&id, &kind)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		if e.kind != ListPlaylist || kind.Playable() {
			items = append(items, id)
			continue
		}
		under, err := playableUnder(e.ctx, e.tx, e.v, e.acc, id, kind)
		if err != nil {
			return nil, err
		}
		items = append(items, under...)
	}
	return items, nil
}

// retryAddition counts only this addition's original entries the current profile may see.
// The receipt keeps newly arrived children and unrelated entries out of the cached answer.
func (e *listEdit) retryAddition(items []string, made string, out *ListAdded) error {
	if _, err := e.additionItems(items); err != nil {
		return err
	}
	var receipt string
	if _, err := fmt.Sscanf(made, "%d %d %s", &out.Added, &out.Already, &receipt); err != nil {
		return err
	}
	e.unchanged = true
	if e.kind != ListPlaylist {
		return nil
	} // collections never expand a holder; every counted target was checked above
	return e.tx.QueryRowContext(e.ctx, `SELECT count(*) FROM list_entries e CROSS JOIN items i ON i.id = e.item_id
 WHERE e.list_id = @list AND e.addition_receipt = @receipt AND `+e.acc.visible("i"),
		append(e.v.args(), sql.Named("list", e.id), sql.Named("receipt", receipt))...).Scan(&out.Added)
}
