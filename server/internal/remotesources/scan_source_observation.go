package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// ScanFileObservation retains one actual running inventory job and metadata
// observation until its caller's publication transaction. It grants no media
// read or decode and cannot be constructed from caller-supplied provider facts.
type ScanFileObservation struct {
	mu            sync.Mutex
	terminal      error
	life          context.Context
	service       *Service
	job, file     string
	librarySource catalog.LibrarySource
	binding       SourceReadBinding
	process       *rcloneprogram.ServeProcess
	value         ScanObservation
	snapshot      storage.Snapshot
	expected      int64
}

func (s *Service) ObserveScanFile(ctx context.Context, job, file string) (*ScanFileObservation, error) {
	if s == nil || s.db == nil || job == "" || !filepath.IsAbs(file) || filepath.Clean(file) != file {
		return nil, storage.ErrRemoteConfig
	}
	view, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return nil, err
	}
	tx := view.Tx()
	source, err := catalog.InventoryFenceTx(ctx, tx, job)
	if err != nil {
		view.Rollback()
		return nil, err
	}
	relative, e := filepath.Rel(source.ResolvedPath, file)
	b, object, ok := s.find(file)
	if source.Kind != "remote" || e != nil || relative == "." || !filepath.IsLocal(relative) || !ok || b.Removed || !validObservationObject(object) {
		view.Rollback()
		return nil, storage.ErrRemoteConfig
	}
	if err = s.ValidateScan(ctx, tx, job, source.ResolvedPath); err != nil {
		view.Rollback()
		return nil, err
	}
	generation, err := observationGenerationTx(ctx, tx, b.ID)
	if err != nil {
		view.Rollback()
		return nil, err
	}
	profile, err := SourceReadProfileTx(ctx, tx, b.ID)
	if err != nil {
		view.Rollback()
		return nil, err
	}
	previous, err := readScanObservationTx(ctx, tx, b.ID, object)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		view.Rollback()
		return nil, err
	}
	expected := previous.Ref.Revision
	view.Rollback()
	c := &ScanFileObservation{life: ctx, service: s, job: job, file: file, librarySource: source, expected: expected, binding: SourceReadBinding{Purpose: SourceInventory, Observation: ObservationRef{SourceID: b.ID, Generation: generation, Object: object}, Path: file, Root: b.Root, Profile: profile}}
	if err = c.check(ctx); err != nil {
		return nil, err
	}
	c.process, err = s.ensureSourceServe(ctx, c.binding, "")
	if err != nil {
		return nil, err
	}
	facts, err := observeProcessFile(ctx, c.process, object, c.check)
	if err != nil {
		return nil, err
	}
	c.snapshot, err = observationSnapshot(b, generation, facts)
	if err != nil {
		return nil, err
	}
	c.value = ScanObservation{Facts: facts, ScanRevision: c.snapshot.Revision}
	if err = c.check(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *ScanFileObservation) Snapshot() storage.Snapshot { return c.snapshot }

func (c *ScanFileObservation) checkTx(ctx context.Context, tx *sql.Tx) (err error) {
	c.mu.Lock()
	terminal := c.terminal
	c.mu.Unlock()
	if terminal != nil {
		return terminal
	}
	defer func() {
		if err != nil {
			err = c.fail(err)
		}
	}()
	if err = c.life.Err(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	source, err := catalog.InventoryFenceTx(ctx, tx, c.job)
	if err != nil {
		return err
	}
	if source.Kind != "remote" || source.ID != c.librarySource.ID || source.Generation != c.librarySource.Generation || source.Incarnation != c.librarySource.Incarnation || source.RootIdentity != c.librarySource.RootIdentity || source.ResolvedPath != c.librarySource.ResolvedPath {
		return storage.ErrRemoteChanged
	}
	var capturedPolicy, currentPolicy int64
	if err = tx.QueryRowContext(ctx, `SELECT r.policy_revision,p.revision FROM inventory_runs r JOIN library_scan_policies p ON p.library_id=? WHERE r.job_id=?`, source.LibraryID, c.job).Scan(&capturedPolicy, &currentPolicy); err != nil {
		return err
	}
	if capturedPolicy < 1 || capturedPolicy != currentPolicy {
		return storage.ErrRemoteChanged
	}
	if err = c.service.ValidateScan(ctx, tx, c.job, source.ResolvedPath); err != nil {
		return err
	}
	generation, err := observationGenerationTx(ctx, tx, c.binding.Observation.SourceID)
	if err != nil {
		return err
	}
	profile, err := SourceReadProfileTx(ctx, tx, c.binding.Observation.SourceID)
	if err != nil {
		return err
	}
	var root string
	if err = tx.QueryRowContext(ctx, `SELECT root FROM remote_sources WHERE id=? AND removed=0`, c.binding.Observation.SourceID).Scan(&root); err != nil {
		return err
	}
	if generation != c.binding.Observation.Generation || profile != c.binding.Profile || root != c.binding.Root {
		return storage.ErrRemoteChanged
	}
	if c.process != nil {
		if err = c.process.CheckLocal(ctx); err != nil {
			return err
		}
		c.service.serveMu.Lock()
		slot := c.service.serveLeases[c.binding.Observation.SourceID]
		c.service.serveMu.Unlock()
		if slot == nil || slot.process != c.process {
			return storage.ErrRemoteChanged
		}
		network, e := c.service.sourceNetworkTx(ctx, tx, slot.source, slot.root)
		if e != nil {
			return e
		}
		sequential, e := sourceSequentialTx(ctx, tx, slot.source, slot.root)
		if e != nil {
			return e
		}
		if network.Fingerprint != slot.networkFingerprint || sequential != slot.sequential {
			return storage.ErrRemoteChanged
		}
	}
	return nil
}
func (c *ScanFileObservation) check(ctx context.Context) error {
	view, err := dbwork.BeginSnapshot(ctx, c.service.db)
	if err != nil {
		return err
	}
	defer view.Rollback()
	return c.checkTx(ctx, view.Tx())
}

// PublishTx does no network I/O, nested DB work or capacity wait. Its actual
// running inventory/current source checks and the normalized CAS writer share
// the caller's transaction. The caller must roll back on any error.
func (c *ScanFileObservation) PublishTx(ctx context.Context, tx *sql.Tx) (ObservationRef, error) {
	if c == nil || tx == nil || c.process == nil {
		return ObservationRef{}, storage.ErrRemoteConfig
	}
	if err := c.checkTx(ctx, tx); err != nil {
		return ObservationRef{}, err
	}
	ref, err := c.service.RecordScanObservationTx(ctx, tx, c.job, c.file, c.value, c.expected)
	if err != nil {
		return ObservationRef{}, c.fail(err)
	}
	return ref, nil
}
func (c *ScanFileObservation) fail(err error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal == nil {
		c.terminal = err
	}
	return c.terminal
}
