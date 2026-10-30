package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"path/filepath"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Remote absence proof for source verification.
//
// The verification walk owns the decision to mark a catalogued file missing. It
// asks this adapter one narrow question per file: does FRESH, complete,
// per-directory source evidence produced for THIS verification epoch still lack
// this exact name? The answer must come from a listing produced under the
// verification key (one key per source health epoch), never from an arbitrary
// older cached listing. A listing from before a reconnect can omit a file that
// now exists, or can be stale after the source went away and came back; reusing
// it would be a false missing mark.
//
// The existing admitted listing machinery is reused as-is: ListInventoryPage
// starts or awaits the bounded producer for (key, parent), and returns Pending
// while that producer is still working. Pending is storage.ErrBusy, which keeps
// the walk's object cursor and pending row exactly where they are. A producer
// that cannot be authorised, or that failed, is also Pending, never an absence.
//
// The producer itself is fenced by verification_listing.go: a listing under a
// verification key is authorised by catalog_source_verification and its library
// source instead of an inventory run, so a verification epoch never needs a
// synthetic job or scan. One complete parent listing is reused for every file in
// that directory: the listing is keyed by (verification key, parent), and the
// exact filename is a primary-key lookup in remote_listing_entries. A newer
// health epoch supersedes the older producer for the same parent before it
// starts, so flaps coalesce instead of piling up producers.
//
// The accepting writer fences that same key/directory listing plus the captured
// source generation, the same library source incarnation and configuration
// generation, the same root identity and a still-healthy source; nothing is
// accepted from current configuration alone.

var _ storage.RemoteInventoryAbsence = (*Service)(nil)

// absenceBlockedReported explains one outstanding blocker per source, not once
// per health epoch or once per file. It is keyed by the library source, so a
// long-lived process cannot grow it per flap; the cap is a backstop, not the
// design.
var absenceBlockedReported struct {
	sync.Mutex
	seen map[string]string
}

const absenceBlockedSources = 1024

func reportAbsenceBlocked(source, reason string) {
	absenceBlockedReported.Lock()
	if absenceBlockedReported.seen == nil {
		absenceBlockedReported.seen = map[string]string{}
	}
	if absenceBlockedReported.seen[source] == reason {
		absenceBlockedReported.Unlock()
		return
	}
	if _, seen := absenceBlockedReported.seen[source]; !seen && len(absenceBlockedReported.seen) >= absenceBlockedSources {
		absenceBlockedReported.Unlock()
		return
	}
	absenceBlockedReported.seen[source] = reason
	absenceBlockedReported.Unlock()
	log.Printf("source verification %s is pending: %s", source, reason)
}

// absenceStateError maps a recorded source state/error to the answer the walk
// should receive. Only an empty (or range-only) error code is healthy enough to
// derive absence from source listing evidence.
func absenceStateError(state, code string) error {
	switch code {
	case "":
	case "range_unsupported":
		// Byte-range support is unrelated to directory evidence.
	case "credentials_required":
		return storage.ErrRemoteCredentials
	case "offline":
		return storage.ErrRemoteOffline
	case "binary_changed":
		return storage.ErrRemoteBinary
	case "rate_limited":
		return storage.ErrBusy
	case "source_changed":
		return storage.ErrRemoteChanged
	default:
		return storage.ErrRemoteConfig
	}
	switch state {
	case "credentials_required":
		return storage.ErrRemoteCredentials
	case "binary_changed":
		return storage.ErrRemoteBinary
	case "rate_limited":
		return storage.ErrBusy
	case "offline", "removing", "removed":
		return storage.ErrRemoteOffline
	case "ready", "healthy", "degraded":
		return nil
	default:
		return storage.ErrRemoteOffline
	}
}

// captureAbsenceScan records the source generation this verification key is
// fenced to. It is CheckScan's capture at the caller's class: the verification
// walk runs as ClassMaintenance, and this capture must not raise itself above
// that or above any other lower-priority caller. The writer rechecks the exact
// captured value with ValidateScan.
func (s *Service) captureAbsenceScan(ctx context.Context, key string, b binding, generation string) error {
	var expected string
	err := s.db.QueryRowContext(ctx, `SELECT generation FROM remote_scan_generations WHERE job_id=? AND source_id=?`, key, b.ID).Scan(&expected)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err = listingExec(withVerificationListing(ctx), s.db, `INSERT OR IGNORE INTO remote_scan_generations VALUES(?,?,?)`, key, b.ID, generation); err != nil {
			return err
		}
		err = s.db.QueryRowContext(ctx, `SELECT generation FROM remote_scan_generations WHERE job_id=? AND source_id=?`, key, b.ID).Scan(&expected)
	}
	if err != nil {
		return err
	}

	if expected != generation {
		return storage.ErrRemoteChanged
	}
	return nil
}

// absenceListing is the verification-key listing already recorded for one
// parent. Its readiness is the durable "complete" marker; a listing that is not
// ready is never evidence.
type absenceListing struct {
	generation string
	ready      bool
}

func (s *Service) absenceListing(ctx context.Context, key, source, directory string) (absenceListing, bool, error) {
	var out absenceListing
	var ready int
	err := s.db.QueryRowContext(ctx, `SELECT generation,ready FROM remote_listing_sessions WHERE job_id=? AND source_id=? AND directory=?`, key, source, directory).
		Scan(&out.generation, &ready)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	out.ready = ready != 0
	return out, true, nil
}

// InventoryAbsence answers one remote absence question for the verification
// walk. key is the walk's own name (one per verification epoch).
func (s *Service) InventoryAbsence(ctx context.Context, key string, r storage.InventoryRequest) (bool, error) {
	if s == nil || s.db == nil {
		return false, storage.ErrRemoteConfig
	}
	ctx = withVerificationListing(ctx)
	scope, err := s.verificationScopeOf(ctx, s.db, key)
	if err != nil {
		return false, err
	}
	b, _, ok := s.find(r.Root)
	if !ok {
		return false, storage.ErrRemoteChanged
	}
	listing, exists, err := s.absenceListing(ctx, key, b.ID, filepath.Join(r.Root, filepath.Dir(r.RelativePath)))
	if err != nil {
		return false, err
	}
	if exists && listing.ready && listing.generation == scope.generation {
		// The complete parent listing is the fresh physical evidence for this
		// exact epoch. Root/config/health are still checked cheaply on every call.
		return s.inventoryAbsence(ctx, key, r, storage.Snapshot{Directory: true, ObjectIdentity: scope.rootIdentity}, nil)
	}
	root, err := s.InspectRoot(ctx, r.Root)
	return s.inventoryAbsence(ctx, key, r, root, err)
}

// inventoryAbsence is the testable core: root is the adapter's own InspectRoot
// answer (or its error), already captured outside any writer.
func (s *Service) inventoryAbsence(ctx context.Context, key string, r storage.InventoryRequest, root storage.Snapshot, rootErr error) (bool, error) {
	if key == "" || r.RootIdentity == "" || !filepath.IsAbs(r.Root) || !filepath.IsLocal(r.RelativePath) || r.RelativePath == "." {
		return false, storage.ErrInventoryChanged
	}
	if rootErr != nil {
		return false, mapError(rootErr)
	}
	// The namespace proof is the adapter's observed root identity, not the
	// caller's path string: a replaced configuration or another source that
	// happens to share the path must not answer for this one.
	if !root.Directory || storage.RootIdentity(root) != r.RootIdentity {
		return false, storage.ErrInventoryChanged
	}
	b, _, ok := s.find(r.Root)
	if !ok || b.Removed {
		return false, storage.ErrRemoteOffline
	}
	// The key is the walk's name for one source and one health epoch. Only that
	// exact current row, and the source facts it is fenced to, authorise provider
	// evidence. A stale epoch is not a reason to read an older listing or to
	// start another producer.
	scope, err := s.verificationScopeOf(ctx, s.db, key)
	if err != nil {
		return false, err
	}
	if scope.remoteSource != b.ID || scope.root != r.Root || scope.rootIdentity != r.RootIdentity {
		return false, storage.ErrRemoteChanged
	}
	if scope.errorCode == "" && s.sourcePacerBackoff(b.ID, scope.generation) {
		return false, storage.ErrBusy
	}
	if err = s.captureAbsenceScan(ctx, key, b, scope.generation); err != nil {
		return false, err
	}
	parent := filepath.Join(r.Root, filepath.Dir(r.RelativePath))
	file := filepath.Join(r.Root, r.RelativePath)

	listing, exists, err := s.absenceListing(ctx, key, b.ID, parent)
	if err != nil {
		return false, err
	}
	if exists && listing.generation != scope.generation {
		reportAbsenceBlocked(scope.librarySource, "the listing recorded for this verification key belongs to another source generation; fresh evidence is required")
		return false, storage.ErrBusy
	}
	if !exists {
		// A flap moves the key, not the parent. The previous epoch's producer
		// must finish, fail or be cancelled before this epoch starts its own, so
		// a flapping source never accumulates parallel producers for one folder.
		superseded, err := s.verificationSupersede(ctx, key, b.ID, parent)
		if err != nil {
			return false, err
		}
		if superseded {
			return false, storage.ErrBusy
		}

	}

	// Start or await the bounded producer for this exact verification key and
	// parent. It never materialises the whole listing: one page of one is
	// enough to know the session is complete, then the exact child is a PK
	// lookup below.
	page, err := s.ListInventoryPage(ctx, key, parent, "", 1)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrBusy), errors.Is(err, storage.ErrRemoteChanged), errors.Is(err, storage.ErrRemoteConfig), errors.Is(err, storage.ErrRemoteLimit):
			return false, err
		case ctx.Err() == nil && (errors.Is(err, context.Canceled) || errors.Is(err, storage.ErrRemoteCursor) || errors.Is(err, storage.ErrRemoteOffline)):
			reportAbsenceBlocked(scope.librarySource, "no authorized inventory producer can build fresh evidence for this verification key; keeping the walk pending")
			return false, storage.ErrBusy
		}
		return false, mapError(err)
	}
	if page.Pending {
		return false, storage.ErrBusy
	}
	if page.ID == "" || page.SourceID != b.ID || page.Directory != parent || page.Generation != scope.generation {
		return false, storage.ErrRemoteChanged
	}
	// Only a complete listing reaches the child lookup: a pending or failed
	// listing produced no evidence to be absent from.
	var present bool
	if err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM remote_listing_entries WHERE listing_id=? AND name=?)`, page.ID, file).Scan(&present); err != nil {
		return false, err
	}
	return !present, nil
}

// ValidateInventoryAbsenceTx is the same-writer fence for a remote proof. It
// runs no provider I/O and trusts no caller-supplied listing: the key must still
// name the current pending health epoch of an enabled, identity-confirmed,
// healthy library source with the same incarnation, configuration generation and
// root identity; the listing for this exact key and parent must still be the one
// recorded for the same source generation; and the exact child must still be
// absent from that listing.
func (s *Service) ValidateInventoryAbsenceTx(ctx context.Context, tx *sql.Tx, key string, r storage.InventoryRequest) error {
	if s == nil || tx == nil || key == "" || r.RootIdentity == "" || !filepath.IsAbs(r.Root) || !filepath.IsLocal(r.RelativePath) || r.RelativePath == "." {
		return storage.ErrRemoteChanged
	}
	b, _, ok := s.find(r.Root)
	if !ok || b.Removed {
		return storage.ErrRemoteConfig
	}
	scope, err := s.verificationScopeOf(ctx, tx, key)
	if err != nil {
		return err
	}
	if scope.remoteSource != b.ID || scope.root != r.Root || scope.rootIdentity != r.RootIdentity {
		return storage.ErrRemoteChanged
	}
	// The captured (key,source) generation, not the current configuration
	// alone, is the authority this writer accepts.
	if err = s.ValidateScan(ctx, tx, key, b.Root); err != nil {
		return err
	}
	parent := filepath.Join(r.Root, filepath.Dir(r.RelativePath))
	file := filepath.Join(r.Root, r.RelativePath)
	var listing, source, generation, directory string
	var ready int
	err = tx.QueryRowContext(ctx, `SELECT id,source_id,generation,directory,ready FROM remote_listing_sessions WHERE job_id=? AND directory=?`, key, parent).
		Scan(&listing, &source, &generation, &directory, &ready)
	if err != nil || listing == "" || source != b.ID || directory != parent || ready != 1 || generation != scope.generation {
		return storage.ErrRemoteChanged
	}
	var expected string
	if err = tx.QueryRowContext(ctx, `SELECT generation FROM remote_scan_generations WHERE job_id=? AND source_id=?`, key, b.ID).Scan(&expected); err != nil {
		return storage.ErrRemoteChanged
	}
	if generation != expected {
		return storage.ErrRemoteChanged
	}
	var present bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM remote_listing_entries WHERE listing_id=? AND name=?)`, listing, file).Scan(&present); err != nil {
		return err
	}
	if present {
		// The file returned after the proof. The walk must not retire it.
		return storage.ErrInventoryChanged
	}
	return nil
}
