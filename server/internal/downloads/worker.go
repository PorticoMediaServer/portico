package downloads

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/preparedmedia"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

// Housekeeping looks for claims that have run out, and pruning removes rows
// nobody will read again. Neither is urgent, so neither runs on every wake of
// a loop that any commit anywhere in the server can wake.
const (
	tidyEvery  = time.Minute
	pruneEvery = time.Hour
	// A finished claim, a spent request and a withdrawn receipt are kept this
	// long for the lists that show them, then removed.
	finishedRowsKept = 30 * 24 * time.Hour
	// A grant that has run out is kept for a while so the same address can be
	// brought back by renewal when the app that holds it comes back online.
	spentGrantsKept = 7 * 24 * time.Hour
)

// Start runs the preparation worker until Close. Everything it does is also
// reachable by calling Advance directly, which is how the tests drive it: the
// loop adds scheduling, never behaviour.
func (s *Service) Start() {
	if s.done != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel, s.done = cancel, make(chan struct{})
	shared := worker.NewSignal()
	unregister := watchDownloadWork(shared)
	supervise.Go("downloads.worker", func() {
		defer close(s.done)
		defer unregister()
		// A preparation exists because somebody asked for one, so the loop is
		// driven by that request and by relevant input changes, not by a five-second tick
		// that asked an empty queue the same question all night.
		worker.RunWith(ctx, "downloads.worker", s.wake, shared, func(ctx context.Context) time.Duration {
			// A failing pass is not fatal: the next wake retries, and every
			// preparation carries its own terminal state if it truly cannot run.
			_ = s.advance(ctx, false)
			return 0
		})
	})
}

func (s *Service) Close() error {
	if s.cancel != nil {
		s.cancel()
		<-s.done
		s.cancel, s.done = nil, nil
	}
	return nil
}

// Advance runs one bounded pass: container requests, housekeeping, then a page
// of live work. It is safe to call concurrently with itself; every write is
// fenced on what the pass read.
func (s *Service) Advance(ctx context.Context) error { return s.advance(ctx, true) }

func (s *Service) advance(ctx context.Context, tidyNow bool) error {
	s.advanceMu.Lock()
	defer s.advanceMu.Unlock()
	if e := s.advanceRequests(ctx); e != nil {
		return e
	}
	if clock := time.Now(); tidyNow || clock.Sub(s.tidied) >= tidyEvery {
		s.tidied = clock
		if e := s.cleanupSelectionActionTransfers(ctx); e != nil {
			return e
		}
		if e := s.tidy(ctx); e != nil {
			return e
		}
		if tidyNow || clock.Sub(s.pruned) >= pruneEvery {
			s.pruned = clock
			if e := s.prune(ctx); e != nil {
				return e
			}
		}
	}
	rows, e := s.db.QueryContext(ctx, `SELECT id FROM download_preparations WHERE state='running' OR (state='queued' AND (SELECT count(*) FROM download_preparations active WHERE active.profile_key=download_preparations.profile_key AND active.state='running')<?) ORDER BY updated_ms,id LIMIT 8`, MaxLivePerViewer)
	if e != nil {
		return e
	}
	ids, e := collectIDs(rows)
	if e != nil {
		return e
	}
	moved := false
	for _, id := range ids {
		progressed, err := s.step(ctx, id)
		if errors.Is(err, compactcatalog.ErrBuilding) {
			// This item's catalogue change isn't published yet: its
			// preparation waits for a later pass, and the others still run.
			continue
		}
		if err != nil {
			return err
		}
		moved = moved || progressed
	}
	// A large original is hashed one bounded window per pass. Waking the loop
	// immediately after a window that moved bytes keeps a long file running at
	// disk speed instead of one window every tick, while a pass that moved
	// nothing still waits, so watching a conversion someone else is running
	// costs one poll per tick rather than a spin.
	if moved {
		s.signal()
	}
	return nil
}

func collectIDs(rows *sql.Rows) ([]string, error) {
	out := []string{}
	for rows.Next() {
		var id string
		if e := rows.Scan(&id); e != nil {
			rows.Close()
			return out, e
		}
		out = append(out, id)
	}
	e := rows.Err()
	rows.Close()
	return out, e
}

// titleGone is true for a claim whose title has left the library. The public
// id is compared as well as the entity, so a new title that happens to be
// given a removed one's number is never mistaken for it.
const titleGone = `NOT EXISTS(SELECT 1 FROM catalog_entities e WHERE e.id=download_preparations.item_id AND e.public_id=pid_blob(download_preparations.item_public))`

// tidy ends claims the server can no longer honour. It ends the claim only: a
// copy already on a device stays playable, because its receipt answers to what
// the viewer may do, not to what the server still holds. Four things end a
// claim here:
//
//   - nobody transferred it for retentionDays;
//   - its title left the library;
//   - the converted file it pointed at was removed;
//   - the library file it pointed at was replaced.
//
// The reads come first and use no write transaction, so a pass with nothing to
// end takes no writer at all.
type tidyCandidatesObserverKey struct{}

func (s *Service) tidy(ctx context.Context) error {
	var retentionDays int
	if e := s.db.QueryRowContext(ctx, `SELECT retention_days FROM download_settings WHERE singleton=1`).Scan(&retentionDays); e != nil {
		return e
	}
	now := s.millis()
	deadline := now - int64(retentionDays)*24*60*60*1000
	type ending struct {
		predicate, state, reason string
		args                     []any
	}
	endings := []ending{
		{`state='ready' AND (CASE WHEN used_ms>0 THEN used_ms ELSE ready_ms END)<?`, StateExpired, ReasonRetention, []any{deadline}},
		{`state IN('queued','running','paused','ready','failed') AND ` + titleGone, StateUnavailable, ReasonItemDeleted, nil},
		{`state='ready' AND artifact_kind='prepared' AND NOT EXISTS(SELECT 1 FROM prepared_media_versions v WHERE v.id=download_preparations.artifact_ref AND v.digest=download_preparations.artifact_digest AND v.state='published')`, StateUnavailable, ReasonNotOptimized, nil},
		{`state='ready' AND artifact_kind='source' AND NOT EXISTS(SELECT 1 FROM catalog_asset_links link JOIN catalog_assets a ON a.id=link.asset_id WHERE link.entity_id=download_preparations.item_id AND a.token=download_preparations.artifact_ref AND a.size=download_preparations.bytes_total AND a.modified_ns=download_preparations.source_modified_ns)`, StateUnavailable, ReasonSourceChanged, nil},
	}
	type ended struct {
		id        string
		condition ending
	}
	due := []ended{}
	seen := map[string]bool{}
	for _, c := range endings {
		rows, e := s.db.QueryContext(ctx, `SELECT id FROM download_preparations WHERE `+c.predicate+` ORDER BY updated_ms LIMIT 64`, c.args...)
		if e != nil {
			return e
		}
		ids, e := collectIDs(rows)
		if e != nil {
			return e
		}
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				due = append(due, ended{id, c})
			}
		}
	}
	if observe, ok := ctx.Value(tidyCandidatesObserverKey{}).(func()); ok {
		observe()
	}
	var spent bool
	if e := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM download_grants WHERE expires_ms<? OR (revoked=1 AND expires_ms<?))`, now-int64(spentGrantsKept/time.Millisecond), now).Scan(&spent); e != nil {
		return e
	}
	if len(due) == 0 && !spent {
		return nil
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	for _, d := range due {
		// Permission to end a claim comes from this snapshot, not the earlier
		// candidate read. A grant may have refreshed it in between.
		var stillDue bool
		args := append([]any{d.id}, d.condition.args...)
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM download_preparations WHERE id=? AND (`+d.condition.predicate+`))`, args...).Scan(&stillDue); e != nil {
			return e
		}
		if !stillDue {
			continue
		}
		live := `('queued','running','paused','ready','failed')`
		if d.condition.reason != ReasonItemDeleted {
			live = `('ready')`
		}
		if e = endClaim(ctx, tx, d.id, d.condition.state, d.condition.reason, now, live); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM download_grants WHERE expires_ms<? OR (revoked=1 AND expires_ms<?)`, now-int64(spentGrantsKept/time.Millisecond), now); e != nil {
		return e
	}
	return gated.Commit()
}

// endClaim moves a claim to a state it cannot be transferred from, and
// withdraws its transfer addresses. It never touches a receipt.
func endClaim(ctx context.Context, tx *sql.Tx, id, state, reason string, now int64, from string) error {
	result, e := tx.ExecContext(ctx, `UPDATE download_preparations SET state=?,reason=?,hash_state=NULL,revision=revision+1,updated_ms=? WHERE id=? AND state IN`+from, state, reason, now, id)
	if e != nil {
		return e
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return nil
	}
	_, e = tx.ExecContext(ctx, `UPDATE download_grants SET revoked=1 WHERE preparation_id=?`, id)
	return e
}

// prune removes rows that have told their story: claims that ended a month
// ago, requests that finished, receipts nobody could still be holding, and the
// bookkeeping of titles and files that no longer exist. Each statement is
// bounded, so a server that has never been pruned catches up over several
// passes instead of holding the writer for one long one.
func (s *Service) prune(ctx context.Context) error {
	now := s.millis()
	old := now - int64(finishedRowsKept/time.Millisecond)
	statements := []struct {
		query string
		args  []any
	}{
		// Live receipts still depend on the preparation's publication facts.
		// Expiring a transfer claim must not discard those facts underneath a
		// device that is keeping its offline authorization current.
		{`DELETE FROM download_preparations WHERE id IN(SELECT id FROM download_preparations WHERE state IN('cancelled','expired','unavailable') AND updated_ms<? AND NOT EXISTS(SELECT 1 FROM download_receipts r WHERE r.preparation_id=download_preparations.id AND r.revoked_ms=0 AND r.expires_ms>?) LIMIT 256)`, []any{old, now}},
		// A finished request whose members are still being prepared is still
		// how its device finds them, however long ago it finished admitting.
		{`DELETE FROM download_requests WHERE id IN(SELECT id FROM download_requests WHERE state IN('complete','failed') AND updated_ms<? AND id NOT IN(` + activeRequestIDs + `) LIMIT 64)`, []any{old}},
		// A withdrawn receipt is kept until the copy it covered could not have
		// been played anyway, so a device that comes back is still told plainly
		// that it was withdrawn. A receipt nobody renewed for a year is gone
		// from every device that could have held it.
		{`DELETE FROM download_receipts WHERE id IN(SELECT id FROM download_receipts WHERE (revoked_ms>0 AND revoked_ms<? AND expires_ms<?) OR expires_ms<? LIMIT 256)`, []any{old, old, now - int64(365*24*time.Hour/time.Millisecond)}},
		{`DELETE FROM download_progress_marks WHERE rowid IN(SELECT m.rowid FROM download_progress_marks m WHERE NOT EXISTS(SELECT 1 FROM catalog_entities e WHERE e.id=m.item_id) LIMIT 256)`, nil},
		{`DELETE FROM download_source_digests WHERE asset_id IN(SELECT d.asset_id FROM download_source_digests d WHERE NOT EXISTS(SELECT 1 FROM catalog_assets a WHERE a.token=d.asset_id AND a.size=d.size AND a.modified_ns=d.modified_ns) LIMIT 256)`, nil},
	}
	for _, statement := range statements {
		if _, e := dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, statement.query, statement.args...); e != nil {
			return e
		}
	}
	return nil
}

// plan is what one pass decided to do outside the database transaction.
type optimization struct {
	p                               identity.Principal
	item, asset, profile, operation string
}
type plan struct {
	optimization *optimization
	hashBytes    bool
	file         pinned
	offset       int64
	state        []byte
	revision     int64
}

func (s *Service) step(ctx context.Context, id string) (bool, error) {
	p, e := s.prepare(ctx, id)
	if e == nil && p.optimization != nil {
		o := p.optimization
		if e = s.optimizer.RequestOptimization(ctx, o.p, o.item, o.asset, o.profile, o.operation); e != nil {
			if ctx.Err() != nil || dbwork.Retryable(e) {
				return false, e
			}
			return false, s.fail(ctx, id, p.revision, ReasonOptimizeFailed)
		}
		_, e = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `UPDATE download_preparation_authority SET optimization_requested=1 WHERE preparation_id=?`, id)
		return true, e
	}
	if e != nil || !p.hashBytes {
		return false, e
	}
	digestState, read, e := s.hashWindow(ctx, p)
	if e != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		// The file could not be opened as the file that was pinned: it is
		// unreachable, or it has changed. The next attempt re-reads the library
		// and pins whatever is there then.
		return false, s.fail(ctx, id, p.revision, ReasonSourceMissing)
	}
	if e = s.record(ctx, id, p, digestState, read); e != nil {
		return false, e
	}
	return read > 0 && p.offset+read < p.file.size, nil
}

// prepare decides, inside one transaction, what a preparation needs next. It
// finalizes everything it can decide from the database alone, and only hands
// back work when bytes have to be read.
func (s *Service) prepare(ctx context.Context, id string) (plan, error) {
	var out plan
	gated2, e := dbwork.Begin(ctx, s.db, dbwork.ClassForegroundTransfer)
	if e != nil {
		return out, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	now := s.millis()
	var state, quality, recipeID, item, authority, account, profile, artifactRef string
	var bytesDone, bytesTotal, revision, modifiedNS int64
	var hashState []byte
	var present bool
	e = tx.QueryRowContext(ctx, `SELECT state,quality,recipe,item_public,authority,account_id,profile_id,artifact_ref,bytes_done,bytes_total,revision,hash_state,source_modified_ns,NOT (`+titleGone+`) FROM download_preparations WHERE id=?`, id).Scan(&state, &quality, &recipeID, &item, &authority, &account, &profile, &artifactRef, &bytesDone, &bytesTotal, &revision, &hashState, &modifiedNS, &present)
	if errors.Is(e, sql.ErrNoRows) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	if state != StateQueued && state != StateRunning {
		return out, nil
	}
	if !present {
		return out, terminal(ctx, gated2, id, StateUnavailable, ReasonItemDeleted, now)
	}
	if state == StateQueued {
		var running int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM download_preparations WHERE profile_key=(SELECT profile_key FROM download_preparations WHERE id=?) AND state='running'`, id).Scan(&running); e != nil {
			return out, e
		}
		if running >= MaxLivePerViewer {
			return out, nil
		}
	}
	viewer := identity.Viewer{AccountID: account, ProfileID: profile, Authority: authority}
	allowed, e := ProfileAllowsDownloads(tx, viewer)
	if e != nil {
		return out, e
	}
	if !allowed {
		return out, terminal(ctx, gated2, id, StateUnavailable, ReasonNotAllowed, now)
	}
	var stored, device, optimizationOperation string
	var requested bool
	e = tx.QueryRowContext(ctx, `SELECT principal,device_id,optimization_requested,optimization_operation FROM download_preparation_authority WHERE preparation_id=?`, id).Scan(&stored, &device, &requested, &optimizationOperation)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	principal := identity.Principal{Viewer: viewer}
	if stored != "" {
		if e = json.Unmarshal([]byte(stored), &principal); e != nil {
			return out, e
		}
	}
	if device != "" {
		visible, args, err := s.requestAccess(ctx, tx, principal, device)
		if errors.Is(err, errRequestUnavailable) {
			return out, err
		}
		if err != nil {
			return out, terminal(ctx, gated2, id, StateUnavailable, ReasonNotAllowed, now)
		}
		var visibleNow bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities i WHERE i.public_id=pid_blob(?) AND (`+visible+`))`, append([]any{item}, args...)...).Scan(&visibleNow); e != nil {
			return out, e
		}
		if !visibleNow {
			// Facts are synchronous, so a present-but-invisible item is a
			// refusal — except while the derived data behind the visibility
			// check rebuilds, which answers retry, never the end of the
			// download.
			if e = compactcatalog.CheckReadiness(ctx, tx, compactcatalog.DomainBrowseRows, compactcatalog.DomainBrowseEdges, compactcatalog.DomainAvailability); e != nil {
				return out, e
			}
			return out, terminal(ctx, gated2, id, StateUnavailable, ReasonItemDeleted, now)
		}
	} else if err := s.visibleItem(ctx, tx, principal, item); err != nil {
		if !errors.Is(err, identity.ErrContentRestricted) {
			return out, err
		}
		return out, terminal(ctx, gated2, id, StateUnavailable, ReasonItemDeleted, now)
	}
	var version string
	if e = tx.QueryRowContext(ctx, `SELECT source_asset_id FROM download_preparations WHERE id=?`, id).Scan(&version); e != nil {
		return out, e
	}
	var found source
	var err error
	if version != "" {
		found, err = resolvePinnedDownloadSource(ctx, tx, item, version)
	} else {
		found, err = resolveSource(ctx, tx, item)
	}
	if reason := sourceReason(found, err); reason != "" {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		return out, terminal(ctx, gated2, id, StateUnavailable, reason, now)
	}
	doc, docError := publicationTx(ctx, tx, id)
	if docError != nil {
		return out, docError
	}
	var publication *reading.OfflineDocument
	if doc != nil {
		publication = doc.OfflineDocument
	}
	if err := s.checkPublicationTx(ctx, tx, principal, found, publication); err != nil {
		return out, terminal(ctx, gated2, id, StateUnavailable, ReasonSourceChanged, now)
	}
	if doc != nil {
		found.Container = doc.Format
	}
	if reason := s.conversionPolicyReason(ctx, tx, quality, recipeID, found); reason != "" {
		return out, terminal(ctx, gated2, id, StateUnavailable, reason, now)
	}
	if quality != QualityOriginal || recipeID != "" {
		if s.optimizer != nil && principal.Role == "owner" && stored != "" && !requested {
			rung, ok := ladderRung(quality)
			if !ok && recipeID == "" {
				return out, ErrInput
			}
			recipe := recipeID
			if recipe == "" {
				recipe = preparedProfile(found.Kind, rung)
			}
			if !preparedmedia.Producible(recipe) {
				return out, s.advanceOptimized(ctx, gated2, id, quality, recipeID, found, now)
			}
			if _, e = tx.ExecContext(ctx, `UPDATE download_preparations SET state='running',revision=revision+(state<>'running'),updated_ms=? WHERE id=?`, now, id); e != nil {
				return out, e
			}
			if state != StateRunning {
				revision++
			}
			if optimizationOperation == "" {
				optimizationOperation = identity.Token()
				if _, e = tx.ExecContext(ctx, `UPDATE download_preparation_authority SET optimization_operation=? WHERE preparation_id=?`, optimizationOperation, id); e != nil {
					return out, e
				}
			}
			out.optimization = &optimization{principal, item, found.Asset, recipe, optimizationOperation}
			out.revision = revision
			return out, gated2.Commit()
		}
		return out, s.advanceOptimized(ctx, gated2, id, quality, recipeID, found, now)
	}
	file := pinned{asset: found.Asset, path: found.Path, size: found.Size, modifiedNS: found.ModifiedNS}
	// Somebody has already read this exact file: the claim is ready at once.
	if digest, known, err := knownDigest(ctx, tx, file); err != nil {
		return out, err
	} else if known {
		if _, e = tx.ExecContext(ctx, `UPDATE download_preparations SET state='ready',reason='',artifact_kind='source',artifact_ref=?,artifact_digest=?,artifact_container=?,source_modified_ns=?,bytes_total=?,bytes_done=?,estimated=0,hash_state=NULL,started_ms=CASE WHEN started_ms=0 THEN ? ELSE started_ms END,ready_ms=?,revision=revision+1,updated_ms=? WHERE id=?`,
			file.asset, digest, found.Container, file.modifiedNS, file.size, file.size, now, now, now, id); e != nil {
			return out, e
		}
		if e = s.settle(ctx, tx, id, now); e != nil {
			return out, e
		}
		return out, gated2.Commit()
	}
	// Pin the file as the library knows it now. A claim that was part-way
	// through a file which has since changed starts again on the new one: bytes
	// of two different files are never folded into one digest.
	if artifactRef != file.asset || bytesTotal != file.size || modifiedNS != file.modifiedNS {
		bytesDone, hashState = 0, nil
		if _, e = tx.ExecContext(ctx, `UPDATE download_preparations SET artifact_kind='source',artifact_ref=?,artifact_digest='',artifact_container=?,source_modified_ns=?,bytes_total=?,bytes_done=0,estimated=0,hash_state=NULL,state='running',started_ms=?,revision=revision+(state<>'running'),updated_ms=? WHERE id=?`, file.asset, found.Container, file.modifiedNS, file.size, now, now, id); e != nil {
			return out, e
		}
		if state != StateRunning {
			revision++
		}
	} else if state == StateQueued {
		if _, e = tx.ExecContext(ctx, `UPDATE download_preparations SET state='running',started_ms=CASE WHEN started_ms=0 THEN ? ELSE started_ms END,revision=revision+1,updated_ms=? WHERE id=?`, now, now, id); e != nil {
			return out, e
		}
		revision++
	}
	out = plan{hashBytes: true, file: file, offset: bytesDone, state: hashState, revision: revision}
	return out, gated2.Commit()
}

// settle finishes a claim that has just become ready: its listing facts are
// refreshed from the library, and the notifications seam is told, both inside
// the transaction that made it ready.
func (s *Service) settle(ctx context.Context, tx *sql.Tx, id string, now int64) error {
	if e := refreshPinnedDownloadTitle(ctx, tx, id); e != nil {
		return e
	}
	return s.announce(ctx, tx, id, now, true, "")
}

// advanceOptimized resolves a ladder rung against prepared media. The bytes are
// produced by internal/preparedmedia; downloads only observes them.
func (s *Service) advanceOptimized(ctx context.Context, gatedArg *dbwork.Write, id, quality, recipe string, found source, now int64) error {
	tx := gatedArg.Tx()
	rung, ok := ladderRung(quality)
	if !ok && recipe == "" {
		return terminal(ctx, gatedArg, id, StateFailed, ReasonFailed, now)
	}
	// The recipe chosen for the device when the download was asked for.
	profileID := recipe
	if profileID == "" {
		profileID = preparedProfile(found.Kind, rung)
	}
	versions, e := selectionDownloadVersions(ctx, tx, found, profileID)
	if e != nil {
		return e
	}
	for _, v := range versions {
		if v.SourceID != found.Asset || v.ProfileID != profileID || !v.Selectable || !validDigest.MatchString(v.Digest) {
			continue
		}
		if _, e = tx.ExecContext(ctx, `UPDATE download_preparations SET state='ready',reason='',artifact_kind='prepared',artifact_ref=?,artifact_digest=?,artifact_container=?,source_modified_ns=0,bytes_total=?,bytes_done=?,estimated=0,ready_ms=?,revision=revision+1,updated_ms=? WHERE id=?`,
			v.ID, v.Digest, v.Facts.Container, v.Size, v.Size, now, now, id); e != nil {
			return e
		}
		if e = s.settle(ctx, tx, id, now); e != nil {
			return e
		}
		return gatedArg.Commit()
	}
	// No published version yet. An optimization already in flight is progress;
	// a failed one is a failure; nothing at all means no owner has prepared this
	// rung, and a viewer cannot order a conversion for themselves.
	var state string
	var bytes int64
	itemEntity, e := entityid.Resolve(ctx, tx, found.Item)
	if e != nil {
		return terminal(ctx, gatedArg, id, StateUnavailable, ReasonItemDeleted, now)
	}
	e = tx.QueryRowContext(ctx, `SELECT state,bytes FROM prepared_media_jobs WHERE item_id=? AND profile_id=? AND asset_id=? ORDER BY created_ms DESC LIMIT 1`, itemEntity, profileID, found.Asset).Scan(&state, &bytes)
	if errors.Is(e, sql.ErrNoRows) {
		return terminal(ctx, gatedArg, id, StateUnavailable, ReasonNotOptimized, now)
	}
	if e != nil {
		return e
	}
	switch state {
	case "queued", "running", "cancelling":
		// Watching somebody else's conversion is progress, not a change of
		// state: the claim's revision moves only when the claim itself does, so
		// a Pause or Cancel made against the row a person is looking at is not
		// refused because a byte count ticked.
		if _, e = tx.ExecContext(ctx, `UPDATE download_preparations SET state='running',reason='',bytes_done=?,started_ms=CASE WHEN started_ms=0 THEN ? ELSE started_ms END,revision=revision+(state<>'running'),updated_ms=? WHERE id=?`, bytes, now, now, id); e != nil {
			return e
		}
		return gatedArg.Commit()
	case "failed":
		return terminal(ctx, gatedArg, id, StateFailed, ReasonOptimizeFailed, now)
	default:
		return terminal(ctx, gatedArg, id, StateUnavailable, ReasonNotOptimized, now)
	}
}

// terminal writes a final state and commits. A claim that can no longer be
// transferred loses its transfer addresses; the receipts of copies already on
// devices are left alone.
func terminal(ctx context.Context, gatedArg *dbwork.Write, id, state, reason string, now int64) error {
	tx := gatedArg.Tx()
	if state != StateReady {
		if _, e := tx.ExecContext(ctx, `UPDATE download_grants SET revoked=1 WHERE preparation_id=?`, id); e != nil {
			return e
		}
	}
	if _, e := tx.ExecContext(ctx, `UPDATE download_preparations SET state=?,reason=?,hash_state=NULL,revision=revision+1,updated_ms=? WHERE id=?`, state, reason, now, id); e != nil {
		return e
	}
	return gatedArg.Commit()
}

// announce calls the notifications seam inside the same transaction that made
// the state true, so a notification can never describe a state that rolled back.
func (s *Service) announce(ctx context.Context, tx *sql.Tx, id string, now int64, ready bool, reason string) error {
	var authority, account, profile, item string
	if e := tx.QueryRowContext(ctx, `SELECT authority,account_id,profile_id,item_public FROM download_preparations WHERE id=?`, id).Scan(&authority, &account, &profile, &item); e != nil {
		return e
	}
	viewer := identity.Viewer{AccountID: account, ProfileID: profile, Authority: authority}
	if ready {
		return s.notifier.DownloadReady(tx, now, viewer, id, item)
	}
	return s.notifier.DownloadFailed(tx, now, viewer, id, item, reason)
}

// hashWindow reads one bounded window of the library file and folds it into
// the running SHA-256. The hash state is checkpointed by the caller, so a
// pause, a restart or a crash costs one window rather than the whole file. The
// file is opened afresh for each window, and each open refuses a file whose
// size or modified time is not the pinned one.
func (s *Service) hashWindow(ctx context.Context, p plan) ([]byte, int64, error) {
	digest := sha256.New()
	if len(p.state) > 0 {
		if e := digest.(encoding.BinaryUnmarshaler).UnmarshalBinary(p.state); e != nil {
			return nil, 0, e
		}
	}
	f, e := s.openSource(ctx, p.file)
	if e != nil {
		return nil, 0, e
	}
	defer f.Close()
	if p.offset > 0 {
		if _, e = f.Seek(p.offset, io.SeekStart); e != nil {
			return nil, 0, e
		}
	}
	remaining := p.file.size - p.offset
	if remaining > hashChunk {
		remaining = hashChunk
	}
	if remaining < 0 {
		return nil, 0, ErrInput
	}
	read, e := io.CopyN(digest, f, remaining)
	if e != nil {
		return nil, read, e
	}
	state, e := digest.(encoding.BinaryMarshaler).MarshalBinary()
	return state, read, e
}

// record folds one window's work back into the row, refusing to write if the
// viewer paused, cancelled or retried while the bytes were being read, or if
// the claim was pinned to a different file meanwhile. Progress is not a change
// of state, so it leaves the revision a person's action is fenced on alone.
func (s *Service) record(ctx context.Context, id string, p plan, state []byte, read int64) error {
	gated3, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	now := s.millis()
	var revision, total, done, modifiedNS int64
	var current, asset string
	if e = tx.QueryRowContext(ctx, `SELECT revision,bytes_total,bytes_done,state,artifact_ref,source_modified_ns FROM download_preparations WHERE id=?`, id).Scan(&revision, &total, &done, &current, &asset, &modifiedNS); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		return e
	}
	if revision != p.revision || current != StateRunning || done != p.offset || asset != p.file.asset || total != p.file.size || modifiedNS != p.file.modifiedNS {
		return nil
	}
	done = p.offset + read
	if done < total {
		if _, e = tx.ExecContext(ctx, `UPDATE download_preparations SET bytes_done=?,hash_state=?,updated_ms=? WHERE id=?`, done, state, now, id); e != nil {
			return e
		}
		return gated3.Commit()
	}
	digest, e := finishHash(state)
	if e != nil {
		return e
	}
	if e = rememberDigest(ctx, tx, p.file, digest, now); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE download_preparations SET state='ready',reason='',artifact_digest=?,bytes_done=?,hash_state=NULL,ready_ms=?,revision=revision+1,updated_ms=? WHERE id=?`, digest, done, now, now, id); e != nil {
		return e
	}
	if e = s.settle(ctx, tx, id, now); e != nil {
		return e
	}
	return gated3.Commit()
}

// fail records a terminal failure discovered outside a transaction.
func (s *Service) fail(ctx context.Context, id string, revision int64, reason string) error {
	gated4, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	now := s.millis()
	var current int64
	var state string
	if e = tx.QueryRowContext(ctx, `SELECT revision,state FROM download_preparations WHERE id=?`, id).Scan(&current, &state); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		return e
	}
	if current != revision || state != StateRunning {
		return nil
	}
	if e = s.announce(ctx, tx, id, now, false, reason); e != nil {
		return e
	}
	return terminal(ctx, gated4, id, StateFailed, reason, now)
}

// directSource is the reader used when no isolated media process is configured
// (tests, and a composition with no library helper). The path comes from the
// server's own asset table, never from a request, it is opened read-only, and
// the opened file must be the size and age the caller pinned.
func directSource(_ context.Context, name string, size, modifiedNS int64) (io.ReadSeekCloser, error) {
	f, e := os.Open(name)
	if e != nil {
		return nil, e
	}
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() != size || (modifiedNS != 0 && info.ModTime().UnixNano() != modifiedNS) {
		f.Close()
		return nil, errSourceChanged
	}
	return f, nil
}

var errSourceChanged = errors.New("the library file is not the one that was pinned")

// finishHash reads back the checkpointed state and renders the digest in the
// lowercase hex the rest of the server publishes for content digests.
func finishHash(state []byte) (string, error) {
	digest := sha256.New()
	if e := digest.(encoding.BinaryUnmarshaler).UnmarshalBinary(state); e != nil {
		return "", e
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// watchDownloadWork follows this runtime's durable inputs; clock-only expiry
// still runs through worker.SafetyTick. Unrelated telemetry is not work.
func watchDownloadWork(signal *worker.Signal) func() {
	return dbwork.WakeOnTables(signal,
		"download_*", "prepared_media_*", "catalog_asset_links", "catalog_assets",
		"catalog_entities", "catalog_item_availability", "catalog_libraries", "catalog_episodes",
		"catalog_seasons", "catalog_albums", "catalog_songs", "catalog_books",
		"catalog_book_chapters", "catalog_book_files", "catalog_playlists", "catalog_playlist_entries",
		"asset_stream*", "episode_asset_boundaries", "music_virtual_tracks", "music_technical_facts",
		"inventory_objects", "library_sources", "library_revisions", "library_network_policy",
		"source_*", "remote_source_*", "remote_sources", "remote_scan_observations", "playback_owner_policy",
		"server_settings", "console_settings_revision", "authorization_*", "accounts", "direct_profile_trust", "restrictions", "hosted_*", "identity_devices",
		"direct_profiles", "direct_memberships", "profile_*", "playlist_shares",
		"container_personal_state", "personal_explicit_events", "viewer_revisions", "dvr_catalog_retirements",
	)
}
