package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
)

// Book context (domain 30): a book's author key and file presence, its group
// memberships, and the series/position evidence behind its effective series.
//
// Facts are written synchronously by the write API, so the book, file and
// group rows already exist when this drains: the head is upserted from
// catalog_books/catalog_book_files, then the book's group memberships are
// refreshed. Book group heads are kept by triggers on listening_book_groups
// (see the "Listening context" section of the baseline), and file/asset/group
// evidence jobs are queued by triggers too; StepBookContextEvidence keeps
// draining catalog_book_evidence_jobs with integer keys (kind 1 = audiobook
// file entity id, 2 = asset id, 3 = book group id).
const (
	DomainBookContext = 30
)

// drainBookContext brings one book's context head up to date: the head from
// catalog_books/catalog_book_files, then its group memberships. A missing or
// retired book takes the old "book no longer exists" branch: its file
// presence and effective series are cleared and its memberships drain.
func drainBookContext(ctx context.Context, tx *sql.Tx, id int64) error {
	var library int64
	var author string
	err := tx.QueryRowContext(ctx, `SELECT e.library_id,b.author FROM catalog_entities e JOIN catalog_books b ON b.entity_id=e.id WHERE e.id=? AND e.kind=8 AND e.retired=0`, id).Scan(&library, &author)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_book_context SET has_file=0,series_name='',series_key='',series_index=0 WHERE book_id=?`, id); err != nil {
			return err
		}
		return refreshBookGroupMembers(ctx, tx, id)
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_book_context(book_id,library_id,author_key,has_file)
	 SELECT ?,?,lower(trim(?)),EXISTS(SELECT 1 FROM catalog_book_files f WHERE f.book_id=?)
	 ON CONFLICT(book_id) DO UPDATE SET library_id=excluded.library_id,author_key=excluded.author_key,has_file=excluded.has_file`, id, library, author, id); err != nil {
		return err
	}
	return refreshBookGroupMembers(ctx, tx, id)
}

// A book can have at most one effective author and one effective series.
// Membership deltas are therefore constant-size even for a million-book group.
func refreshBookGroupMembers(ctx context.Context, tx *sql.Tx, bookID int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT group_id FROM catalog_book_group_members WHERE book_id=?`, bookID)
	if err != nil {
		return err
	}
	old := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		old = append(old, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	wanted := map[int64]bool{}
	rows, err = tx.QueryContext(ctx, `SELECT g.id FROM catalog_book_context c JOIN catalog_book_groups g ON g.library_id=c.library_id AND g.retired=0 AND ((g.kind=1 AND g.name_key=c.author_key AND c.author_key!='') OR (g.kind=2 AND g.name_key=c.series_key AND c.series_key!='')) WHERE c.book_id=? AND c.has_file=1`, bookID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		wanted[id] = true
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range old {
		if wanted[id] {
			delete(wanted, id)
			continue
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_book_group_members WHERE group_id=? AND book_id=?`, id, bookID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_book_groups SET member_count=member_count-1 WHERE id=? AND member_count>0`, id); err != nil {
			return err
		}
	}
	for id := range wanted {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_book_group_members(group_id,book_id) VALUES(?,?)`, id, bookID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_book_groups SET member_count=member_count+1 WHERE id=?`, id); err != nil {
			return err
		}
	}
	return nil
}

func init() {
	Register(DomainBookContext, Derivation{
		Version: 1,
		Drain: func(ctx context.Context, tx *sql.Tx, keys []Key, limit int) ([]int64, error) {
			ids := keyIDs(keys)
			for _, id := range ids {
				if err := drainBookContext(ctx, tx, id); err != nil {
					return nil, err
				}
			}
			return ids, nil
		},
		Backfill: func(ctx context.Context, tx *sql.Tx, after int64, limit int) ([]int64, error) {
			return scanIDs(ctx, tx, `SELECT id FROM catalog_entities WHERE kind=8 AND id>? ORDER BY id LIMIT ?`, after, limit)
		},
	})
}
