package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Verification producer scope.
//
// A source-verification walk is not a scan job: it is one health epoch of one
// library source, recorded in catalog_source_verification. It still needs the
// same admitted, complete, per-directory provider listing an inventory run
// gets, but it has no job row and no inventory_runs row to fence that listing
// against. This file is that fence. A listing whose job_id is a verification
// key is authorised by the verification row and its library source alone:
//
//   - the key is exactly "source-verify:<library source>:<health epoch>";
//   - that row is pending, holds that epoch, and agrees with the library
//     source's incarnation and configuration generation;
//   - the library source is enabled, identity confirmed, healthy, remote, and
//     attached to the adapter source the listing names;
//   - the adapter generation the listing was produced for is still current,
//     the source is not removed, and the directory is under the source root;
//   - the catalogued root identity is exactly the one this source, root and
//     generation produce.
//
// Every check runs in the caller's transaction, so the producer's own writer and
// the accepting writer fence the same facts. A health epoch or configuration
// change moves the key or the row, and the producer is cancelled: it can never
// publish evidence for an epoch that no longer exists. No job, inventory_run or
// schema row is created for a verification listing.
//
// Producer seam (owned by inventory_native.go and serve_listing.go): before
// resolving an inventory run for a listing, the producer asks
// verificationNativeListingFence (watchdog and adoption) or
// verificationServeListingFenceTx (batch and readiness writers). handled is
// false for every scan listing, and the existing inventory_run fence then runs
// unchanged.

const (
	verificationKeyPrefix = "source-verify:"
	// verificationReapBatch bounds how many entries one cleanup transaction
	// removes from a superseded verification listing, and how many superseded
	// listings one pass looks at.
	verificationReapBatch = 96
	verificationReapPage  = 8
)

// verificationQuerier is the narrow read shape shared by *sql.DB and *sql.Tx.
type verificationQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// verificationScope is the exact source and health epoch a verification listing
// is authorised by. It is rebuilt from durable rows for every proof: a caller's
// key is never taken as authority on its own.
type verificationScope struct {
	key                      string
	librarySource, library   string
	remoteSource             string
	root, rootIdentity       string
	incarnation              string
	configuration            int64
	generation               string
	state, errorCode, health string
	epoch                    int64
	follow                   bool
	directory                string
	ready                    bool
}

func verificationKey(source string, epoch int64) string {
	return verificationKeyPrefix + source + ":" + strconv.FormatInt(epoch, 10)
}

// parseVerificationKey accepts only the canonical form the verifier writes:
// an opaque source id and one positive decimal epoch.
func parseVerificationKey(key string) (source string, epoch int64, ok bool) {
	rest, found := strings.CutPrefix(key, verificationKeyPrefix)
	if !found || rest == "" {
		return "", 0, false
	}
	at := strings.LastIndexByte(rest, ':')
	if at < 0 || at == len(rest)-1 {
		return "", 0, false
	}
	source = rest[:at]
	value := rest[at+1:]
	epoch, err := strconv.ParseInt(value, 10, 64)
	if err != nil || epoch < 1 || strconv.FormatInt(epoch, 10) != value {
		return "", 0, false
	}
	return source, epoch, true
}

func verificationHealthy(health string) bool {
	return health == "ready" || health == "healthy" || health == "degraded"
}

// verificationScopeOf rebuilds the scope a key names. A key that is not the
// current pending epoch of an enabled, healthy, identity-confirmed remote source
// is refused; the caller never falls back to configuration alone.
func (s *Service) verificationScopeOf(ctx context.Context, q verificationQuerier, key string) (verificationScope, error) {
	scope := verificationScope{key: key}
	source, epoch, ok := parseVerificationKey(key)
	if !ok {
		return scope, storage.ErrRemoteChanged
	}
	var pending, enabled, confirmed, follow, removed int
	var kind, health, remoteRoot, capturedIncarnation string
	var capturedConfiguration, libraryGeneration int64
	err := q.QueryRowContext(ctx, `SELECT v.health_epoch,v.pending,v.incarnation,v.configuration_generation,
 s.library_id,s.kind,s.enabled,s.identity_confirmed,s.health,s.root,s.root_identity,s.incarnation,s.generation,
 COALESCE(s.remote_source_id,''),s.follow_symlinks,
 r.root,r.removed,
 CASE WHEN r.kind IN('rclone','sftp','smb') THEN CAST(m.generation AS TEXT) ELSE CAST(r.generation AS TEXT) END,
 r.state,r.error_code
 FROM catalog_source_verification v
 JOIN library_sources s ON s.id=v.source_id
 JOIN remote_sources r ON r.id=COALESCE(s.remote_source_id,'')
 LEFT JOIN mount_backend_configs m ON m.mount_id=r.mount_id
 WHERE v.source_id=? AND (r.kind NOT IN('rclone','sftp','smb') OR EXISTS(SELECT 1 FROM mount_controls k WHERE k.mount_id=r.mount_id AND k.removal_pending=0))`, source).Scan(&scope.epoch, &pending, &capturedIncarnation, &capturedConfiguration,
		&scope.library, &kind, &enabled, &confirmed, &health, &scope.root, &scope.rootIdentity, &scope.incarnation, &libraryGeneration,
		&scope.remoteSource, &follow,
		&remoteRoot, &removed, &scope.generation, &scope.state, &scope.errorCode)
	if errors.Is(err, sql.ErrNoRows) {
		return scope, storage.ErrRemoteChanged
	}
	if err != nil {
		return scope, err
	}
	scope.librarySource, scope.follow = source, follow != 0
	scope.health = health
	if epoch != scope.epoch || pending != 1 || enabled != 1 || confirmed != 1 || removed != 0 || kind != "remote" {
		return scope, storage.ErrRemoteChanged
	}
	if scope.incarnation != capturedIncarnation || libraryGeneration != capturedConfiguration {
		// A configuration or incarnation change is a different namespace, even
		// when the epoch string still looks current.
		return scope, storage.ErrRemoteChanged
	}
	if scope.remoteSource == "" || scope.generation == "" || !filepath.IsAbs(scope.root) {
		return scope, storage.ErrRemoteChanged
	}
	if relative, err := filepath.Rel(remoteRoot, scope.root); err != nil || !filepath.IsLocal(relative) {
		return scope, storage.ErrRemoteChanged
	}
	if !verificationHealthy(health) {
		return scope, storage.ErrRemoteChanged
	}
	// The library source's health says "reachable", but a recorded provider
	// failure still means its listing evidence carries no authority.
	if err = absenceStateError(scope.state, scope.errorCode); err != nil {
		return scope, err
	}
	expected := storage.RemoteDirectoryIdentity(scope.remoteSource, scope.root) + ":generation:" + scope.generation
	if scope.rootIdentity != expected {
		return scope, storage.ErrRemoteChanged
	}
	// Scans have priority over the detached verification producer as well as
	// over its accepting walk. A completed listing remains retained evidence.
	var scanning bool
	if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_source_active WHERE source_id=?1)
 OR EXISTS(SELECT 1 FROM jobs j INDEXED BY jobs_status_directory WHERE j.status IN('queued','running','paused') AND EXISTS(SELECT 1 FROM inventory_runs r WHERE r.job_id=j.id))`, source).Scan(&scanning); err != nil {
		return scope, err
	}
	if scanning {
		return scope, context.Canceled
	}
	// A verification key must never be re-pointed at a scan job: that path would
	// resolve the listing to an inventory run the verification walk never had.
	var mapped bool
	if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM remote_inventory_checks WHERE check_job=?)`, key).Scan(&mapped); err != nil {
		return scope, err
	}
	if mapped {
		return scope, storage.ErrRemoteChanged
	}
	return scope, nil
}

// verificationListingScope resolves one listing only when it belongs to a
// verification key. handled is false for every scan listing, so the existing
// inventory-run fence stays in charge of those.
func (s *Service) verificationListingScope(ctx context.Context, q verificationQuerier, id string) (verificationScope, bool, error) {
	var key, source, directory, generation string
	var ready int
	err := q.QueryRowContext(ctx, `SELECT job_id,source_id,directory,generation,ready FROM remote_listing_sessions WHERE id=?`, id).
		Scan(&key, &source, &directory, &generation, &ready)
	if errors.Is(err, sql.ErrNoRows) {
		return verificationScope{}, false, nil
	}
	if err != nil {
		return verificationScope{}, false, err
	}
	if !strings.HasPrefix(key, verificationKeyPrefix) {
		return verificationScope{}, false, nil
	}
	scope, err := s.verificationScopeOf(ctx, q, key)
	if err != nil {
		return scope, true, err
	}
	if scope.remoteSource != source || scope.generation != generation {
		return scope, true, storage.ErrRemoteChanged
	}
	relative, err := filepath.Rel(scope.root, directory)
	if err != nil || !filepath.IsLocal(relative) {
		return scope, true, storage.ErrRemoteChanged
	}
	scope.directory, scope.ready = directory, ready != 0
	return scope, true, nil
}

// verificationNativeListingFence is the verification half of nativeListingFence.
// It returns handled=false for a scan listing. A verification producer is
// cancelled (context.Canceled) when its listing is no longer unpublished or its
// ownership moved, and rejected (ErrRemoteChanged/ErrRemoteOffline) when the
// source, epoch, configuration or generation drifted.
func (s *Service) verificationNativeListingFence(ctx context.Context, q verificationQuerier, id, owner string) (bool, error) {
	scope, handled, err := s.verificationListingScope(ctx, q, id)
	if !handled || err != nil {
		return handled, err
	}
	var actual, state string
	err = q.QueryRowContext(ctx, `SELECT owner,state FROM remote_native_listings WHERE listing_id=?`, id).Scan(&actual, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return true, context.Canceled
	}
	if err != nil {
		return true, err
	}
	if state != "running" || scope.ready {
		return true, context.Canceled
	}
	if actual == "" || actual != owner {
		return true, storage.ErrRemoteChanged
	}
	return true, nil
}

// verificationServeListingFenceTx is the verification half of
// serveListingFenceTx: the capture the listing batch and readiness writers fence
// on, resolved from the verification row instead of an inventory run. handled is
// false for a scan listing, and the caller's existing fence then runs unchanged.
func (s *Service) verificationServeListingFenceTx(ctx context.Context, tx *sql.Tx, b binding, relative, id, generation string) (serveListingCapture, bool, error) {
	var capture serveListingCapture
	scope, handled, err := s.verificationListingScope(ctx, tx, id)
	if !handled || err != nil {
		return capture, handled, err
	}
	directory := filepath.Join(b.Root, filepath.FromSlash(relative))
	rel, relErr := filepath.Rel(b.Root, scope.root)
	if relErr != nil || !filepath.IsLocal(rel) {
		return capture, true, storage.ErrRemoteChanged
	}
	if scope.ready || scope.remoteSource != b.ID || scope.generation != generation || scope.directory != directory {
		return capture, true, storage.ErrRemoteChanged
	}
	actual, _, ok := s.find(directory)
	if !ok || actual.ID != b.ID || actual.Removed {
		return capture, true, storage.ErrRemoteChanged
	}
	var state string
	err = tx.QueryRowContext(ctx, `SELECT owner,state FROM remote_native_listings WHERE listing_id=?`, id).Scan(&capture.owner, &state)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return capture, true, err
	}
	if err == nil && (state != "running" || capture.owner == "") {
		return capture, true, storage.ErrRemoteCursor
	}
	capture.job = scope.key
	capture.source = catalog.LibrarySource{
		ID: scope.librarySource, LibraryID: scope.library, Kind: "remote",
		Path: scope.root, ResolvedPath: scope.root, Classification: "network",
		Generation: scope.configuration, Health: scope.health, Enabled: true,
		FollowSymlinks: scope.follow, RootIdentity: scope.rootIdentity,
		Incarnation: scope.incarnation, IdentityConfirmed: true,
	}
	capture.profile, err = SourceReadProfileTx(ctx, tx, b.ID)
	if err != nil {
		return capture, true, err
	}
	return capture, true, nil
}

// verificationListingLive reports whether this process still owns a running
// producer for one listing. The map is keyed by listing id and retired by the
// producer itself, so it cannot grow past the listings actually being built.
func (s *Service) verificationListingLive(id string) bool {
	s.listings.Lock()
	defer s.listings.Unlock()
	return s.nativeListings[id] != nil
}

// verificationSupersede coalesces flaps: before a newer health epoch starts a
// producer for one parent, every superseded verification listing for that source
// and parent must be finished, failed or cancelled. A live producer is awaited,
// a dead incomplete one is retired in bounded chunks, and a completed one is
// left to the ordinary listing retention. It reports whether the caller must
// stay pending for now.
func (s *Service) verificationSupersede(ctx context.Context, current, source, directory string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,job_id,ready FROM remote_listing_sessions INDEXED BY remote_listing_directory_history
 WHERE source_id=? AND directory=? AND ready=0
 ORDER BY created_at DESC,id DESC LIMIT ?`, source, directory, verificationReapPage)
	if err != nil {
		return false, err
	}
	type superseded struct {
		id, key string
		ready   bool
	}
	page := make([]superseded, 0, verificationReapPage)
	for rows.Next() {
		var row superseded
		var ready int
		if err = rows.Scan(&row.id, &row.key, &ready); err != nil {
			rows.Close()
			return false, err
		}
		row.ready = ready != 0
		page = append(page, row)
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return false, readErr
	}
	for _, old := range page {
		if old.key == current {
			continue
		}
		if !strings.HasPrefix(old.key, verificationKeyPrefix) {
			if s.verificationListingLive(old.id) {
				return true, nil
			}
			continue
		}
		if s.verificationListingLive(old.id) {
			return true, nil
		}
		more, err := s.verificationRetire(ctx, old.key, source, directory, old.id)
		if err != nil {
			return false, err
		}
		if more {
			return true, nil
		}
	}
	return false, nil
}

// verificationRetire removes one superseded, unpublished verification listing:
// its staged entries first, in bounded batches, then its own rows. Only the
// caller's class is used, so a maintenance walk never raises itself into
// background media to clean up after itself.
func (s *Service) verificationRetire(ctx context.Context, key, source, directory, id string) (bool, error) {
	ctx = withVerificationListing(ctx)
	began := time.Now()
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassMaintenance))
	if err != nil {
		return false, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if _, err = tx.ExecContext(ctx, `DELETE FROM remote_listing_entries WHERE rowid IN(
 SELECT rowid FROM remote_listing_entries WHERE listing_id=? LIMIT ?)`, id, verificationReapBatch); err != nil {
		return false, err
	}
	var remains bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM remote_listing_entries WHERE listing_id=?)`, id).Scan(&remains); err != nil {
		return false, err
	}
	if remains {
		return true, verificationListingCommit(ctx, gated, began)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`DELETE FROM remote_inventory_checks WHERE job_id=? AND directory=? AND listing_id=?`, []any{key, directory, id}},
		{`DELETE FROM remote_scan_cursors WHERE job_id=? AND path=? AND listing_id=?`, []any{key, directory, id}},
		{`DELETE FROM remote_listing_sessions WHERE id=? AND job_id=? AND ready=0`, []any{id, key}},
		{`DELETE FROM remote_scan_generations WHERE job_id=? AND source_id=?`, []any{key, source}},
	} {
		if _, err = tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return false, err
		}
	}
	return false, verificationListingCommit(ctx, gated, began)
}
