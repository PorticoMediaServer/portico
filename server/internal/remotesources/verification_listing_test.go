package remotesources

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// The verification producer fence is exercised against durable rows only: no
// provider, helper, mount, job or inventory_run exists in this fixture. A
// listing under a verification key must be authorised by the verification row
// and its library source alone.

func armSourceVerification(t *testing.T, f observationFixture, epoch int64, pending bool) {
	t.Helper()
	if _, err := f.s.db.Exec(`UPDATE jobs SET status='complete' WHERE id=?`, f.job); err != nil {
		t.Fatal(err)
	}
	var incarnation string
	var generation int64
	if err := f.s.db.QueryRow(`SELECT incarnation,generation FROM library_sources WHERE id=?`, f.library).Scan(&incarnation, &generation); err != nil {
		t.Fatal(err)
	}
	state := 0
	if pending {
		state = 1
	}
	if _, err := f.s.db.Exec(`INSERT INTO catalog_source_verification(source_id,incarnation,configuration_generation,health_epoch,pending)
 VALUES(?,?,?,?,?)
 ON CONFLICT(source_id) DO UPDATE SET incarnation=excluded.incarnation,configuration_generation=excluded.configuration_generation,
  health_epoch=excluded.health_epoch,pending=excluded.pending,started=0,after_object='',fence_object=''`, f.library, incarnation, generation, epoch, state); err != nil {
		t.Fatal(err)
	}
}

func startVerificationListing(t *testing.T, f observationFixture, id, key, directory string) {
	t.Helper()
	if _, err := f.s.db.Exec(`INSERT INTO remote_listing_sessions(id,job_id,source_id,generation,directory,ready,created_at) VALUES(?,?,?,?,?,0,?)`, id, key, "remote", "1", directory, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.Exec(`INSERT INTO remote_native_listings(listing_id,owner,state,updated_at) VALUES(?,?,'running',?)`, id, "owner", time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
}

func verificationBinding(root string) binding {
	return binding{ID: "remote", Kind: "webdav", Root: root}
}

func TestParseVerificationKey(t *testing.T) {
	for _, test := range []struct {
		key    string
		source string
		epoch  int64
		ok     bool
	}{
		{"source-verify:source:1", "source", 1, true},
		{"source-verify:a-b_c:42", "a-b_c", 42, true},
		{"source-verify:source:0", "", 0, false},
		{"source-verify:source:-1", "", 0, false},
		{"source-verify:source:01", "", 0, false},
		{"source-verify:source:", "", 0, false},
		{"source-verify::1", "", 1, true},
		{"source-verify:source", "", 0, false},
		{"inventory:source:1", "", 0, false},
		{"source-verify:two:parts:1", "two:parts", 1, true},
	} {
		source, epoch, ok := parseVerificationKey(test.key)
		if ok != test.ok || source != test.source || epoch != test.epoch {
			t.Fatalf("parseVerificationKey(%q) = %q,%d,%v; want %q,%d,%v", test.key, source, epoch, ok, test.source, test.epoch, test.ok)
		}
	}
	if got := verificationKey("source", 7); got != "source-verify:source:7" {
		t.Fatalf("verificationKey = %q", got)
	}
}

func TestVerificationListingFenceAcceptsCurrentEpoch(t *testing.T) {
	f := newObservationFixture(t)
	ctx := context.Background()
	root := filepath.Dir(f.file)
	key := verificationKey(f.library, 1)
	armSourceVerification(t, f, 1, true)
	startVerificationListing(t, f, "verification", key, root)

	handled, err := f.s.verificationNativeListingFence(ctx, f.s.db, "verification", "owner")
	if !handled || err != nil {
		t.Fatalf("native fence = handled=%v err=%v, want handled and accepted", handled, err)
	}

	// A scan listing keeps its inventory-run fence: the verification fence must
	// never answer for it.
	if _, err := f.s.db.Exec(`INSERT INTO remote_listing_sessions(id,job_id,source_id,generation,directory,ready,created_at) VALUES('scan-listing','scan','remote','1',?,0,?)`, root, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if handled, err := f.s.verificationNativeListingFence(ctx, f.s.db, "scan-listing", "owner"); handled || err != nil {
		t.Fatalf("scan listing = handled=%v err=%v, want unhandled", handled, err)
	}

	tx, err := f.s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	capture, handled, err := f.s.verificationServeListingFenceTx(ctx, tx, verificationBinding(root), ".", "verification", "1")
	if !handled || err != nil {
		t.Fatalf("serve fence = handled=%v err=%v, want handled and accepted", handled, err)
	}
	if capture.job != key || capture.owner != "owner" {
		t.Fatalf("capture job/owner = %q/%q, want %q/owner", capture.job, capture.owner, key)
	}
	if capture.source.ID != f.library || capture.source.LibraryID != f.library || capture.source.ResolvedPath != root {
		t.Fatalf("capture source = %+v, want this library source at %s", capture.source, root)
	}
	if capture.source.RootIdentity != storage.RemoteDirectoryIdentity("remote", root)+":generation:1" {
		t.Fatalf("capture root identity = %q", capture.source.RootIdentity)
	}
	if capture.profile.SourceID != "remote" {
		t.Fatalf("capture profile source = %q", capture.profile.SourceID)
	}

	tx.Rollback()
	scanTx, err := f.s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer scanTx.Rollback()
	if _, handled, err := f.s.verificationServeListingFenceTx(ctx, scanTx, verificationBinding(root), ".", "scan-listing", "1"); handled || err != nil {
		t.Fatalf("scan serve listing = handled=%v err=%v, want unhandled", handled, err)
	}
}

func TestVerificationListingFenceRejectsEpochAndSourceDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	tests := []struct {
		name   string
		mutate func(*testing.T, observationFixture, string)
		want   error
	}{
		{"health epoch moved", func(t *testing.T, f observationFixture, _ string) {
			if _, err := f.s.db.Exec(`UPDATE catalog_source_verification SET health_epoch=2 WHERE source_id=?`, f.library); err != nil {
				t.Fatal(err)
			}
		}, storage.ErrRemoteChanged},
		{"verification no longer pending", func(t *testing.T, f observationFixture, _ string) {
			if _, err := f.s.db.Exec(`UPDATE catalog_source_verification SET pending=0 WHERE source_id=?`, f.library); err != nil {
				t.Fatal(err)
			}
		}, storage.ErrRemoteChanged},
		{"library incarnation moved", func(t *testing.T, f observationFixture, _ string) {
			if _, err := f.s.db.Exec(`UPDATE library_sources SET incarnation='replacement' WHERE id=?`, f.library); err != nil {
				t.Fatal(err)
			}
		}, storage.ErrRemoteChanged},
		{"configuration generation moved", func(t *testing.T, f observationFixture, _ string) {
			if _, err := f.s.db.Exec(`UPDATE library_sources SET generation=generation+1 WHERE id=?`, f.library); err != nil {
				t.Fatal(err)
			}
		}, storage.ErrRemoteChanged},
		{"library source disabled", func(t *testing.T, f observationFixture, _ string) {
			if _, err := f.s.db.Exec(`UPDATE library_sources SET enabled=0 WHERE id=?`, f.library); err != nil {
				t.Fatal(err)
			}
		}, storage.ErrRemoteChanged},
		{"identity unconfirmed", func(t *testing.T, f observationFixture, _ string) {
			if _, err := f.s.db.Exec(`UPDATE library_sources SET identity_confirmed=0 WHERE id=?`, f.library); err != nil {
				t.Fatal(err)
			}
		}, storage.ErrRemoteChanged},
		{"library health offline", func(t *testing.T, f observationFixture, _ string) {
			if _, err := f.s.db.Exec(`UPDATE library_sources SET health='offline' WHERE id=?`, f.library); err != nil {
				t.Fatal(err)
			}
		}, storage.ErrRemoteChanged},
		{"root identity replaced", func(t *testing.T, f observationFixture, _ string) {
			if _, err := f.s.db.Exec(`UPDATE library_sources SET root_identity='remote-dir:other' WHERE id=?`, f.library); err != nil {
				t.Fatal(err)
			}
		}, storage.ErrRemoteChanged},
		{"adapter generation moved", func(t *testing.T, f observationFixture, _ string) {
			if _, err := f.s.db.Exec(`UPDATE remote_sources SET generation=2 WHERE id='remote'`); err != nil {
				t.Fatal(err)
			}
		}, storage.ErrRemoteChanged},
		{"adapter root replaced", func(t *testing.T, f observationFixture, root string) {
			if _, err := f.s.db.Exec(`UPDATE remote_sources SET root=? WHERE id='remote'`, filepath.Join(filepath.Dir(root), "replacement")); err != nil {
				t.Fatal(err)
			}
		}, storage.ErrRemoteChanged},
		{"source removed", func(t *testing.T, f observationFixture, _ string) {
			if _, err := f.s.db.Exec(`UPDATE remote_sources SET removed=1 WHERE id='remote'`); err != nil {
				t.Fatal(err)
			}
		}, storage.ErrRemoteChanged},
		{"source failed credentials", func(t *testing.T, f observationFixture, _ string) {
			if _, err := f.s.db.Exec(`UPDATE remote_sources SET state='credentials_required',error_code='credentials_required' WHERE id='remote'`); err != nil {
				t.Fatal(err)
			}
		}, storage.ErrRemoteCredentials},
		{"directory outside root", func(t *testing.T, f observationFixture, root string) {
			if _, err := f.s.db.Exec(`UPDATE remote_listing_sessions SET directory=? WHERE id='verification'`, filepath.Dir(root)); err != nil {
				t.Fatal(err)
			}
		}, storage.ErrRemoteChanged},
		{"key mapped to a scan job", func(t *testing.T, f observationFixture, root string) {
			if _, err := f.s.db.Exec(`INSERT INTO remote_inventory_checks(job_id,directory,listing_id,check_job) VALUES('scan',?,'verification',?)`, root, verificationKey(f.library, 1)); err != nil {
				t.Fatal(err)
			}
		}, storage.ErrRemoteChanged},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newObservationFixture(t)
			root := filepath.Dir(f.file)
			key := verificationKey(f.library, 1)
			armSourceVerification(t, f, 1, true)
			startVerificationListing(t, f, "verification", key, root)
			test.mutate(t, f, root)
			handled, err := f.s.verificationNativeListingFence(context.Background(), f.s.db, "verification", "owner")
			if !handled || !errors.Is(err, test.want) {
				t.Fatalf("native fence = handled=%v err=%v, want %v", handled, err, test.want)
			}
		})
	}
}

func TestVerificationListingFenceCancelsRetiredOrForeignProducer(t *testing.T) {
	f := newObservationFixture(t)
	ctx := context.Background()
	root := filepath.Dir(f.file)
	key := verificationKey(f.library, 1)
	armSourceVerification(t, f, 1, true)
	startVerificationListing(t, f, "verification", key, root)

	if handled, err := f.s.verificationNativeListingFence(ctx, f.s.db, "verification", "other"); !handled || !errors.Is(err, storage.ErrRemoteChanged) {
		t.Fatalf("owner mismatch = handled=%v err=%v, want ErrRemoteChanged", handled, err)
	}
	if _, err := f.s.db.Exec(`UPDATE remote_native_listings SET state='complete' WHERE listing_id='verification'`); err != nil {
		t.Fatal(err)
	}
	if handled, err := f.s.verificationNativeListingFence(ctx, f.s.db, "verification", "owner"); !handled || !errors.Is(err, context.Canceled) {
		t.Fatalf("completed producer = handled=%v err=%v, want context.Canceled", handled, err)
	}
	if _, err := f.s.db.Exec(`UPDATE remote_native_listings SET state='running' WHERE listing_id='verification'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.Exec(`UPDATE remote_listing_sessions SET ready=1 WHERE id='verification'`); err != nil {
		t.Fatal(err)
	}
	if handled, err := f.s.verificationNativeListingFence(ctx, f.s.db, "verification", "owner"); !handled || !errors.Is(err, context.Canceled) {
		t.Fatalf("ready producer = handled=%v err=%v, want context.Canceled", handled, err)
	}
	tx, err := f.s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, handled, err := f.s.verificationServeListingFenceTx(ctx, tx, verificationBinding(root), ".", "verification", "1"); !handled || !errors.Is(err, storage.ErrRemoteChanged) {
		t.Fatalf("ready serve listing = handled=%v err=%v, want ErrRemoteChanged", handled, err)
	}
	tx.Rollback()
	if _, err := f.s.db.Exec(`UPDATE remote_listing_sessions SET ready=0 WHERE id='verification'`); err != nil {
		t.Fatal(err)
	}
	tx, err = f.s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, handled, err := f.s.verificationServeListingFenceTx(ctx, tx, verificationBinding(root), ".", "verification", "2"); !handled || !errors.Is(err, storage.ErrRemoteChanged) {
		t.Fatalf("stale generation serve listing = handled=%v err=%v, want ErrRemoteChanged", handled, err)
	}
}

func TestVerificationListingFenceRejectsMalformedVerificationKey(t *testing.T) {
	f := newObservationFixture(t)
	root := filepath.Dir(f.file)
	if _, err := f.s.db.Exec(`INSERT INTO remote_listing_sessions(id,job_id,source_id,generation,directory,ready,created_at) VALUES('malformed','source-verify:broken','remote','1',?,0,?)`, root, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	handled, err := f.s.verificationNativeListingFence(context.Background(), f.s.db, "malformed", "owner")
	if !handled || !errors.Is(err, storage.ErrRemoteChanged) {
		t.Fatalf("malformed verification key = handled=%v err=%v, want ErrRemoteChanged", handled, err)
	}
}

func TestVerificationSupersedeWaitsThenRetiresDeadProducer(t *testing.T) {
	f := newObservationFixture(t)
	ctx := context.Background()
	root := filepath.Dir(f.file)
	current := verificationKey(f.library, 2)
	old := verificationKey(f.library, 1)
	startVerificationListing(t, f, "old", old, root)
	if _, err := f.s.db.Exec(`INSERT INTO remote_scan_generations(job_id,source_id,generation) VALUES(?,'remote','1')`, old); err != nil {
		t.Fatal(err)
	}
	f.s.nativeListings = map[string]*nativeListing{"old": {}}

	waiting, err := f.s.verificationSupersede(ctx, current, "remote", root)
	if err != nil || !waiting {
		t.Fatalf("live previous producer = waiting=%v err=%v, want waiting", waiting, err)
	}
	if got := listingCount(t, f); got != 1 {
		t.Fatalf("live previous producer was retired: listings=%d", got)
	}

	delete(f.s.nativeListings, "old")
	waiting, err = f.s.verificationSupersede(ctx, current, "remote", root)
	if err != nil || waiting {
		t.Fatalf("dead previous producer = waiting=%v err=%v, want retired", waiting, err)
	}
	if got := listingCount(t, f); got != 0 {
		t.Fatalf("superseded verification listing remains: %d", got)
	}
	var captures int
	if err := f.s.db.QueryRow(`SELECT count(*) FROM remote_scan_generations WHERE job_id=?`, old).Scan(&captures); err != nil || captures != 0 {
		t.Fatalf("superseded generation capture rows = %d err=%v, want none", captures, err)
	}

	// A completed listing from an older epoch is retained by the ordinary
	// listing retention, never reused as this epoch's evidence.
	readyAbsenceListing(t, f, "ready-old", verificationKey(f.library, 3), root, "present.mkv")
	if _, err := f.s.db.Exec(`INSERT INTO remote_scan_generations(job_id,source_id,generation) VALUES(?,'remote','1')`, verificationKey(f.library, 3)); err != nil {
		t.Fatal(err)
	}
	waiting, err = f.s.verificationSupersede(ctx, current, "remote", root)
	if err != nil || waiting {
		t.Fatalf("completed previous listing = waiting=%v err=%v, want left alone", waiting, err)
	}
	if got := listingCount(t, f); got != 1 {
		t.Fatalf("completed previous listing was deleted: listings=%d", got)
	}
	if err := f.s.db.QueryRow(`SELECT count(*) FROM remote_scan_generations WHERE job_id=?`, verificationKey(f.library, 3)).Scan(&captures); err != nil || captures != 1 {
		t.Fatalf("retained completed listing capture rows = %d err=%v, want none", captures, err)
	}
}

func TestVerificationSupersedeRetiresInBoundedBatches(t *testing.T) {
	f := newObservationFixture(t)
	ctx := context.Background()
	root := filepath.Dir(f.file)
	current := verificationKey(f.library, 2)
	old := verificationKey(f.library, 1)
	startVerificationListing(t, f, "old", old, root)
	const total = 2*verificationReapBatch + 8
	for i := 0; i < total; i++ {
		if _, err := f.s.db.Exec(`INSERT INTO remote_listing_entries(listing_id,name,snapshot) VALUES('old',?,'{}')`, filepath.Join(root, fmt.Sprintf("f%03d.mkv", i))); err != nil {
			t.Fatal(err)
		}
	}
	var remaining int
	for pass, want := range []int{total - verificationReapBatch, total - 2*verificationReapBatch, 0} {
		waiting, err := f.s.verificationSupersede(ctx, current, "remote", root)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.s.db.QueryRow(`SELECT count(*) FROM remote_listing_entries WHERE listing_id='old'`).Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining != want {
			t.Fatalf("pass %d removed %d entries, want %d", pass, remaining, want)
		}
		if want > 0 && !waiting {
			t.Fatalf("pass %d stopped before the listing was empty", pass)
		}
		if want == 0 && waiting {
			t.Fatalf("pass %d waited after the listing was empty", pass)
		}
	}
	if got := listingCount(t, f); got != 0 {
		t.Fatalf("retired listing remains: %d", got)
	}
}

func TestVerificationProducerYieldsToScanAndAcceptsLibrarySubroot(t *testing.T) {
	f := newObservationFixture(t)
	ctx := context.Background()
	root := filepath.Dir(f.file)
	child := filepath.Join(root, "library")
	identity := storage.RemoteDirectoryIdentity("remote", child) + ":generation:1"
	if _, err := f.s.db.Exec(`UPDATE library_sources SET root=?,root_identity=? WHERE id=?`, child, identity, f.library); err != nil {
		t.Fatal(err)
	}
	armSourceVerification(t, f, 1, true)
	key := verificationKey(f.library, 1)
	startVerificationListing(t, f, "child-listing", key, child)
	if handled, err := f.s.verificationNativeListingFence(ctx, f.s.db, "child-listing", "owner"); !handled || err != nil {
		t.Fatalf("subroot fence %v %v", handled, err)
	}
	if _, err := f.s.db.Exec(`UPDATE jobs SET status='queued' WHERE id='scan'`); err != nil {
		t.Fatal(err)
	}
	if handled, err := f.s.verificationNativeListingFence(ctx, f.s.db, "child-listing", "owner"); !handled || !errors.Is(err, context.Canceled) {
		t.Fatalf("queued scan did not preempt producer: %v %v", handled, err)
	}
}
