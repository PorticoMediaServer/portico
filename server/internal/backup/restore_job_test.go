package backup

import (
	"context"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"testing"
	"time"
)

func TestRestoreProgressSurvivesRequestCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	svc, _, _ := testService(t)
	id, err := svc.Create(context.Background(), KindManual)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	job, err := svc.StartRestore(ctx, Source{BackupID: id}, "restore-operation")
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		progress := svc.RestoreProgress()
		if progress.ErrorCode != "" {
			t.Fatal(progress)
		}
		if progress.Phase == "staged" {
			if progress.JobID != job || progress.Result == nil || !progress.Result.Staged {
				t.Fatal(progress)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("restore did not finish", progress)
		}
		time.Sleep(10 * time.Millisecond)
	}
	replay, err := svc.StartRestore(context.Background(), Source{BackupID: id}, "restore-operation")
	if err != nil || replay != job {
		t.Fatal(replay, err)
	}
}

func TestSeparateBackupDestinationAndRestoreRefusalWithoutLock(t *testing.T) {
	svc, state, _ := testService(t)
	directory := t.TempDir()
	if err := svc.SetDirectory(directory); err != nil {
		t.Fatal(err)
	}
	id, err := svc.Create(context.Background(), KindManual)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := svc.List()
	if err != nil || len(listed) != 1 || listed[0].ID != id || svc.backupRoot() == storagepaths.Path(state, BackupsDir) {
		t.Fatal(listed, err)
	}
	svc.SetRestoreAvailable(false)
	if _, err = svc.StartRestore(context.Background(), Source{BackupID: id}, "restore-refused"); err != ErrRestoreLockUnavailable {
		t.Fatal(err)
	}
}

func TestCompletedRestoreReceiptReplaysAfterRestart(t *testing.T) {
	service, state, db := testService(t)
	source := Source{BackupID: "2026-10-13T010000Z"}
	marker := restoreMarker{JobID: "completed-job", OperationID: "completed-operation", Source: source, SchemaVersion: service.schemaVersion}
	if err := writeResult(state, "restored", "", marker); err != nil {
		t.Fatal(err)
	}
	restarted := New(state, db, "server", "version", service.schemaVersion)
	id, err := restarted.StartRestore(context.Background(), source, "completed-operation")
	if err != nil || id != "completed-job" || restarted.RestoreProgress().Result == nil {
		t.Fatal(id, err, restarted.RestoreProgress())
	}
	if _, err = restarted.StartRestore(context.Background(), Source{Path: "/different"}, "completed-operation"); err != ErrInvalid {
		t.Fatal("operation accepted changed source", err)
	}
}

func TestFailedRestoreCanRetryTheSameOperation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	service, _, _ := testService(t)
	source := Source{Path: filepath.Join(t.TempDir(), "candidate")}
	first, err := service.StartRestore(context.Background(), source, "retry-operation")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for service.RestoreProgress().Phase != "failed" {
		if time.Now().After(deadline) {
			t.Fatal("missing source did not fail")
		}
		time.Sleep(10 * time.Millisecond)
	}
	backupID, err := service.Create(context.Background(), KindManual)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(filepath.Join(service.backupRoot(), backupID), source.Path); err != nil {
		t.Fatal(err)
	}
	second, err := service.StartRestore(context.Background(), source, "retry-operation")
	if err != nil || second == first {
		t.Fatal("failed receipt prevented retry", first, second, err)
	}
	// Creating the replacement source belongs to setup, not the retry's
	// observation window. Each asynchronous attempt gets its own bound.
	deadline = time.Now().Add(10 * time.Second)
	for service.RestoreProgress().Phase != "staged" {
		if p := service.RestoreProgress(); p.ErrorCode != "" {
			t.Fatal(p)
		}
		if time.Now().After(deadline) {
			t.Fatal("retry did not stage")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
