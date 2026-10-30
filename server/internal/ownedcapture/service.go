package ownedcapture

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

type Options struct {
	DB        *sql.DB
	Directory string
	Producer  Producer
}
type Service struct {
	db              *sql.DB
	artifacts       *mediaartifact.Store
	destination     Destination
	producer        Producer
	locks           *livechannels.PhysicalLocks
	life            context.Context
	cancel          context.CancelFunc
	mu              sync.Mutex
	closed, started bool
	wg              sync.WaitGroup
	readers         map[string]*objectLease
	active          map[string]map[*Reader]bool
	collectMu       sync.Mutex
	inventory       *mediaartifact.Inventory
	cursor          string
}

func New(o Options) (*Service, error) {
	if o.DB == nil || o.Producer == nil || !filepath.IsAbs(o.Directory) {
		return nil, ErrInput
	}
	artifacts, err := mediaartifact.New(o.Directory)
	if err != nil {
		return nil, err
	}
	destination, err := newDestination(artifacts.Root())
	if err != nil {
		artifacts.Close()
		return nil, err
	}
	locks, err := livechannels.NewPhysicalLocks(filepath.Join(o.Directory, "custody"))
	if err != nil {
		artifacts.Close()
		return nil, err
	}
	life, cancel := context.WithCancel(context.Background())
	return &Service{db: o.DB, artifacts: artifacts, producer: o.Producer, destination: destination, locks: locks, life: life, cancel: cancel, readers: map[string]*objectLease{}, active: map[string]map[*Reader]bool{}}, nil
}
func (s *Service) enter() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.wg.Add(1)
	return true
}
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	if s.closed || s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.wg.Add(1)
	s.mu.Unlock()
	supervise.Go("ownedcapture.collect", func() {
		defer s.wg.Done()
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			_ = s.Collect(s.life)
			select {
			case <-ctx.Done():
				return
			case <-s.life.Done():
				return
			case <-tick.C:
			}
		}
	})
}
func (s *Service) Close() error {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
	s.collectMu.Lock()
	defer s.collectMu.Unlock()
	if s.inventory != nil {
		s.inventory.Close()
		s.inventory = nil
	}
	return s.artifacts.Close()
}
func (s *Service) gate(kind, id string) (*os.File, error) {
	return s.locks.Lock(livechannels.Allocation{ID: framed("portico.owned-capture.lock.v1", []string{kind, id})[:48], Generation: 1})
}

// uniqueID is the capture schema's only producer: 32 cryptographically random
// bytes encoded as lower-case hex, so the value is 64 characters and satisfies
// the persisted capture_id contract. The shared identity digest keeps its own
// encoding for its own purposes.
func uniqueID() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return hex.EncodeToString(raw)
}
func bindLife(parent, other context.Context) (context.Context, func()) {
	life, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(other, cancel)
	return life, func() { stop(); cancel() }
}

// Capture never probes/reopens/resumes a provider. Producer.Admit must return
// one already admitted from-start sequential body; only complete EOF bytes seal.
func (s *Service) Capture(ctx context.Context, request Request) (receipt Receipt, err error) {
	return s.capture(ctx, request, nil, nil)
}

func (s *Service) capture(ctx context.Context, request Request, original PlayCaptureCheck, ticket *PlayCaptureTicket) (receipt Receipt, err error) {
	// Acquisition and immutable sealing must never join an outer transaction;
	// publication here must be committed before any usable receipt is returned.
	if ctx == nil || dbwork.Snapshot(ctx) != nil {
		return receipt, ErrInput
	}
	if !s.enter() {
		return receipt, ErrCurrent
	}
	defer s.wg.Done()
	ctx, done := bindLife(ctx, s.life)
	defer done()
	if err = s.checkPlayCapture(ctx, original); err != nil {
		return receipt, err
	}
	id := uniqueID()
	if ticket != nil {
		id = ticket.ID()
	}
	custody, err := s.gate("acquisition", id)
	if err != nil {
		return receipt, err
	}
	defer custody.Close()
	acquisition, err := s.producer.Admit(ctx, id, request, s.destination)
	if err != nil {
		return receipt, err
	}
	defer acquisition.Close()
	if err = s.checkPlayCapture(ctx, original); err != nil {
		return receipt, err
	}
	sourceLife := acquisition.Context()
	if sourceLife == nil || sourceLife.Done() == nil {
		return receipt, ErrInput
	}
	ctx, sourceDone := bindLife(ctx, sourceLife)
	defer sourceDone()
	b, size, expires := acquisition.Binding(), acquisition.Size(), acquisition.ExpiresAt().UTC()
	now := time.Now().UTC()
	if !b.valid() || b.AssetID != request.AssetID || size <= 0 || size == int64(^uint64(0)>>1) || !expires.After(now) || expires.After(now.Add(MaxCaptureLifetime)) {
		return receipt, ErrInput
	}
	if ticket != nil && !ticket.matches(b, size) {
		return receipt, ErrCurrent
	}
	expires = time.UnixMilli(expires.UnixMilli()).UTC()
	ctx, expiryCancel := context.WithDeadline(ctx, expires)
	defer expiryCancel()
	stopClose := context.AfterFunc(ctx, func() { _ = acquisition.Close() })
	defer stopClose()
	// The admission row precedes all owned scratch, so a crash never leaves an
	// unjournaled retained staging file. Source reservation recovery owns the
	// prior no-scratch Admit/row gap and must keep it charged until safely retired.
	err = dbwork.WithWriteTxContext(ctx, s.db, dbwork.ClassBackgroundMedia, func(ctx context.Context, tx *sql.Tx) error {
		if e := checkPlayCaptureTx(ctx, tx, original); e != nil {
			return e
		}
		if e := acquisition.CheckTx(ctx, tx); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO owned_capture_sources(capture_id,source_id,config_generation,object_id,object_revision,asset_id,acquisition_id,reservation_id,policy_witness,authority_witness,expected_size,created_ms,expires_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, b.SourceID, b.ConfigGeneration, b.ObjectID, b.ObjectRevision, b.AssetID, b.AcquisitionID, b.ReservationID, b.PolicyWitness, b.AuthorityWitness, size, now.UnixMilli(), expires.UnixMilli())
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO owned_capture_lifecycle(capture_id,state,updated_ms) VALUES(?,'capturing',?)`, id, now.UnixMilli())
		return e
	})
	if err != nil {
		return receipt, err
	}
	var writer *mediaartifact.Writer
	var objectGate *os.File
	defer func() {
		if objectGate != nil {
			objectGate.Close()
		}
		if err != nil {
			_ = acquisition.Close()
			if writer != nil {
				// Close only the original descriptor. Checked managed cleanup below
				// proves the original directories before removing any owned pathname.
				_ = writer.Retain()
			}
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// The same acquisition custody is still held. Never release quota before
			// physical retained staging/object removal; uncertain cleanup stays charged.
			_ = s.retireAndClean(cleanup, id, true)
		}
	}()
	if err = s.destination.check(); err != nil {
		return receipt, err
	}
	writer, err = s.artifacts.BeginRetained(id+".1", size)
	if err != nil {
		return receipt, err
	}
	hash := sha256.New()
	checkOriginal := func(check context.Context) error { return s.checkPlayCapture(check, original) }
	sink := &persistedWriter{ctx: ctx, writer: writer, source: acquisition, destination: s.destination, original: checkOriginal}
	n, copyErr := io.CopyBuffer(io.MultiWriter(sink, hash), io.LimitReader(&checkedReader{ctx: ctx, source: acquisition, original: checkOriginal}, size+1), make([]byte, 64<<10))
	if copyErr != nil {
		return receipt, copyErr
	}
	if n != size {
		return receipt, ErrIncomplete
	}
	if err = acquisition.Check(ctx); err != nil {
		return receipt, err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	objectGate, err = s.gate("object", digest)
	if err != nil {
		return receipt, err
	}
	// Journal the actual completed-byte hash BEFORE sealing, including the crash
	// window before SQL publication. It is not a usable receipt or provider proof.
	err = dbwork.WithWriteTxContext(ctx, s.db, dbwork.ClassBackgroundMedia, func(ctx context.Context, tx *sql.Tx) error {
		if e := checkPlayCaptureTx(ctx, tx, original); e != nil {
			return e
		}
		if e := acquisition.CheckTx(ctx, tx); e != nil {
			return e
		}
		result, e := tx.ExecContext(ctx, `UPDATE owned_capture_lifecycle SET pending_digest=?,updated_ms=? WHERE capture_id=? AND state='capturing' AND pending_digest=''`, digest, time.Now().UnixMilli(), id)
		if e != nil {
			return e
		}
		changed, e := result.RowsAffected()
		if e == nil && changed != 1 {
			e = ErrCurrent
		}
		return e
	})
	if err != nil {
		return receipt, err
	}
	if err = s.destination.check(); err != nil {
		return receipt, err
	}
	if err = checkOriginal(ctx); err != nil {
		return receipt, err
	}
	object, err := writer.Seal(ctx)
	if err != nil {
		return receipt, err
	}
	if object.Size != size || object.Digest != digest {
		return receipt, ErrIncomplete
	}
	version, err := captureVersion(id, b, object)
	if err != nil {
		return receipt, err
	}
	receipt = Receipt{ID: id, ArtifactID: uniqueID(), Binding: b, Object: object, Version: version, ExpiresAt: expires}
	err = dbwork.WithWriteTxContext(ctx, s.db, dbwork.ClassBackgroundMedia, func(ctx context.Context, tx *sql.Tx) error {
		if e := checkPlayCaptureTx(ctx, tx, original); e != nil {
			return e
		}
		if e := acquisition.CheckTx(ctx, tx); e != nil {
			return e
		}
		row, e := sourceTx(ctx, tx, id)
		if e != nil {
			return e
		}
		if row.b != b || row.state != "capturing" || row.digest != digest || row.size != size || row.expires <= time.Now().UnixMilli() {
			return ErrCurrent
		}
		if e = acquisition.CommitTx(ctx, tx); e != nil {
			return e
		}
		if e = checkPlayCaptureTx(ctx, tx, original); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO owned_capture_receipts(capture_id,artifact_id,digest,size,version_id) VALUES(?,?,?,?,?)`, id, receipt.ArtifactID, digest, size, version.ID())
		if e != nil {
			return e
		}
		result, e := tx.ExecContext(ctx, `UPDATE owned_capture_lifecycle SET state='ready',updated_ms=? WHERE capture_id=? AND state='capturing'`, time.Now().UnixMilli(), id)
		if e != nil {
			return e
		}
		changed, e := result.RowsAffected()
		if e == nil && changed != 1 {
			e = ErrCurrent
		}
		return e
	})
	if err != nil {
		return Receipt{}, err
	}
	if err = acquisition.Check(ctx); err != nil {
		return Receipt{}, err
	}
	if err = checkOriginal(ctx); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

type checkedReader struct {
	ctx      context.Context
	source   Acquisition
	original func(context.Context) error
}

func (r *checkedReader) Read(p []byte) (int, error) {
	if e := r.source.Check(r.ctx); e != nil {
		return 0, e
	}
	if r.original != nil {
		if e := r.original(r.ctx); e != nil {
			return 0, e
		}
	}
	n, err := r.source.Read(p)
	if n < 0 || n > len(p) {
		return 0, ErrIncomplete
	}
	if r.original != nil {
		if e := r.original(r.ctx); e != nil {
			clear(p[:n])
			return 0, e
		}
	}
	return n, err
}

type persistedWriter struct {
	destination Destination
	ctx         context.Context
	writer      io.Writer
	source      Acquisition
	total       int64
	original    func(context.Context) error
}

func (w *persistedWriter) Write(p []byte) (int, error) {
	if w.original != nil {
		if e := w.original(w.ctx); e != nil {
			return 0, e
		}
	}
	if e := w.destination.check(); e != nil {
		return 0, e
	}
	n, e := w.writer.Write(p)
	if n > 0 {
		w.total += int64(n)
		e = errors.Join(e, w.source.Persisted(w.ctx, w.total))
	}
	if w.original != nil {
		e = errors.Join(e, w.original(w.ctx))
	}
	return n, e
}
