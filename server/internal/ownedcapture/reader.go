package ownedcapture

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
)

type objectLease struct {
	file  *os.File
	count int
}

func (s *Service) readerGate(digest string) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrCurrent
	}
	count := 0
	for _, held := range s.readers {
		count += held.count
	}
	if count >= 256 {
		return nil, ErrCurrent
	}
	lease := s.readers[digest]
	if lease == nil {
		file, e := s.gate("object", digest)
		if e != nil {
			return nil, e
		}
		lease = &objectLease{file: file}
		s.readers[digest] = lease
	}
	lease.count++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			lease.count--
			if lease.count == 0 {
				delete(s.readers, digest)
				lease.file.Close()
			}
		})
	}, nil
}

// Reader is the exact receipt plus original source lease, normalized read pin
// and independent hash-verified read-only artifact descriptor. Keep it alive
// until the ORIGINAL helper/converter exits. Local possession grants no play.
type Reader struct {
	service    *Service
	receipt    Receipt
	source     SourceLease
	descriptor *mediaartifact.ReadDescriptor
	original   os.FileInfo
	pin        string
	life       context.Context
	cancel     context.CancelFunc
	stops      []func() bool
	release    func()
	once       sync.Once
	closed     atomic.Bool
	closeErr   error
}

func (r *Reader) Receipt() Receipt { return r.receipt }
func (r *Reader) File() *os.File {
	if r == nil || r.closed.Load() {
		return nil
	}
	return r.descriptor.File()
}
func (r *Reader) CaptureIdentity() (string, mediasource.Version) {
	return r.receipt.ID, r.receipt.Version
}
func (r *Reader) Context() context.Context { return r.life }
func (r *Reader) checkFile(ctx context.Context) error {
	if r.closed.Load() || r.life.Err() != nil || ctx.Err() != nil {
		return ErrCurrent
	}
	file := r.File()
	if file == nil {
		return ErrCurrent
	}
	info, e := file.Stat()
	if e != nil || !os.SameFile(info, r.original) || info.Size() != r.receipt.Object.Size || info.Mode().Perm()&0222 != 0 {
		return ErrCurrent
	}
	return nil
}
func (r *Reader) CheckTx(ctx context.Context, tx *sql.Tx) error {
	if tx == nil {
		r.cancel()
		return ErrInput
	}
	fail := func(e error) error {
		if e != nil {
			r.cancel()
		}
		return e
	}
	if e := r.checkFile(ctx); e != nil {
		return fail(e)
	}
	if e := r.source.CheckTx(ctx, tx); e != nil {
		return fail(e)
	}
	receipt, e := r.service.resolveTx(ctx, tx, r.receipt.ID, r.receipt.Binding)
	if e != nil {
		return fail(e)
	}
	if !sameReceipt(receipt, r.receipt) {
		return fail(ErrCurrent)
	}
	var live bool
	e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM owned_capture_reader_pins WHERE id=? AND capture_id=? AND expires_ms>? AND expires_ms<=?)`, r.pin, r.receipt.ID, time.Now().UnixMilli(), r.receipt.ExpiresAt.UnixMilli()).Scan(&live)
	if e != nil {
		return fail(e)
	}
	if !live {
		return fail(ErrCurrent)
	}
	return fail(r.checkFile(ctx))
}
func (r *Reader) Check(ctx context.Context) error {
	if tx := dbwork.Snapshot(ctx); tx != nil {
		return r.CheckTx(ctx, tx)
	}
	if e := r.source.Check(ctx); e != nil {
		r.cancel()
		return e
	}
	view, e := dbwork.BeginSnapshot(ctx, r.service.db)
	if e != nil {
		return e
	}
	defer view.Rollback()
	return r.CheckTx(ctx, view.Tx())
}
func (r *Reader) Close() error {
	r.once.Do(func() {
		r.closed.Store(true)
		r.cancel()
		for _, stop := range r.stops {
			stop()
		}
		r.service.mu.Lock()
		delete(r.service.active[r.receipt.ID], r)
		if len(r.service.active[r.receipt.ID]) == 0 {
			delete(r.service.active, r.receipt.ID)
		}
		r.service.mu.Unlock()
		r.closeErr = r.descriptor.Close()
		r.release()
		r.closeErr = errors.Join(r.closeErr, r.source.Close())
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, e := dbwork.ExecWrite(cleanup, r.service.db, dbwork.ClassMaintenance, `DELETE FROM owned_capture_reader_pins WHERE id=? AND capture_id=?`, r.pin, r.receipt.ID)
		r.closeErr = errors.Join(r.closeErr, e)
	})
	return r.closeErr
}

// Open requires an exact previously accepted receipt value. No ID-only/latest
// artifact lookup, path, provider reopen or digest-based sharing is available.
func (s *Service) Open(ctx context.Context, expected Receipt) (*Reader, error) {
	// Read pins must commit before opening/hash-verifying bytes, never remain
	// pending in a caller transaction while a descriptor is already usable.
	if dbwork.Snapshot(ctx) != nil {
		return nil, ErrInput
	}
	if !s.enter() {
		return nil, ErrCurrent
	}
	defer s.wg.Done()
	if !captureID.MatchString(expected.ID) || !captureID.MatchString(expected.ArtifactID) || !captureID.MatchString(expected.Object.Digest) || expected.Object.Size <= 0 || !expected.Binding.valid() || !expected.ExpiresAt.After(time.Now()) {
		return nil, ErrInput
	}
	if e := s.destination.check(); e != nil {
		return nil, e
	}
	lease, e := s.producer.Lease(ctx, expected.Binding)
	if e != nil {
		return nil, e
	}
	good := false
	defer func() {
		if !good {
			lease.Close()
		}
	}()
	sourceLife := lease.Context()
	if sourceLife == nil || sourceLife.Done() == nil {
		return nil, ErrInput
	}
	release, e := s.readerGate(expected.Object.Digest)
	if e != nil {
		return nil, e
	}
	defer func() {
		if !good {
			release()
		}
	}()
	life, cancel := context.WithDeadline(ctx, expected.ExpiresAt)
	stops := []func() bool{context.AfterFunc(s.life, cancel), context.AfterFunc(sourceLife, cancel)}
	defer func() {
		if !good {
			cancel()
			for _, stop := range stops {
				stop()
			}
		}
	}()
	pin := uniqueID()
	published := false
	defer func() {
		if !good && published {
			cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			_, _ = dbwork.ExecWrite(cleanup, s.db, dbwork.ClassMaintenance, `DELETE FROM owned_capture_reader_pins WHERE id=? AND capture_id=?`, pin, expected.ID)
		}
	}()
	e = dbwork.WithWriteTx(life, s.db, dbwork.ClassPlaybackStart, func(tx *sql.Tx) error {
		if err := lease.CheckTx(life, tx); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(life, `SELECT count(*) FROM owned_capture_reader_pins WHERE expires_ms>?`, time.Now().UnixMilli()).Scan(&count); err != nil {
			return err
		}
		if count >= 256 {
			return ErrCurrent
		}
		actual, err := s.resolveTx(life, tx, expected.ID, expected.Binding)
		if err != nil {
			return err
		}
		if !sameReceipt(actual, expected) {
			return ErrCurrent
		}
		_, err = tx.ExecContext(life, `INSERT INTO owned_capture_reader_pins(id,capture_id,expires_ms) VALUES(?,?,?)`, pin, actual.ID, actual.ExpiresAt.UnixMilli())
		return err
	})
	if e != nil {
		return nil, e
	}
	published = true
	descriptor, e := s.artifacts.OpenDescriptor(life, expected.Object)
	if e != nil {
		return nil, e
	}
	info, e := descriptor.File().Stat()
	if e != nil {
		descriptor.Close()
		return nil, e
	}
	reader := &Reader{service: s, receipt: expected, source: lease, descriptor: descriptor, original: info, pin: pin, life: life, cancel: cancel, stops: stops, release: release}
	if e = reader.Check(life); e != nil {
		descriptor.Close()
		return nil, e
	}
	s.mu.Lock()
	if s.closed || life.Err() != nil {
		s.mu.Unlock()
		descriptor.Close()
		return nil, ErrCurrent
	}
	if s.active[expected.ID] == nil {
		s.active[expected.ID] = map[*Reader]bool{}
	}
	s.active[expected.ID][reader] = true
	s.mu.Unlock()
	// A retirement could commit between the first check and registration; this
	// second check closes that gap before a caller receives any descriptor.
	if e = reader.Check(life); e != nil {
		s.mu.Lock()
		delete(s.active[expected.ID], reader)
		if len(s.active[expected.ID]) == 0 {
			delete(s.active, expected.ID)
		}
		s.mu.Unlock()
		descriptor.Close()
		return nil, e
	}
	good = true
	return reader, nil
}
