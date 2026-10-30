package catalog

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"sort"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

type listeningGroupCandidate struct {
	library, kind, name, key string
}

// listeningGroupBatch is how many books one background step reads. A variable
// so tests can exercise the continuation with a small library.
var listeningGroupBatch = 256

// listeningGroupPassGap coalesces a burst of audiobook commits (a scan) into
// one follow-up pass instead of a pass per commit.
const ListeningGroupPassGap = 30 * time.Second

var listeningGroupPassGap = ListeningGroupPassGap

// ListeningGroupPassSettled is replaced only by the isolated idle-budget test
// process. It marks a pass that read its last batch, which is the point after
// which only the documented coalescing gap can schedule one more pass.
var ListeningGroupPassSettled = func() {}

// RefreshListeningGroups advances the current pass over the books by one
// bounded batch and publishes any author or series navigation identity the
// batch names that does not exist yet. It returns true while the pass has more
// books; the next call after a completed pass starts a new one. A pass reads
// each book once, so publishing a library costs O(books) in total, not a
// whole-library aggregation per batch (B85). Existing IDs survive metadata
// republishing: groups are only ever added here. It runs in the background,
// never on an audiobook browse request.
func (s *Service) RefreshListeningGroups(ctx context.Context) (bool, error) {
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	s.state.groupMu.Lock()
	cursor := s.state.groupCursor
	s.state.groupMu.Unlock()
	next, candidates, err := s.listeningGroupBatch(ctx, cursor)
	if err != nil {
		return false, err
	}
	if len(candidates) > 0 {
		if err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			changedLibraries := map[string]bool{}
			for _, c := range candidates {
				result, err := tx.ExecContext(ctx, `INSERT INTO listening_book_groups(id,library_id,kind,name,name_key)
				 VALUES(lower(hex(randomblob(16))),?,?,?,?) ON CONFLICT(library_id,kind,name_key) DO NOTHING`, c.library, c.kind, c.name, c.key)
				if err != nil {
					return err
				}
				if n, err := result.RowsAffected(); err != nil {
					return err
				} else if n > 0 {
					changedLibraries[c.library] = true
				}
			}
			for library := range changedLibraries {
				if _, err := tx.ExecContext(ctx, `UPDATE library_revisions SET revision=revision+1 WHERE library_id=?`, library); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return false, err
		}
	}
	s.state.groupMu.Lock()
	s.state.groupCursor = next
	s.state.groupMu.Unlock()
	return next != "", nil
}

// listeningGroupBatch reads the next books after cursor in primary-key order
// and returns the groups they name that are not yet published, plus the cursor
// for the following batch ("" when the pass is complete). It only reads.
func (s *Service) listeningGroupBatch(ctx context.Context, cursor string) (string, []listeningGroupCandidate, error) {
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return "", nil, err
	}
	defer snapshot.Rollback()
	tx := snapshot.Tx()
	var cursorID, last int64
	if cursor != "" {
		cursorID, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || cursorID < 0 {
			return "", nil, ErrCursor
		}
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(entity_id),0),count(*) FROM (SELECT entity_id FROM catalog_books WHERE entity_id>? ORDER BY entity_id LIMIT ?)`, cursorID, listeningGroupBatch).Scan(&last, &count); err != nil || count == 0 {
		return "", nil, err
	}
	// Both source tables are keyed by the book entity id, so each pass stays
	// bounded to the current batch of books.
	rows, err := tx.QueryContext(ctx, `SELECT cl.library_id,'author',trim(b.author),lower(trim(b.author)) FROM catalog_books b
	  JOIN catalog_libraries cl ON cl.id=b.library_id WHERE b.entity_id>? AND b.entity_id<=? AND trim(b.author)<>''
	 UNION ALL
	 SELECT cl.library_id,'book_series',context.series_name,lower(trim(context.series_name)) FROM catalog_book_context context
	  JOIN catalog_libraries cl ON cl.id=context.library_id WHERE context.book_id>? AND context.book_id<=? AND context.series_name<>''`, cursorID, last, cursorID, last)
	if err != nil {
		return "", nil, err
	}
	named := map[[3]string]string{}
	for rows.Next() {
		var c listeningGroupCandidate
		if err = rows.Scan(&c.library, &c.kind, &c.name, &c.key); err != nil {
			rows.Close()
			return "", nil, err
		}
		key := [3]string{c.library, c.kind, c.key}
		// The same lowest spelling the whole-library aggregation chose, within
		// the batch that first names the group.
		if prior, ok := named[key]; !ok || c.name < prior {
			named[key] = c.name
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", nil, err
	}
	out := make([]listeningGroupCandidate, 0, len(named))
	for key, name := range named {
		var exists int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM listening_book_groups WHERE library_id=? AND kind=? AND name_key=?`, key[0], key[1], key[2]).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			out = append(out, listeningGroupCandidate{library: key[0], kind: key[1], name: name, key: key[2]})
			continue
		}
		if err != nil {
			return "", nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.library != b.library {
			return a.library < b.library
		}
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		return a.key < b.key
	})
	if count < listeningGroupBatch {
		return "", out, nil
	}
	return strconv.FormatInt(last, 10), out, nil
}

// ListeningGroupStep is the background loop body. changed is woken by commits
// that can name a new author or series (books and book_files, which every
// audiobook ingest writes); a pass runs at startup and then only after such a
// change, never on the safety tick or on another library's scan. A change that
// arrives during a pass schedules one more pass after it, at least
// listeningGroupPassGap after the previous one ended.
func (s *Service) ListeningGroupStep(changed *worker.Signal) worker.Step {
	dirty, inPass := true, false
	var ended time.Time
	return func(ctx context.Context) time.Duration {
		if changed.Take() {
			dirty = true
		}
		if !inPass {
			if !dirty {
				return 0
			}
			if wait := listeningGroupPassGap - time.Since(ended); !ended.IsZero() && wait > 0 {
				return wait
			}
			dirty, inPass = false, true
		}
		more, err := s.RefreshListeningGroups(ctx)
		if err != nil {
			if ctx.Err() == nil {
				logListeningGroups(err)
			}
			return 15 * time.Second
		}
		if more {
			return time.Millisecond
		}
		inPass, ended = false, time.Now()
		ListeningGroupPassSettled()
		if dirty {
			return listeningGroupPassGap
		}
		return 0
	}
}

func logListeningGroups(err error) { log.Printf("Listening group refresh: %v", err) }
