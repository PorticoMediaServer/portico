package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type nativeListing struct {
	done    chan struct{}
	retired chan struct{}
	cancel  context.CancelFunc
	err     error // published by closing done
}

// A detached listing is owned by the running inventory, not its HTTP/request
// context. Every batch and readiness publication revalidates the actual scan
// incarnation, credentials and process ownership in the writing transaction.
// A verification listing has no scan: it is fenced against its library source
// and health epoch instead, and handled=true means that fence has answered.
func (s *Service) nativeListingFence(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id, owner string) error {
	if handled, err := s.verificationNativeListingFence(ctx, db, id, owner); handled {
		return err
	}
	var actualOwner, status, directory, root, generation, current string
	var incarnationOK, removed bool
	err := db.QueryRowContext(ctx, `SELECT n.owner,j.status,l.directory,ls.root,l.generation,CASE WHEN rs.kind IN('rclone','sftp','smb') THEN CAST(m.generation AS TEXT) ELSE CAST(rs.generation AS TEXT) END,
 (r.source_generation=ls.generation AND r.root_incarnation=ls.incarnation AND r.root_identity=ls.root_identity AND ls.enabled=1 AND ls.identity_confirmed=1 AND r.policy_revision=p.revision),rs.removed
 FROM remote_listing_sessions l JOIN remote_native_listings n ON n.listing_id=l.id AND n.state='running'
 JOIN remote_sources rs ON rs.id=l.source_id LEFT JOIN mount_backend_configs m ON m.mount_id=rs.mount_id
 JOIN jobs j ON j.id=COALESCE((SELECT c.job_id FROM remote_inventory_checks c WHERE c.check_job=l.job_id),l.job_id)
 JOIN inventory_runs r ON r.job_id=j.id JOIN library_sources ls ON ls.id=r.source_id JOIN library_scan_policies p ON p.library_id=ls.library_id
 WHERE l.id=? AND l.ready=0`, id).Scan(&actualOwner, &status, &directory, &root, &generation, &current, &incarnationOK, &removed)
	if errors.Is(err, sql.ErrNoRows) {
		return context.Canceled
	}
	if err != nil {
		return err
	}
	if status != "running" {
		return context.Canceled
	}
	if actualOwner != owner || !incarnationOK || removed || generation != current {
		return storage.ErrRemoteChanged
	}
	relative, err := filepath.Rel(root, directory)
	if err != nil || !filepath.IsLocal(relative) {
		return storage.ErrRemoteChanged
	}
	return nil
}
func nativeListingError(code string) error {
	switch code {
	case "cancelled":
		return context.Canceled
	case "source_changed":
		return storage.ErrRemoteChanged
	case "budget_exceeded":
		return storage.ErrRemoteLimit
	case "binary_changed":
		return storage.ErrRemoteBinary
	case "invalid_configuration":
		return storage.ErrRemoteConfig
	case "capacity_busy":
		return storage.ErrBusy
	default:
		return storage.ErrRemoteOffline
	}
}
func (s *Service) startNativeListing(ctx context.Context, b binding, relative, id, generation string) error {
	// The producer outlives this call, so its writers and cleanup carry the
	// caller's class instead of raising themselves to background media. A
	// verification walk is maintenance; an inventory run is background media.
	class := dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia)
	s.listings.Lock()
	defer s.listings.Unlock()
	if s.nativeClosed {
		return context.Canceled
	}
	s.serveMu.Lock()
	runtime := s.serveLife
	s.serveMu.Unlock()
	if runtime == nil || runtime.Err() != nil {
		return storage.ErrRemoteOffline
	}
	if existing := s.nativeListings[id]; existing != nil {
		select {
		case <-existing.done:
			return existing.err
		default:
			return nil
		}
	}
	var state, code string
	err := s.db.QueryRowContext(ctx, `SELECT state,error_code FROM remote_native_listings WHERE listing_id=?`, id).Scan(&state, &code)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if state == "failed" && code != "cancelled" && code != "capacity_busy" {
		return nativeListingError(code)
	}
	if state == "complete" {
		return nil
	}
	select {
	case s.inventory <- struct{}{}:
	default:
		return storage.ErrBusy
	}
	reserved := true
	defer func() {
		if reserved {
			<-s.inventory
		}
	}()
	owner := identity.Token()
	began := time.Now()
	gated, err := dbwork.Begin(ctx, s.db, class)
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	// No in-memory producer owns this unpublished session: it either has never
	// started, or the previous server process died. Discard ONLY its incomplete
	// snapshot, once at adoption. Worker polling must never repeat this deletion.
	if isVerificationListing(ctx) {
		if _, err = tx.ExecContext(ctx, `DELETE FROM remote_listing_entries WHERE rowid IN(SELECT rowid FROM remote_listing_entries WHERE listing_id=? LIMIT ?)`, id, verificationReapBatch); err != nil {
			return err
		}
		var remains bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM remote_listing_entries WHERE listing_id=?)`, id).Scan(&remains); err != nil {
			return err
		}
		if remains {
			if err = verificationListingCommit(ctx, gated, began); err != nil {
				return err
			}
			return storage.ErrBusy
		}
	} else {
		if _, err = tx.ExecContext(ctx, `DELETE FROM remote_listing_entries WHERE listing_id=? AND EXISTS(SELECT 1 FROM remote_listing_sessions WHERE id=? AND ready=0)`, id, id); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO remote_native_listings(listing_id,owner,state,updated_at) VALUES(?,?,'running',?) ON CONFLICT(listing_id) DO UPDATE SET owner=excluded.owner,state='running',error_code='',entries=0,bytes=0,updated_at=excluded.updated_at`, id, owner, time.Now().UnixMilli()); err != nil {
		return err
	}
	if err = s.nativeListingFence(ctx, tx, id, owner); err != nil {
		return err
	}
	if err = verificationListingCommit(ctx, gated, began); err != nil {
		return err
	}
	lifeContext := dbwork.WithClass(runtime, class)
	if isVerificationListing(ctx) {
		lifeContext = withVerificationListing(lifeContext)
	}
	life, cancel := context.WithTimeout(lifeContext, 30*time.Minute)
	p := &nativeListing{done: make(chan struct{}), retired: make(chan struct{}), cancel: cancel}
	if s.nativeListings == nil {
		s.nativeListings = map[string]*nativeListing{}
	}
	s.nativeListings[id] = p
	s.mu.Lock()
	if s.active == nil {
		s.active = map[string]map[string]activeRead{}
	}
	if s.active[b.ID] == nil {
		s.active[b.ID] = map[string]activeRead{}
	}
	s.active[b.ID][owner] = activeRead{cancel: cancel, lane: "inventory"}
	s.mu.Unlock()
	reserved = false
	var once sync.Once
	retire := func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.active[b.ID], owner)
			if len(s.active[b.ID]) == 0 {
				delete(s.active, b.ID)
			}
			s.mu.Unlock()
			<-s.inventory
			close(p.retired)
		})
	}
	supervise.Go("remotesources.inventory.run", func() {
		// Configuration invalidation also calls the registered cancel immediately.
		// This watchdog covers scan cancel/pause, incarnation changes and abandoned
		// verification runs while the child waits on a quiet/slow provider.
		watchDone := make(chan struct{})
		supervise.Go("remotesources.inventory.watch", func() {
			defer close(watchDone)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-life.Done():
					return
				case <-ticker.C:
					check, done := context.WithTimeout(life, 2*time.Second)
					err := s.nativeListingFence(check, s.db, id, owner)
					done()
					if err != nil {
						cancel()
						return
					}
				}
			}
		})
		p.err = s.buildListing(life, b, relative, id, generation, owner, retire)
		// Capture whether the listing's own custody ended before this routine
		// cancels it for cleanup. This is the one real provider completion: the
		// cached ready/failed replay in ListPage records nothing.
		retired := life.Err() != nil
		s.health(!retired, b, generation, p.err)
		cancel()
		<-watchDone
		if p.err != nil {
			cleanup, done := context.WithTimeout(dbwork.WithClass(context.Background(), class), 5*time.Second)
			code := ErrorCode(p.err)
			_, _ = dbwork.ExecWrite(cleanup, s.db, class, `UPDATE remote_native_listings SET state='failed',error_code=?,updated_at=? WHERE listing_id=? AND owner=? AND state='running'`, code, time.Now().UnixMilli(), id, owner)
			done()
		}
		close(p.done)
		// Do not release ownership/cancel hooks or clear partial work before the
		// supervisor has retired the old process. Late writes cannot mix attempts.
		<-p.retired
		if p.err != nil && !isVerificationListing(ctx) {
			cleanup, done := context.WithTimeout(dbwork.WithClass(context.Background(), class), 5*time.Second)
			_, _ = dbwork.ExecWrite(cleanup, s.db, class, `DELETE FROM remote_listing_entries WHERE listing_id=? AND EXISTS(SELECT 1 FROM remote_native_listings WHERE listing_id=? AND owner=? AND state='failed')`, id, id, owner)
			done()
		}
		s.listings.Lock()
		if s.nativeListings[id] == p {
			delete(s.nativeListings, id)
		}
		s.listings.Unlock()
	})
	return nil
}
func (s *Service) stopNativeListings() {
	s.listings.Lock()
	s.nativeClosed = true
	for _, p := range s.nativeListings {
		p.cancel()
	}
	s.listings.Unlock()
}
