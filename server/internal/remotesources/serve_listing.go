package remotesources

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type serveListingCapture struct {
	job     string
	owner   string
	source  catalog.LibrarySource
	profile SourceReadProfile
}

func (c serveListingCapture) same(other serveListingCapture) bool {
	a, b := c.source, other.source
	return c.job == other.job && c.owner == other.owner && c.profile == other.profile && a.ID == b.ID && a.LibraryID == b.LibraryID && a.Generation == b.Generation && a.Incarnation == b.Incarnation && a.RootIdentity == b.RootIdentity && a.ResolvedPath == b.ResolvedPath && a.IdentityConfirmed == b.IdentityConfirmed && a.FollowSymlinks == b.FollowSymlinks
}

// This JSON is private listing staging, never a DTO or byte-version grant.
// File facts are enriched after the complete listing body has closed, avoiding
// metadata deadlock on a one-connection source. Publication uses the captured
// expected observation revision in the catalogue's own transaction.
type serveListingEntry struct {
	storage.Snapshot
	Observation *ScanObservation  `json:"scanObservation,omitempty"`
	Expected    int64             `json:"expectedObservationRevision,omitempty"`
	Epoch       string            `json:"processEpoch"`
	Profile     SourceReadProfile `json:"readProfile"`
	ServedETag  string            `json:"servedETag,omitempty"`
}

// serveListingFenceTx resolves the actual running inventory, including the
// original job behind a verification listing. A private listing ID or a source
// picker record is never inventory authority. No network runs in this method.
// A listing under a source-verification key has no scan: it is fenced against
// its library source and health epoch instead, and handled=true means that
// fence answered.
func (s *Service) serveListingFenceTx(ctx context.Context, tx *sql.Tx, b binding, relative, id, generation string) (serveListingCapture, error) {
	if capture, handled, err := s.verificationServeListingFenceTx(ctx, tx, b, relative, id, generation); handled {
		return capture, err
	}
	var capture serveListingCapture
	directory := filepath.Join(b.Root, filepath.FromSlash(relative))
	err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT c.job_id FROM remote_inventory_checks c WHERE c.check_job=l.job_id),l.job_id) FROM remote_listing_sessions l WHERE l.id=? AND l.source_id=? AND l.generation=? AND l.directory=? AND l.ready=0`, id, b.ID, generation, directory).Scan(&capture.job)
	if err != nil {
		return capture, storage.ErrRemoteCursor
	}
	capture.source, err = catalog.InventoryFenceTx(ctx, tx, capture.job)
	if err != nil {
		return capture, err
	}
	source := capture.source
	child, err := filepath.Rel(source.ResolvedPath, directory)
	if err != nil || source.Kind != "remote" || source.RootIdentity == "" || source.Incarnation == "" || !filepath.IsLocal(child) {
		return capture, storage.ErrRemoteChanged
	}
	var capturedPolicy, currentPolicy int64
	if err = tx.QueryRowContext(ctx, `SELECT r.policy_revision,p.revision FROM inventory_runs r JOIN library_scan_policies p ON p.library_id=? WHERE r.job_id=?`, source.LibraryID, capture.job).Scan(&capturedPolicy, &currentPolicy); err != nil {
		return capture, err
	}
	if capturedPolicy < 1 || capturedPolicy != currentPolicy {
		return capture, storage.ErrRemoteChanged
	}
	if err = s.ValidateScan(ctx, tx, capture.job, source.ResolvedPath); err != nil {
		return capture, err
	}
	current, err := observationGenerationTx(ctx, tx, b.ID)
	if err != nil {
		return capture, err
	}
	if current != generation {
		return capture, storage.ErrRemoteChanged
	}
	var root string
	if err = tx.QueryRowContext(ctx, `SELECT root FROM remote_sources WHERE id=? AND removed=0`, b.ID).Scan(&root); err != nil {
		return capture, err
	}
	actual, _, ok := s.find(directory)
	if root != b.Root || !ok || actual.ID != b.ID || actual.Removed {
		return capture, storage.ErrRemoteChanged
	}
	capture.profile, err = SourceReadProfileTx(ctx, tx, b.ID)
	if err == nil {
		var state string
		e := tx.QueryRowContext(ctx, `SELECT owner,state FROM remote_native_listings WHERE listing_id=?`, id).Scan(&capture.owner, &state)
		if e != nil && e != sql.ErrNoRows {
			return capture, e
		}
		if e == nil && (state != "running" || capture.owner == "") {
			return capture, storage.ErrRemoteCursor
		}
	}
	return capture, err
}

// buildServeSnapshot retains the old two-million-child/512MiB private staging
// budget, with only 128 entries held per SQL batch. No partial body, duplicate,
// failed job/profile/process fence or late parse error publishes ready. This is
// directory evidence, not a file observation or content/decode permission.
func (s *Service) buildServeSnapshot(ctx context.Context, process *rcloneprogram.ServeProcess, b binding, relative, id, generation string) error {
	if process == nil {
		return storage.ErrRemoteConfig
	}
	endpoint, err := process.Endpoint(ctx)
	if err != nil {
		return err
	}
	initialBegan := time.Now()
	initial, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia))
	if err != nil {
		return err
	}
	defer initial.Rollback()
	capture, err := s.serveListingFenceTx(ctx, initial.Tx(), b, relative, id, generation)
	if err == nil {
		err = process.CheckLocal(ctx)
	}
	if err == nil {
		if isVerificationListing(ctx) {
			_, err = initial.Tx().ExecContext(ctx, `DELETE FROM remote_listing_entries WHERE rowid IN(SELECT rowid FROM remote_listing_entries WHERE listing_id=? LIMIT ?)`, id, verificationReapBatch)
		} else {
			_, err = initial.Tx().ExecContext(ctx, `DELETE FROM remote_listing_entries WHERE listing_id=?`, id)
		}
	}
	if err == nil && isVerificationListing(ctx) {
		var remains bool
		err = initial.Tx().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM remote_listing_entries WHERE listing_id=?)`, id).Scan(&remains)
		if err == nil && remains {
			if err = verificationListingCommit(ctx, initial, initialBegan); err != nil {
				return err
			}
			return storage.ErrBusy
		}
	}
	if err == nil {
		_, err = initial.Tx().ExecContext(ctx, `DELETE FROM remote_listing_progress WHERE listing_id=?`, id)
	}
	if err == nil {
		err = verificationListingCommit(ctx, initial, initialBegan)
	} else {
		initial.Rollback()
	}
	if err != nil {
		return err
	}
	fence := func(ctx context.Context, tx *sql.Tx) error {
		current, err := s.serveListingFenceTx(ctx, tx, b, relative, id, generation)
		if err != nil {
			return err
		}
		if !current.same(capture) {
			return storage.ErrRemoteChanged
		}
		_, err = s.currentServeProcessTx(ctx, tx, b.ID, capture.profile, process.Generation())
		return err
	}
	check := func(ctx context.Context) error {
		if err := process.Check(ctx); err != nil {
			return err
		}
		view, err := dbwork.BeginSnapshot(ctx, s.db)
		if err != nil {
			return err
		}
		defer view.Rollback()
		return fence(ctx, view.Tx())
	}
	var count, bytes int64
	batch := make([]serveListingEntry, 0, 128)
	flush := func(ready bool) error {
		began := time.Now()
		writer, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia))
		if err != nil {
			return err
		}
		defer writer.Rollback()
		if err = fence(ctx, writer.Tx()); err != nil {
			return err
		}
		for _, entry := range batch {
			raw, err := json.Marshal(entry)
			if err != nil {
				return err
			}
			count++
			bytes += int64(len(raw))
			if count > 2_000_000 || bytes > 512<<20 {
				return storage.ErrRemoteLimit
			}
			if _, err = writer.Tx().ExecContext(ctx, `INSERT INTO remote_listing_entries VALUES(?,?,?)`, id, entry.Path, string(raw)); err != nil {
				return storage.ErrRemoteConfig
			}
		}
		if ready {
			receipt, err := json.Marshal(serveListingReceipt{Epoch: process.Generation(), Profile: capture.profile})
			if err != nil {
				return err
			}
			if _, err = writer.Tx().ExecContext(ctx, `INSERT INTO remote_listing_progress(listing_id,token,mode,steps,restarted) VALUES(?,?,'serve',1,0)`, id, receipt); err != nil {
				return err
			}
			result, err := writer.Tx().ExecContext(ctx, `UPDATE remote_listing_sessions SET ready=1 WHERE id=? AND source_id=? AND generation=? AND ready=0`, id, b.ID, generation)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil || n != 1 {
				return storage.ErrRemoteChanged
			}
			if capture.owner != "" {
				if _, err = writer.Tx().ExecContext(ctx, `UPDATE remote_native_listings SET state='complete',entries=?,bytes=? WHERE listing_id=? AND owner=? AND state='running'`, count, bytes, id, capture.owner); err != nil {
					return err
				}
			}
		} else if capture.owner != "" {
			if _, err = writer.Tx().ExecContext(ctx, `UPDATE remote_native_listings SET entries=?,bytes=? WHERE listing_id=? AND owner=? AND state='running'`, count, bytes, id, capture.owner); err != nil {
				return err
			}
		}
		if err = verificationListingCommit(ctx, writer, began); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	err = endpoint.VisitSnapshot(ctx, relative, check, func(entry remotemedia.DAVEntry) error {
		value := inventorySnapshot(b, generation, davSnapshot(entry, b.Root))
		if filepath.Dir(value.Path) != filepath.Join(b.Root, filepath.FromSlash(relative)) {
			return storage.ErrRemoteConfig
		}
		batch = append(batch, serveListingEntry{Snapshot: value, ServedETag: entry.ETag})
		if len(batch) == cap(batch) {
			return flush(false)
		}
		return nil
	})
	if err != nil {
		return mapError(err)
	}
	if err = flush(false); err != nil {
		return err
	}
	// The full listing body is closed before the directory metadata request.
	// The same upstream process supplies its fresh backend listing once, without
	// Portico issuing a metadata/stat request for every child.
	metadata, err := process.Metadata(ctx)
	if err != nil {
		return err
	}
	metaBatch := make([]remotemedia.RcloneFileMetadata, 0, 128)
	var enriched int64
	writeMetadata := func() error {
		if len(metaBatch) == 0 {
			return nil
		}
		began := time.Now()
		writer, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia))
		if err != nil {
			return err
		}
		defer writer.Rollback()
		if err = fence(ctx, writer.Tx()); err != nil {
			return err
		}
		for _, fresh := range metaBatch {
			file := filepath.Join(b.Root, filepath.FromSlash(fresh.Path))
			var raw string
			var entry serveListingEntry
			if err = writer.Tx().QueryRowContext(ctx, `SELECT snapshot FROM remote_listing_entries WHERE listing_id=? AND name=?`, id, file).Scan(&raw); err != nil {
				return storage.ErrRemoteChanged
			}
			if json.Unmarshal([]byte(raw), &entry) != nil || entry.Epoch != "" || entry.Directory != fresh.Directory || !entry.Directory && entry.Size != fresh.Size {
				return storage.ErrRemoteChanged
			}
			entry.Epoch, entry.Profile = process.Generation(), capture.profile
			if !entry.Directory {
				facts := remotemedia.FileObservation{SourceID: b.ID, Incarnation: generation, Object: fresh.Path, ObjectID: fresh.ID, Size: fresh.Size, ProviderHashes: fresh.Hashes, ServeETag: entry.ServedETag}
				if !fresh.Modified.IsZero() && time.Unix(0, fresh.Modified.UnixNano()).Equal(fresh.Modified) {
					facts.ModifiedKnown, facts.ModifiedNS = true, fresh.Modified.UnixNano()
					if entry.ModifiedNS != 0 && !time.Unix(0, entry.ModifiedNS).Truncate(time.Second).Equal(fresh.Modified.Truncate(time.Second)) {
						return storage.ErrRemoteChanged
					}
				}
				entry.Snapshot, err = observationSnapshot(b, generation, facts)
				if err != nil {
					return err
				}
				entry.Observation = &ScanObservation{Facts: facts, ScanRevision: entry.Revision}
				previous, err := readScanObservationTx(ctx, writer.Tx(), b.ID, fresh.Path)
				if err != nil && err != sql.ErrNoRows {
					return err
				}
				entry.Expected = previous.Ref.Revision
			}
			encoded, err := json.Marshal(entry)
			if err != nil {
				return err
			}
			bytes += int64(len(encoded) - len(raw))
			if bytes > 512<<20 {
				return storage.ErrRemoteLimit
			}
			if _, err = writer.Tx().ExecContext(ctx, `UPDATE remote_listing_entries SET snapshot=? WHERE listing_id=? AND name=?`, string(encoded), id, file); err != nil {
				return err
			}
			enriched++
		}
		if err = verificationListingCommit(ctx, writer, began); err != nil {
			return err
		}
		metaBatch = metaBatch[:0]
		return nil
	}
	report := process.PacerObservation()
	err = metadata.VisitDirectory(ctx, relative, check, func(fresh remotemedia.RcloneFileMetadata) error {
		metaBatch = append(metaBatch, fresh)
		if len(metaBatch) == cap(metaBatch) {
			return writeMetadata()
		}
		return nil
	})
	if err != nil {
		return mapError(err)
	}
	if err = writeMetadata(); err != nil {
		return err
	}
	if enriched != count {
		return storage.ErrRemoteChanged
	}
	if err = check(ctx); err != nil {
		return err
	}
	process.BackendObserved(report.Sequence)
	return flush(true)
}
