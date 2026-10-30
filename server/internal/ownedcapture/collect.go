package ownedcapture

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
)

func (s *Service) cancelReaders(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for r := range s.active[id] {
		r.cancel()
	}
}
func (s *Service) markRetiredTx(ctx context.Context, tx *sql.Tx, id string) error {
	_, e := tx.ExecContext(ctx, `UPDATE owned_capture_lifecycle SET state='retired',updated_ms=? WHERE capture_id=? AND state IN('capturing','ready')`, time.Now().UnixMilli(), id)
	if e != nil {
		return e
	}
	return dbwork.AfterCommit(tx, func() { s.cancelReaders(id) })
}

// Retire revokes new/active uses of this exact receipt; physical cleanup waits
// for its existing readers/helper exit and real immutable-byte leases.
func (s *Service) Retire(ctx context.Context, expected Receipt) error {
	return dbwork.WithWriteTx(ctx, s.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		row, e := sourceTx(ctx, tx, expected.ID)
		if e != nil {
			return e
		}
		if row.b != expected.Binding {
			return ErrCurrent
		}
		var artifact, digest, version string
		var size int64
		e = tx.QueryRowContext(ctx, `SELECT artifact_id,digest,size,version_id FROM owned_capture_receipts WHERE capture_id=?`, expected.ID).Scan(&artifact, &digest, &size, &version)
		if e != nil {
			return e
		}
		if artifact != expected.ArtifactID || digest != expected.Object.Digest || size != expected.Object.Size || version != expected.Version.ID() {
			return ErrCurrent
		}
		return s.markRetiredTx(ctx, tx, expected.ID)
	})
}
func (s *Service) retireAndClean(ctx context.Context, id string, held bool) error {
	if !held {
		gate, e := s.gate("acquisition", id)
		if e != nil {
			return e
		}
		defer gate.Close()
	}
	if e := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error { return s.markRetiredTx(ctx, tx, id) }); e != nil {
		return e
	}
	view, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return e
	}
	row, e := sourceTx(ctx, view.Tx(), id)
	var pins int
	if e == nil {
		pins, e = retainedCapturePinsTx(ctx, view.Tx(), id)
	}
	view.Rollback()
	if e != nil {
		return e
	}
	if row.state == "deleted" {
		return nil
	}
	if row.state != "retired" || pins > 0 {
		return nil
	}
	// Never recover/resume this weak source. Remove only this journaled owned
	// staging file, after acquiring the exact original attempt's physical gate.
	if e = s.destination.check(); e != nil {
		return e
	}
	if e = s.artifacts.RemoveRetained(id + ".1"); e != nil {
		return e
	}
	if row.digest != "" {
		objectGate, e := s.gate("object", row.digest)
		if e != nil {
			return e
		}
		defer objectGate.Close()
		var others int
		e = s.db.QueryRowContext(ctx, `SELECT count(*) FROM owned_capture_lifecycle WHERE pending_digest=? AND state!='deleted' AND capture_id!=?`, row.digest, id).Scan(&others)
		if e != nil {
			return e
		}
		// Content-addressed physical deduplication confers no receipt authority.
		// A different acquisition retains its own FULL source quota and immutable
		// receipt; only the last owned reference can remove shared physical bytes.
		if others == 0 {
			if e = s.artifacts.Remove(mediaartifact.Object{Digest: row.digest, Size: row.size}); e != nil && !errors.Is(e, os.ErrNotExist) {
				return e
			}
		}
	}
	// Once state is retired no new reader can pin it. Recheck the exact immutable
	// provenance and pins in the same transaction that releases source accounting.
	return dbwork.WithWriteTx(ctx, s.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		current, e := sourceTx(ctx, tx, id)
		if e != nil {
			return e
		}
		if current.b != row.b || current.state != "retired" || current.digest != row.digest {
			return ErrCurrent
		}
		var active int
		if active, e = retainedCapturePinsTx(ctx, tx, id); e != nil {
			return e
		}
		if active > 0 {
			return ErrCurrent
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM owned_capture_reader_pins WHERE capture_id=?`, id); e != nil {
			return e
		}
		if e = s.producer.ReleaseTx(ctx, tx, row.b); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE owned_capture_lifecycle SET state='deleted',updated_ms=? WHERE capture_id=? AND state='retired'`, time.Now().UnixMilli(), id)
		return e
	})
}

// Collect is bounded and retryable. Physical custody, not an expired row or a
// PID, proves that a crashed producer/helper can no longer write/read bytes.
func (s *Service) Collect(ctx context.Context) error {
	if dbwork.Snapshot(ctx) != nil {
		return ErrInput
	}
	s.collectMu.Lock()
	defer s.collectMu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := s.destination.check(); e != nil {
		return e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT `+sourceColumns+` FROM owned_capture_sources s JOIN owned_capture_lifecycle l ON l.capture_id=s.capture_id WHERE l.state!='deleted' AND s.capture_id>? ORDER BY s.capture_id LIMIT 64`, s.cursor)
	if e != nil {
		return e
	}
	list := []sourceRow{}
	for rows.Next() {
		row, err := readSource(rows)
		if err != nil {
			rows.Close()
			return err
		}
		list = append(list, row)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	s.cursor = ""
	if len(list) == 64 {
		s.cursor = list[len(list)-1].id
	}
	for _, row := range list {
		custody, err := s.gate("acquisition", row.id)
		if errors.Is(err, livechannels.ErrPhysicalBusy) {
			continue
		}
		if err != nil {
			return err
		}
		retire := row.state != "ready" || row.expires <= time.Now().UnixMilli()
		if !retire {
			view, err := dbwork.BeginSnapshot(ctx, s.db)
			if err != nil {
				custody.Close()
				return err
			}
			_, err = s.resolveTx(ctx, view.Tx(), row.id, row.b)
			view.Rollback()
			retire = err != nil
		}
		if retire {
			err = s.retireAndClean(ctx, row.id, true)
		}
		custody.Close()
		if err != nil && !errors.Is(err, livechannels.ErrPhysicalBusy) && !errors.Is(err, mediaartifact.ErrLeased) {
			return err
		}
	}
	// An uncertain crash can leave a sealed object without a published receipt;
	// pending_digest is journaled before Seal and guards the normal gap. An owned
	// inventory sweep also catches an object left by a failed seal/fsync operation.
	if s.inventory == nil {
		s.inventory, e = s.artifacts.Inventory()
		if e != nil {
			return e
		}
	}
	objects, next := s.inventory.Next(ctx, 64)
	for _, entry := range objects {
		gate, err := s.gate("object", entry.Object.Digest)
		if errors.Is(err, livechannels.ErrPhysicalBusy) {
			continue
		}
		if err != nil {
			return err
		}
		var refs int
		err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM owned_capture_lifecycle WHERE pending_digest=? AND state!='deleted'`, entry.Object.Digest).Scan(&refs)
		if err == nil && refs == 0 {
			err = s.artifacts.Remove(entry.Object)
		}
		gate.Close()
		if err != nil && !errors.Is(err, mediaartifact.ErrLeased) {
			return err
		}
	}
	if errors.Is(next, io.EOF) {
		s.inventory.Close()
		s.inventory = nil
		return nil
	}
	return next
}

// A presentation reference retains bytes/accounting, never source authority.
// Actual retirement still cancels readers immediately. Cleanup waits only on
// the original ready presentation deadline or that presentation's real reader
// pins; another actor/acquisition cannot be substituted by equal bytes.
func retainedCapturePinsTx(ctx context.Context, tx *sql.Tx, id string) (int, error) {
	now := time.Now().UnixMilli()
	var readers, presentations int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM owned_capture_reader_pins WHERE capture_id=? AND expires_ms>?`, id, now).Scan(&readers); err != nil {
		return 0, err
	}
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM owned_capture_presentation_refs r JOIN playback_disc_lifecycle l ON l.snapshot_id=r.snapshot_id WHERE r.capture_id=? AND (
 (l.state IN('pending','active') AND l.sealed=1 AND l.expires_ms>? AND r.expires_ms>?)
 OR EXISTS(SELECT 1 FROM playback_disc_reader_pins p WHERE p.snapshot_id=r.snapshot_id AND p.expires_ms>?))`, id, now, now, now).Scan(&presentations)
	return readers + presentations, err
}
