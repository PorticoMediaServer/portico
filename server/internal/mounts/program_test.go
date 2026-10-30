package mounts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

func programFixture(t *testing.T) (*Service, Mount, string, context.Context) {
	t.Helper()
	s, input := fixture(t)
	m, err := s.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('account','account',X'00','profile')`); err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("p251_%013d_%032x", time.Now().UnixMilli(), 1)
	ctx := usermutation.WithRequest(dbwork.WithDurableUser(context.Background()), usermutation.Binding{Authority: "local", AccountID: "account", ProfileID: "profile", FamilyID: "family"}, usermutation.Envelope{Key: key, Operation: "put_storage_source_program", Digest: strings.Repeat("a", 64)})
	return s, m, key, ctx
}

func TestSourceProgramMutationConcurrentReplayCurrentReadAndTinyReceipt(t *testing.T) {
	s, m, key, ctx := programFixture(t)
	prepared, invalidated := 0, 0
	prepare := s.PrepareSourceCandidate
	s.PrepareSourceCandidate = func(ctx context.Context, in SourceCandidateInput, proof func(*sql.Tx) error) (*SourceCandidatePublication, error) {
		prepared++
		return prepare(ctx, in, proof)
	}
	s.NativeInvalidated = func(string) { invalidated++ }
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.ConfigureSourceProgram(ctx, "owner", "source", m.ID, key, m.Executable, false, m.ControlRevision, allowCommand, allowCommand); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if prepared != 1 || invalidated != 1 {
		t.Fatalf("duplicate repeated preparation/reload: %d/%d", prepared, invalidated)
	}
	// Current readback can advance without rewriting the historical receipt.
	if _, err := s.db.Exec(`UPDATE mount_controls SET revision=revision+1 WHERE mount_id=?`, m.ID); err != nil {
		t.Fatal(err)
	}
	replayed, err := s.ConfigureSourceProgram(ctx, "owner", "source", m.ID, key, m.Executable, false, m.ControlRevision, allowCommand, func(*sql.Tx) error { return ErrCommandConflict })
	if err != nil || replayed.ControlRevision != m.ControlRevision+1 || replayed.Mount.ControlRevision != m.ControlRevision+2 || prepared != 1 || invalidated != 1 {
		t.Fatalf("replay did not precede mutable checks/current read: %+v %v", replayed, err)
	}
	var full, n, bytes int
	if err = s.db.QueryRow(`SELECT count(*) FROM mount_operations WHERE operation_id=?`, key).Scan(&full); err != nil || full != 0 {
		t.Fatal("full response persisted", full, err)
	}
	if err = s.db.QueryRow(`SELECT count(*),sum(length(key)+length(operation)+length(digest)+length(status)+length(target_id)+length(profile_id)+length(family_id)+8*8) FROM user_mutation_receipts WHERE key=?`, key).Scan(&n, &bytes); err != nil || n != 1 || bytes > usermutation.MaxReceiptRecordBytes {
		t.Fatal("receipt row bound", n, bytes, err)
	}
	raw, err := json.Marshal(usermutation.Receipt{Key: key, Operation: "put_storage_source_program", Result: usermutation.Result{Status: "applied", TargetID: "source", Revision: m.ControlRevision + 1}, CommittedMs: time.Now().UnixMilli(), ExpiresMs: time.Now().UnixMilli() + usermutation.RetentionMs})
	if err != nil || len(raw) > usermutation.MaxReceiptRecordBytes || strings.Contains(string(raw), m.Executable) || strings.Contains(string(raw), "leaky-secret") {
		t.Fatal("receipt projection is not tiny", err)
	}
	var recordBytes int
	if err = s.db.QueryRow(`SELECT max(mx_payload) FROM dbstat WHERE name='user_mutation_receipts'`).Scan(&recordBytes); err != nil || recordBytes > usermutation.MaxReceiptRecordBytes {
		t.Fatal("physical receipt record bound", recordBytes, err)
	}
	t.Logf("receipt SQLite record bytes=%d, retained field bound=%d, wire bytes=%d; charged=%d", recordBytes, bytes, len(raw), usermutation.ReceiptChargeBytes)
	// Rebuild the service over the same persisted state, as after a lost reply
	// and restart. A recovered receipt needs no candidate callback or new CAS.
	restarted, err := New(s.db, filepath.Dir(filepath.Dir(s.private)), s.root, s.helper, storage.New(s.helper))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.shutdown)
	replayed, err = restarted.ConfigureSourceProgram(ctx, "owner", "source", m.ID, key, m.Executable, false, m.ControlRevision, allowCommand, func(*sql.Tx) error { return ErrCommandConflict })
	if err != nil || replayed.Mount.ControlRevision != m.ControlRevision+2 {
		t.Fatal("restart lost original acceptance", replayed, err)
	}
}

func TestSourceProgramMutationAuthorityDigestCapacityAndLegacyBoundary(t *testing.T) {
	s, m, key, ctx := programFixture(t)
	prepared := false
	s.PrepareSourceCandidate = func(context.Context, SourceCandidateInput, func(*sql.Tx) error) (*SourceCandidatePublication, error) {
		prepared = true
		return nil, ErrConfigInvalid
	}
	call := func(ctx context.Context, operation string, proof func(*sql.Tx) error) error {
		_, err := s.ConfigureSourceProgram(ctx, "owner", "source", m.ID, operation, m.Executable, false, m.ControlRevision, proof, allowCommand)
		return err
	}
	denied := usermutation.WithAuthority(ctx, func(context.Context, *sql.Tx) error { return identity.ErrUnauthorized })
	if err := call(denied, key, allowCommand); !errors.Is(err, identity.ErrUnauthorized) || prepared {
		t.Fatal("authority did not precede reservation/preparation", err)
	}
	if err := call(ctx, key, func(*sql.Tx) error { return ErrCommandConflict }); !errors.Is(err, ErrCommandConflict) || prepared {
		t.Fatal("mount authority did not precede preparation", err)
	}
	if err := call(ctx, key+"0", allowCommand); !errors.Is(err, usermutation.ErrConflict) || prepared {
		t.Fatal("body/header mismatch accepted", err)
	}
	if _, err := s.Command(ctx, "owner", m.ID, Command{OperationID: key, Action: "stop", ExpectedRevision: m.ControlRevision}, allowCommand); !errors.Is(err, ErrInvalid) {
		t.Fatal("unrelated storage command accepted protocol key", err)
	}
	if err := call(context.Background(), key, allowCommand); !errors.Is(err, ErrInvalid) {
		t.Fatal("protocol key accepted without context", err)
	}
	binding, envelope, _ := usermutation.RequestFrom(ctx)
	envelope.Key = fmt.Sprintf("p251_%013d_%032x", time.Now().Add(-31*24*time.Hour).UnixMilli(), 2)
	expired := usermutation.WithRequest(ctx, binding, envelope)
	if err := call(expired, envelope.Key, allowCommand); !errors.Is(err, usermutation.ErrExpired) || prepared {
		t.Fatal("expired key reached external configuration work", err)
	}
	if _, err := s.db.Exec(`UPDATE user_mutation_global SET ordinary_bytes=? WHERE singleton=1`, usermutation.OrdinaryByteLimit); err != nil {
		t.Fatal(err)
	}
	if err := call(ctx, key, allowCommand); !errors.Is(err, usermutation.ErrCapacity) || prepared {
		t.Fatal("quota refusal reached external configuration work", err)
	}
}

func TestSourceProgramMutationReceiptFailureRollsBackInstallationAndDomain(t *testing.T) {
	s, m, key, ctx := programFixture(t)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_program_receipt BEFORE UPDATE OF status ON user_mutation_receipts WHEN NEW.status='applied' BEGIN SELECT RAISE(ABORT,'receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := s.ConfigureSourceProgram(ctx, "owner", "source", m.ID, key, m.Executable, false, m.ControlRevision, allowCommand, allowCommand)
	if err == nil {
		t.Fatal("receipt storage error succeeded")
	}
	current, err := s.get(m.ID)
	if err != nil || current.ControlRevision != m.ControlRevision || current.BackendRevision != m.BackendRevision {
		t.Fatal("partial domain commit", current, err)
	}
	var n int
	if err = s.db.QueryRow(`SELECT count(*) FROM user_mutation_receipts WHERE key=?`, key).Scan(&n); err != nil || n != 0 {
		t.Fatal("partial receipt commit", n, err)
	}
	files, err := filepath.Glob(filepath.Join(s.private, m.ID+".g-*.conf"))
	if err != nil || len(files) != 0 {
		t.Fatal("failed acceptance left staged installation", files, err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER fail_program_receipt`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfigureSourceProgram(ctx, "owner", "source", m.ID, key, m.Executable, false, m.ControlRevision, allowCommand, allowCommand); err != nil {
		t.Fatal("same-key retry failed", err)
	}
	binding, envelope, _ := usermutation.RequestFrom(ctx)
	envelope.Digest = strings.Repeat("b", 64)
	changed := usermutation.WithRequest(ctx, binding, envelope)
	if _, err = s.ConfigureSourceProgram(changed, "owner", "source", m.ID, key, m.Executable, false, m.ControlRevision, allowCommand, allowCommand); !errors.Is(err, usermutation.ErrConflict) {
		t.Fatal("changed immutable request replayed", err)
	}
}

type programCloser struct {
	calls int
	fail  bool
}

func (c *programCloser) Write(p []byte) (int, error) { return len(p), nil }

func (c *programCloser) Close() error {
	c.calls++
	if c.fail {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func TestSourceProgramMutationRuntimeFailureReconcilesWithoutNewAcceptance(t *testing.T) {
	s, m, key, ctx := programFixture(t)
	closer := &programCloser{fail: true}
	// This synthetic child is never launched; cleanup must not wait for it.
	s.active[m.ID] = &child{control: closer, done: make(chan error), restartGeneration: 0}
	t.Cleanup(func() { delete(s.active, m.ID) })
	if _, err := s.ConfigureSourceProgram(ctx, "owner", "source", m.ID, key, m.Executable, false, m.ControlRevision, allowCommand, allowCommand); err == nil || !strings.Contains(err.Error(), "runtime retirement pending") {
		t.Fatal("postcommit failure silently succeeded", err)
	}
	closer.fail = false
	r, err := s.ConfigureSourceProgram(ctx, "owner", "source", m.ID, key, m.Executable, false, m.ControlRevision, allowCommand, allowCommand)
	if err != nil || r.ControlRevision != m.ControlRevision+1 || closer.calls != 2 {
		t.Fatal("same receipt did not finish retirement", r, err, closer.calls)
	}
	// A successor must survive receipt replay.
	s.active[m.ID] = &child{control: closer, done: make(chan error), restartGeneration: 1}
	if _, err = s.ConfigureSourceProgram(ctx, "owner", "source", m.ID, key, m.Executable, false, m.ControlRevision, allowCommand, allowCommand); err != nil || closer.calls != 2 {
		t.Fatal("replay retired current runtime", err, closer.calls)
	}
}
