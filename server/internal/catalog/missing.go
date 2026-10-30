package catalog

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Missing files, the way Plex's "trash" works. Nothing here is a folder.
//
// A file that is no longer on disk is marked missing the first time a
// complete scan of its folder does not find it. It stops being a version of
// its title at once (it is not offered, picked or played), and a title left
// with no file at all stays in the library, marked missing with the time its
// last file went, shown as unavailable. A file that resolves to the title
// brings it back whole.
//
// Missing things are finally removed by the owner's rule (immediately, after
// a number of days, never) or by "empty now". Removing a title keeps each
// profile's state under the title's identities (retained_state.go).
//
// A source that cannot be read, or whose folder is suddenly empty where it
// held media before, is not a list of missing files: its scan fails before
// anything is marked.

// ErrSourceLooksUnmounted: the scan found an empty folder where there was
// media before. That is what an unmounted disk looks like, so nothing was
// marked missing.
var ErrSourceLooksUnmounted = errors.New("the folder is empty but held media before; nothing was marked missing")

// SourceLooksUnmounted reports whether a completed listing found nothing at
// all (no media files and no folders) under a source that had available files
// before this scan. The scan loop asks before it marks anything missing.
func (s *Service) SourceLooksUnmounted(ctx context.Context, job, source string) (bool, error) {
	var unmounted bool
	err := s.db.QueryRowContext(ctx, `SELECT r.discovered=0 AND (r.root_rebound=1 OR NOT EXISTS(SELECT 1 FROM inventory_scan_issues x WHERE x.job_id=r.job_id AND (x.protect=1 OR x.reason IN('ignored_name','ignored_rule','symlink_disabled'))
 AND EXISTS(SELECT 1 FROM inventory_objects excluded WHERE excluded.source_id=r.source_id AND excluded.root_incarnation=r.root_incarnation AND excluded.retired=0 AND excluded.state='available' AND excluded.seen_job<>r.job_id
 AND (excluded.relative_path=x.relative_path OR substr(excluded.relative_path,1,length(x.relative_path)+1)=x.relative_path||?))))
 AND EXISTS(SELECT 1 FROM inventory_directories d WHERE d.job_id=r.job_id AND d.relative_path='.' AND d.state='done' AND d.skipped=0)
 AND NOT EXISTS(SELECT 1 FROM inventory_directories d WHERE d.job_id=r.job_id AND d.relative_path<>'.')
 AND EXISTS(SELECT 1 FROM inventory_objects o WHERE o.source_id=r.source_id AND o.retired=0 AND o.state='available' AND o.root_incarnation=r.root_incarnation AND o.seen_job<>r.job_id)
 FROM inventory_runs r WHERE r.job_id=? AND r.source_id=?`, string(filepath.Separator), job, source).Scan(&unmounted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return unmounted, err
}

// MarkInventoryMissingTx records that a complete, verified listing of a file's
// folder did not contain it. The file is missing from now; the titles it
// backed are settled (a title with no file left is marked missing).
func MarkInventoryMissingTx(ctx context.Context, tx *sql.Tx, source, job, object string, now time.Time) error {
	var asset, revision, state, since string
	var observations int
	var last string
	err := tx.QueryRowContext(ctx, `SELECT asset_id,revision,state,missing_since,missing_observations,last_missing_job FROM inventory_objects WHERE id=? AND source_id=? AND retired=0`, object, source).Scan(&asset, &revision, &state, &since, &observations, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if last != job {
		observations++
	}
	if since == "" {
		since = now.UTC().Format(time.RFC3339Nano)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_objects SET missing_since=?,missing_observations=?,last_missing_job=?,state='missing' WHERE id=?`, since, observations, job, object); err != nil {
		return err
	}
	if state == "missing" {
		return nil
	}
	// The inventory UPDATE's point trigger already queues source41 availability
	// and missing work. Its bounded reducer publishes asset evidence before the
	// owner fanout; acceptance never aggregates all other objects of this asset.
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO inventory_changes(source_id,job_id,object_id,asset_id,revision,kind,created_at) VALUES(?,?,?,?,?,'missing',?)`, source, job, object, asset, revision, now.UTC().Format(time.RFC3339Nano))
	return err
}

// MissingRule is the owner's choice of when missing titles are emptied.
type MissingRule struct {
	Mode string // "immediately", "after_days" or "never"
	Days int
}

// DefaultMissingRule keeps missing titles for thirty days.
var DefaultMissingRule = MissingRule{Mode: "after_days", Days: 30}

// MissingRule reads the owner's rule from normalized server settings.
func (s *Service) MissingRule(ctx context.Context) MissingRule {
	rule := DefaultMissingRule
	var mode sql.NullString
	var days sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT settings."library.missingTitles",settings."library.missingTitlesDays" FROM server_settings AS settings WHERE settings.singleton=1`).Scan(&mode, &days); err != nil {
		return rule
	}
	switch mode.String {
	case "immediately", "after_days", "never":
		rule.Mode = mode.String
	}
	if days.Valid && days.Int64 >= 1 && days.Int64 <= 3650 {
		rule.Days = int(days.Int64)
	}
	return rule
}

type automaticMissingKey struct{}

func missingRuleTx(ctx context.Context, tx *sql.Tx) (MissingRule, error) {
	rule := DefaultMissingRule
	var mode sql.NullString
	var days sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT settings."library.missingTitles",settings."library.missingTitlesDays" FROM server_settings settings WHERE singleton=1`).Scan(&mode, &days)
	if errors.Is(err, sql.ErrNoRows) {
		return rule, nil
	}
	if err != nil {
		return rule, err
	}
	switch mode.String {
	case "immediately", "after_days", "never":
		rule.Mode = mode.String
	}
	if days.Valid && days.Int64 >= 1 && days.Int64 <= 3650 {
		rule.Days = int(days.Int64)
	}
	return rule, nil
}

// Cutoff is the time before which a missing title is due for removal under
// the rule, and whether anything is due at all.
func (r MissingRule) Cutoff(now time.Time) (time.Time, bool) {
	switch r.Mode {
	case "immediately":
		return now, true
	case "never":
		return time.Time{}, false
	}
	return now.Add(-time.Duration(r.Days) * 24 * time.Hour), true
}

// forgetAssetTx removes a file's catalogue row and the evidence that hangs off
// it. A file something else still points at (a session in flight, subtitles
// or lyrics a person attached to it, a recording's provenance) keeps its row,
// unlinked and unavailable, so those references stay valid.
func forgetAssetTx(ctx context.Context, tx *sql.Tx, id int64, token string) error {
	var held bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM item_extras WHERE asset_id=?1) OR EXISTS(SELECT 1 FROM playback_sessions WHERE asset_id=?1) OR EXISTS(SELECT 1 FROM lyric_resources WHERE asset_id=?1) OR EXISTS(SELECT 1 FROM lyric_candidates WHERE asset_id=?1) OR EXISTS(SELECT 1 FROM subtitle_resources WHERE source_id=?1) OR EXISTS(SELECT 1 FROM dvr_catalog_provenance WHERE asset_id=?1)`, token).Scan(&held); err != nil {
		return err
	}
	// This transaction destroys or unlinks every holder below. Release only
	// this exact asset's observation pins; other lifetimes' FKs still veto.
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_assets SET info_owner_object=NULL,info_owner_revision=NULL WHERE id=? AND token=? AND info_owner_object IS NOT NULL`, id, token); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE catalog_info_changes SET target_owner_object=NULL,target_owner_revision=NULL WHERE asset_id=? AND asset_token=? AND target_owner_object IS NOT NULL`, id, token); err != nil {
		return err
	}
	for _, query := range []string{
		`DELETE FROM inventory_analysis_queue WHERE object_id IN(SELECT id FROM inventory_objects WHERE asset_id=?)`,
		`DELETE FROM inventory_objects WHERE asset_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, query, token); err != nil {
			return err
		}
	}
	if held {
		items, err := scanInt64s(tx.QueryContext(ctx, `SELECT entity_id FROM catalog_asset_links INDEXED BY catalog_asset_links_asset WHERE asset_id=?`, id))
		if err != nil {
			return err
		}
		for _, item := range items {
			if err = compactcatalog.UnlinkAssetTx(ctx, tx, item, id); err != nil {
				return err
			}
		}
		return compactcatalog.SetAssetAvailableTx(ctx, tx, id, false)
	}
	for _, query := range []string{
		`DELETE FROM audio_tag_evidence WHERE asset_id=?`,
		`DELETE FROM audio_source_metadata WHERE asset_id=?`,
		`DELETE FROM episodic_sources WHERE asset_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, query, token); err != nil {
			return err
		}
	}
	return compactcatalog.DeleteAssetTx(ctx, tx, id)
}

// RemoveTitleTx removes a title for good: each profile's state is kept under
// the title's identities, then the title, its files' catalogue rows and its
// names in the ledger go. A season or show left without episodes, an album
// without tracks and a book without parts follow. Files on disk are never
// touched here.
func RemoveTitleTx(ctx context.Context, tx *sql.Tx, entity int64) error {
	if err := compactcatalog.RequireNoEntityMBPinsTx(ctx, tx, entity); err != nil {
		return err
	}
	if err := compactcatalog.RequireEntityAcquirableTx(ctx, tx, entity); err != nil {
		return err
	}
	var kind compactcatalog.Kind
	err := tx.QueryRowContext(ctx, `SELECT kind FROM catalog_entities WHERE id=?`, entity).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var show, season, album, book sql.NullInt64
	switch kind {
	case compactcatalog.Episode:
		err = tx.QueryRowContext(ctx, `SELECT show_id,season_id FROM catalog_episodes WHERE entity_id=?`, entity).Scan(&show, &season)
	case compactcatalog.Track:
		err = tx.QueryRowContext(ctx, `SELECT album_id FROM catalog_songs WHERE entity_id=?`, entity).Scan(&album)
	case compactcatalog.Part:
		err = tx.QueryRowContext(ctx, `SELECT book_id FROM catalog_book_files WHERE entity_id=?`, entity).Scan(&book)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err = retainStateTx(ctx, tx, entity); err != nil {
		return err
	}
	if err = compactcatalog.ForgetIdentitiesTx(ctx, tx, entity); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM book_resume WHERE item_id=?`, entity); err != nil {
		return err
	}
	if show.Valid {
		// "Where was I in this show" must not point at an episode that is gone:
		// it moves to the nearest episode before it, else the nearest after.
		if _, err = tx.ExecContext(ctx, `UPDATE profile_show_activity SET last_item_id=COALESCE(
 (SELECT CAST(n.entity_id AS TEXT) FROM catalog_episodes gone JOIN catalog_episodes n INDEXED BY catalog_episodes_show ON n.show_id=gone.show_id AND n.entity_id<>gone.entity_id
   LEFT JOIN catalog_seasons gs ON gs.entity_id=gone.season_id LEFT JOIN catalog_seasons ns ON ns.entity_id=n.season_id
   WHERE gone.entity_id=?1 AND (COALESCE(ns.number,0)<COALESCE(gs.number,0) OR COALESCE(ns.number,0)=COALESCE(gs.number,0) AND n.number<gone.number)
   ORDER BY COALESCE(ns.number,0) DESC,n.number DESC LIMIT 1),
 (SELECT CAST(n.entity_id AS TEXT) FROM catalog_episodes gone JOIN catalog_episodes n INDEXED BY catalog_episodes_show ON n.show_id=gone.show_id AND n.entity_id<>gone.entity_id
   LEFT JOIN catalog_seasons ns ON ns.entity_id=n.season_id WHERE gone.entity_id=?1 ORDER BY COALESCE(ns.number,0),n.number LIMIT 1),
 last_item_id) WHERE show_id=?2 AND last_item_id=CAST(?1 AS TEXT)`, entity, show.Int64); err != nil {
			return err
		}
	}
	if err = purgeEntityTx(ctx, tx, entity); err != nil {
		return err
	}
	if season.Valid {
		if err = removeEmptyContainerTx(ctx, tx, season.Int64, `SELECT EXISTS(SELECT 1 FROM catalog_episodes WHERE season_id=?)`); err != nil {
			return err
		}
	}
	if show.Valid {
		if err = enqueueEmptyShowTx(ctx, tx, show.Int64); err != nil {
			return err
		}
	}
	if album.Valid && !entityMutationHeld(ctx, tx, album.Int64) {
		if err = retireEmptiedAlbumTx(ctx, tx, album.Int64, 0); err != nil {
			return err
		}
	}
	if book.Valid && !entityMutationHeld(ctx, tx, book.Int64) {
		if err = settleEmptiedBook(ctx, tx, book.Int64, 0); err != nil {
			return err
		}
	}
	return nil
}

func removeEmptyContainerTx(ctx context.Context, tx *sql.Tx, id int64, hasMembers string) error {
	if held, err := entityMutationHeldTx(ctx, tx, id); err != nil || held {
		return err
	}
	var members bool
	if err := tx.QueryRowContext(ctx, hasMembers, id).Scan(&members); err != nil || members {
		return err
	}
	return removeContainerTx(ctx, tx, id)
}

// removeContainerTx removes a show or season that has no members left,
// keeping what people had marked on it.
func removeContainerTx(ctx context.Context, tx *sql.Tx, id int64) error {
	if err := retainStateTx(ctx, tx, id); err != nil {
		return err
	}
	if err := compactcatalog.ForgetIdentitiesTx(ctx, tx, id); err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM container_personal_state WHERE container_id=?`,
		`DELETE FROM container_personal_undo WHERE container_id=?`,
		`DELETE FROM playback_sessions WHERE item_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	return compactcatalog.DeleteEntityTx(ctx, tx, id)
}

// MissingEmptied says what one pass of emptying removed.
type MissingEmptied struct {
	Titles   int  `json:"titles"`
	Files    int  `json:"files"`
	More     bool `json:"more"`
	Advanced bool `json:"-"`
}

const missingBatch = 24

// EmptyMissing removes, in one bounded transaction, missing files and missing
// titles that went missing at or before cutoff. library "" means every
// library. More is true when another pass has work to do. Files under a
// source that is offline, being scanned or waiting for its folder to be
// accepted are left alone: only a healthy source's absences are acted on.
func (s *Service) EmptyMissing(ctx context.Context, library string, cutoff time.Time, authorize func(*sql.Tx) error) (MissingEmptied, error) {
	var authority MissingAuthority
	if authorize != nil {
		authority = func(_ context.Context, tx *sql.Tx) (MissingScope, error) {
			return MissingScope{VisibilitySQL: "1"}, authorize(tx)
		}
	}
	return s.EmptyMissingAuthorized(ctx, library, cutoff, authority)
}

// EmptyMissingAuthorized uses the same bounded removal algorithm with live
// management and content authority resolved in each write transaction.
func (s *Service) EmptyMissingAuthorized(ctx context.Context, library string, cutoff time.Time, authority MissingAuthority) (MissingEmptied, error) {
	var out MissingEmptied
	err := dbwork.WithWriteTx(MutationWriteContext(ctx), s.db, dbwork.ClassFrom(ctx, dbwork.ClassMaintenance), func(tx *sql.Tx) error {
		out = MissingEmptied{}
		scope, err := resolveMissingScope(ctx, tx, authority)
		if err != nil {
			return err
		}
		if automatic, _ := ctx.Value(automaticMissingKey{}).(bool); automatic {
			rule, e := missingRuleTx(ctx, tx)
			if e != nil {
				return e
			}
			current, due := rule.Cutoff(time.Now())
			if !due {
				var ref compactcatalog.MutationRef
				query := `SELECT m.mutation_id,m.generation FROM catalog_entity_mutations m INDEXED BY catalog_entity_mutations_automatic_missing WHERE m.owner_kind='missing' AND m.cause_operation='automatic-missing' AND m.status='active' AND m.parent_mutation_id IS NULL`
				var args []any
				if library != "" {
					query += ` AND m.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?)`
					args = append(args, library)
				}
				e := tx.QueryRowContext(ctx, query+` ORDER BY m.library_id,m.mutation_id LIMIT 1`, args...).Scan(&ref.ID, &ref.Generation)
				if errors.Is(e, sql.ErrNoRows) {
					return nil
				}
				if e != nil {
					return e
				}
				m, e := compactcatalog.ReadEntityMutationTx(ctx, tx, ref)
				if e != nil {
					return e
				}
				step, e := staleMutationStepTx(ctx, tx, m)
				if e != nil {
					return e
				}
				out.More = true
				out.Advanced = step.Progressed
				if step.Complete {
					return acknowledgeMutationTx(ctx, tx, ref)
				}
				return nil
			}
			if current.Before(cutoff) {
				cutoff = current
			}
		}
		cause, ownerPrefix := "manual-missing", "manual:"
		if automatic, _ := ctx.Value(automaticMissingKey{}).(bool); automatic {
			cause, ownerPrefix = "automatic-missing", "automatic:"
		}
		fileLibrary, fileArgs := scope.librarySQL("s.library_id")
		titleLibrary, titleArgs := scope.librarySQL("cl.library_id")
		// Resume admitted work before looking at missing marks or deleting
		// inventory custody. Every turn re-resolves the caller's live scope.
		var ref compactcatalog.MutationRef
		var title int64
		pendingErr := tx.QueryRowContext(ctx, `SELECT m.mutation_id,m.generation,m.entity_id FROM catalog_entity_mutations m JOIN catalog_entities missing_candidate ON missing_candidate.id=m.entity_id JOIN catalog_libraries cl ON cl.id=missing_candidate.library_id WHERE m.owner_kind='missing' AND m.cause_operation=? AND m.parent_mutation_id IS NULL AND m.status='active' AND (?='' OR cl.library_id=?) AND cl.retired=0 AND `+titleLibrary+` AND (`+scope.VisibilitySQL+`) ORDER BY m.mutation_id LIMIT 1`, append(append([]any{cause, library, library}, titleArgs...), scope.VisibilityArgs...)...).Scan(&ref.ID, &ref.Generation, &title)
		if pendingErr != nil && !errors.Is(pendingErr, sql.ErrNoRows) {
			return pendingErr
		}
		if errors.Is(pendingErr, sql.ErrNoRows) {
			err = tx.QueryRowContext(ctx, `SELECT m.entity_id FROM catalog_missing_titles m JOIN catalog_libraries cl ON cl.id=m.library_id JOIN catalog_entities missing_candidate ON missing_candidate.id=m.entity_id WHERE (?='' OR cl.library_id=?) AND m.missing_since_ms<=? AND NOT EXISTS(SELECT 1 FROM catalog_entity_mutations refused WHERE refused.owner_kind='missing' AND refused.owner_id=?||pid(missing_candidate.public_id) AND refused.owner_generation=? AND refused.status='terminal' AND refused.outcome='stale') AND cl.retired=0 AND `+titleLibrary+` AND (`+scope.VisibilitySQL+`) AND NOT EXISTS(SELECT 1 FROM inventory_source_active a JOIN library_sources src ON src.id=a.source_id WHERE src.library_id=cl.library_id) AND NOT EXISTS(SELECT 1 FROM library_sources src WHERE src.library_id=cl.library_id AND src.health IN('offline','root_changed','inventory_running')) ORDER BY m.library_id,m.missing_since_ms,m.entity_id LIMIT 1`, append(append([]any{library, library, cutoff.UnixMilli(), ownerPrefix, cutoff.UnixMilli()}, titleArgs...), scope.VisibilityArgs...)...).Scan(&title)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if errors.Is(err, sql.ErrNoRows) {
				title = 0
			}
		}
		if title != 0 {
			out.More = true
			var healthy bool
			if err = tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN library_sources src ON src.library_id=cl.library_id WHERE e.id=? AND (src.health IN('offline','root_changed','inventory_running') OR EXISTS(SELECT 1 FROM inventory_source_active a WHERE a.source_id=src.id)))`, title).Scan(&healthy); err != nil {
				return err
			}
			if !healthy {
				return nil
			}
			if err = compactcatalog.SettleMissingTx(ctx, tx, title); err != nil {
				return err
			}
			var still bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_missing_titles WHERE entity_id=? AND missing_since_ms<=?)`, title, cutoff.UnixMilli()).Scan(&still); err != nil {
				return err
			}
			if !still {
				if ref.ID != "" {
					_, err = compactcatalog.FinishEntityMutationTx(ctx, tx, ref, "stale")
					out.Advanced = err == nil
				}
				return err
			}
			if ref.ID == "" {
				var public string
				if err = tx.QueryRowContext(ctx, `SELECT pid(public_id) FROM catalog_entities WHERE id=?`, title).Scan(&public); err != nil {
					return err
				}
				generation := cutoff.UnixMilli()
				if generation < 1 {
					generation = 1
				}
				if err = ackSupersededMissingTx(ctx, tx, ownerPrefix+public, generation); err != nil {
					return err
				}

				if cause == "manual-missing" {
					var old compactcatalog.MutationRef
					e := tx.QueryRowContext(ctx, `SELECT mutation_id,generation FROM catalog_entity_mutations WHERE entity_id=? AND owner_kind='missing' AND cause_operation='automatic-missing' AND status='active'`, title).Scan(&old.ID, &old.Generation)
					if e == nil {
						m, e := compactcatalog.ReadEntityMutationTx(ctx, tx, old)
						if e != nil {
							return e
						}
						step, e := staleMutationStepTx(ctx, tx, m)
						if e != nil {
							return e
						}
						out.Advanced = step.Progressed
						if step.Complete {
							return acknowledgeMutationTx(ctx, tx, old)
						}
						return nil
					}
					if !errors.Is(e, sql.ErrNoRows) {
						return e
					}
				}
				// Keep the synchronous pin-free domain path. It cannot drop
				// a release closure and needs no cross-writer custody.
				if pinErr := compactcatalog.RequireNoEntityMBPinsTx(ctx, tx, title); pinErr == nil {
					var files int
					if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id JOIN inventory_objects o ON o.asset_id=a.token WHERE l.entity_id=? AND NOT EXISTS(SELECT 1 FROM catalog_asset_links shared INDEXED BY catalog_asset_links_asset WHERE shared.asset_id=l.asset_id AND shared.entity_id<>l.entity_id)`, title).Scan(&files); err != nil {
						return err
					}
					if err = RemoveTitleTx(ctx, tx, title); err != nil {
						return err
					}
					out.Titles = 1
					out.Files = files
					out.Advanced = true
					return PruneRetainedStateTx(ctx, tx)
				} else if !errors.Is(pinErr, compactcatalog.ErrEntityPinned) {
					return pinErr
				}
				_, err = compactcatalog.BeginEntityMutationTx(ctx, tx, compactcatalog.MutationIntent{Entity: title, OwnerKind: "missing", OwnerID: ownerPrefix + public, OwnerGeneration: generation, Mode: "remove", CauseOperation: cause})
				out.Advanced = err == nil
				return err
			}
			var files int
			if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id JOIN inventory_objects o ON o.asset_id=a.token WHERE l.entity_id=? AND NOT EXISTS(SELECT 1 FROM catalog_asset_links shared INDEXED BY catalog_asset_links_asset WHERE shared.asset_id=l.asset_id AND shared.entity_id<>l.entity_id)`, title).Scan(&files); err != nil {
				return err
			}
			step, e := RemoveTitleStepTx(ctx, tx, ref)
			if e != nil {
				return e
			}
			out.Advanced = step.Progressed
			if step.Complete && step.Outcome == "stale" {
				if e = EnqueueMutationCleanupTx(ctx, tx); e != nil {
					return e
				}
			}
			if step.Complete && step.Outcome == "removed" {
				if e = acknowledgeMutationTx(ctx, tx, ref); e != nil {
					return e
				}
				out.Titles = 1
				out.Files = files
				return PruneRetainedStateTx(ctx, tx)
			}
			return nil
		}
		type gone struct{ object, token string }
		rows, err := tx.QueryContext(ctx, `SELECT o.id,o.asset_id FROM library_sources s CROSS JOIN inventory_objects o INDEXED BY inventory_objects_missing ON o.source_id=s.id
 WHERE (?='' OR s.library_id=?) AND s.health NOT IN('offline','root_changed','inventory_running') AND o.state='missing' AND o.retired=0 AND o.missing_since<>'' AND o.missing_since<=?
 AND `+fileLibrary+`
 AND NOT EXISTS(SELECT 1 FROM catalog_assets asset JOIN catalog_asset_links link ON link.asset_id=asset.id JOIN catalog_entities missing_candidate ON missing_candidate.id=link.entity_id WHERE asset.token=o.asset_id AND NOT (`+scope.VisibilitySQL+`))
 AND NOT EXISTS(SELECT 1 FROM catalog_assets held_asset JOIN catalog_asset_links held_link ON held_link.asset_id=held_asset.id JOIN catalog_entities held_entity ON held_entity.id=held_link.entity_id JOIN catalog_entity_mutations refused ON refused.owner_kind='missing' AND refused.owner_id=?||pid(held_entity.public_id) AND refused.owner_generation=? AND refused.status='terminal' AND refused.outcome='stale' WHERE held_asset.token=o.asset_id)
 AND NOT EXISTS(SELECT 1 FROM inventory_source_active a WHERE a.source_id=s.id) ORDER BY o.id LIMIT ?`, append(append(append([]any{library, library, cutoff.UTC().Format(time.RFC3339Nano)}, fileArgs...), scope.VisibilityArgs...), ownerPrefix, cutoff.UnixMilli(), missingBatch+1)...)
		if err != nil {
			return err
		}
		var files []gone
		for rows.Next() {
			var g gone
			if err = rows.Scan(&g.object, &g.token); err != nil {
				rows.Close()
				return err
			}
			files = append(files, g)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(files) > missingBatch {
			files, out.More = files[:missingBatch], true
		}
		for _, g := range files {
			ready, e := compactcatalog.PrepareInfoObjectDeleteTx(ctx, tx, g.object)
			if e != nil {
				return e
			}
			if !ready {
				out.More = true
				continue
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_analysis_queue WHERE object_id=?`, g.object); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_objects WHERE id=?`, g.object); err != nil {
				return err
			}
			var elsewhere bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_objects WHERE asset_id=? AND retired=0)`, g.token).Scan(&elsewhere); err != nil {
				return err
			}
			if !elsewhere {
				id, e := compactcatalog.AssetByTokenTx(ctx, tx, g.token)
				if e != nil {
					return e
				}
				if id != 0 {
					if e = forgetAssetTx(ctx, tx, id, g.token); e != nil {
						return e
					}
				}
			}
			out.Files++
		}

		return nil
	})
	return out, err
}

// EmptyMissingDue applies the owner's rule once: everything due is removed in
// bounded passes until nothing is left or the context ends.
func (s *Service) EmptyMissingDue(ctx context.Context) (MissingEmptied, error) {
	ctx = context.WithValue(ctx, automaticMissingKey{}, true)
	var total MissingEmptied
	cutoff, due := s.MissingRule(ctx).Cutoff(time.Now())
	if !due {
		cutoff = time.Time{}
	}
	for ctx.Err() == nil {
		step, err := s.EmptyMissing(ctx, "", cutoff, nil)
		total.Titles += step.Titles
		total.Files += step.Files
		if err != nil || !step.More {
			return total, err
		}
		if step.Titles == 0 && step.Files == 0 && !step.Advanced {
			// Only things waiting on a source remain.
			return total, nil
		}
		dbwork.Yield(ctx)
	}
	return total, ctx.Err()
}

// MissingTitle is one title whose files are all gone, for the owner's list.
type MissingTitle struct {
	ID           string `json:"id"`
	LibraryID    string `json:"libraryId"`
	Kind         string `json:"kind"`
	Title        string `json:"title"`
	ParentTitle  string `json:"parentTitle,omitempty"`
	Year         int    `json:"year,omitempty"`
	MissingSince string `json:"missingSince"`
	// RemovedAt is when the owner's rule will remove the title; empty when the
	// rule is "never".
	RemovedAt string `json:"removedAt,omitempty"`
}

// MissingTitlesPage is a page of the owner's missing-titles list.
type MissingTitlesPage struct {
	ObservedAt string         `json:"observedAt,omitempty"`
	Items      []MissingTitle `json:"items"`
	NextCursor string         `json:"nextCursor,omitempty"`
	Total      int64          `json:"total"`
	Rule       string         `json:"rule"`
	Days       int            `json:"days"`
}

// MissingTitles lists missing titles, oldest first. library "" lists every
// library. The cursor is opaque: the last row's time and id.
func (s *Service) MissingTitles(ctx context.Context, library, cursor string, limit int) (MissingTitlesPage, error) {
	return s.MissingTitlesAuthorized(ctx, library, cursor, limit, nil)
}

func (s *Service) MissingTitlesAuthorized(ctx context.Context, library, cursor string, limit int, authority MissingAuthority) (MissingTitlesPage, error) {
	var out MissingTitlesPage
	err := dbwork.WithReadSnapshot(ctx, s.db, func(ctx context.Context) error {
		scope, err := resolveMissingScope(ctx, dbwork.Snapshot(ctx), authority)
		if err != nil {
			return err
		}
		out, err = s.WithContext(ctx).missingTitles(ctx, library, cursor, limit, scope)
		return err
	})
	return out, err
}
func (s *Service) missingTitles(ctx context.Context, library, cursor string, limit int, visibility MissingScope) (MissingTitlesPage, error) {
	rule := s.MissingRule(ctx)
	out := MissingTitlesPage{Items: []MissingTitle{}, Rule: rule.Mode, Days: rule.Days, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	var afterMS, afterID int64
	if cursor != "" {
		ms, id, ok := cutCursor(cursor)
		if !ok {
			return out, ErrAdminQuery
		}
		afterMS, afterID = ms, id
	}
	libraryPredicate, libraryArgs := visibility.librarySQL("cl.library_id")
	if visibility.Libraries == nil && visibility.VisibilitySQL == "1" {
		if err := s.read().QueryRowContext(ctx, `SELECT COALESCE(sum(m.total),0) FROM catalog_libraries cl LEFT JOIN catalog_missing_title_counts m ON m.library_id=cl.id WHERE (?1='' OR cl.library_id=?1) AND cl.retired=0`, library).Scan(&out.Total); err != nil {
			return out, err
		}
	} else {
		countArgs := append(append([]any{library, library}, libraryArgs...), visibility.VisibilityArgs...)
		if err := s.read().QueryRowContext(ctx, `SELECT count(*) FROM catalog_missing_titles m JOIN catalog_libraries cl ON cl.id=m.library_id JOIN catalog_entities missing_candidate ON missing_candidate.id=m.entity_id WHERE (?='' OR cl.library_id=?) AND cl.retired=0 AND `+libraryPredicate+` AND (`+visibility.VisibilitySQL+`)`, countArgs...).Scan(&out.Total); err != nil {
			return out, err
		}
	}
	scope := "cl.retired=0 AND " + libraryPredicate + " AND (" + visibility.VisibilitySQL + ")"
	args := append(append([]any{afterMS, afterID}, libraryArgs...), visibility.VisibilityArgs...)
	if library != "" {
		scope += " AND m.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?)"
		args = append(args, library)
	}
	args = append(args, limit+1)
	rows, err := s.read().QueryContext(ctx, `SELECT m.entity_id,m.missing_since_ms,pid(missing_candidate.public_id),cl.library_id,k.name,missing_candidate.title,missing_candidate.year,
 COALESCE((SELECT sh.title FROM catalog_episodes ep JOIN catalog_entities sh ON sh.id=ep.show_id WHERE ep.entity_id=missing_candidate.id),
  (SELECT al.title FROM catalog_songs so JOIN catalog_entities al ON al.id=so.album_id WHERE so.entity_id=missing_candidate.id),
  (SELECT bk.title FROM catalog_book_files bf JOIN catalog_entities bk ON bk.id=bf.book_id WHERE bf.entity_id=missing_candidate.id),'')
 FROM catalog_missing_titles m JOIN catalog_entities missing_candidate ON missing_candidate.id=m.entity_id JOIN catalog_kinds k ON k.id=missing_candidate.kind JOIN catalog_libraries cl ON cl.id=m.library_id
 WHERE (m.missing_since_ms,m.entity_id)>(?,?) AND `+scope+`
 ORDER BY m.missing_since_ms,m.entity_id LIMIT ?`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var lastMS, lastID int64
	for rows.Next() {
		var v MissingTitle
		var id, ms int64
		if err = rows.Scan(&id, &ms, &v.ID, &v.LibraryID, &v.Kind, &v.Title, &v.Year, &v.ParentTitle); err != nil {
			return out, err
		}
		if len(out.Items) == limit {
			out.NextCursor = strconv.FormatInt(lastMS, 10) + "." + strconv.FormatInt(lastID, 10)
			break
		}
		since := time.UnixMilli(ms).UTC()
		v.MissingSince = since.Format(time.RFC3339)
		switch rule.Mode {
		case "immediately":
			v.RemovedAt = v.MissingSince
		case "after_days":
			v.RemovedAt = since.Add(time.Duration(rule.Days) * 24 * time.Hour).Format(time.RFC3339)
		}
		out.Items = append(out.Items, v)
		lastMS, lastID = ms, id
	}
	return out, rows.Err()
}

func cutCursor(cursor string) (ms, id int64, ok bool) {
	for i := 0; i < len(cursor); i++ {
		if cursor[i] == '.' {
			a, errA := strconv.ParseInt(cursor[:i], 10, 64)
			b, errB := strconv.ParseInt(cursor[i+1:], 10, 64)
			return a, b, errA == nil && errB == nil && a >= 0 && b >= 0
		}
	}
	return 0, 0, false
}

// MissingSince returns when a title went missing ("" when it is not missing).
func (s *Service) MissingSince(ctx context.Context, item string) (string, error) {
	var ms sql.NullInt64
	err := s.read().QueryRowContext(ctx, `SELECT m.missing_since_ms FROM catalog_entities e JOIN catalog_missing_titles m ON m.entity_id=e.id WHERE e.public_id=pid_blob(?)`, item).Scan(&ms)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !ms.Valid {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return time.UnixMilli(ms.Int64).UTC().Format(time.RFC3339), nil
}
