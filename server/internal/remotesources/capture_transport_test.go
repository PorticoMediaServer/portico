package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// DB-only source regressions. No native approval, rclone process or provider
// body is fabricated here; actual no-cache/from-start transport qualification
// remains separate from these admission/currentness checks.
func TestOwnedCapturePlanPreservesSourceRangeClassification(t *testing.T) {
	for _, kind := range []string{"sftp", "smb", "rclone", "webdav"} {
		t.Run(kind, func(t *testing.T) {
			f, _ := isoObservationFixture(t)
			writer, err := dbwork.Begin(context.Background(), f.s.db, dbwork.ClassPlaybackStart)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Rollback()
			if _, err = writer.Tx().Exec(`UPDATE remote_sources SET kind=?,range_support='conditional',webdav_leaf_count=0 WHERE id='remote'`, kind); err != nil {
				t.Fatal(err)
			}
			plan, err := captureReadPlanTx(context.Background(), writer.Tx(), "remote", f.file)
			if kind == "webdav" {
				if !errors.Is(err, storage.ErrRemoteRange) {
					t.Fatal("strong direct DAV acquired a full-capture plan", plan, err)
				}
			} else if err != nil || plan.mode != captureManagedUnversioned || plan.sourceSequential {
				t.Fatal("missing conditional adapter was not an explicit acquisition mode", plan, err)
			}
			var support string
			if err = writer.Tx().QueryRow(`SELECT range_support FROM remote_sources WHERE id='remote'`).Scan(&support); err != nil || support != "conditional" {
				t.Fatal("capture changed saved range support", support, err)
			}
			sequential, err := sourceObjectSequentialTx(context.Background(), writer.Tx(), "remote", f.file)
			if err != nil || sequential {
				t.Fatal("capture promoted source classification", sequential, err)
			}
			if len(f.s.serveLeases) != 0 || len(f.s.readLeases) != 0 {
				t.Fatal("DB-only planning allocated process or reader custody")
			}
		})
	}
}

func captureManagedLeaseFixture(t *testing.T) (observationFixture, *SourceReadLease) {
	t.Helper()
	f, ref := isoObservationFixture(t)
	identities, owner, _ := observationOwner(t, f)
	// Named columns from the real baseline; the existing mount insert trigger
	// creates its backend/control rows at generation 1. No backend is opened.
	if _, err := f.s.db.Exec(`INSERT INTO managed_mounts(id,name,executable,digest,remote,mount_path) VALUES('capture-mount','Fixture','/unused/rclone','fixture','fixture:',?)`, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.Exec(`UPDATE remote_sources SET kind='sftp',mount_id='capture-mount',range_support='conditional' WHERE id='remote'`); err != nil {
		t.Fatal(err)
	}
	view, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Rollback()
	lease, err := f.s.captureOwnedReadSourceTx(context.Background(), view.Tx(), SourceSessionAdmission{
		Purpose: SourcePlayback, Observation: ref, OnChanged: func() {},
		CheckTx: func(ctx context.Context, tx *sql.Tx, binding SourceReadBinding) error {
			if binding.Observation != ref || binding.Path != f.file || binding.Sequential || binding.captureMode != captureManagedUnversioned {
				return storage.ErrRemoteChanged
			}
			return currentLocalOwnerTx(ctx, tx, identities, owner)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lease.Close() })
	return f, lease
}

func TestOwnedCaptureLeaseKeepsActualClassificationAndCannotOpenRange(t *testing.T) {
	f, lease := captureManagedLeaseFixture(t)
	if lease.Binding().Sequential || lease.Binding().Profile.AllowAdvancedMediaAnalysis {
		t.Fatal("capture invented sequential classification or analysis consent")
	}
	if err := lease.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := lease.OpenRange(context.Background()); !errors.Is(err, storage.ErrRemoteRange) {
		t.Fatal("private owned-capture lease opened a range", err)
	}
	if len(f.s.serveLeases) != 0 {
		t.Fatal("range refusal started a process")
	}
}

func TestOwnedCapturePlanReprovesClassificationAndRetiresOnABA(t *testing.T) {
	f, lease := captureManagedLeaseFixture(t)
	writer, err := dbwork.Begin(context.Background(), f.s.db, dbwork.ClassPlaybackStart)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Tx().Exec(`UPDATE remote_sources SET range_support='unsupported' WHERE id='remote'`); err != nil {
		writer.Rollback()
		t.Fatal(err)
	}
	err = lease.CheckTx(context.Background(), writer.Tx())
	writer.Rollback()
	if !errors.Is(err, storage.ErrRemoteChanged) {
		t.Fatal("accepting writer adopted a different acquisition mode", err)
	}
	if err = lease.Check(context.Background()); !errors.Is(err, storage.ErrRemoteChanged) {
		t.Fatal("rolled-back classification change revived original capture lease", err)
	}
}

func TestOwnedCaptureModeCannotBorrowExistingNormalProcess(t *testing.T) {
	f, lease := captureManagedLeaseFixture(t)
	life, cancel := context.WithCancel(context.Background())
	defer cancel()
	binding := lease.Binding()
	slot := &sourceProcessLease{service: f.s, source: "remote", generation: binding.Observation.Generation, root: binding.Root, profile: binding.Profile, life: life, cancel: cancel}
	f.s.serveLeases = map[string]*sourceProcessLease{"remote": slot}
	// A not-yet-published normal slot is still reserved. The capture may not
	// mutate its mode, cancel it, release it or allocate a replacement slot.
	if _, err := f.s.ensureSourceServe(context.Background(), binding, ""); !errors.Is(err, storage.ErrBusy) {
		t.Fatal("capture borrowed an incompatible process", err)
	}
	if slot.captureMode != "" || slot.released.Load() || life.Err() != nil || f.s.serveLeases["remote"] != slot {
		t.Fatal("mode conflict changed existing process custody")
	}
}

func TestOwnedCaptureFinalLeaseRetiresOnlyItsProcessWithoutReleasingSlot(t *testing.T) {
	f, first := captureManagedLeaseFixture(t)
	secondLife, secondCancel := context.WithCancel(context.Background())
	defer secondCancel()
	secondBinding := first.Binding()
	secondBinding.LeaseID++
	second := &SourceReadLease{service: f.s, binding: secondBinding, life: secondLife, cancel: secondCancel}
	f.s.readLeases["remote"][secondBinding.LeaseID] = second
	life, cancel := context.WithCancel(context.Background())
	defer cancel()
	binding := first.Binding()
	slot := &sourceProcessLease{service: f.s, source: "remote", generation: binding.Observation.Generation, root: binding.Root, profile: binding.Profile, captureMode: captureManagedUnversioned, life: life, cancel: cancel}
	f.s.serveLeases = map[string]*sourceProcessLease{"remote": slot}
	first.Close()
	if life.Err() != nil {
		t.Fatal("one capture lease retired another's live process")
	}
	second.Close()
	if life.Err() == nil || !slot.retiring || slot.released.Load() || f.s.serveLeases["remote"] != slot {
		t.Fatal("last capture did not cancel while preserving physical Wait custody")
	}
	slot.Release()
	if f.s.serveLeases["remote"] != nil {
		t.Fatal("physical release retained the slot")
	}
}

func TestOldCaptureCannotRetireDifferentConfigurationProcess(t *testing.T) {
	f, lease := captureManagedLeaseFixture(t)
	life, cancel := context.WithCancel(context.Background())
	defer cancel()
	binding := lease.Binding()
	slot := &sourceProcessLease{service: f.s, source: "remote", generation: "2", root: binding.Root, profile: binding.Profile, captureMode: captureManagedUnversioned, life: life, cancel: cancel}
	f.s.serveLeases = map[string]*sourceProcessLease{"remote": slot}
	lease.Close()
	if life.Err() != nil || slot.retiring || slot.released.Load() {
		t.Fatal("retired old capture canceled a different configuration's process")
	}
}

func TestOwnedCaptureRejectsWrapperReopenAndSpoolLayers(t *testing.T) {
	for _, wrapper := range []string{"alias", "crypt", "chunker", "hasher", "compress", "cache", "union", "combine"} {
		raw := []byte("[outer]\ntype=" + wrapper + "\nremote=origin:media\nupstreams=origin:media\n[origin]\ntype=sftp\nhost=192.0.2.1\n")
		if err := checkCaptureServeConfiguration(raw, "outer:"); !errors.Is(err, storage.ErrRemoteRange) {
			t.Fatal("wrapper was treated as one qualified from-start origin", wrapper, err)
		}
	}
	for _, backend := range []string{"sftp", "smb", "webdav", "s3"} {
		if err := checkCaptureServeConfiguration([]byte("[origin]\ntype="+backend+"\n"), "origin:media"); err != nil {
			t.Fatal("direct backend rejected before its separate approval checks", backend, err)
		}
	}
}
