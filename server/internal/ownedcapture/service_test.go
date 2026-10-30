//go:build linux || darwin

package ownedcapture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Regression SOURCES only during campaign work. These exercise real managed
// artifacts and normalized SQL receipts; fake policy is not production authority.
func init() { persistence.UseSchemaTemplate() }

type policyRecord struct {
	b                        Binding
	live, retained, released bool
	size                     int64
}
type policyFixture struct {
	data             []byte
	sizeDelta        int64
	deny             bool
	readFailure      bool
	records          map[string]*policyRecord
	beforeCommit     func(*fakeAcquisition) error
	leases           []context.CancelFunc
	reads, persisted int
}
type fakeLease struct {
	p      *policyFixture
	b      Binding
	life   context.Context
	cancel context.CancelFunc
}

func (l *fakeLease) Context() context.Context { return l.life }
func (l *fakeLease) Check(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if l.life.Err() != nil {
		return l.life.Err()
	}
	r := l.p.records[l.b.ReservationID]
	if r == nil || r.b != l.b || !r.live {
		return ErrCurrent
	}
	return nil
}
func (l *fakeLease) CheckTx(ctx context.Context, _ *sql.Tx) error { return l.Check(ctx) }
func (l *fakeLease) Close() error                                 { l.cancel(); return nil }

type fakeAcquisition struct {
	*fakeLease
	body                     *bytes.Reader
	size, delivered, written int64
	eof                      bool
}

func (a *fakeAcquisition) Binding() Binding     { return a.b }
func (a *fakeAcquisition) Size() int64          { return a.size }
func (a *fakeAcquisition) ExpiresAt() time.Time { return time.Now().Add(time.Hour) }
func (a *fakeAcquisition) Read(p []byte) (int, error) {
	if e := a.Check(context.Background()); e != nil {
		return 0, e
	}
	a.p.reads++
	if a.p.readFailure && a.delivered > 0 {
		return 0, io.ErrUnexpectedEOF
	}
	if a.p.readFailure && len(p) > 2 {
		p = p[:2]
	}
	n, e := a.body.Read(p)
	a.delivered += int64(n)
	if errors.Is(e, io.EOF) {
		a.eof = true
	}
	return n, e
}
func (a *fakeAcquisition) Persisted(ctx context.Context, total int64) error {
	if e := a.Check(ctx); e != nil {
		return e
	}
	if total < a.written || total > a.delivered {
		return ErrInput
	}
	a.written = total
	a.p.persisted++
	return nil
}
func (a *fakeAcquisition) CommitTx(ctx context.Context, tx *sql.Tx) error {
	if e := a.CheckTx(ctx, tx); e != nil {
		return e
	}
	if !a.eof || a.delivered != a.size || a.written != a.size {
		return ErrIncomplete
	}
	if a.p.beforeCommit != nil {
		if e := a.p.beforeCommit(a); e != nil {
			return e
		}
	}
	return dbwork.AfterCommit(tx, func() { a.p.records[a.b.ReservationID].retained = true })
}
func (p *policyFixture) Admit(ctx context.Context, id string, request Request, destination Destination) (Acquisition, error) {
	if p.deny {
		return nil, ErrCurrent
	}
	if destination.root == "" {
		return nil, ErrInput
	}
	b := Binding{SourceID: "source", ConfigGeneration: "config-1", ObjectID: "physical-object", ObjectRevision: "observation-1", AssetID: request.AssetID, AcquisitionID: uniqueID(), ReservationID: uniqueID(), PolicyWitness: "saved-policy-1", AuthorityWitness: "original-family-1"}
	p.records[b.ReservationID] = &policyRecord{b: b, live: true, size: int64(len(p.data)) + p.sizeDelta}
	life, cancel := context.WithCancel(ctx)
	p.leases = append(p.leases, cancel)
	return &fakeAcquisition{fakeLease: &fakeLease{p: p, b: b, life: life, cancel: cancel}, body: bytes.NewReader(p.data), size: int64(len(p.data)) + p.sizeDelta}, nil
}
func (p *policyFixture) CheckTx(ctx context.Context, _ *sql.Tx, b Binding) error {
	r := p.records[b.ReservationID]
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if r == nil || r.b != b || !r.live || !r.retained || r.released {
		return ErrCurrent
	}
	return nil
}
func (p *policyFixture) Lease(ctx context.Context, b Binding) (SourceLease, error) {
	if e := p.CheckTx(ctx, nil, b); e != nil {
		return nil, e
	}
	life, cancel := context.WithCancel(ctx)
	p.leases = append(p.leases, cancel)
	return &fakeLease{p: p, b: b, life: life, cancel: cancel}, nil
}
func (p *policyFixture) ReleaseTx(_ context.Context, tx *sql.Tx, b Binding) error {
	r := p.records[b.ReservationID]
	if r == nil || r.b != b {
		return ErrCurrent
	}
	// Cleanup retains original reservation identity even after actor revocation.
	return dbwork.AfterCommit(tx, func() { r.released = true })
}
func fixture(t *testing.T) (*Service, *policyFixture) {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	db, e := persistence.OpenFresh(filepath.Join(root, "state.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	policy := &policyFixture{data: []byte("one complete finite owned ISO byte fixture"), records: map[string]*policyRecord{}}
	service, e := New(Options{DB: db, Directory: filepath.Join(root, "captures"), Producer: policy})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { service.Close() })
	return service, policy
}
func countRows(t *testing.T, s *Service, table string) int {
	t.Helper()
	var n int
	if e := s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func capture(t *testing.T, s *Service) Receipt {
	t.Helper()
	r, e := s.Capture(context.Background(), Request{AssetID: "physical-asset"})
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func TestCompleteOwnedBytesHaveExactReceiptIndependentCursorsAndNoLatestSubstitution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p := fixture(t)
	first := capture(t, s)
	sum := sha256.Sum256(p.data)
	if first.Object.Digest != hex.EncodeToString(sum[:]) || first.Object.Size != int64(len(p.data)) {
		t.Fatal("receipt did not name actual captured bytes")
	}
	one, e := s.Open(context.Background(), first)
	if e != nil {
		t.Fatal(e)
	}
	defer one.Close()
	two, e := s.Open(context.Background(), first)
	if e != nil {
		t.Fatal(e)
	}
	defer two.Close()
	b := make([]byte, 4)
	if _, e = one.File().Read(b); e != nil {
		t.Fatal(e)
	}
	other := make([]byte, 4)
	if _, e = two.File().Read(other); e != nil || !bytes.Equal(b, other) {
		t.Fatal("readers shared a seek cursor", e)
	}
	if _, e = one.File().Write([]byte("x")); e == nil {
		t.Fatal("descriptor was writable")
	}
	if one.Context().Done() == nil {
		t.Fatal("reader had no retirable lifetime")
	}
	if e = one.Check(context.Background()); e != nil {
		t.Fatal(e)
	}
	// Close the readers before another independent acquisition of equal bytes;
	// equal content confers no shared capture/facts/authority identity.
	one.Close()
	two.Close()
	second := capture(t, s)
	if first.ID == second.ID || first.Version.ID() == second.Version.ID() || first.Binding == second.Binding {
		t.Fatal("new acquisition reused capture identity")
	}
	forged := first
	forged.ID = second.ID
	if r, e := s.Open(context.Background(), forged); e == nil {
		r.Close()
		t.Fatal("capture ID silently selected another source receipt")
	}
	wrong := first
	wrong.Binding.AuthorityWitness = "new-family"
	if r, e := s.Open(context.Background(), wrong); e == nil {
		r.Close()
		t.Fatal("receipt was rebound to a different authority")
	}
	if p.persisted == 0 {
		t.Fatal("actual successful writes were never acknowledged")
	}
}

func TestIncompleteOverlongFailedAndDeniedAcquisitionsPublishNoReceipt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, kind := range []string{"short", "long", "error", "denied"} {
		t.Run(kind, func(t *testing.T) {
			s, p := fixture(t)
			switch kind {
			case "short":
				p.sizeDelta = 1
			case "long":
				p.sizeDelta = -1
			case "error":
				p.readFailure = true
			case "denied":
				p.deny = true
			}
			if r, e := s.Capture(context.Background(), Request{AssetID: "physical-asset"}); e == nil || r.ID != "" {
				t.Fatal("invalid acquisition became usable", r.ID, e)
			}
			if n := countRows(t, s, "owned_capture_receipts"); n != 0 {
				t.Fatal("published a partial receipt", n)
			}
			if kind == "denied" && p.reads != 0 {
				t.Fatal("read before source permission")
			}
			inventory, e := s.artifacts.Inventory()
			if e != nil {
				t.Fatal(e)
			}
			defer inventory.Close()
			objects, _ := inventory.Next(context.Background(), 64)
			if len(objects) != 0 {
				t.Fatal("failed capture left a closed uncharged object")
			}
			for _, r := range p.records {
				if !r.released {
					t.Fatal("failed capture reservation not cleaned after physical bytes removed")
				}
			}
		})
	}
}

func TestRetirementCancelsExactReaderButQuotaWaitsForPhysicalReaderExit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p := fixture(t)
	receipt := capture(t, s)
	reader, e := s.Open(context.Background(), receipt)
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	if e = s.Retire(context.Background(), receipt); e != nil {
		t.Fatal(e)
	}
	select {
	case <-reader.Context().Done():
	default:
		t.Fatal("retirement did not cancel original reader")
	}
	if e = reader.Check(context.Background()); e == nil {
		t.Fatal("retired reader passed currentness")
	}
	if e = s.Collect(context.Background()); e != nil {
		t.Fatal(e)
	}
	if p.records[receipt.Binding.ReservationID].released {
		t.Fatal("quota released under a pinned physical reader")
	}
	reader.Close()
	if e = s.Collect(context.Background()); e != nil {
		t.Fatal(e)
	}
	if !p.records[receipt.Binding.ReservationID].released {
		t.Fatal("retired owned bytes never released source quota")
	}
	if r, e := s.Open(context.Background(), receipt); e == nil {
		r.Close()
		t.Fatal("retired receipt reopened")
	}
}

func TestRevokedOriginalPolicyNeverPublishesOrRebindsACompletedArtifact(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p := fixture(t)
	p.beforeCommit = func(a *fakeAcquisition) error { p.records[a.b.ReservationID].live = false; return ErrCurrent }
	if _, e := s.Capture(context.Background(), Request{AssetID: "physical-asset"}); e == nil {
		t.Fatal("revoked producer published")
	}
	if countRows(t, s, "owned_capture_receipts") != 0 {
		t.Fatal("revoked source retained a usable receipt")
	}
	for _, r := range p.records {
		if !r.released {
			t.Fatal("retired actor blocked safe exact-reservation cleanup")
		}
	}
}

func TestUncertainPostSealCleanupCannotUnchargeUntilRealPhysicalLeaseRetires(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p := fixture(t)
	var held *mediaartifact.ReadDescriptor
	p.beforeCommit = func(a *fakeAcquisition) error {
		sum := sha256.Sum256(p.data)
		var e error
		held, e = s.artifacts.OpenDescriptor(context.Background(), mediaartifact.Object{Digest: hex.EncodeToString(sum[:]), Size: int64(len(p.data))})
		if e != nil {
			t.Fatal(e)
		}
		return ErrCurrent
	}
	if _, e := s.Capture(context.Background(), Request{AssetID: "physical-asset"}); e == nil {
		t.Fatal("failed publication accepted")
	}
	defer held.Close()
	for _, r := range p.records {
		if r.released {
			t.Fatal("physical lease was ignored")
		}
	}
	held.Close()
	if e := s.Collect(context.Background()); e != nil {
		t.Fatal(e)
	}
	for _, r := range p.records {
		if !r.released {
			t.Fatal("uncertain journal could not recover after physical retirement")
		}
	}
}

func TestProvenanceAndReceiptRowsRejectMutation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, _ := fixture(t)
	receipt := capture(t, s)
	for _, query := range []string{`UPDATE owned_capture_sources SET source_id='replacement' WHERE capture_id=?`, `UPDATE owned_capture_receipts SET artifact_id='replacement' WHERE capture_id=?`} {
		_, e := dbwork.ExecWrite(context.Background(), s.db, dbwork.ClassMaintenance, query, receipt.ID)
		if e == nil {
			t.Fatal("immutable capture identity was mutable")
		}
	}
}

func TestDestinationRejectsAReplacedOwnedDirectoryWithoutDeletingReplacement(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, _ := fixture(t)
	originalKey, e := s.destination.VolumeKey()
	if e != nil || originalKey == "" {
		t.Fatal("missing actual filesystem identity", e)
	}
	staging := filepath.Join(s.artifacts.Root(), "staging")
	if e = os.Rename(staging, staging+"-original"); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(staging, 0700); e != nil {
		t.Fatal(e)
	}
	sentinel := filepath.Join(staging, "preserve-original.txt")
	if e = os.WriteFile(sentinel, []byte("preserve"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.destination.VolumeKey(); e == nil {
		t.Fatal("original directory identity was silently rebound")
	}
	if s.destination.SpaceFor(0, 1) {
		t.Fatal("replacement destination supplied growth permission")
	}
	if e = s.Collect(context.Background()); e == nil {
		t.Fatal("cleanup accepted replaced owned directories")
	}
	if got, e := os.ReadFile(sentinel); e != nil || string(got) != "preserve" {
		t.Fatal("cleanup modified replacement content", e)
	}
}

func TestCaptureAndOpenRefuseAnOuterTransactionBeforeSourceOrByteWork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p := fixture(t)
	outer, e := dbwork.Begin(context.Background(), s.db, dbwork.ClassInteractive)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Capture(outer.Context(), Request{AssetID: "physical-asset"}); e == nil {
		outer.Rollback()
		t.Fatal("source IO joined outer accepting transaction")
	}
	if len(p.records) != 0 || p.reads != 0 {
		outer.Rollback()
		t.Fatal("source admission ran before outer-transaction refusal")
	}
	outer.Rollback()
	receipt := capture(t, s)
	outer, e = dbwork.Begin(context.Background(), s.db, dbwork.ClassInteractive)
	if e != nil {
		t.Fatal(e)
	}
	defer outer.Rollback()
	if reader, e := s.Open(outer.Context(), receipt); e == nil {
		reader.Close()
		t.Fatal("descriptor escaped before its reader pin transaction committed")
	}
	if e = s.Collect(outer.Context()); e == nil {
		t.Fatal("physical cleanup joined outer transaction")
	}
}
