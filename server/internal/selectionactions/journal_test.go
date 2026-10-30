package selectionactions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// Local fixture until the lead composes the source DDL into the owned baseline.
const journalFixture = `-- Confirmed executable commands only. No query, traversal cursor or preview storage.
CREATE TABLE selection_action_groups (
 id INTEGER PRIMARY KEY,
 binding TEXT NOT NULL, operation_id TEXT NOT NULL, request_hash TEXT NOT NULL,
 account_id TEXT NOT NULL, library_id TEXT NOT NULL, action TEXT NOT NULL CHECK(action IN ('delete','download')),
 profile_id TEXT NOT NULL, server_id TEXT NOT NULL, authority TEXT NOT NULL, role TEXT NOT NULL,
 session_hash TEXT NOT NULL, session_epoch INTEGER NOT NULL, expires_at TEXT NOT NULL,
 principal_device_id TEXT NOT NULL, grant_kind TEXT NOT NULL,
 delete_files INTEGER, device_id TEXT NOT NULL, quality TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('capturing','sealed','running','cancelling')),
 total INTEGER NOT NULL CHECK(total>0), admitted INTEGER NOT NULL DEFAULT 0,
 expected_digest TEXT NOT NULL, seal_count INTEGER, seal_digest TEXT,
 done INTEGER NOT NULL DEFAULT 0, failed INTEGER NOT NULL DEFAULT 0,
 bytes_deleted INTEGER NOT NULL DEFAULT 0, files_deleted INTEGER NOT NULL DEFAULT 0,
 error_code TEXT NOT NULL DEFAULT '', terminal_state TEXT NOT NULL DEFAULT '',
 created_ms INTEGER NOT NULL,
 UNIQUE(binding,operation_id)
);
CREATE INDEX selection_action_groups_account ON selection_action_groups(account_id,id);
CREATE TABLE selection_action_targets (
 group_id INTEGER NOT NULL REFERENCES selection_action_groups(id), entity_id INTEGER NOT NULL,
 seq INTEGER NOT NULL, item_id TEXT NOT NULL, library_id TEXT NOT NULL, kind TEXT NOT NULL,
 title TEXT NOT NULL, identity TEXT NOT NULL, version_id TEXT NOT NULL,
 download_identity TEXT NOT NULL, lifetime TEXT NOT NULL,
 ready INTEGER NOT NULL DEFAULT 0, error_code TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(group_id,entity_id), UNIQUE(group_id,seq)
);
CREATE TABLE selection_action_files (
 group_id INTEGER NOT NULL, seq INTEGER NOT NULL, target_id INTEGER NOT NULL,
 asset_id INTEGER NOT NULL, token TEXT NOT NULL, path TEXT NOT NULL, root TEXT NOT NULL,
 root_identity TEXT NOT NULL, size INTEGER NOT NULL, modified_ns INTEGER NOT NULL,
 shared INTEGER NOT NULL, present INTEGER NOT NULL, part INTEGER NOT NULL,
 start REAL NOT NULL, end REAL, edition TEXT NOT NULL,
 source_id TEXT NOT NULL, source_incarnation TEXT NOT NULL, source_generation INTEGER NOT NULL,
 started INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(group_id,seq),
 FOREIGN KEY(group_id,target_id) REFERENCES selection_action_targets(group_id,entity_id)
);
CREATE INDEX selection_action_files_target ON selection_action_files(group_id,target_id,seq);
CREATE TABLE selection_action_history (
 id INTEGER PRIMARY KEY,
 binding TEXT NOT NULL, operation_id TEXT NOT NULL, request_hash TEXT NOT NULL,
 account_id TEXT NOT NULL, library_id TEXT NOT NULL, action TEXT NOT NULL,
 profile_id TEXT NOT NULL, server_id TEXT NOT NULL, authority TEXT NOT NULL, role TEXT NOT NULL,
 session_hash TEXT NOT NULL, session_epoch INTEGER NOT NULL, expires_at TEXT NOT NULL,
 principal_device_id TEXT NOT NULL, grant_kind TEXT NOT NULL,
 state TEXT NOT NULL, total INTEGER NOT NULL, admitted INTEGER NOT NULL,
 done INTEGER NOT NULL, failed INTEGER NOT NULL, bytes_deleted INTEGER NOT NULL,
 files_deleted INTEGER NOT NULL, error_code TEXT NOT NULL, expires_ms INTEGER NOT NULL,
 UNIQUE(binding,operation_id)
);
CREATE INDEX selection_action_history_expiry ON selection_action_history(expires_ms,id);
CREATE INDEX selection_action_history_account ON selection_action_history(account_id,id);
`

type journalSource struct {
	records  []Record
	after    int
	stepHook func()
}

func (s *journalSource) Bytes() int64 { return int64(len(s.records)) * 512 }
func (s *journalSource) Step(context.Context) (Batch, error) {
	if s.stepHook != nil {
		s.stepHook()
	}
	end := min(s.after+MaxRecords, len(s.records))
	b := Batch{Records: s.records[s.after:end], Done: end == len(s.records)}
	s.after = end
	return b, nil
}
func journalRecords(targets, files int) []Record {
	var out []Record
	for i := 1; i <= targets; i++ {
		id := int64(i)
		out = append(out, Record{Target: &Target{EntityID: id, ItemID: fmt.Sprint("item-", i), LibraryID: "library", Kind: "movie", Title: "Title", Identity: "incarnation", Lifetime: "123", DownloadIdentity: "pinned-content", VersionID: "version"}})
		for j := 0; j < files; j++ {
			out = append(out, Record{File: &File{TargetID: id, AssetID: int64(j + 1), Token: fmt.Sprint(i, "-", j), Path: "/media/file", Root: "/media", RootIdentity: "root-inode", SourceID: "source", SourceIncarnation: "source-life", SourceGeneration: 4, Size: 9, ModifiedNS: 7, Present: true}})
		}
		out = append(out, Record{Finish: id})
	}
	return out
}
func journalOpen(t *testing.T, path string, seed bool) *sql.DB {
	t.Helper()
	db, e := dbwork.OpenHandle(path, dbwork.DefaultPolicy())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = db.Close() })
	if seed {
		if _, e = db.Exec(journalFixture + `;CREATE TABLE journal_effects(operation_id TEXT,target_id INTEGER,file_key TEXT,PRIMARY KEY(operation_id,target_id,file_key));`); e != nil {
			t.Fatal(e)
		}
	}
	return db
}
func journalAuth(context.Context, *sql.Tx, identity.Principal, string, string, *Target) error {
	return nil
}
func journalApply(db *sql.DB) Apply {
	return func(ctx context.Context, p identity.Principal, action string, opts Options, target Target, file *File, checkpoint func(*sql.Tx, Effect) error) error {
		if target.OperationID == "" || target.DownloadIdentity != "pinned-content" || target.Lifetime != "123" {
			return fmt.Errorf("lost target facts")
		}
		key := "target"
		effect := Effect{}
		if file != nil {
			if file.SourceID != "source" || file.SourceIncarnation != "source-life" || file.SourceGeneration != 4 {
				return fmt.Errorf("lost source facts")
			}
			key = file.Token
			effect = Effect{Bytes: file.Size, Files: 1}
		}
		return dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			if _, e := tx.ExecContext(ctx, `INSERT INTO journal_effects VALUES(?,?,?)`, target.OperationID, target.EntityID, key); e != nil {
				return e
			}
			return checkpoint(tx, effect)
		})
	}
}
func journalAcceptance(op string, records []Record) *Acceptance {
	var digest Digest
	var total int64
	for _, r := range records {
		digest.Add(r)
		if r.Target != nil {
			total++
		}
	}
	yes := true
	return &Acceptance{Binding: "binding", Account: "account", Library: "library", Action: "delete", OperationID: op, RequestHash: "hash-" + op, Principal: identity.Principal{Viewer: identity.Viewer{AccountID: "account", ProfileID: "profile", ServerID: "server", Authority: "owner"}, Hash: "session", Epoch: 1}, Options: Options{DeleteFiles: &yes}, Total: total, Digest: digest.String(), Factory: func(context.Context) (Source, error) { return &journalSource{records: records}, nil }, Check: func(context.Context, *sql.Tx) error { return nil }, Reserve: func(n int64) error {
		if n != captureReservation {
			return ErrInput
		}
		return nil
	}, Release: func() {}}
}
func journalStep(t *testing.T, s *Service) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, e := s.step(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func journalUntil(t *testing.T, s *Service, op, state string) Job {
	t.Helper()
	for i := 0; i < 300; i++ {
		j, e := s.Lookup(context.Background(), "binding", op)
		if e != nil {
			t.Fatal(e)
		}
		if j.State == state {
			if Terminal(state) {
				var remaining int
				if err := s.db.QueryRow(`SELECT count(*) FROM selection_action_groups WHERE binding='binding' AND operation_id=?`, op).Scan(&remaining); err != nil {
					t.Fatal(err)
				}
				if remaining > 0 {
					journalStep(t, s)
					continue
				}
			}
			return j
		}
		journalStep(t, s)
	}
	t.Fatalf("did not reach %s", state)
	return Job{}
}

func TestJournalCaptureWriterFailureRetriesExactPendingBatch(t *testing.T) {
	for _, failCheck := range []int{2, 3, 4} { // pre-read, admission, seal
		t.Run(fmt.Sprint(failCheck), func(t *testing.T) {
			db := journalOpen(t, filepath.Join(t.TempDir(), "db"), true)
			s := New(db, nil, journalAuth, journalApply(db))
			defer s.Close()
			a := journalAcceptance("retry-capture", journalRecords(1, 1))
			checks, steps := 0, 0
			fault := errors.New("transient capture storage failure")
			a.Check = func(context.Context, *sql.Tx) error {
				checks++
				if checks == failCheck {
					return fault
				}
				return nil
			}
			a.Factory = func(context.Context) (Source, error) {
				return &journalSource{records: journalRecords(1, 1), stepHook: func() { steps++ }}, nil
			}
			if _, err := s.Accept(context.Background(), *a); err != nil {
				t.Fatal(err)
			}
			if _, err := s.step(context.Background()); !errors.Is(err, fault) {
				t.Fatal(err)
			}
			job, err := s.Lookup(context.Background(), a.Binding, a.OperationID)
			if err != nil || job.State != "capturing" || job.ErrorCode != "" || journalCount(t, db, "journal_effects") != 0 {
				t.Fatalf("transient failure discarded intent: %+v %v", job, err)
			}
			journalUntil(t, s, a.OperationID, "sealed")
			if steps != 1 {
				t.Fatalf("replayed or skipped source batch: %d steps", steps)
			}
			job = journalUntil(t, s, a.OperationID, "complete")
			if job.Done != 1 || job.FilesDeleted != 1 || journalCount(t, db, "journal_effects") != 2 {
				t.Fatalf("lost or duplicated command after retry: %+v", job)
			}
		})
	}
}

func TestJournalLateCancelPreservesRecordedOutcomeBeforePurge(t *testing.T) {
	db := journalOpen(t, filepath.Join(t.TempDir(), "db"), true)
	s := New(db, nil, journalAuth, journalApply(db))
	defer s.Close()
	a := journalAcceptance("late-cancel", journalRecords(1, 0))
	if _, err := s.Accept(context.Background(), *a); err != nil {
		t.Fatal(err)
	}
	journalUntil(t, s, a.OperationID, "sealed")
	journalStep(t, s) // target effect
	journalStep(t, s) // durable terminal publication, before bounded purge
	if journalCount(t, db, "selection_action_groups") != 1 {
		t.Fatal("fixture already purged")
	}
	job, err := s.Lookup(context.Background(), a.Binding, a.OperationID)
	if err != nil || job.State != "complete" || job.Done != 1 {
		t.Fatalf("outcome hidden: %+v %v", job, err)
	}
	job, err = s.Cancel(context.Background(), a.Binding, a.OperationID)
	if err != nil || job.State != "complete" || job.ErrorCode != "" {
		t.Fatalf("late cancel rewrote completion: %+v %v", job, err)
	}
	job, found, err := s.Replay(context.Background(), a.Binding, a.OperationID, a.RequestHash)
	if err != nil || !found || job.State != "complete" {
		t.Fatalf("replay lost outcome: %+v %v", job, err)
	}
	job = journalUntil(t, s, a.OperationID, "complete")
	if job.ErrorCode != "" || job.Done != 1 {
		t.Fatalf("cleanup rewrote outcome: %+v", job)
	}
}

type journalRefusedSource struct{ err error }

func (s journalRefusedSource) Bytes() int64                        { return 0 }
func (s journalRefusedSource) Step(context.Context) (Batch, error) { return Batch{}, s.err }

func TestJournalCaptureCapacityIsNotReportedAsSelectionDrift(t *testing.T) {
	for _, refusal := range []error{ErrCapacity, readprep.ErrCapacity} {
		t.Run(refusal.Error(), func(t *testing.T) {
			db := journalOpen(t, filepath.Join(t.TempDir(), "db"), true)
			s := New(db, nil, journalAuth, journalApply(db))
			defer s.Close()
			a := journalAcceptance("source-capacity", journalRecords(1, 0))
			a.Factory = func(context.Context) (Source, error) { return journalRefusedSource{refusal}, nil }
			if _, err := s.Accept(context.Background(), *a); err != nil {
				t.Fatal(err)
			}
			journalStep(t, s)
			job := journalUntil(t, s, a.OperationID, "failed")
			if job.ErrorCode != "capacity_exhausted" || job.Done != 0 || journalCount(t, db, "journal_effects") != 0 {
				t.Fatalf("wrong refusal or premature effects: %+v", job)
			}
		})
	}
}
func journalCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if e := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

func TestJournalSealAndAtomicEffects(t *testing.T) {
	db := journalOpen(t, filepath.Join(t.TempDir(), "journal.db"), true)
	s := New(db, nil, journalAuth, journalApply(db))
	defer s.Close()
	a := journalAcceptance("seal", journalRecords(2, 25))
	released := 0
	a.Release = func() { released++ }
	if j, e := s.Accept(context.Background(), *a); e != nil || j.State != "capturing" {
		t.Fatalf("accept %+v %v", j, e)
	}
	for i := 0; i < 2; i++ {
		journalStep(t, s)
		if journalCount(t, db, "journal_effects") != 0 {
			t.Fatal("effect before seal")
		}
	}
	sealed := journalUntil(t, s, "seal", "sealed")
	if sealed.Admitted != 2 || released != 1 {
		t.Fatalf("seal %+v releases %d", sealed, released)
	}
	var count int64
	var digest string
	if e := db.QueryRow(`SELECT seal_count,seal_digest FROM selection_action_groups`).Scan(&count, &digest); e != nil || count != 2 || digest != a.Digest {
		t.Fatalf("seal not durable: %d %s %v", count, digest, e)
	}
	final := journalUntil(t, s, "seal", "complete")
	if final.Done != 2 || final.Failed != 0 || final.FilesDeleted != 50 || final.BytesDeleted != 450 {
		t.Fatalf("bad aggregate %+v", final)
	}
	for _, table := range []string{"selection_action_groups", "selection_action_targets", "selection_action_files"} {
		if journalCount(t, db, table) != 0 {
			t.Fatal("detailed command retained", table)
		}
	}
	if journalCount(t, db, "journal_effects") != 52 {
		t.Fatal("lost or duplicated effects")
	}
	// Replay does not need any source/preparation and cannot accept changed body.
	replay := Acceptance{Binding: a.Binding, OperationID: a.OperationID, RequestHash: a.RequestHash}
	if j, e := s.Accept(context.Background(), replay); e != nil || j.State != "complete" {
		t.Fatalf("summary replay %+v %v", j, e)
	}
	if _, found, e := s.Replay(context.Background(), a.Binding, a.OperationID, a.RequestHash); e != nil || !found {
		t.Fatalf("preparation-free replay %v %v", found, e)
	}
	if _, _, e := s.Replay(context.Background(), a.Binding, a.OperationID, "changed"); !errors.Is(e, ErrConflict) {
		t.Fatalf("changed hash %v", e)
	}
	if _, e := s.Lookup(context.Background(), "foreign", a.OperationID); !errors.Is(e, ErrNotFound) {
		t.Fatal("foreign lookup", e)
	}
}
func TestJournalCaptureDriftAndDigestMismatchZeroEffects(t *testing.T) {
	for _, mode := range []string{"writer-drift", "digest", "record-bound"} {
		t.Run(mode, func(t *testing.T) {
			db := journalOpen(t, filepath.Join(t.TempDir(), "journal.db"), true)
			s := New(db, nil, journalAuth, journalApply(db))
			defer s.Close()
			records := journalRecords(1, 1)
			a := journalAcceptance(mode, records)
			released := 0
			a.Release = func() { released++ }
			switch mode {
			case "writer-drift":
				stale := false
				a.Factory = func(context.Context) (Source, error) {
					return &journalSource{records: records, stepHook: func() { stale = true }}, nil
				}
				a.Check = func(context.Context, *sql.Tx) error {
					if stale {
						return ErrStale
					}
					return nil
				}
			case "digest":
				a.Digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			case "record-bound":
				a.Factory = func(context.Context) (Source, error) { return oversizedSource{}, nil }
			}
			if _, e := s.Accept(context.Background(), *a); e != nil {
				t.Fatal(e)
			}
			j := journalUntil(t, s, mode, "failed")
			if j.Done != 0 || j.FilesDeleted != 0 || journalCount(t, db, "journal_effects") != 0 || released != 1 {
				t.Fatalf("unsafe capture failure %+v releases=%d", j, released)
			}
		})
	}
}

type oversizedSource struct{}

func (oversizedSource) Bytes() int64 { return 100 }
func (oversizedSource) Step(context.Context) (Batch, error) {
	return Batch{Records: make([]Record, MaxRecords+1)}, nil
}

func TestJournalRestartCapturingAbortsAndSealedResumes(t *testing.T) {
	for _, sealed := range []bool{false, true} {
		t.Run(fmt.Sprint(sealed), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "journal.db")
			db := journalOpen(t, path, true)
			s := New(db, nil, journalAuth, journalApply(db))
			records := journalRecords(1, 25)
			a := journalAcceptance("restart", records)
			calls := 0
			a.Factory = func(context.Context) (Source, error) { calls++; return &journalSource{records: records}, nil }
			if _, e := s.Accept(context.Background(), *a); e != nil {
				t.Fatal(e)
			}
			journalStep(t, s)
			if sealed {
				journalUntil(t, s, "restart", "sealed")
			}
			s.Close()
			if e := db.Close(); e != nil {
				t.Fatal(e)
			}
			db = journalOpen(t, path, false)
			s = New(db, nil, journalAuth, journalApply(db))
			defer s.Close()
			state := "failed"
			if sealed {
				state = "complete"
			}
			j := journalUntil(t, s, "restart", state)
			if calls != 1 {
				t.Fatal("source replayed")
			}
			if !sealed && (j.ErrorCode != "interrupted" || j.Done != 0 || journalCount(t, db, "journal_effects") != 0) {
				t.Fatalf("capturing restart %+v", j)
			}
			if sealed && j.FilesDeleted != 25 {
				t.Fatalf("sealed restart %+v", j)
			}
		})
	}
}
func TestJournalStartedRestartAndRollbackCheckpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.db")
	db := journalOpen(t, path, true)
	crash := errors.New("simulated crash")
	apply := func(ctx context.Context, p identity.Principal, action string, o Options, target Target, file *File, checkpoint func(*sql.Tx, Effect) error) error {
		if file == nil {
			t.Fatal("target before file")
		}
		if file.Started {
			t.Fatal("first attempt marked replay")
		}
		var started bool
		if e := db.QueryRow(`SELECT started FROM selection_action_files`).Scan(&started); e != nil || !started {
			t.Fatalf("intent not committed %v %v", started, e)
		}
		return dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			if _, e := tx.Exec(`INSERT INTO journal_effects VALUES('rollback',1,'file')`); e != nil {
				return e
			}
			if e := checkpoint(tx, Effect{Files: 1, Bytes: 9}); e != nil {
				return e
			}
			return crash
		})
	}
	s := New(db, nil, journalAuth, apply)
	a := journalAcceptance("started", journalRecords(1, 1))
	if _, e := s.Accept(context.Background(), *a); e != nil {
		t.Fatal(e)
	}
	journalUntil(t, s, "started", "sealed")
	s.mu.Lock()
	_, e := s.step(context.Background())
	s.mu.Unlock()
	if !errors.Is(e, crash) {
		t.Fatal(e)
	}
	if journalCount(t, db, "journal_effects") != 0 || journalCount(t, db, "selection_action_files") != 1 {
		t.Fatal("checkpoint escaped transaction rollback")
	}
	s.Close()
	db.Close()
	db = journalOpen(t, path, false)
	normal := journalApply(db)
	s = New(db, nil, journalAuth, func(ctx context.Context, p identity.Principal, action string, o Options, target Target, file *File, checkpoint func(*sql.Tx, Effect) error) error {
		if file != nil && !file.Started {
			t.Fatal("durable intent lost")
		}
		return normal(ctx, p, action, o, target, file, checkpoint)
	})
	defer s.Close()
	j := journalUntil(t, s, "started", "complete")
	if j.FilesDeleted != 1 || j.BytesDeleted != 9 || journalCount(t, db, "journal_effects") != 2 {
		t.Fatalf("restart %+v", j)
	}
}

func TestJournalCancellationAndPerFileRevocation(t *testing.T) {
	for _, mode := range []string{"before-seal", "partial", "revoked", "file-refusal"} {
		t.Run(mode, func(t *testing.T) {
			db := journalOpen(t, filepath.Join(t.TempDir(), "journal.db"), true)
			allowed := true
			authCalls := 0
			auth := func(ctx context.Context, tx *sql.Tx, p identity.Principal, action, library string, target *Target) error {
				if target != nil {
					authCalls++
				}
				if !allowed {
					return identity.ErrForbidden
				}
				return nil
			}
			normal := journalApply(db)
			apply := normal
			if mode == "file-refusal" {
				apply = func(ctx context.Context, p identity.Principal, action string, o Options, target Target, file *File, checkpoint func(*sql.Tx, Effect) error) error {
					if file == nil {
						t.Fatal("failed file granted final catalogue mutation")
					}
					return dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error { return checkpoint(tx, Effect{Code: "file_changed"}) })
				}
			}
			s := New(db, nil, auth, apply)
			defer s.Close()
			a := journalAcceptance("cancel", journalRecords(1, 25))
			if _, e := s.Accept(context.Background(), *a); e != nil {
				t.Fatal(e)
			}
			if mode != "before-seal" {
				journalUntil(t, s, "cancel", "sealed")
				journalStep(t, s)
			}
			state := "cancelled"
			switch mode {
			case "revoked":
				allowed = false
				state = "partial"
			case "partial":
				state = "partial"
			case "file-refusal":
				state = "failed"
			}
			if mode == "before-seal" || mode == "partial" {
				if j, e := s.Cancel(context.Background(), a.Binding, a.OperationID); e != nil || j.State != "cancelling" {
					t.Fatalf("cancel %+v %v", j, e)
				}
			}
			if mode == "revoked" {
				journalStep(t, s)
				allowed = true
			}
			j := journalUntil(t, s, "cancel", state)
			if mode == "before-seal" && (j.FilesDeleted != 0 || j.Done != 0) {
				t.Fatal("unsealed effects")
			}
			if (mode == "partial" || mode == "revoked") && (j.FilesDeleted != 1 || journalCount(t, db, "journal_effects") != 1) {
				t.Fatalf("effects continued %+v", j)
			}
			if mode == "file-refusal" && (j.Failed != 1 || j.FilesDeleted != 0 || j.ErrorCode != "file_changed") {
				t.Fatalf("file refusal %+v", j)
			}
			if mode != "before-seal" && authCalls < 3 {
				t.Fatal("missing per-target/file authority calls", authCalls)
			}
		})
	}
}
func TestJournalCapacityReplayAndHistoryRetention(t *testing.T) {
	db := journalOpen(t, filepath.Join(t.TempDir(), "journal.db"), true)
	now := time.Unix(1000, 0)
	cfg := DefaultConfig()
	cfg.Now = func() time.Time { return now }
	s := New(db, &cfg, journalAuth, journalApply(db))
	defer s.Close()
	for i := 0; i < 2; i++ {
		a := journalAcceptance(fmt.Sprint("cap-", i), journalRecords(1, 0))
		if _, e := s.Accept(context.Background(), *a); e != nil {
			t.Fatal(e)
		}
	}
	a := journalAcceptance("over", journalRecords(1, 0))
	released := 0
	a.Release = func() { released++ }
	if _, e := s.Accept(context.Background(), *a); !errors.Is(e, ErrCapacity) || released != 1 {
		t.Fatalf("capacity %v release %d", e, released)
	}
	// Different account can use remaining server capacity, but not bypass four.
	for i := 2; i < 5; i++ {
		a := journalAcceptance(fmt.Sprint("cap-", i), journalRecords(1, 0))
		a.Account = fmt.Sprint("account-", i)
		a.Principal.AccountID = a.Account
		_, e := s.Accept(context.Background(), *a)
		if i < 4 && e != nil || i == 4 && !errors.Is(e, ErrCapacity) {
			t.Fatalf("server cap %d: %v", i, e)
		}
	}
	for i := 0; i < 4; i++ {
		journalUntil(t, s, fmt.Sprint("cap-", i), "complete")
	}
	for i := 0; i < 65; i++ {
		op := fmt.Sprint("history-", i)
		a := journalAcceptance(op, journalRecords(1, 0))
		if _, e := s.Accept(context.Background(), *a); e != nil {
			t.Fatal(e)
		}
		journalUntil(t, s, op, "complete")
	}
	var count int
	if e := db.QueryRow(`SELECT count(*) FROM selection_action_history WHERE account_id='account'`).Scan(&count); e != nil || count != historyLimit {
		t.Fatalf("history cap %d %v", count, e)
	}
	now = now.Add(2 * time.Hour)
	if _, found, e := s.Replay(context.Background(), "binding", "history-64", "hash-history-64"); e != nil || found {
		t.Fatalf("expired replay %v %v", found, e)
	}
	for i := 0; i < 5; i++ {
		journalStep(t, s)
	}
	if journalCount(t, db, "selection_action_history") != 0 {
		t.Fatal("expired history retained")
	}
}
func TestJournalDigestIncludesFactsExcludesExecution(t *testing.T) {
	records := journalRecords(1, 1)
	var original, execution, changed Digest
	for _, r := range records {
		original.Add(r)
	}
	target := *records[0].Target
	target.OperationID = "execution-only"
	file := *records[1].File
	file.Started = true
	for _, r := range []Record{{Target: &target}, {File: &file}, {Finish: 1}} {
		execution.Add(r)
	}
	if original.String() != execution.String() {
		t.Fatal("execution state changed capture digest")
	}
	file.SourceGeneration++
	for _, r := range []Record{{Target: &target}, {File: &file}, {Finish: 1}} {
		changed.Add(r)
	}
	if original.String() == changed.String() {
		t.Fatal("source generation excluded")
	}
}

func TestJournalConcurrentCancelAcknowledgesInflightFile(t *testing.T) {
	db := journalOpen(t, filepath.Join(t.TempDir(), "journal.db"), true)
	observed := make(chan struct{})
	resume := make(chan struct{})
	result := make(chan error, 1)
	apply := func(ctx context.Context, p identity.Principal, action string, o Options, target Target, file *File, checkpoint func(*sql.Tx, Effect) error) error {
		if file == nil {
			t.Error("cancelled operation reached catalogue finalization")
			return ErrInput
		}
		close(observed) // external effect already observed, writer not held
		<-resume
		return dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error { return checkpoint(tx, Effect{Files: 1, Bytes: 9}) })
	}
	s := New(db, nil, journalAuth, apply)
	defer s.Close()
	a := journalAcceptance("inflight", journalRecords(1, 2))
	if _, e := s.Accept(context.Background(), *a); e != nil {
		t.Fatal(e)
	}
	journalUntil(t, s, "inflight", "sealed")
	go func() { s.mu.Lock(); _, e := s.step(context.Background()); s.mu.Unlock(); result <- e }()
	select {
	case <-observed:
	case <-time.After(5 * time.Second):
		t.Fatal("executor did not begin")
	}
	j, e := s.Cancel(context.Background(), a.Binding, a.OperationID)
	if e != nil || j.State != "cancelling" {
		t.Fatalf("cancel %+v %v", j, e)
	}
	close(resume)
	if e = <-result; e != nil {
		t.Fatal(e)
	}
	j = journalUntil(t, s, "inflight", "partial")
	if j.FilesDeleted != 1 || j.BytesDeleted != 9 || j.Done != 0 {
		t.Fatalf("inflight effect erased %+v", j)
	}
}
func TestJournalCloseReleasesCaptureAndRunStops(t *testing.T) {
	db := journalOpen(t, filepath.Join(t.TempDir(), "journal.db"), true)
	s := New(db, nil, journalAuth, journalApply(db))
	released := 0
	a := journalAcceptance("close", journalRecords(1, 25))
	a.Release = func() { released++ }
	if _, e := s.Accept(context.Background(), *a); e != nil {
		t.Fatal(e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	s.Close()
	if released != 1 {
		t.Fatalf("release count %d", released)
	}
	s = New(db, nil, journalAuth, journalApply(db))
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { done <- s.Run(ctx) }()
	// The restart supervisor, not a client poll, must retire unsealed work.
	deadline := time.After(5 * time.Second)
	for {
		j, e := s.Lookup(context.Background(), a.Binding, a.OperationID)
		if e != nil {
			t.Fatal(e)
		}
		if j.State == "failed" {
			if j.ErrorCode != "interrupted" {
				t.Fatal(j)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("restart did not retire capture")
		case <-time.After(time.Millisecond):
		}
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestJournalRunReportsCancellationInsteadOfRolledBackTransaction(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled=%t", cancelled), func(t *testing.T) {
			db := journalOpen(t, filepath.Join(t.TempDir(), "journal.db"), true)
			initial := New(db, nil, journalAuth, journalApply(db))
			a := journalAcceptance("shutdown", journalRecords(1, 25))
			if _, err := initial.Accept(context.Background(), *a); err != nil {
				t.Fatal(err)
			}
			if err := initial.Close(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := New(db, nil, journalAuth, journalApply(db))
			defer s.Close()
			called := false
			s.OnTerminal = func(context.Context, *sql.Tx, identity.Principal, Job, Options) error {
				called = true
				if cancelled {
					cancel()
				}
				// Model the SQL boundary observed when cancellation has already
				// rolled the transaction back. An active owner must still see it.
				return sql.ErrTxDone
			}
			err := s.Run(ctx)
			if !called {
				t.Fatal("restart did not reach the terminal transaction")
			}
			want := error(sql.ErrTxDone)
			if cancelled {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("Run returned %v, want %v", err, want)
			}
		})
	}
}

func TestJournalAdmissionReservesBeforeFactoryAndReleasesFailures(t *testing.T) {
	for _, mode := range []string{"reserve", "factory", "source-bytes", "check"} {
		t.Run(mode, func(t *testing.T) {
			db := journalOpen(t, filepath.Join(t.TempDir(), "journal.db"), true)
			s := New(db, nil, journalAuth, journalApply(db))
			defer s.Close()
			a := journalAcceptance("admit", journalRecords(1, 0))
			reserved, factories, released := 0, 0, 0
			a.Reserve = func(n int64) error {
				reserved++
				if mode == "reserve" {
					return ErrCapacity
				}
				return nil
			}
			a.Release = func() { released++ }
			a.Factory = func(context.Context) (Source, error) {
				factories++
				if reserved != 1 {
					t.Fatal("allocation before reservation")
				}
				if mode == "factory" {
					return nil, ErrStale
				}
				if mode == "source-bytes" {
					return tooLargeSource{}, nil
				}
				return &journalSource{records: journalRecords(1, 0)}, nil
			}
			if mode == "check" {
				a.Check = func(context.Context, *sql.Tx) error { return ErrStale }
			}
			if _, e := s.Accept(context.Background(), *a); e == nil {
				t.Fatal("accepted failing boundary")
			}
			if journalCount(t, db, "selection_action_groups") != 0 {
				t.Fatal("failed admission persisted command")
			}
			if mode == "reserve" {
				if factories != 0 || released != 0 {
					t.Fatal("reserved denial allocated or released unowned charge")
				}
			} else if factories != 1 || released != 1 {
				t.Fatalf("factory %d releases %d", factories, released)
			}
		})
	}
}

type tooLargeSource struct{}

func (tooLargeSource) Bytes() int64                        { return MaxSourceBytes + 1 }
func (tooLargeSource) Step(context.Context) (Batch, error) { panic("oversized source stepped") }

func TestJournalExpiredOperationKeyCanPublishNewSummary(t *testing.T) {
	db := journalOpen(t, filepath.Join(t.TempDir(), "journal.db"), true)
	now := time.Unix(1000, 0)
	cfg := DefaultConfig()
	cfg.Now = func() time.Time { return now }
	apply := func(ctx context.Context, p identity.Principal, action string, o Options, target Target, file *File, checkpoint func(*sql.Tx, Effect) error) error {
		return dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error { return checkpoint(tx, Effect{}) })
	}
	s := New(db, &cfg, journalAuth, apply)
	defer s.Close()
	a := journalAcceptance("reuse", journalRecords(1, 0))
	if _, e := s.Accept(context.Background(), *a); e != nil {
		t.Fatal(e)
	}
	journalUntil(t, s, "reuse", "complete")
	now = now.Add(2 * time.Hour)
	a.RequestHash = "new-body"
	if j, e := s.Accept(context.Background(), *a); e != nil || j.State != "capturing" {
		t.Fatalf("expired key %+v %v", j, e)
	}
	journalUntil(t, s, "reuse", "complete")
	if _, found, e := s.Replay(context.Background(), a.Binding, a.OperationID, "new-body"); e != nil || !found {
		t.Fatalf("new summary %v %v", found, e)
	}
}

func TestJournalCommittedRefusalKeepsObservedFileAndFailsTarget(t *testing.T) {
	db := journalOpen(t, filepath.Join(t.TempDir(), "journal.db"), true)
	allowed := true
	calls := 0
	auth := func(context.Context, *sql.Tx, identity.Principal, string, string, *Target) error {
		if !allowed {
			return identity.ErrForbidden
		}
		return nil
	}
	apply := func(ctx context.Context, p identity.Principal, action string, o Options, target Target, file *File, checkpoint func(*sql.Tx, Effect) error) error {
		calls++
		if file == nil {
			t.Fatal("refused unlink granted target deletion")
		}
		allowed = false
		e := dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error { return checkpoint(tx, Effect{Files: 1, Bytes: 9, Code: "authority_revoked"}) })
		if e != nil {
			return e
		}
		return identity.ErrForbidden
	}
	s := New(db, nil, auth, apply)
	defer s.Close()
	a := journalAcceptance("observed-refusal", journalRecords(1, 25))
	if _, e := s.Accept(context.Background(), *a); e != nil {
		t.Fatal(e)
	}
	journalUntil(t, s, a.OperationID, "sealed")
	for i := 0; i < 7; i++ {
		journalStep(t, s)
	}
	allowed = true
	j, e := s.Lookup(context.Background(), a.Binding, a.OperationID)
	if e != nil || j.State != "partial" || j.Failed != 1 || j.FilesDeleted != 1 || j.BytesDeleted != 9 || j.Done != 0 || calls != 1 {
		t.Fatalf("post-unlink refusal %+v calls=%d err=%v", j, calls, e)
	}
}

func TestJournalTypedDomainRefusalCompletesWithoutRetryingEffect(t *testing.T) {
	for _, files := range []int{0, 1} {
		t.Run(fmt.Sprint("files-", files), func(t *testing.T) {
			db := journalOpen(t, filepath.Join(t.TempDir(), "db"), true)
			calls := 0
			service := New(db, nil, journalAuth, func(context.Context, identity.Principal, string, Options, Target, *File, func(*sql.Tx, Effect) error) error {
				calls++
				return &TargetFailure{Code: "file_changed", Cause: ErrConflict}
			})
			defer service.Close()
			accepted := journalAcceptance("typed-refusal", journalRecords(1, files))
			if _, err := service.Accept(context.Background(), *accepted); err != nil {
				t.Fatal(err)
			}
			for turn := 0; turn < 20; turn++ {
				worked, err := service.step(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if !worked {
					break
				}
			}
			job, err := service.Lookup(context.Background(), accepted.Binding, accepted.OperationID)
			if err != nil || job.State != "failed" || job.Failed != 1 || job.Done != 0 || job.ErrorCode != "file_changed" || calls != 1 {
				t.Fatalf("job %+v calls%d err%v", job, calls, err)
			}
		})
	}
}
