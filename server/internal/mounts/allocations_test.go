package mounts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/authorityfiles"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"testing"
)

func TestAllocationCrashStagesAndRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, stage := range []string{"reserved", "mkdir_unknown", "identified", "partial_config", "written", "replaced_root", "nonempty", "referenced"} {
		t.Run(stage, func(t *testing.T) {
			s, _ := fixture(t)
			id := "allocation-fixture"
			data := []byte("synthetic encrypted bytes")
			if e := s.reserveAllocation(id, data); e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(s.root, id)
			config := filepath.Join(s.private, id+".conf")
			if stage != "reserved" {
				if e := os.Mkdir(path, 0700); e != nil {
					t.Fatal(e)
				}
			}
			if stage != "reserved" && stage != "mkdir_unknown" {
				identity, e := storage.DirectoryIdentity(path)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = s.db.Exec(`UPDATE mount_allocations SET directory_identity=? WHERE id=?`, identity, id); e != nil {
					t.Fatal(e)
				}
			}
			if stage == "partial_config" {
				os.WriteFile(config, []byte("partial"), 0600)
			}
			if stage == "written" || stage == "replaced_root" || stage == "nonempty" || stage == "referenced" {
				os.WriteFile(config, data, 0600)
			}
			if stage == "replaced_root" {
				os.Rename(path, path+"-original")
				os.Mkdir(path, 0700)
			}
			if stage == "nonempty" {
				os.WriteFile(filepath.Join(path, "unknown-media"), []byte("preserve"), 0600)
			}
			if stage == "referenced" {
				_, e := s.db.Exec(`INSERT INTO managed_mounts(id,name,executable,digest,remote,mount_path) VALUES(?,?,?,?,?,?)`, id, "Protected", "/none", "digest", "cloud:", path)
				if e != nil {
					t.Fatal(e)
				}
			}
			// New service startup exercises reconciliation after a simulated host interruption.
			_, e := New(s.db, filepath.Dir(filepath.Dir(s.private)), s.root, s.helper, s.storage)
			if e != nil {
				t.Fatal(e)
			}
			var n int
			s.db.QueryRow(`SELECT count(*) FROM mount_allocations WHERE id=?`, id).Scan(&n)
			cleaned := stage == "reserved" || stage == "identified" || stage == "written"
			if cleaned {
				if n != 0 {
					t.Fatal("reservation not cleared", stage)
				}
				if _, e = os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
					t.Fatal("owned allocation retained", e)
				}
			} else {
				if n != 1 {
					t.Fatal("unknown/referenced reservation lost")
				}
				if _, e = os.Lstat(path); e != nil {
					t.Fatal("unknown/referenced path removed", e)
				}
			}
			if stage == "referenced" {
				if _, e = os.Stat(config); e != nil {
					t.Fatal("referenced configuration removed")
				}
			}
		})
	}
}
func TestAllocationQuotaAndUnknownNamespace(t *testing.T) {
	s, _ := fixture(t)
	for i := 0; i < 8; i++ {
		if e := s.reserveAllocation(fmt.Sprint("reserved-", i), []byte("bounded")); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.reserveAllocation("ninth", []byte("bounded")); !errors.Is(e, ErrAllocationCapacity) {
		t.Fatal("reservation quota", e)
	}
	s.recoverAllocations(context.Background())
	var count int
	s.db.QueryRow(`SELECT count(*) FROM mount_allocations`).Scan(&count)
	if count != 6 {
		t.Fatal("recovery batch unbounded", count)
	}
	s2, _ := fixture(t)
	for i := 0; i < 33; i++ {
		os.WriteFile(filepath.Join(s2.private, fmt.Sprint("unknown-", i)), []byte("preserve"), 0600)
	}
	if e := s2.reserveAllocation("extra", []byte("bounded")); !errors.Is(e, ErrAllocationCapacity) {
		t.Fatal("unknown namespace quota", e)
	}
	if _, e := os.Stat(filepath.Join(s2.private, "unknown-0")); e != nil {
		t.Fatal("unknown removed")
	}
}
func TestSuccessfulCreateCommitsAllocationWithReceipt(t *testing.T) {
	s, input := fixture(t)
	c := Command{OperationID: operation(800), Action: "create", Name: input.Name, Executable: input.Executable, Remote: input.Remote, Config: input.Config}
	r, e := s.Command(context.Background(), "owner", "", c, allowCommand)
	if e != nil {
		t.Fatal(e)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM mount_allocations`).Scan(&n)
	if n != 0 {
		t.Fatal("accepted reservation retained")
	}
	_, e = New(s.db, filepath.Dir(filepath.Dir(s.private)), s.root, s.helper, s.storage)
	if e != nil {
		t.Fatal(e)
	}
	replay, e := s.Command(context.Background(), "owner", "", c, allowCommand)
	if e != nil || replay.MountID != r.MountID {
		t.Fatal("restart receipt", e)
	}
	if _, e = os.Stat(filepath.Join(s.private, r.MountID+".conf")); e != nil {
		t.Fatal("accepted configuration removed", e)
	}
}

func TestAllocationRecoveryCannotRacePendingWriter(t *testing.T) {
	s, _ := fixture(t)
	if e := s.reserveAllocation("pending-writer", []byte("pending")); e != nil {
		t.Fatal(e)
	}
	unlock, e := s.allocationLock()
	if e != nil {
		t.Fatal(e)
	}
	s.recoverAllocations(context.Background())
	var n int
	s.db.QueryRow(`SELECT count(*) FROM mount_allocations`).Scan(&n)
	if n != 1 {
		t.Fatal("recovery raced an active writer")
	}
	unlock()
	s.recoverAllocations(context.Background())
	s.db.QueryRow(`SELECT count(*) FROM mount_allocations`).Scan(&n)
	if n != 0 {
		t.Fatal("unlocked reservation not recovered")
	}
}

func TestAllocationByteBudgetPreservesUnknownLargeFile(t *testing.T) {
	s, _ := fixture(t)
	path := filepath.Join(s.private, "unknown-large-file")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	e = f.Truncate(3 << 20)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	if e = s.reserveAllocation("new-allocation", []byte("bounded")); !errors.Is(e, ErrAllocationCapacity) {
		t.Fatal("byte budget ignored", e)
	}
	if info, e := os.Stat(path); e != nil || info.Size() != 3<<20 {
		t.Fatal("unknown file changed", e)
	}
}

func TestCreateCommitAbortRecovery(t *testing.T) {
	s, input := fixture(t)
	denied := errors.New("injected acceptance failure")
	_, e := s.create(context.Background(), input, func(*sql.Tx, Mount) error { return denied }, nil)
	if !errors.Is(e, denied) {
		t.Fatal(e)
	}
	var allocations, mounts, receipts int
	s.db.QueryRow(`SELECT count(*) FROM mount_allocations`).Scan(&allocations)
	s.db.QueryRow(`SELECT count(*) FROM managed_mounts`).Scan(&mounts)
	s.db.QueryRow(`SELECT count(*) FROM mount_operations`).Scan(&receipts)
	if allocations != 1 || mounts != 0 || receipts != 0 {
		t.Fatal("partial acceptance", allocations, mounts, receipts)
	}
	s.recoverAllocations(context.Background())
	s.db.QueryRow(`SELECT count(*) FROM mount_allocations`).Scan(&allocations)
	if allocations != 0 {
		t.Fatal("failed acceptance did not recover")
	}
}

func TestAllocationReconcileFencesConfigRemovalAndLeavesIdleGenerationAlone(t *testing.T) {
	s, _ := fixture(t)
	idle, ok := authorityfiles.Process.Baseline()
	if !ok {
		t.Fatal("unexpected writer")
	}
	s.recoverAllocations(t.Context())
	if !authorityfiles.Process.Unchanged(idle) {
		t.Fatal("idle reconciliation changed generation")
	}
	id := "fenced-allocation"
	raw := []byte("config bytes")
	if err := s.reserveAllocation(id, raw); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(s.private, id+".conf")
	if err := os.WriteFile(config, raw, 0600); err != nil {
		t.Fatal(err)
	}
	before, ok := authorityfiles.Process.Baseline()
	if !ok {
		t.Fatal("unexpected writer")
	}
	s.recoverAllocations(t.Context())
	if authorityfiles.Process.Unchanged(before) {
		t.Fatal("live config cleanup escaped the mutation fence")
	}
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Fatal("config not removed", err)
	}
	if _, ok := authorityfiles.Process.Baseline(); !ok {
		t.Fatal("cleanup leaked its writer token")
	}
}
