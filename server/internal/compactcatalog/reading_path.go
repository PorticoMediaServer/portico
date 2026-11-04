package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"unicode/utf8"
)

const readingPathBatch = 128
const pathColumns = `object_id,source_revision,author,author_source,issue_date,series,series_source,series_number`

type pathBundle struct {
	Object, Revision                                 string
	Author, AuthorSource, Date, Series, SeriesSource sql.NullString
	Number                                           sql.NullFloat64
}

func (v pathBundle) args() []any {
	return []any{v.Object, v.Revision, v.Author, v.AuthorSource, v.Date, v.Series, v.SeriesSource, v.Number}
}
func (v *pathBundle) scan() []any {
	return []any{&v.Object, &v.Revision, &v.Author, &v.AuthorSource, &v.Date, &v.Series, &v.SeriesSource, &v.Number}
}
func bundle(object, revision string, v ReadingValues) (pathBundle, error) {
	out := pathBundle{Object: object, Revision: revision}
	for _, f := range []struct {
		v            *ReadingValue
		text, source *sql.NullString
	}{{v.Author, &out.Author, &out.AuthorSource}, {v.Series, &out.Series, &out.SeriesSource}} {
		if f.v == nil {
			continue
		}
		if f.v.Source != ReadingFileName && f.v.Source != ReadingFolder || strings.TrimSpace(f.v.Value) == "" || len(f.v.Value) > 4096 || !utf8.ValidString(f.v.Value) || strings.ContainsAny(f.v.Value, "\x00\r\n") {
			return out, ErrReadingValue
		}
		*f.text = sql.NullString{String: f.v.Value, Valid: true}
		*f.source = sql.NullString{String: string(f.v.Source), Valid: true}
	}
	if v.IssueDate != nil {
		if v.IssueDate.Source != ReadingFileName || !ValidReadingIssueDate(v.IssueDate.Value) {
			return out, ErrReadingValue
		}
		out.Date = sql.NullString{String: v.IssueDate.Value, Valid: true}
	}
	if v.SeriesNumber != nil {
		if v.SeriesNumber.Source != ReadingFileName {
			return out, ErrReadingValue
		}
		n := v.SeriesNumber.Value
		if n < 0 || math.IsNaN(n) || math.IsInf(n, 0) {
			return out, ErrReadingValue
		}
		out.Number = sql.NullFloat64{Float64: n, Valid: true}
	}
	return out, nil
}
func (v pathBundle) values() ReadingValues {
	out := ReadingValues{}
	if v.Author.Valid {
		out.Author = &ReadingValue{v.Author.String, ReadingValueSource(v.AuthorSource.String)}
	}
	if v.Series.Valid {
		out.Series = &ReadingValue{v.Series.String, ReadingValueSource(v.SeriesSource.String)}
	}
	if v.Date.Valid {
		out.IssueDate = &ReadingValue{v.Date.String, ReadingFileName}
	}
	if v.Number.Valid {
		out.SeriesNumber = &ReadingNumber{v.Number.Float64, ReadingFileName}
	}
	return out
}
func pathSequence(ctx context.Context, tx *sql.Tx) (int64, error) {
	var n int64
	err := tx.QueryRowContext(ctx, `UPDATE reading_path_clock SET sequence=sequence+1 WHERE singleton=1 AND sequence<9223372036854775807 RETURNING sequence`).Scan(&n)
	return n, err
}
func pathTurn(ctx context.Context, tx *sql.Tx) (int64, error) {
	var n int64
	err := tx.QueryRowContext(ctx, `UPDATE reading_path_clock SET turn=turn+1 WHERE singleton=1 AND turn<9223372036854775807 RETURNING turn`).Scan(&n)
	return n, err
}

// Queue one semantic invalidation per library/transaction. Flatten never calls it.
func pathSemantic(ctx context.Context, tx *sql.Tx, library int64) error {
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO reading_path_semantic_pending VALUES(?)`, library)
	return err
}
func FlushReadingPathSemanticTx(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT library_id FROM reading_path_semantic_pending ORDER BY library_id LIMIT 128`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		for _, q := range []string{`INSERT INTO library_revisions(library_id,revision) SELECT library_id,1 FROM catalog_libraries WHERE id=? ON CONFLICT(library_id) DO UPDATE SET revision=revision+1`, `UPDATE reading_attribution_state SET input_generation=input_generation+1,phase='scan' WHERE library_id=?`, `DELETE FROM reading_path_semantic_pending WHERE library_id=?`} {
			if _, err = tx.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// The winner is selected BEFORE acceptance. Invalid newer evidence is an absence,
// never permission to resurrect an overwritten source. No witness FK is used.
func pathWinner(ctx context.Context, tx *sql.Tx, item, asset int64) (pathBundle, bool, error) {
	var v pathBundle
	err := tx.QueryRowContext(ctx, `SELECT `+pathColumns+` FROM reading_path_head WHERE item_id=? AND asset_id=?`, item, asset).Scan(v.scan()...)
	if errors.Is(err, sql.ErrNoRows) {
		return v, false, nil
	}
	return v, err == nil, err
}
func prefixColumns(alias string) string {
	parts := strings.Split(pathColumns, ",")
	for i := range parts {
		parts[i] = alias + "." + parts[i]
	}
	return strings.Join(parts, ",")
}
func pathAccepted(ctx context.Context, tx *sql.Tx, item, asset int64, v pathBundle) (bool, error) {
	var ok bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM reading_publications p JOIN catalog_asset_links link ON link.entity_id=p.item_id AND link.asset_id=p.asset_id JOIN catalog_assets a ON a.id=p.asset_id JOIN catalog_entities e ON e.id=p.item_id JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries lib ON lib.id=cl.library_id JOIN inventory_objects o ON o.id=?3 AND o.revision=?4 AND o.asset_id=a.token AND o.retired=0 JOIN library_sources s ON s.id=o.source_id AND s.library_id=cl.library_id AND s.incarnation=o.root_incarnation WHERE p.item_id=?1 AND p.asset_id=?2 AND e.retired=0 AND cl.retired=0 AND ((e.kind=20 AND lib.kind='book' AND p.format IN('epub','pdf','mobi','azw3')) OR(e.kind=21 AND lib.kind='comic' AND p.format IN('cbz','pdf','cb7','cbr'))))`, item, asset, v.Object, v.Revision).Scan(&ok)
	return ok, err
}
func writePathHead(ctx context.Context, tx *sql.Tx, item, asset, seq int64, v pathBundle) error {
	args := append([]any{item, asset, seq}, v.args()...)
	_, err := tx.ExecContext(ctx, `INSERT INTO reading_path_head(item_id,asset_id,publisher_sequence,`+pathColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(item_id,asset_id) DO UPDATE SET publisher_sequence=excluded.publisher_sequence,object_id=excluded.object_id,source_revision=excluded.source_revision,author=excluded.author,author_source=excluded.author_source,issue_date=excluded.issue_date,series=excluded.series,series_source=excluded.series_source,series_number=excluded.series_number WHERE reading_path_head.publisher_sequence<=excluded.publisher_sequence`, args...)
	return err
}

// ReadingPublishPathTx is also used by explicit changed-input publication tests.
// It writes one typed bundle, including a real all-null empty bundle.
func ReadingPublishPathTx(ctx context.Context, tx *sql.Tx, item, asset int64, object, revision string, values ReadingValues) (bool, error) {
	next, err := bundle(object, revision, values)
	if err != nil {
		return false, err
	}
	accepted, err := pathAccepted(ctx, tx, item, asset, next)
	if err != nil {
		return false, err
	}
	if !accepted {
		return false, context.Canceled
	}
	old, exists, err := pathWinner(ctx, tx, item, asset)
	if err != nil {
		return false, err
	}
	if exists && reflect.DeepEqual(old, next) {
		return false, nil
	}
	seq, err := pathSequence(ctx, tx)
	if err != nil {
		return false, err
	}
	if err = writePathHead(ctx, tx, item, asset, seq, next); err != nil {
		return false, err
	}
	var lib int64
	if err = tx.QueryRowContext(ctx, `SELECT library_id FROM catalog_entities WHERE id=?`, item).Scan(&lib); err != nil {
		return false, err
	}
	return true, pathSemantic(ctx, tx, lib)
}

type pathObject struct {
	ID, Source, Incarnation, Relative, Revision, Root, RootIdentity, Actual, LibraryKind, Format string
	Library, Item, Asset, Generation                                                             int64
	Kind                                                                                         Kind
	Eligible, Context                                                                            bool
}

// pathObjectColumns is the one projection that hydrates a raw object. It is
// shared by the point read and by the bounded batch read in
// reading_path_batch.go so the two can never disagree about eligibility.
//
// reading_publications.asset_id is UNIQUE and catalog_assets.token is UNIQUE,
// so each raw object hydrates to at most one row: a bounded id batch cannot
// fan out through this join.
const pathObjectColumns = `o.id,o.source_id,o.root_incarnation,o.relative_path,o.revision,s.root,s.root_identity,a.path,lib.kind,COALESCE(p.format,''),cl.id,COALESCE(e.id,0),a.id,s.generation,COALESCE(e.kind,20),COALESCE(link.available,0),
 o.retired=0 AND o.state='available' AND o.unsupported_reason='' AND s.incarnation=o.root_incarnation AND COALESCE(e.retired=0 AND e.library_id=cl.id AND cl.retired=0,0) AND a.available=1
 AND ((e.kind=20 AND lib.kind='book' AND p.format IN('epub','pdf')) OR(e.kind=21 AND lib.kind='comic' AND p.format IN('cbz','pdf','cb7','cbr'))) AND (p.format NOT IN('cb7','cbr') OR s.kind='local')
 AND o.id=(SELECT first.id FROM inventory_objects first INDEXED BY inventory_objects_path WHERE first.source_id=o.source_id AND first.relative_path=o.relative_path AND first.retired=0 AND first.asset_id=o.asset_id AND first.root_incarnation=o.root_incarnation ORDER BY first.id LIMIT 1)`

const pathObjectJoins = ` JOIN library_sources s ON s.id=o.source_id JOIN libraries lib ON lib.id=s.library_id AND lib.kind IN('book','comic') JOIN catalog_libraries cl ON cl.library_id=lib.id JOIN catalog_assets a ON a.token=o.asset_id LEFT JOIN reading_publications p ON p.asset_id=a.id LEFT JOIN catalog_entities e ON e.id=p.item_id LEFT JOIN catalog_asset_links link ON link.entity_id=p.item_id AND link.asset_id=p.asset_id`

func (v *pathObject) scan() []any {
	return []any{&v.ID, &v.Source, &v.Incarnation, &v.Relative, &v.Revision, &v.Root, &v.RootIdentity, &v.Actual, &v.LibraryKind, &v.Format, &v.Library, &v.Item, &v.Asset, &v.Generation, &v.Kind, &v.Eligible, &v.Context}
}

// finish applies the Go-side path containment checks the SQL projection cannot
// express. Both hydration paths call it, so batch and point reads are equal.
func (v *pathObject) finish() {
	v.Context = v.Context && v.Actual == rootPrefix(v.Root)+v.Relative && filepath.Clean(filepath.Join(v.Root, v.Relative)) == filepath.Clean(v.Actual)
	v.Eligible = v.Eligible && v.Context
}

// pathObjectPointSQL is the single-object hydration. The batch statement in
// reading_path_batch.go shares the same projection and joins.
var pathObjectPointSQL = `SELECT ` + pathObjectColumns + ` FROM inventory_objects o` + pathObjectJoins + ` WHERE o.id=? AND o.retired=0`

func readPathObject(ctx context.Context, tx *sql.Tx, id string) (pathObject, error) {
	var v pathObject
	err := tx.QueryRowContext(ctx, pathObjectPointSQL, id).Scan(v.scan()...)
	if err == nil {
		v.finish()
	}
	return v, err
}
func rootPrefix(root string) string {
	root = filepath.Clean(root)
	if !strings.HasSuffix(root, string(filepath.Separator)) {
		root += string(filepath.Separator)
	}
	return root
}

// ReadingNormalizeObjectTx normalizes ONE raw object, never its siblings. This
// seam is used on observation and by the durable feed for all other writers.
// One object is admitted alone even when its own scalars exceed the window
// budget, so the point seam stays O(B).
func ReadingNormalizeObjectTx(ctx context.Context, tx *sql.Tx, id string) error {
	objects, n, err := readPathObjectsCapped(ctx, tx, []string{id})
	if err != nil {
		return err
	}
	if n != 1 {
		return errPathObjectProgress
	}
	v, found := objects[id]
	return normalizePathObjectTx(ctx, tx, id, v, found)
}

// normalizePathObjectTx is the one-object body shared by the public point seam
// and the bounded batch sweeps, so both obey the same eligibility, winner,
// acceptance and restart rules. A raw object absent from the hydration is
// today's sql.ErrNoRows: its stale key row is dropped and nothing else happens.
func normalizePathObjectTx(ctx context.Context, tx *sql.Tx, id string, v pathObject, found bool) error {
	if !found {
		_, err := tx.ExecContext(ctx, `DELETE FROM reading_path_object_keys WHERE object_id=?`, id)
		return err
	}
	// This normalized witness is also the durable notification that publication
	// still owes work. Availability may settle after the raw feed was consumed;
	// its existing pending event replays this object without losing that debt.
	_, err := tx.ExecContext(ctx, `INSERT INTO reading_path_object_keys(object_id,library_id,item_id,asset_id,source_id,incarnation,root_prefix,source_generation,root_identity,source_revision,relative_path,eligible,context_active,pending) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,1)
 ON CONFLICT(object_id) DO UPDATE SET library_id=excluded.library_id,item_id=excluded.item_id,asset_id=excluded.asset_id,source_id=excluded.source_id,incarnation=excluded.incarnation,root_prefix=excluded.root_prefix,source_generation=excluded.source_generation,root_identity=excluded.root_identity,source_revision=excluded.source_revision,relative_path=excluded.relative_path,eligible=excluded.eligible,context_active=excluded.context_active,pending=1
 WHERE reading_path_object_keys.library_id IS NOT excluded.library_id OR reading_path_object_keys.item_id IS NOT excluded.item_id OR reading_path_object_keys.asset_id IS NOT excluded.asset_id OR reading_path_object_keys.source_id IS NOT excluded.source_id OR reading_path_object_keys.incarnation IS NOT excluded.incarnation OR reading_path_object_keys.root_prefix IS NOT excluded.root_prefix OR reading_path_object_keys.source_generation IS NOT excluded.source_generation OR reading_path_object_keys.root_identity IS NOT excluded.root_identity OR reading_path_object_keys.source_revision IS NOT excluded.source_revision OR reading_path_object_keys.relative_path IS NOT excluded.relative_path OR reading_path_object_keys.eligible IS NOT excluded.eligible OR reading_path_object_keys.context_active IS NOT excluded.context_active`, id, v.Library, v.Item, v.Asset, v.Source, v.Incarnation, rootPrefix(v.Root), v.Generation, v.RootIdentity, v.Revision, v.Relative, v.Eligible, v.Context)
	if err != nil || !v.Eligible {
		return err
	}
	var ready bool
	err = tx.QueryRowContext(ctx, `SELECT pending=1 AND NOT EXISTS(SELECT 1 FROM reading_path_availability_pending WHERE entity_id=?) FROM reading_path_object_keys WHERE object_id=?`, v.Item, id).Scan(&ready)
	if err != nil || !ready {
		return err
	}
	values, err := ReadingPathValue(ReadingRuleInput{Item: v.Item, SourceRoot: v.Source, RelativePath: filepath.ToSlash(v.Relative), Kind: v.Kind})
	if errors.Is(err, ErrReadingValue) {
		return nil
	} // Invalid lexical input cannot poison other objects or infer absence.
	if err != nil {
		return err
	}
	if _, err = ReadingPublishPathTx(ctx, tx, v.Item, v.Asset, v.ID, v.Revision, values); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE reading_path_object_keys SET pending=0 WHERE object_id=? AND pending<>0`, id)
	return err
}

// ReadingQueuePathCoverageTx retains the scanner's existing barrier feed.
func ReadingQueuePathCoverageTx(ctx context.Context, tx *sql.Tx, job string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO reading_path_dirty_work(library_id,kind,work_key) SELECT cl.id,'blocks',r.job_id FROM inventory_runs r JOIN library_sources s ON s.id=r.source_id JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE r.job_id=? AND l.kind IN('book','comic') ON CONFLICT(library_id,kind,work_key) DO UPDATE SET requested=requested+1`, job)
	return err
}

// ReadingPathBlockTx records the scanner's normalized coverage dependencies.
// Coverage still governs traversal closure; it does not gate an accepted
// file's own lexical bundle.
func ReadingPathBlockTx(ctx context.Context, tx *sql.Tx, job, relative, reason string) error {
	physical := filepath.ToSlash(filepath.Clean(relative))
	if physical == "." {
		physical = ""
	}
	logical, err := ReadingDirectoryKey(filepath.ToSlash(filepath.Clean(relative)))
	if err != nil {
		logical = physical
	}
	type dependency struct{ key, mode string }
	deps := []dependency{{physical, "subtree"}}
	if logical != physical {
		deps = append(deps, dependency{logical, "scope"})
	}
	ext := strings.ToLower(filepath.Ext(relative))
	if (strings.HasPrefix(reason, "entry_") || strings.HasPrefix(reason, "symlink_")) && (ext == ".epub" || ext == ".pdf" || ext == ".cbz" || ext == ".cb7" || ext == ".cbr") {
		parent, e := ReadingDirectoryKey(filepath.ToSlash(filepath.Dir(relative)))
		if e == nil {
			deps = append(deps, dependency{parent, "scope"})
		}
	}
	for _, d := range deps {
		r, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO reading_path_block(job_id,source_id,directory_key,mode,reason) SELECT ?,source_id,?,?,? FROM inventory_runs WHERE job_id=?`, job, d.key, d.mode, reason, job)
		if e != nil {
			return e
		}
		n, _ := r.RowsAffected()
		if n > 0 {
			if _, e = tx.ExecContext(ctx, `UPDATE reading_path_source_barrier SET coverage_revision=coverage_revision+1 WHERE source_id=(SELECT source_id FROM inventory_runs WHERE job_id=?1) AND job_id=?1`, job); e != nil {
				return e
			}
		}
	}
	return nil
}

type pathWork struct {
	Library, Requested, Captured, Asset, Scope, Phase, FenceAsset, FenceScope int64
	Served                                                                    int64
	Kind, Key, Object, FenceObject                                            string
}

func pathWorkStep(ctx context.Context, tx *sql.Tx, limit int, turn int64) (int, error) {
	var w pathWork
	err := tx.QueryRowContext(ctx, `SELECT library_id,kind,work_key,requested,captured,after_object,after_asset,after_scope,phase,fence_object,fence_asset,fence_scope,served FROM reading_path_dirty_work ORDER BY served,kind,work_key LIMIT 1`).Scan(&w.Library, &w.Kind, &w.Key, &w.Requested, &w.Captured, &w.Object, &w.Asset, &w.Scope, &w.Phase, &w.FenceObject, &w.FenceAsset, &w.FenceScope, &w.Served)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if w.Captured == 0 {
		w.Captured = w.Requested
		if w.Kind != "blocks" {
			w.Object = ""
			w.Asset = 0
			w.Scope = 0
			w.Phase = 0
		}
		if err = pathWorkFence(ctx, tx, &w); err != nil {
			return 0, err
		}
	}
	done := false
	n := 0
	normalize := func(ids []string) error {
		return normalizePathObjects(ctx, tx, ids)
	}
	switch w.Kind {
	case "object":
		// Consecutive 'object' rows that share the head row's served stamp are
		// contiguous in the queue's exact ORDER BY served,kind,work_key sequence.
		// They share one bounded hydration, then each row retires or re-arms on
		// its own requested/captured guard; this helper owns that tail.
		return pathObjectWorkStep(ctx, tx, w, limit, turn)
	case "asset", "source":
		q := `SELECT id FROM inventory_objects INDEXED BY inventory_objects_reading_asset_object WHERE asset_id=? AND id>? AND id<=? ORDER BY id LIMIT ?`
		if w.Kind == "source" {
			q = `SELECT id FROM inventory_objects INDEXED BY inventory_objects_reading_source_object WHERE source_id=? AND id>? AND id<=? ORDER BY id LIMIT ?`
		}
		var ids []string
		ids, err = pathStrings(ctx, tx, q, w.Key, w.Object, w.FenceObject, limit)
		n = len(ids)
		if err == nil {
			err = normalize(ids)
		}
		if n > 0 {
			w.Object = ids[n-1]
		} else {
			done = true
		}
	case "item", "pending":
		if w.Phase == 0 {
			var ids []string
			ids, err = pathStrings(ctx, tx, `SELECT object_id FROM reading_path_object_keys WHERE item_id=? AND object_id>? AND object_id<=? ORDER BY object_id LIMIT ?`, w.Key, w.Object, w.FenceObject, limit)
			n = len(ids)
			if err == nil {
				err = normalize(ids)
			}
			if n > 0 {
				w.Object = ids[n-1]
			} else {
				w.Phase = 1
				w.Object = ""
			}
		} else if w.Phase == 1 {
			var next int64
			err = tx.QueryRowContext(ctx, `SELECT asset_id FROM reading_publications WHERE item_id=? AND asset_id>? AND asset_id<=? ORDER BY asset_id LIMIT 1`, w.Key, w.Asset, w.FenceAsset).Scan(&next)
			if errors.Is(err, sql.ErrNoRows) {
				err = nil
				done = true
			} else if err == nil {
				w.Asset = next
				w.Phase = 2
				w.Object = ""
				err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT o.id FROM catalog_assets a CROSS JOIN inventory_objects o INDEXED BY inventory_objects_reading_asset_object ON o.asset_id=a.token WHERE a.id=? ORDER BY o.id DESC LIMIT 1),'')`, next).Scan(&w.FenceObject)
			}
			n = 1
		} else {
			var ids []string
			ids, err = pathStrings(ctx, tx, `SELECT o.id FROM catalog_assets a CROSS JOIN inventory_objects o INDEXED BY inventory_objects_reading_asset_object ON o.asset_id=a.token WHERE a.id=? AND o.id>? AND o.id<=? ORDER BY o.id LIMIT ?`, w.Asset, w.Object, w.FenceObject, limit)
			n = len(ids)
			if err == nil {
				err = normalize(ids)
			}
			if n > 0 {
				w.Object = ids[n-1]
			} else {
				w.Phase = 1
				w.Object = ""
			}
		}
	case "blocks":
		// Raw issue windows include non-protecting records. At most three bounded
		// dependency rows are emitted per issue, so cap output as well as input.
		rows, e := tx.QueryContext(ctx, `SELECT sequence,relative_path,reason,protect FROM inventory_scan_issues WHERE job_id=? AND sequence>? AND sequence<=? ORDER BY sequence LIMIT ?`, w.Key, w.Scope, w.FenceScope, max(1, min(limit, 42)))
		if e != nil {
			return 0, e
		}
		type issue struct {
			seq          int64
			path, reason string
			protect      bool
		}
		var batch []issue
		for rows.Next() {
			var v issue
			if e = rows.Scan(&v.seq, &v.path, &v.reason, &v.protect); e != nil {
				rows.Close()
				return 0, e
			}
			batch = append(batch, v)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return 0, e
		}
		for _, v := range batch {
			if v.protect {
				if err = ReadingPathBlockTx(ctx, tx, w.Key, v.path, v.reason); err != nil {
					break
				}
			}
			w.Scope = v.seq
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE reading_path_source_barrier SET after_issue=? WHERE source_id=(SELECT source_id FROM inventory_runs WHERE job_id=?) AND job_id=?`, w.Scope, w.Key, w.Key)
		}
		n = len(batch)
		done = n == 0
	case "coverage":
		rows, e := tx.QueryContext(ctx, `SELECT id,job_id FROM reading_path_block WHERE source_id=? AND id>? AND id<=? ORDER BY id LIMIT ?`, w.Key, w.Scope, w.FenceScope, limit)
		if e != nil {
			return 0, e
		}
		type block struct {
			id  int64
			job string
		}
		var batch []block
		for rows.Next() {
			var v block
			if e = rows.Scan(&v.id, &v.job); e != nil {
				rows.Close()
				return 0, e
			}
			batch = append(batch, v)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return 0, e
		}
		var job string
		err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT b.job_id FROM reading_path_source_barrier b JOIN library_sources s ON s.id=b.source_id WHERE b.source_id=?),'')`, w.Key).Scan(&job)
		for _, v := range batch {
			if err != nil {
				break
			}
			if v.job != job {
				_, err = tx.ExecContext(ctx, `DELETE FROM reading_path_block WHERE id=?`, v.id)
			}
			w.Scope = v.id
		}
		n = len(batch)
		done = n == 0
		if done {
			_, err = tx.ExecContext(ctx, `DELETE FROM reading_path_source_barrier WHERE source_id=? AND NOT EXISTS(SELECT 1 FROM library_sources WHERE id=?)`, w.Key, w.Key)
		}
	case "library":
		var ids []string
		ids, err = pathStrings(ctx, tx, `SELECT s.id FROM catalog_libraries cl CROSS JOIN library_sources s ON s.library_id=cl.library_id WHERE cl.id=? AND s.id>? AND s.id<=? ORDER BY s.id LIMIT ?`, w.Library, w.Object, w.FenceObject, limit)
		n = len(ids)
		for _, id := range ids {
			if err != nil {
				break
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO reading_path_dirty_work(library_id,kind,work_key) VALUES(?,'source',?) ON CONFLICT(library_id,kind,work_key) DO UPDATE SET requested=requested+1`, w.Library, id)
			w.Object = id
		}
		done = n == 0
	default:
		// The baseline CHECK admits only the kinds above.
		return 0, fmt.Errorf("unknown Reading path work kind %q", w.Kind)
	}
	if err != nil {
		return 0, err
	}
	if done && w.Requested != w.Captured {
		w.Captured = 0
		done = false
	}
	if done {
		_, err = tx.ExecContext(ctx, `DELETE FROM reading_path_dirty_work WHERE library_id=? AND kind=? AND work_key=? AND requested=?`, w.Library, w.Kind, w.Key, w.Captured)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE reading_path_dirty_work SET captured=?,after_object=?,after_asset=?,after_scope=?,phase=?,served=?,fence_object=?,fence_asset=?,fence_scope=? WHERE library_id=? AND kind=? AND work_key=?`, w.Captured, w.Object, w.Asset, w.Scope, w.Phase, turn, w.FenceObject, w.FenceAsset, w.FenceScope, w.Library, w.Kind, w.Key)
	}
	return max(1, n), err
}

func pathWorkFence(ctx context.Context, tx *sql.Tx, w *pathWork) error {
	var err error
	switch w.Kind {
	case "asset":
		err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT id FROM inventory_objects INDEXED BY inventory_objects_reading_asset_object WHERE asset_id=? ORDER BY id DESC LIMIT 1),'')`, w.Key).Scan(&w.FenceObject)
	case "source":
		err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT id FROM inventory_objects INDEXED BY inventory_objects_reading_source_object WHERE source_id=? ORDER BY id DESC LIMIT 1),'')`, w.Key).Scan(&w.FenceObject)
	case "item", "pending":
		err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT object_id FROM reading_path_object_keys WHERE item_id=?1 ORDER BY object_id DESC LIMIT 1),''),COALESCE((SELECT asset_id FROM reading_publications WHERE item_id=?1 ORDER BY asset_id DESC LIMIT 1),0)`, w.Key).Scan(&w.FenceObject, &w.FenceAsset)
	case "blocks":
		err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT sequence FROM inventory_scan_issues WHERE job_id=?1 ORDER BY sequence DESC LIMIT 1),0),COALESCE((SELECT after_issue FROM reading_path_source_barrier WHERE job_id=?1),?2)`, w.Key, w.Scope).Scan(&w.FenceScope, &w.Scope)
	case "coverage":
		err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT id FROM reading_path_block WHERE source_id=? ORDER BY id DESC LIMIT 1),0)`, w.Key).Scan(&w.FenceScope)
	case "library":
		err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT s.id FROM catalog_libraries cl CROSS JOIN library_sources s ON s.library_id=cl.library_id WHERE cl.id=? ORDER BY s.id DESC LIMIT 1),'')`, w.Library).Scan(&w.FenceObject)
	}
	return err
}

// A quantum may finish several tiny coalesced feed keys. The aggregate raw
// budget, including empty subcursor/EOF probes, remains <=128.
func pathWorkQuantum(ctx context.Context, tx *sql.Tx, limit int, turn int64) (int, error) {
	total := 0
	for total < limit {
		n, err := pathWorkStep(ctx, tx, limit-total, turn)
		if err != nil {
			return 0, err
		}
		if n == 0 {
			break
		}
		total += n
	}
	if err := FlushReadingPathSemanticTx(ctx, tx); err != nil {
		return 0, err
	}
	return total, nil
}

func pathStrings(ctx context.Context, tx *sql.Tx, q string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var out []string
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, v)
	}
	err = rows.Err()
	rows.Close()
	return out, err
}

// StepReadingPaths consumes the existing durable, byte-bounded dirty feed.
// Each admitted object publishes one complete lexical bundle in this writer;
// no sibling capture, overlay, quiet gap or second scheduler is involved.
func StepReadingPaths(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	limit = max(1, min(limit, readingPathBatch))
	var work bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM reading_path_dirty_work)`).Scan(&work); err != nil || !work {
		return 0, err
	}
	turn, err := pathTurn(ctx, tx)
	if err != nil {
		return 0, err
	}
	return pathWorkQuantum(ctx, tx, limit, turn)
}

type readingPrimary struct{ item, asset int64 }

func readingPrimaryPublicationsTx(ctx context.Context, tx *sql.Tx, marks string, args []any) ([]readingPrimary, error) {
	rows, err := tx.QueryContext(ctx, `SELECT e.id,p.asset_id FROM catalog_entities e JOIN reading_publications p ON p.item_id=e.id AND p.asset_id=(SELECT v.asset_id FROM reading_publications v WHERE v.item_id=e.id AND ((e.kind=20 AND v.format IN('epub','pdf','mobi','azw3')) OR(e.kind=21 AND v.format IN('cbz','pdf','cb7','cbr'))) ORDER BY EXISTS(SELECT 1 FROM inventory_item_availability av WHERE av.item_id=v.item_id AND av.asset_id=(SELECT token FROM catalog_assets WHERE id=v.asset_id) AND av.available=1) DESC,v.asset_id LIMIT 1) WHERE e.id IN (`+marks+`)`, args...)
	if err != nil {
		return nil, err
	}
	var out []readingPrimary
	for rows.Next() {
		var p readingPrimary
		if err = rows.Scan(&p.item, &p.asset); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	err = rows.Err()
	rows.Close()
	return out, err
}
func readingPrimaryBindings(items []readingPrimary) (string, []any) {
	if len(items) == 0 {
		return "SELECT 0,0 WHERE 0", nil
	}
	args := make([]any, 0, len(items)*2)
	for _, p := range items {
		args = append(args, p.item, p.asset)
	}
	return "VALUES " + strings.TrimSuffix(strings.Repeat("(?,?),", len(items)), ","), args
}
func readingPathAutomaticTx(ctx context.Context, tx *sql.Tx, items []readingPrimary, out map[int64]ReadingValues) error {
	if len(items) == 0 {
		return nil
	}
	chosen, args := readingPrimaryBindings(items)
	rows, err := tx.QueryContext(ctx, `WITH chosen(item_id,asset_id) AS (`+chosen+`),
 bundles AS MATERIALIZED (SELECT c.item_id,c.asset_id,`+prefixColumns("h")+` FROM chosen c JOIN reading_path_head h ON h.item_id=c.item_id AND h.asset_id=c.asset_id)
 SELECT w.item_id,`+prefixColumns("w")+` FROM bundles w
 CROSS JOIN reading_publications p ON p.item_id=w.item_id AND p.asset_id=w.asset_id
 CROSS JOIN catalog_asset_links link ON link.entity_id=p.item_id AND link.asset_id=p.asset_id
 CROSS JOIN catalog_assets a ON a.id=p.asset_id
 CROSS JOIN catalog_entities e ON e.id=p.item_id
 CROSS JOIN catalog_libraries cl ON cl.id=e.library_id
 CROSS JOIN libraries lib ON lib.id=cl.library_id
 CROSS JOIN inventory_objects o ON o.id=w.object_id AND o.revision=w.source_revision AND o.asset_id=a.token AND o.retired=0
 CROSS JOIN library_sources s ON s.id=o.source_id AND s.library_id=cl.library_id AND s.incarnation=o.root_incarnation
 WHERE e.retired=0 AND cl.retired=0 AND ((e.kind=20 AND lib.kind='book' AND p.format IN('epub','pdf','mobi','azw3')) OR(e.kind=21 AND lib.kind='comic' AND p.format IN('cbz','pdf','cb7','cbr')))`, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var item int64
		var v pathBundle
		if err = rows.Scan(append([]any{&item}, v.scan()...)...); err != nil {
			rows.Close()
			return err
		}
		values := v.values()
		if _, err = bundle(v.Object, v.Revision, values); err != nil {
			rows.Close()
			return err
		}
		out[item] = values
	}
	err = rows.Err()
	rows.Close()
	return err
}
