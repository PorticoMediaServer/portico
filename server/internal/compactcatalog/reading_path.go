package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

// Private publication rule; independent of file metadata and ready projections.
const readingPathRule = 1
const readingPathBatch = 128
const pathColumns = `object_id,source_revision,author,author_source,issue_date,series,series_source,series_number`

// After a build aborts because its library's path inputs changed, that library
// claims again only once it has been quiet for a gap. The gap doubles with each
// consecutive abort, up to the cap, and resets when a build activates. Other
// libraries are unaffected; this is the accepted same-library delay.
const (
	readingQuietGap    = 2 * time.Second
	readingQuietGapMax = 2 * time.Minute
)

// readingPathNow is the quiet-gap clock; tests replace it.
var readingPathNow = time.Now

func readingQuietGapFor(aborts int64) time.Duration {
	return min(readingQuietGap<<min(max(aborts, 1)-1, 16), readingQuietGapMax)
}

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
	err := tx.QueryRowContext(ctx, `SELECT `+pathColumns+` FROM (
 SELECT h.publisher_sequence,`+pathColumns+` FROM reading_path_head h WHERE item_id=?1 AND asset_id=?2
 UNION ALL SELECT b.publish_sequence,`+prefixColumns("v")+` FROM reading_path_slot z JOIN reading_path_build b ON b.id=z.overlay_id JOIN reading_path_stage v ON v.build_id=b.id AND v.item_id=?1 AND v.asset_id=?2 WHERE z.singleton=1
 ) ORDER BY publisher_sequence DESC LIMIT 1`, item, asset).Scan(v.scan()...)
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
	key, parseErr := ReadingDirectoryKey(filepath.ToSlash(filepath.Dir(v.Relative)))
	invalid := parseErr != nil || !utf8.ValidString(v.Relative) || len(v.Relative) > 4096 || !filepath.IsLocal(v.Relative) || filepath.Clean(v.Relative) != v.Relative || strings.ContainsAny(filepath.ToSlash(v.Relative), "\\\x00")
	if parseErr != nil {
		key = filepath.ToSlash(filepath.Dir(v.Relative))
	}
	var scope int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM reading_path_scope WHERE source_id=? AND incarnation=? AND root_prefix=? AND directory_key=? AND rule_version=?`, v.Source, v.Incarnation, rootPrefix(v.Root), key, readingPathRule).Scan(&scope)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `INSERT INTO reading_path_scope(library_id,source_id,incarnation,root_prefix,directory_key,rule_version,desired,queued) VALUES(?,?,?,?,?,?,?,?) RETURNING id`, v.Library, v.Source, v.Incarnation, rootPrefix(v.Root), key, readingPathRule, v.Context, v.Context).Scan(&scope)
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE reading_path_scope SET library_id=? WHERE id=? AND library_id IS NOT ?`, v.Library, scope, v.Library); err != nil {
		return err
	}
	var oldScope, oldItem, oldAsset int64
	var oldRevision, oldRelative string
	var oldContext, oldEligible bool
	err = tx.QueryRowContext(ctx, `SELECT scope_id,item_id,context_active,asset_id,source_revision,relative_path,eligible FROM reading_path_object_keys WHERE object_id=?`, id).Scan(&oldScope, &oldItem, &oldContext, &oldAsset, &oldRevision, &oldRelative, &oldEligible)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	membershipChanged := oldScope != scope || oldItem != v.Item || oldContext != v.Context
	changed := membershipChanged || oldAsset != v.Asset || oldRevision != v.Revision || oldRelative != v.Relative || oldEligible != v.Eligible
	if membershipChanged {
		if _, err = tx.ExecContext(ctx, `DELETE FROM reading_path_object_keys WHERE object_id=?`, id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO reading_path_object_keys VALUES(?,?,?,?,?,?,?,?)`, id, scope, v.Item, v.Context, v.Asset, v.Revision, v.Relative, v.Eligible); err != nil {
			return err
		}
	} else if changed {
		if _, err = tx.ExecContext(ctx, `UPDATE reading_path_object_keys SET asset_id=?,source_revision=?,relative_path=?,eligible=? WHERE object_id=?`, v.Asset, v.Revision, v.Relative, v.Eligible, id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE reading_path_scope SET desired=desired+1,queued=1 WHERE id=? AND directory_key<>'' AND ?`, scope, v.Context); err != nil {
			return err
		}
	}
	if invalid && v.Context {
		// A malformed witness quarantines only its logical context. It cannot
		// poison the global worker or an unrelated folder/library.
		r, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO reading_path_block(job_id,source_id,directory_key,mode,reason) SELECT job_id,source_id,?,'scope','invalid_reading_path' FROM reading_path_source_barrier WHERE source_id=?`, key, v.Source)
		if e != nil {
			return e
		}
		n, _ := r.RowsAffected()
		if n > 0 {
			if _, e = tx.ExecContext(ctx, `UPDATE reading_path_source_barrier SET coverage_revision=coverage_revision+1 WHERE source_id=?`, v.Source); e != nil {
				return e
			}
		}
	}
	// Membership triggers dirty old/new scopes; other effective input changes
	// dirty this scope above. Merely visiting an unchanged object in a fanout
	// must not turn a superseded source into a newer publisher.
	if key == "" {
		if _, err = tx.ExecContext(ctx, `UPDATE reading_path_scope SET completed=desired,queued=0 WHERE id=? AND (completed<>desired OR queued<>0)`, scope); err != nil {
			return err
		}
	}
	if !v.Eligible || !changed || invalid {
		return nil
	}
	old, exists, err := pathWinner(ctx, tx, v.Item, v.Asset)
	if err != nil {
		return err
	}
	if exists && key != "" {
		ok, e := pathAccepted(ctx, tx, v.Item, v.Asset, old)
		if e != nil {
			return e
		}
		if ok {
			return nil
		}
	}
	input := ReadingRuleInput{Item: v.Item, SourceRoot: v.Source, RelativePath: filepath.ToSlash(v.Relative), Kind: v.Kind}
	values, err := ReadingPathValue(input, 1, false)
	if err != nil {
		return err
	}
	if key != "" {
		if values.Author != nil && values.Author.Source != ReadingFileName {
			values.Author = nil
		}
		values.Series = nil
		values.SeriesNumber = nil
	}
	_, err = ReadingPublishPathTx(ctx, tx, v.Item, v.Asset, v.ID, v.Revision, values)
	return err
}

// ReadingPathBlockTx stores one normalized coverage dependency. Generic
// descendants map to their parent logical scope; non-generic subtrees do not
// hold back the parent's independent direct/generic publications.
func ReadingQueuePathCoverageTx(ctx context.Context, tx *sql.Tx, job string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO reading_path_dirty_work(library_id,kind,work_key) SELECT cl.id,'blocks',r.job_id FROM inventory_runs r JOIN library_sources s ON s.id=r.source_id JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE r.job_id=? AND l.kind IN('book','comic') ON CONFLICT(library_id,kind,work_key) DO UPDATE SET requested=requested+1`, job)
	return err
}
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
		if w.Phase == 0 {
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
			} else if w.Kind == "source" {
				w.Phase = 1
			} else {
				done = true
			}
		} else {
			var ids []int64
			ids, err = scanIDs(ctx, tx, `SELECT id FROM reading_path_scope WHERE source_id=? AND id>? AND id<=? ORDER BY id LIMIT ?`, w.Key, w.Scope, w.FenceScope, limit)
			n = len(ids)
			for _, id := range ids {
				if err != nil {
					break
				}
				_, err = tx.ExecContext(ctx, `UPDATE reading_path_scope SET desired=desired+1,queued=1,sealed=0,seal_job='',seal_cursor=0,reason='' WHERE id=?`, id)
				w.Scope = id
			}
			done = n == 0
			if done {
				_, err = tx.ExecContext(ctx, `INSERT INTO reading_path_dirty_work(library_id,kind,work_key) VALUES(?,'coverage',?) ON CONFLICT(library_id,kind,work_key) DO UPDATE SET requested=requested+1`, w.Library, w.Key)
			}
		}
	case "item":
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
	case "pending":
		var ids []int64
		ids, err = scanIDs(ctx, tx, `SELECT scope_id FROM reading_path_scope_entities WHERE entity_id=? AND scope_id>? AND scope_id<=? ORDER BY scope_id LIMIT ?`, w.Key, w.Scope, w.FenceScope, limit)
		n = len(ids)
		for _, id := range ids {
			if err != nil {
				break
			}
			_, err = tx.ExecContext(ctx, `UPDATE reading_path_scope_entities SET pending=EXISTS(SELECT 1 FROM reading_path_availability_pending WHERE entity_id=reading_path_scope_entities.entity_id) WHERE entity_id=? AND scope_id=?`, w.Key, id)
			w.Scope = id
		}
		done = n == 0
	case "report":
		var source string
		err = tx.QueryRowContext(ctx, `SELECT source_id FROM inventory_runs WHERE job_id=?`, w.Key).Scan(&source)
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
			done = true
			break
		}
		if err != nil {
			break
		}
		var ids []int64
		ids, err = scanIDs(ctx, tx, `SELECT id FROM reading_path_scope WHERE source_id=? AND id>? AND id<=? ORDER BY id LIMIT ?`, source, w.Scope, w.FenceScope, limit)
		n = len(ids)
		for _, id := range ids {
			if err != nil {
				break
			}
			var s pathScope
			s, err = pathScopeRead(ctx, tx, id)
			if err != nil {
				break
			}
			if s.Desired != s.Completed {
				var reason string
				reason, err = scopeReady(ctx, tx, s)
				if reason == pathLibraryReason && err == nil {
					// Inputs still settling in the library are ordinary waiting;
					// a library availability failure keeps its warning.
					var failed bool
					if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_derivation_failures WHERE domain=28 AND entity_id=?)`, s.Library).Scan(&failed); err == nil && !failed {
						reason = "library_changing"
					}
				}
				if reason == "" {
					reason = "publication_queued"
					var held bool
					if err == nil {
						err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM reading_path_inputs WHERE library_id=? AND hold_until_ms>0)`, s.Library).Scan(&held)
					}
					if held {
						reason = "library_changing"
					}
				}
				if s.Job != w.Key {
					reason = "next_scan_started"
				}
				s.Job = w.Key
				if err == nil {
					err = pathReport(ctx, tx, s, reason)
				}
			}
			w.Scope = id
		}
		done = n == 0
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
		if w.Phase == 0 {
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
			if n == 0 {
				w.Phase = 1
			}
		} else {
			var ids []int64
			ids, err = scanIDs(ctx, tx, `SELECT id FROM reading_path_scope WHERE library_id=? AND id>? AND id<=? ORDER BY id LIMIT ?`, w.Library, w.Scope, w.FenceScope, limit)
			n = len(ids)
			for _, id := range ids {
				if err != nil {
					break
				}
				_, err = tx.ExecContext(ctx, `UPDATE reading_path_scope SET desired=desired+1,queued=1 WHERE id=?`, id)
				w.Scope = id
			}
			done = n == 0
		}
	case "wake":
		// Barrier changes and new closure seals queue a bounded wake. An OPEN
		// source can now have proved top-level closures; each claim still
		// requires its own exact seal. No parked scope is timer-polled.
		if w.Phase == 0 {
			var usable bool
			err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM reading_path_source_barrier b JOIN library_sources src ON src.id=b.source_id WHERE b.source_id=? AND b.incarnation=src.incarnation AND b.source_generation=src.generation AND b.root_identity=src.root_identity AND b.root=src.root AND b.reason='' AND (b.phase='sealed' OR (b.phase='open' AND EXISTS(SELECT 1 FROM inventory_reading_jobs c JOIN inventory_reading_seals p ON p.id=c.last_seal WHERE c.job_id=b.job_id AND c.source_id=b.source_id AND p.job_id=c.job_id AND p.source_id=b.source_id AND p.issue_revision=c.coverage_revision AND p.coverage_revision=b.coverage_revision))))`, w.Key).Scan(&usable)
			if err == nil && !usable {
				done = true
				break
			}
			w.Phase = 1
		}
		if err == nil {
			var ids []int64
			ids, err = scanIDs(ctx, tx, `SELECT id FROM reading_path_scope INDEXED BY reading_path_scope_parked WHERE source_id=? AND parked=1 AND id>? AND id<=? ORDER BY id LIMIT ?`, w.Key, w.Scope, w.FenceScope, limit)
			n = len(ids)
			for _, id := range ids {
				if err != nil {
					break
				}
				_, err = tx.ExecContext(ctx, `UPDATE reading_path_scope SET parked=0 WHERE id=?`, id)
				w.Scope = id
			}
			done = n == 0
		}
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
	if err == nil && done {
		err = pathRetireInput(ctx, tx, w.Library)
	}
	return max(1, n), err
}

func pathWorkFence(ctx context.Context, tx *sql.Tx, w *pathWork) error {
	var err error
	switch w.Kind {
	case "asset":
		err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT id FROM inventory_objects INDEXED BY inventory_objects_reading_asset_object WHERE asset_id=? ORDER BY id DESC LIMIT 1),'')`, w.Key).Scan(&w.FenceObject)
	case "source":
		err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT id FROM inventory_objects INDEXED BY inventory_objects_reading_source_object WHERE source_id=?1 ORDER BY id DESC LIMIT 1),''),COALESCE((SELECT id FROM reading_path_scope WHERE source_id=?1 ORDER BY id DESC LIMIT 1),0)`, w.Key).Scan(&w.FenceObject, &w.FenceScope)
	case "item":
		err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT object_id FROM reading_path_object_keys WHERE item_id=?1 ORDER BY object_id DESC LIMIT 1),''),COALESCE((SELECT asset_id FROM reading_publications WHERE item_id=?1 ORDER BY asset_id DESC LIMIT 1),0)`, w.Key).Scan(&w.FenceObject, &w.FenceAsset)
	case "pending":
		err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT scope_id FROM reading_path_scope_entities WHERE entity_id=? ORDER BY scope_id DESC LIMIT 1),0)`, w.Key).Scan(&w.FenceScope)
	case "report":
		err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT id FROM reading_path_scope WHERE source_id=(SELECT source_id FROM inventory_runs WHERE job_id=?) ORDER BY id DESC LIMIT 1),0)`, w.Key).Scan(&w.FenceScope)
	case "blocks":
		err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT sequence FROM inventory_scan_issues WHERE job_id=?1 ORDER BY sequence DESC LIMIT 1),0),COALESCE((SELECT after_issue FROM reading_path_source_barrier WHERE job_id=?1),?2)`, w.Key, w.Scope).Scan(&w.FenceScope, &w.Scope)
	case "coverage":
		err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT id FROM reading_path_block WHERE source_id=? ORDER BY id DESC LIMIT 1),0)`, w.Key).Scan(&w.FenceScope)
	case "wake":
		err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT id FROM reading_path_scope WHERE source_id=? ORDER BY id DESC LIMIT 1),0)`, w.Key).Scan(&w.FenceScope)
	case "library":
		err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT s.id FROM catalog_libraries cl CROSS JOIN library_sources s ON s.library_id=cl.library_id WHERE cl.id=?1 ORDER BY s.id DESC LIMIT 1),''),COALESCE((SELECT id FROM reading_path_scope WHERE library_id=?1 ORDER BY id DESC LIMIT 1),0)`, w.Library).Scan(&w.FenceObject, &w.FenceScope)
	}
	return err
}

// A quantum may finish several tiny coalesced feed keys. The aggregate raw
// budget, including empty subcursor/EOF probes, remains <=128. Only builder
// transitions require a dedicated commit; a one-object feed is not a batch.
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

type pathScope struct {
	ID, Library, Desired, Completed, Pending, SealCursor, Revision, Generation, SealRevision, CoverageRevision, ClosureSeal int64
	Source, Incarnation, Root, Key, SealJob, Reason, Job, Barrier, BarrierReason, RootIdentity                              string
	Sealed                                                                                                                  bool
}

func pathScopeRead(ctx context.Context, tx *sql.Tx, id int64) (pathScope, error) {
	var s pathScope
	err := tx.QueryRowContext(ctx, `SELECT q.id,q.library_id,q.desired,q.completed,q.pending,q.seal_cursor,q.source_id,q.incarnation,q.root_prefix,q.directory_key,q.seal_job,q.reason,q.sealed,COALESCE(i.revision,0),COALESCE(b.job_id,''),COALESCE(b.phase,'open'),COALESCE(b.reason,''),COALESCE(src.generation,0),COALESCE(src.root_identity,''),q.seal_revision,COALESCE(b.coverage_revision,0),q.closure_seal
 FROM reading_path_scope q LEFT JOIN reading_path_inputs i ON i.library_id=q.library_id LEFT JOIN reading_path_source_barrier b ON b.source_id=q.source_id LEFT JOIN library_sources src ON src.id=q.source_id WHERE q.id=?`, id).Scan(&s.ID, &s.Library, &s.Desired, &s.Completed, &s.Pending, &s.SealCursor, &s.Source, &s.Incarnation, &s.Root, &s.Key, &s.SealJob, &s.Reason, &s.Sealed, &s.Revision, &s.Job, &s.Barrier, &s.BarrierReason, &s.Generation, &s.RootIdentity, &s.SealRevision, &s.CoverageRevision, &s.ClosureSeal)
	return s, err
}
func scopeLive(ctx context.Context, tx *sql.Tx, s pathScope) (bool, error) {
	var ok bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM library_sources src JOIN catalog_libraries cl ON cl.library_id=src.library_id JOIN libraries l ON l.id=cl.library_id WHERE src.id=? AND src.incarnation=? AND cl.id=? AND cl.retired=0 AND l.kind IN('book','comic') AND src.root=?)`, s.Source, s.Incarnation, s.Library, strings.TrimSuffix(s.Root, string(filepath.Separator))).Scan(&ok)
	// '/' and a drive root retain their separator.
	if err == nil && !ok {
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM library_sources src JOIN catalog_libraries cl ON cl.library_id=src.library_id JOIN libraries l ON l.id=cl.library_id WHERE src.id=? AND src.incarnation=? AND cl.id=? AND cl.retired=0 AND l.kind IN('book','comic') AND src.root=?)`, s.Source, s.Incarnation, s.Library, s.Root).Scan(&ok)
	}
	return ok, err
}
func scopeReady(ctx context.Context, tx *sql.Tx, s pathScope) (string, error) {
	var current bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM reading_path_source_barrier b JOIN library_sources src ON src.id=b.source_id WHERE b.source_id=? AND b.incarnation=? AND b.source_generation=? AND b.root_identity=src.root_identity AND b.root=src.root)`, s.Source, s.Incarnation, s.Generation).Scan(&current); err != nil {
		return "", err
	}
	if !current {
		return "source_changed", nil
	}
	if s.Barrier == "open" {
		seal, err := pathClosureSeal(ctx, tx, s)
		if err != nil {
			return "", err
		}
		if seal == 0 || seal != s.ClosureSeal {
			return "scan_open", nil
		}
	} else if s.Barrier != "sealed" {
		return "scan_" + s.Barrier, nil
	} else if s.ClosureSeal != 0 {
		return "coverage_pending", nil
	}
	if s.BarrierReason != "" {
		return s.BarrierReason, nil
	}
	if !s.Sealed || s.SealJob != s.Job || s.SealRevision != s.CoverageRevision {
		return "coverage_pending", nil
	}
	if s.Reason != "" {
		return s.Reason, nil
	}
	if s.Pending != 0 {
		return "availability_pending", nil
	}
	var pending bool
	err := tx.QueryRowContext(ctx, `SELECT `+pathLibraryBlocked+` OR `+pathGlobalBlocked, s.Library).Scan(&pending)
	if pending {
		return pathLibraryReason, err
	}
	return "", err
}

// Library- and domain-wide inputs that are still settling. Dispatch checks them
// once per library rather than parking every scope behind them; the commits
// that clear them wake the worker. Wake and report work never change inputs.
const (
	pathLibraryBlocked = `(EXISTS(SELECT 1 FROM reading_path_dirty_work WHERE library_id=?1 AND kind NOT IN('report','coverage','wake')) OR EXISTS(SELECT 1 FROM catalog_dirty WHERE domain=28 AND entity_id=?1) OR EXISTS(SELECT 1 FROM catalog_derivation_failures WHERE domain=28 AND entity_id=?1) OR EXISTS(SELECT 1 FROM catalog_fanouts WHERE domain=28 AND key=?1))`
	pathGlobalBlocked  = `EXISTS(SELECT 1 FROM catalog_derivations WHERE domain IN(20,28) AND (rebuilding=1 OR cleaning=1 OR first_build=1))`
	pathLibraryReason  = "availability_or_input_fanout"
)

type pathQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type pathCandidate struct {
	scope, library int64
	// The library's quiet gap ended but its inputs changed meanwhile.
	extend bool
}

// pathDispatch finds the least-served runnable scope whose library is open. It
// costs a few index seeks per library that has runnable scopes (libraries are
// few and owner-created) and never reads a parked scope. due is the earliest
// end of a quiet gap that is holding a library back, in Unix ms, or zero.
func pathDispatch(ctx context.Context, q pathQueryer, now int64) (best pathCandidate, ok bool, due int64, err error) {
	var global bool
	if err = q.QueryRowContext(ctx, `SELECT `+pathGlobalBlocked).Scan(&global); err != nil || global {
		return best, false, 0, err
	}
	var bestServed int64
	for library := int64(math.MinInt64); ; {
		err = q.QueryRowContext(ctx, `SELECT library_id FROM reading_path_scope INDEXED BY reading_path_scope_runnable WHERE queued=1 AND parked=0 AND library_id>? ORDER BY library_id LIMIT 1`, library).Scan(&library)
		if errors.Is(err, sql.ErrNoRows) {
			return best, ok, due, nil
		}
		if err != nil {
			return best, false, 0, err
		}
		var blocked bool
		var revision, holdRevision, holdUntil int64
		if err = q.QueryRowContext(ctx, `SELECT `+pathLibraryBlocked+`,COALESCE(i.revision,0),COALESCE(i.hold_revision,0),COALESCE(i.hold_until_ms,0) FROM (SELECT 1) LEFT JOIN reading_path_inputs i ON i.library_id=?1`, library).Scan(&blocked, &revision, &holdRevision, &holdUntil); err != nil {
			return best, false, 0, err
		}
		if blocked {
			continue
		}
		// A hold further ahead than the cap came from a clock that moved back.
		if holdUntil > now && holdUntil-now <= readingQuietGapMax.Milliseconds() {
			if due == 0 || holdUntil < due {
				due = holdUntil
			}
			continue
		}
		var served, id int64
		if err = q.QueryRowContext(ctx, `SELECT served,id FROM reading_path_scope INDEXED BY reading_path_scope_runnable WHERE queued=1 AND parked=0 AND library_id=? ORDER BY served,id LIMIT 1`, library).Scan(&served, &id); err != nil {
			return best, false, 0, err
		}
		if !ok || served < bestServed || served == bestServed && id < best.scope {
			best, bestServed, ok = pathCandidate{id, library, holdUntil > 0 && revision != holdRevision}, served, true
		}
	}
}

// pathHold starts (or restarts) a library's quiet gap at its current revision.
// An abort lengthens the gap; a restart after changes during the gap does not.
func pathHold(ctx context.Context, tx *sql.Tx, s pathScope, now int64, abort bool) error {
	var aborts int64
	err := tx.QueryRowContext(ctx, `SELECT aborts FROM reading_path_inputs WHERE library_id=?`, s.Library).Scan(&aborts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if abort {
		if aborts == 0 {
			log.Printf("Reading COMPLETE deferred: library=%d source=%s folder=%q path inputs in this library are changing; waiting for %s without changes", s.Library, s.Source, s.Key, readingQuietGap)
		}
		aborts = min(aborts+1, 16)
	}
	_, err = tx.ExecContext(ctx, `UPDATE reading_path_inputs SET aborts=?,hold_revision=revision,hold_until_ms=? WHERE library_id=?`, aborts, now+readingQuietGapFor(aborts).Milliseconds(), s.Library)
	return err
}

// readingPathNextDue is how long until a quiet gap lets a held library claim
// again, for the worker's timer; zero when only a write can make Reading
// runnable. It is read-only.
func readingPathNextDue(ctx context.Context, db *sql.DB) time.Duration {
	now := readingPathNow().UnixMilli()
	_, ok, due, err := pathDispatch(ctx, db, now)
	if err != nil {
		return 0
	}
	if ok {
		// It became runnable since the last step, e.g. a gap just ended.
		return time.Millisecond
	}
	if due == 0 {
		return 0
	}
	return time.Duration(due-now) * time.Millisecond
}
func pathReport(ctx context.Context, tx *sql.Tx, s pathScope, reason string) error {
	if s.Job == "" {
		return nil
	}
	if reason == "source_changed" || reason == "scan_"+s.Barrier || s.BarrierReason != "" && reason == s.BarrierReason {
		return pathReportSource(ctx, tx, s, reason)
	}
	var finished bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE id=? AND status IN('complete','complete_with_warnings'))`, s.Job).Scan(&finished); err != nil {
		return err
	}
	if !finished {
		return nil
	}
	folder := s.Key
	if folder == "" {
		folder = "."
	}
	// A healthy scope waiting for its builder turn or its seal, or behind
	// changing library inputs, is ordinary progress, not a warning; it still
	// gets its history/log entry. Actual trust/coverage/availability failures
	// keep warning semantics, including blocks the seal reduction finds.
	warning := reason != "publication_queued" && reason != "library_changing" && reason != "coverage_pending"
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO inventory_scan_issues(job_id,relative_path,reason,warning) VALUES(?,?,?,?)`, s.Job, filepath.FromSlash(folder), "reading_path_"+reason, warning)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n > 0 {
		log.Printf("Reading COMPLETE unpublished: source=%s folder=%q reason=%s job=%s", s.Source, folder, reason, s.Job)
		if !warning {
			return nil
		}
		for _, q := range []string{`UPDATE inventory_runs SET warnings=warnings+1 WHERE job_id=?`, `UPDATE jobs SET status='complete_with_warnings' WHERE id=? AND status='complete'`} {
			if _, err = tx.ExecContext(ctx, q, s.Job); err != nil {
				return err
			}
		}
	}
	return nil
}

// pathReportSource records a source whose barrier is not sealed, current and
// trusted: all its scopes wait for the next trusted scan, possibly for good
// (an offline source, untrusted coverage). One history row and one log line per
// source and scan, without a warning, rather than one per folder.
func pathReportSource(ctx context.Context, tx *sql.Tx, s pathScope, reason string) error {
	var ended bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE id=? AND status IN('complete','complete_with_warnings','cancelled','failed','interrupted'))`, s.Job).Scan(&ended); err != nil || !ended {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO inventory_scan_issues(job_id,relative_path,reason,warning) VALUES(?,'.','reading_path_waiting_for_trusted_scan',0)`, s.Job)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n > 0 {
		log.Printf("Reading COMPLETE waiting for a trusted scan: source=%s job=%s reason=%s; folder-derived author and series for this source publish after the next trusted scan", s.Source, s.Job, reason)
	}
	return nil
}

// Coverage reduction consumes exact ancestor keys, at most limit per turn. A
// blocked generic descendant is the same key; a blocked non-generic subtree is
// an ancestor only of contexts that actually depend on that subtree.
func pathSealStep(ctx context.Context, tx *sql.Tx, s pathScope, limit int) (int, error) {
	if s.SealJob != s.Job || s.SealRevision != s.CoverageRevision {
		s.SealCursor = 0
		s.Reason = ""
	}
	type dependency struct{ key, mode string }
	keys := []dependency{{s.Key, "scope"}, {"", "subtree"}}
	if s.Key != "" {
		for i := range s.Key {
			if s.Key[i] == '/' {
				keys = append(keys, dependency{s.Key[:i], "subtree"})
			}
		}
		keys = append(keys, dependency{s.Key, "subtree"})
	}
	n := 0
	for int(s.SealCursor) < len(keys) && n < limit {
		var reason string
		err := tx.QueryRowContext(ctx, `SELECT reason FROM reading_path_block WHERE source_id=? AND job_id=? AND directory_key=? AND mode=?`, s.Source, s.Job, keys[s.SealCursor].key, keys[s.SealCursor].mode).Scan(&reason)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		if err == nil {
			s.Reason = reason
		}
		s.SealCursor++
		n++
	}
	sealed := int(s.SealCursor) == len(keys)
	_, err := tx.ExecContext(ctx, `UPDATE reading_path_scope SET seal_job=?,seal_revision=?,seal_cursor=?,sealed=?,reason=?,closure_seal=? WHERE id=?`, s.Job, s.CoverageRevision, s.SealCursor, sealed, s.Reason, s.ClosureSeal, s.ID)
	if err == nil && sealed && s.Reason != "" {
		err = pathReport(ctx, tx, s, s.Reason)
	}
	return max(n, 1), err
}

// StepReadingPaths is one durable quantum on the existing supervised catalogue
// worker. Feeds, the global slot and scratch retirement take turns leading, so
// another library's constant genuine churn cannot monopolize the slot or
// prevent queue normalization. When nothing is runnable it writes nothing:
// blocked scopes are parked and only a dependency change makes them runnable.
func StepReadingPaths(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	limit = max(1, min(limit, readingPathBatch))
	now := readingPathNow().UnixMilli()
	var work bool
	var build, retired sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM reading_path_dirty_work),(SELECT build_id FROM reading_path_slot WHERE singleton=1),(SELECT id FROM reading_path_build INDEXED BY reading_path_build_retired WHERE phase IN('cleanup','abort') ORDER BY id LIMIT 1)`).Scan(&work, &build, &retired); err != nil {
		return 0, err
	}
	var next pathCandidate
	var claim bool
	if !build.Valid {
		var err error
		if next, claim, _, err = pathDispatch(ctx, tx, now); err != nil {
			return 0, err
		}
	}
	if !work && !build.Valid && !retired.Valid && !claim {
		return 0, nil
	}
	turn, err := pathTurn(ctx, tx)
	if err != nil {
		return 0, err
	}
	for i := range int64(3) {
		n := 0
		switch (turn + i) % 3 {
		case 0:
			if work {
				n, err = pathWorkQuantum(ctx, tx, limit, turn)
			}
		case 1:
			if build.Valid {
				n, err = pathBuildStep(ctx, tx, build.Int64, limit, now)
			} else if claim {
				n, err = pathClaim(ctx, tx, next, turn, now, limit)
			}
		case 2:
			if retired.Valid {
				n, err = pathRetireBuild(ctx, tx, retired.Int64, limit)
			}
		}
		if n > 0 || err != nil {
			return n, err
		}
	}
	return 0, nil
}

// pathClaim serves one dispatched scope: retire, seal, park or claim the slot.
func pathClaim(ctx context.Context, tx *sql.Tx, c pathCandidate, turn, now int64, limit int) (int, error) {
	s, err := pathScopeRead(ctx, tx, c.scope)
	if err != nil {
		return 0, err
	}
	if c.extend {
		return 1, pathHold(ctx, tx, s, now, false)
	}
	live, err := scopeLive(ctx, tx, s)
	if err != nil {
		return 0, err
	}
	if !live {
		return pathRetireScope(ctx, tx, s, limit)
	}
	if s.Key == "" {
		_, err = tx.ExecContext(ctx, `UPDATE reading_path_scope SET completed=desired,queued=0 WHERE id=?`, s.ID)
		return 1, err
	}
	closure := int64(0)
	if s.Barrier == "open" {
		closure, err = pathClosureSeal(ctx, tx, s)
		if err != nil {
			return 0, err
		}
	}
	if (s.Barrier == "sealed" || closure != 0) && (s.SealJob != s.Job || !s.Sealed || s.SealRevision != s.CoverageRevision || s.ClosureSeal != closure) {
		var pending bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM reading_path_dirty_work WHERE kind='blocks' AND work_key=?)`, s.Job).Scan(&pending); err != nil {
			return 0, err
		}
		if pending {
			return pathWorkQuantum(ctx, tx, limit, turn)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE reading_path_scope SET served=? WHERE id=?`, turn, s.ID); err != nil {
			return 0, err
		}
		if s.ClosureSeal != closure {
			s.SealCursor = 0
			s.Reason = ""
			s.Sealed = false
		}
		s.ClosureSeal = closure
		return pathSealStep(ctx, tx, s, limit)
	}
	reason, err := scopeReady(ctx, tx, s)
	if err != nil {
		return 0, err
	}
	if reason == pathLibraryReason {
		// Dispatch already read the same gate in this transaction.
		return 0, nil
	}
	if reason != "" {
		// Its own input, settled availability or its source's barrier unparks it.
		// The probe above and this park share one writer transaction, so a
		// dependency change commits either before the probe (and is seen) or
		// after the park (and its trigger unparks or wakes the scope).
		if err = pathReport(ctx, tx, s, reason); err != nil {
			return 0, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE reading_path_scope SET parked=1 WHERE id=?`, s.ID)
		return 1, err
	}
	if err = pathReport(ctx, tx, s, "publication_queued"); err != nil {
		return 0, err
	}
	var bid int64
	err = tx.QueryRowContext(ctx, `INSERT INTO reading_path_build(scope_id,desired,input_revision,source_generation,root_identity,job_id,coverage_revision,closure_seal,phase) VALUES(?,?,?,?,?,?,?,?,'capture') RETURNING id`, s.ID, s.Desired, s.Revision, s.Generation, s.RootIdentity, s.Job, s.CoverageRevision, s.ClosureSeal).Scan(&bid)
	if err != nil {
		return 0, err
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE reading_path_slot SET build_id=? WHERE singleton=1`, []any{bid}},
		{`UPDATE reading_path_scope SET served=? WHERE id=?`, []any{turn, s.ID}},
		{`UPDATE reading_path_inputs SET hold_until_ms=0 WHERE library_id=? AND hold_until_ms<>0`, []any{s.Library}},
	} {
		if _, err = tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			return 0, err
		}
	}
	return 1, nil
}
func pathRetireScope(ctx context.Context, tx *sql.Tx, s pathScope, limit int) (int, error) {
	// A finished or aborted build may still be retiring its scratch.
	var build int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM reading_path_build WHERE scope_id=? ORDER BY id LIMIT 1`, s.ID).Scan(&build)
	if err == nil {
		return pathRetireBuild(ctx, tx, build, limit)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	// Public deletion already removed authority. Private cleanup never refuses it.
	r, err := tx.ExecContext(ctx, `DELETE FROM reading_path_object_keys WHERE object_id IN(SELECT object_id FROM reading_path_object_keys WHERE scope_id=? ORDER BY object_id LIMIT ?)`, s.ID, limit)
	if err != nil {
		return 0, err
	}
	n, _ := r.RowsAffected()
	if n > 0 {
		return int(n), nil
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO reading_path_dirty_work(library_id,kind,work_key) VALUES(?,'coverage',?) ON CONFLICT(library_id,kind,work_key) DO UPDATE SET requested=requested+1`, s.Library, s.Source); err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM reading_path_scope WHERE id=?`, s.ID)
	if err == nil {
		err = pathRetireInput(ctx, tx, s.Library)
	}
	return 1, err
}
func pathRetireInput(ctx context.Context, tx *sql.Tx, library int64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM reading_path_inputs WHERE library_id=?1 AND NOT EXISTS(SELECT 1 FROM catalog_libraries WHERE id=?1) AND NOT EXISTS(SELECT 1 FROM reading_path_scope WHERE library_id=?1) AND NOT EXISTS(SELECT 1 FROM reading_path_dirty_work WHERE library_id=?1)`, library)
	return err
}

type pathBuild struct {
	ID, Scope, Desired, Revision, Generation, Item, Asset, Titles, NonSeries, Sequence, FlatItem, FlatAsset, CoverageRevision, ClosureSeal int64
	RootIdentity, Job, Phase, Object                                                                                                       string
}

func pathBuildStep(ctx context.Context, tx *sql.Tx, id int64, limit int, now int64) (int, error) {
	var b pathBuild
	b.ID = id
	err := tx.QueryRowContext(ctx, `SELECT scope_id,desired,input_revision,source_generation,root_identity,job_id,phase,after_object,after_item,after_asset,title_count,non_series_count,COALESCE(publish_sequence,0),after_flatten_item,after_flatten_asset,coverage_revision,closure_seal FROM reading_path_build WHERE id=?`, id).Scan(&b.Scope, &b.Desired, &b.Revision, &b.Generation, &b.RootIdentity, &b.Job, &b.Phase, &b.Object, &b.Item, &b.Asset, &b.Titles, &b.NonSeries, &b.Sequence, &b.FlatItem, &b.FlatAsset, &b.CoverageRevision, &b.ClosureSeal)
	if err != nil {
		return 0, err
	}
	s, err := pathScopeRead(ctx, tx, b.Scope)
	if err != nil {
		return 0, err
	}
	if b.Phase == "capture" || b.Phase == "stage" || b.Phase == "ready" {
		reason, e := scopeReady(ctx, tx, s)
		if e != nil {
			return 0, e
		}
		live, e := scopeLive(ctx, tx, s)
		if e != nil {
			return 0, e
		}
		if reason != "" || !live || b.Desired != s.Desired || b.Revision != s.Revision || b.Generation != s.Generation || b.RootIdentity != s.RootIdentity || b.Job != s.Job || b.CoverageRevision != s.CoverageRevision || b.ClosureSeal != s.ClosureSeal {
			// Unpublished scratch is private, so the slot is free at once.
			for _, q := range []string{`UPDATE reading_path_build SET phase='abort' WHERE id=?`, `UPDATE reading_path_slot SET build_id=NULL WHERE singleton=1 AND build_id=?`} {
				if _, err = tx.ExecContext(ctx, q, id); err != nil {
					return 0, err
				}
			}
			// Changing path inputs in the library (this source's or another's)
			// restart the build only after a quiet gap, not on every write.
			if live && (reason == pathLibraryReason || b.Desired != s.Desired || b.Revision != s.Revision) {
				if err = pathHold(ctx, tx, s, now, true); err == nil {
					err = pathReport(ctx, tx, s, "library_changing")
				}
			}
			return 1, err
		}
	}
	switch b.Phase {
	case "capture":
		return pathCaptureStep(ctx, tx, b, s, limit)
	case "stage":
		return pathStageStep(ctx, tx, b, s, limit)
	case "ready":
		seq, e := pathSequence(ctx, tx)
		if e != nil {
			return 0, e
		}
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`UPDATE reading_path_build SET phase='flatten',publish_sequence=? WHERE id=?`, []any{seq, id}},
			{`UPDATE reading_path_slot SET overlay_id=? WHERE singleton=1`, []any{id}},
			{`UPDATE reading_path_scope SET completed=?,queued=(desired<>?) WHERE id=?`, []any{b.Desired, b.Desired, b.Scope}},
			{`UPDATE reading_path_inputs SET aborts=0,hold_until_ms=0 WHERE library_id=? AND (aborts<>0 OR hold_until_ms<>0)`, []any{s.Library}},
		} {
			if _, err = tx.ExecContext(ctx, q.sql, q.args...); err != nil {
				return 0, err
			}
		}
		if err = pathSemantic(ctx, tx, s.Library); err == nil {
			err = FlushReadingPathSemanticTx(ctx, tx)
		}
		return 1, err
	case "flatten":
		return pathFlattenStep(ctx, tx, b, limit)
	case "cleanup", "abort":
		return pathRetireBuild(ctx, tx, id, limit)
	}
	return 0, fmt.Errorf("unknown Reading path phase %q", b.Phase)
}

// pathRetireBuild deletes one bounded batch of a finished or aborted build's
// private scratch, outside the slot; the build row goes last.
func pathRetireBuild(ctx context.Context, tx *sql.Tx, id int64, limit int) (int, error) {
	for _, table := range []string{"reading_path_capture", "reading_path_representative", "reading_path_stage"} {
		// Every table is WITHOUT ROWID with a build-leading primary key.
		key := "item_id,asset_id"
		if table == "reading_path_representative" {
			key = "item_id"
		}
		r, e := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE build_id=?1 AND (`+key+`) IN(SELECT `+key+` FROM `+table+` WHERE build_id=?1 ORDER BY `+key+` LIMIT ?2)`, id, limit)
		if e != nil {
			return 0, e
		}
		n, _ := r.RowsAffected()
		if n > 0 {
			return int(n), nil
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE reading_path_slot SET build_id=NULL WHERE singleton=1 AND build_id=? AND overlay_id IS NULL`, id); err != nil {
		return 0, err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM reading_path_build WHERE id=?`, id)
	return 1, err
}
func pathCaptureStep(ctx context.Context, tx *sql.Tx, b pathBuild, s pathScope, limit int) (int, error) {
	// Materialize the RAW window before eligibility. Rejected prefixes still
	// advance the durable cursor and consume budget. Point joins never hide EOF.
	ids, err := pathStrings(ctx, tx, `WITH raw AS MATERIALIZED (SELECT object_id FROM reading_path_object_keys WHERE scope_id=? AND object_id>? ORDER BY object_id LIMIT ?) SELECT object_id FROM raw ORDER BY object_id`, s.ID, b.Object, limit)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		_, err = tx.ExecContext(ctx, `UPDATE reading_path_build SET phase='stage' WHERE id=?`, b.ID)
		return 1, err
	}
	// One bounded statement hydrates the raw window without retaining more than
	// the ordinary byte budget, so it may consume only a prefix. n is that RAW
	// prefix, including ids whose joins were absent: an id inside the prefix but
	// absent from the map is today's sql.ErrNoRows and stays behind the durable
	// after_object cursor below, while ids past n were never read and stay in
	// front of the cursor for the next step rather than being treated as missing.
	objects, n, err := readPathObjectsCapped(ctx, tx, ids)
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, errPathObjectProgress
	}
	ids = ids[:n]
	for _, id := range ids {
		v, ok := objects[id]
		if !ok {
			continue
		}
		if !v.Eligible {
			continue
		}
		in := ReadingRuleInput{Item: v.Item, SourceRoot: v.Source, RelativePath: filepath.ToSlash(v.Relative), Kind: v.Kind}
		key, evidence, e := ReadingPathFolderEvidence(in)
		if e != nil {
			return 0, e
		}
		if key != s.Key || v.Source != s.Source || v.Incarnation != s.Incarnation || rootPrefix(v.Root) != s.Root {
			continue
		}
		// Duplicate aliases can name the same publication; preserve the first object
		// in exact object-id driver order without changing its representative twice.
		r, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO reading_path_capture VALUES(?,?,?,?,?,?,?,?)`, b.ID, v.Item, v.Asset, v.ID, v.Revision, v.Relative, v.Kind, evidence)
		if e != nil {
			return 0, e
		}
		added, _ := r.RowsAffected()
		if added == 0 {
			continue
		}
		var asset int64
		var oldEvidence bool
		e = tx.QueryRowContext(ctx, `SELECT asset_id,series_evidence FROM reading_path_representative WHERE build_id=? AND item_id=?`, b.ID, v.Item).Scan(&asset, &oldEvidence)
		if errors.Is(e, sql.ErrNoRows) {
			b.Titles++
			if !evidence {
				b.NonSeries++
			}
			_, e = tx.ExecContext(ctx, `INSERT INTO reading_path_representative VALUES(?,?,?,?)`, b.ID, v.Item, v.Asset, evidence)
		} else if e == nil && v.Asset < asset {
			if !oldEvidence {
				b.NonSeries--
			}
			if !evidence {
				b.NonSeries++
			}
			_, e = tx.ExecContext(ctx, `UPDATE reading_path_representative SET asset_id=?,series_evidence=? WHERE build_id=? AND item_id=?`, v.Asset, evidence, b.ID, v.Item)
		}
		if e != nil {
			return 0, e
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE reading_path_build SET after_object=?,title_count=?,non_series_count=? WHERE id=?`, ids[len(ids)-1], b.Titles, b.NonSeries, b.ID)
	return len(ids), err
}
func pathStageStep(ctx context.Context, tx *sql.Tx, b pathBuild, s pathScope, limit int) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT item_id,asset_id,object_id,source_revision,relative_path,item_kind FROM reading_path_capture WHERE build_id=? AND (item_id,asset_id)>(?,?) ORDER BY item_id,asset_id LIMIT ?`, b.ID, b.Item, b.Asset, limit)
	if err != nil {
		return 0, err
	}
	type row struct {
		item, asset                int64
		object, revision, relative string
		kind                       Kind
	}
	var batch []row
	for rows.Next() {
		var v row
		if err = rows.Scan(&v.item, &v.asset, &v.object, &v.revision, &v.relative, &v.kind); err != nil {
			rows.Close()
			return 0, err
		}
		batch = append(batch, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	if len(batch) == 0 {
		_, err = tx.ExecContext(ctx, `UPDATE reading_path_build SET phase='ready' WHERE id=?`, b.ID)
		return 1, err
	}
	for _, v := range batch {
		values, e := ReadingPathValue(ReadingRuleInput{Item: v.item, SourceRoot: s.Source, RelativePath: filepath.ToSlash(v.relative), Kind: v.kind}, int(min(b.Titles, 2)), b.NonSeries == 0)
		if e != nil {
			return 0, e
		}
		value, e := bundle(v.object, v.revision, values)
		if e != nil {
			return 0, e
		}
		// Association deletion cascades staged rows. Never recreate a deleted one.
		_, e = tx.ExecContext(ctx, `INSERT INTO reading_path_stage(build_id,item_id,asset_id,`+pathColumns+`) SELECT ?,?,?,`+strings.TrimSuffix(strings.Repeat("?,", 8), ",")+` WHERE EXISTS(SELECT 1 FROM reading_publications WHERE item_id=? AND asset_id=?)`, append(append([]any{b.ID, v.item, v.asset}, value.args()...), v.item, v.asset)...)
		if e != nil {
			return 0, e
		}
	}
	last := batch[len(batch)-1]
	_, err = tx.ExecContext(ctx, `UPDATE reading_path_build SET after_item=?,after_asset=? WHERE id=?`, last.item, last.asset, b.ID)
	return len(batch), err
}
func pathFlattenStep(ctx context.Context, tx *sql.Tx, b pathBuild, limit int) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT item_id,asset_id,`+pathColumns+` FROM reading_path_stage WHERE build_id=? AND (item_id,asset_id)>(?,?) ORDER BY item_id,asset_id LIMIT ?`, b.ID, b.FlatItem, b.FlatAsset, limit)
	if err != nil {
		return 0, err
	}
	type row struct {
		item, asset int64
		value       pathBundle
	}
	var batch []row
	for rows.Next() {
		var v row
		if err = rows.Scan(append([]any{&v.item, &v.asset}, v.value.scan()...)...); err != nil {
			rows.Close()
			return 0, err
		}
		batch = append(batch, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	if len(batch) == 0 {
		// Every head is copied: release the slot now and retire scratch on its
		// own rail, so the next library's build need not wait for cleanup.
		if _, err = tx.ExecContext(ctx, `UPDATE reading_path_slot SET overlay_id=NULL,build_id=NULL WHERE singleton=1`); err != nil {
			return 0, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE reading_path_build SET phase='cleanup' WHERE id=?`, b.ID)
		return 1, err
	}
	for _, v := range batch {
		if err = writePathHead(ctx, tx, v.item, v.asset, b.Sequence, v.value); err != nil {
			return 0, err
		}
	}
	last := batch[len(batch)-1]
	_, err = tx.ExecContext(ctx, `UPDATE reading_path_build SET after_flatten_item=?,after_flatten_asset=? WHERE id=?`, last.item, last.asset, b.ID)
	return len(batch), err
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
	fields := strings.Split(pathColumns, ",")
	for i, col := range fields {
		fields[i] = "CASE WHEN v.item_id IS NOT NULL AND (h.item_id IS NULL OR b.publish_sequence>h.publisher_sequence) THEN v." + col + " ELSE h." + col + " END AS " + col
	}
	rows, err := tx.QueryContext(ctx, `WITH chosen(item_id,asset_id) AS (`+chosen+`),
 bundles AS MATERIALIZED (SELECT c.item_id,c.asset_id,`+strings.Join(fields, ",")+`
 FROM chosen c CROSS JOIN reading_path_slot z ON z.singleton=1
 LEFT JOIN reading_path_build b ON b.id=z.overlay_id
 LEFT JOIN reading_path_head h ON h.item_id=c.item_id AND h.asset_id=c.asset_id
 LEFT JOIN reading_path_stage v ON v.build_id=z.overlay_id AND v.item_id=c.item_id AND v.asset_id=c.asset_id)
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
