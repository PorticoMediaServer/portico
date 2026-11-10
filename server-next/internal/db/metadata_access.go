package db

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
)

// SeesItem checks that the viewer may see a title before an owner or admin reads or changes
// its metadata, so a profile with a limit never learns of a title hidden from it. A title it
// may not see is ErrNotFound, exactly like one that does not exist.
func (d *DB) SeesItem(ctx context.Context, v Viewer, publicID string) error {
	return d.sees(ctx, v, `SELECT 1 FROM items i WHERE i.public_id = :id AND `, publicID)
}

// SeesVersion is SeesItem for the title a version belongs to.
func (d *DB) SeesVersion(ctx context.Context, v Viewer, publicID string) error {
	return d.sees(ctx, v, `SELECT 1 FROM media m JOIN items i ON i.id = m.item_id WHERE m.public_id = :id AND `, publicID)
}

func (d *DB) sees(ctx context.Context, v Viewer, query, publicID string) error {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return err
	}
	var one int
	err = tx.QueryRowContext(ctx, query+acc.visible("i"), append(v.args(), sql.Named("id", publicID))...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// Cursor is where the next page of the Unmatched items list starts, after this line.
func (u UnmatchedItem) Cursor() string {
	raw, _ := json.Marshal([]any{u.sortTitle, u.id})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// UnmatchedAfter reads a cursor made by UnmatchedItem.Cursor, for Unmatched's after.
func UnmatchedAfter(cursor string) (*UnmatchedItem, error) {
	var at UnmatchedItem
	var parts []json.RawMessage
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || json.Unmarshal(raw, &parts) != nil || len(parts) != 2 ||
		json.Unmarshal(parts[0], &at.sortTitle) != nil || json.Unmarshal(parts[1], &at.id) != nil {
		return nil, invalid("this cursor does not belong to this list")
	}
	return &at, nil
}

// MainViewer is an account reading as its main profile: the profile an owner or admin acts
// from on administration routes, and so the one a job they started reads as.
func (d *DB) MainViewer(ctx context.Context, userID int64) (Viewer, error) {
	v := Viewer{UserID: userID}
	err := d.read.QueryRowContext(ctx, `SELECT id FROM profiles WHERE user_id = ? AND is_primary = 1`, userID).Scan(&v.ProfileID)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

// SeesItems checks that the viewer may see every one of these titles and answers their kinds,
// one each. One that is not there, or hidden from the viewer, is ErrNotFound for the lot.
func (d *DB) SeesItems(ctx context.Context, v Viewer, publicIDs []string) ([]ItemKind, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	kinds := make([]ItemKind, len(publicIDs))
	for n, id := range publicIDs {
		err := tx.QueryRowContext(ctx, `SELECT i.kind FROM items i WHERE i.public_id = :id AND `+acc.visible("i"), sql.Named("id", id)).Scan(&kinds[n])
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
	}
	return kinds, nil
}
