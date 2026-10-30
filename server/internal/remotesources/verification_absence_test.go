package remotesources

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// These tests use the DB-only observation fixture: no provider, helper, mount
// or network. The adapter's own InspectRoot answer is supplied as a captured
// root snapshot so the namespace comparison is exercised directly. A listing
// inserted here stands in for the producer's completed output under one
// verification key; the producer's own authorisation is covered by
// verification_listing_test.go.

func absenceRoot(root string) storage.Snapshot {
	return storage.Snapshot{
		Path:           root,
		Name:           filepath.Base(root),
		Directory:      true,
		Revision:       "1:fixture-root",
		ChangeToken:    "1:fixture-root",
		ObjectIdentity: storage.RemoteDirectoryIdentity("remote", root) + ":generation:1",
	}
}

func absenceRequest(root, relative string) storage.InventoryRequest {
	return storage.InventoryRequest{
		Root:           root,
		RootIdentity:   storage.RemoteDirectoryIdentity("remote", root) + ":generation:1",
		RelativePath:   relative,
		FollowSymlinks: false,
	}
}

// readyAbsenceListing writes the completed output a verification-key producer
// would leave behind. The job id is the verification key, never a scan job.
func readyAbsenceListing(t *testing.T, f observationFixture, id, key, directory string, names ...string) {
	t.Helper()
	if _, err := f.s.db.Exec(`INSERT INTO remote_listing_sessions(id,job_id,source_id,generation,directory,ready,created_at) VALUES(?,?,?,?,?,1,?)`, id, key, "remote", "1", directory, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if _, err := f.s.db.Exec(`INSERT INTO remote_listing_entries(listing_id,name,snapshot) VALUES(?,?,'{}')`, id, filepath.Join(directory, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func listingCount(t *testing.T, f observationFixture) int {
	t.Helper()
	var count int
	if err := f.s.db.QueryRow(`SELECT count(*) FROM remote_listing_sessions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func validateAbsence(t *testing.T, f observationFixture, key string, r storage.InventoryRequest) error {
	t.Helper()
	tx, err := f.s.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	return f.s.ValidateInventoryAbsenceTx(context.Background(), tx, key, r)
}

// A complete listing produced under this verification key is positive evidence:
// the recorded file is absent, many files reuse that one parent listing, and the
// accepting writer fences the same key and generation.
func TestRemoteAbsenceProvesMissingNameFromVerificationEpochListing(t *testing.T) {
	f := newObservationFixture(t)
	ctx := context.Background()
	root := filepath.Dir(f.file)
	key := verificationKey(f.library, 1)
	armSourceVerification(t, f, 1, true)
	readyAbsenceListing(t, f, "listing", key, root, "present.mkv")

	first := absenceRequest(root, "film.mkv")
	absent, err := f.s.inventoryAbsence(ctx, key, first, absenceRoot(root), nil)
	if err != nil || !absent {
		t.Fatalf("missing file = absent=%v err=%v", absent, err)
	}
	if err = validateAbsence(t, f, key, first); err != nil {
		t.Fatalf("epoch listing was not accepted in the writer: %v", err)
	}

	second := absenceRequest(root, "credits.mkv")
	absent, err = f.s.inventoryAbsence(ctx, key, second, absenceRoot(root), nil)
	if err != nil || !absent {
		t.Fatalf("second missing file = absent=%v err=%v", absent, err)
	}
	if got := listingCount(t, f); got != 1 {
		t.Fatalf("listings = %d, want one parent listing reused", got)
	}
	var checks int
	if err := f.s.db.QueryRow(`SELECT count(*) FROM remote_inventory_checks`).Scan(&checks); err != nil || checks != 0 {
		t.Fatalf("absence wrote %d private check rows, want none", checks)
	}

	present := absenceRequest(root, "present.mkv")
	absent, err = f.s.inventoryAbsence(ctx, key, present, absenceRoot(root), nil)
	if err != nil || absent {
		t.Fatalf("present file = absent=%v err=%v", absent, err)
	}
	if err = validateAbsence(t, f, key, present); !errors.Is(err, storage.ErrInventoryChanged) {
		t.Fatalf("present file writer fence = %v, want ErrInventoryChanged", err)
	}
}

// Evidence from another epoch or another source generation must never become
// absence. An older epoch's unfinished listing is retired, and the walk stays
// Pending until this epoch has its own fresh listing.
func TestRemoteAbsenceNeverReusesAnotherEpochListing(t *testing.T) {
	f := newObservationFixture(t)
	ctx := context.Background()
	root := filepath.Dir(f.file)
	key := verificationKey(f.library, 2)
	armSourceVerification(t, f, 2, true)
	startVerificationListing(t, f, "old", verificationKey(f.library, 1), root)

	absent, err := f.s.inventoryAbsence(ctx, key, absenceRequest(root, "film.mkv"), absenceRoot(root), nil)
	if absent || !errors.Is(err, storage.ErrBusy) {
		t.Fatalf("old epoch listing = absent=%v err=%v, want ErrBusy", absent, err)
	}
	if got := listingCount(t, f); got != 1 {
		t.Fatalf("listings = %d, want the old epoch retired and only this epoch's session", got)
	}
	var old int
	if err := f.s.db.QueryRow(`SELECT count(*) FROM remote_listing_sessions WHERE job_id=?`, verificationKey(f.library, 1)).Scan(&old); err != nil || old != 0 {
		t.Fatalf("superseded epoch listings = %d err=%v, want none", old, err)
	}

	// A foreign generation is not the current source's evidence either.
	f2 := newObservationFixture(t)
	ctx2 := context.Background()
	root2 := filepath.Dir(f2.file)
	key2 := verificationKey(f2.library, 1)
	armSourceVerification(t, f2, 1, true)
	readyAbsenceListing(t, f2, "foreign", key2, root2, "other.mkv")
	if _, err := f2.s.db.Exec(`UPDATE remote_listing_sessions SET generation='2' WHERE id='foreign'`); err != nil {
		t.Fatal(err)
	}
	absent, err = f2.s.inventoryAbsence(ctx2, key2, absenceRequest(root2, "film.mkv"), absenceRoot(root2), nil)
	if absent || !errors.Is(err, storage.ErrBusy) {
		t.Fatalf("foreign generation = absent=%v err=%v, want ErrBusy", absent, err)
	}
}

// Without a current verification row there is no source and health epoch that
// can authorise a producer. The walk must stay Pending and must not create a
// doomed listing or claim absence.
func TestRemoteAbsenceRequiresCurrentVerificationEpoch(t *testing.T) {
	f := newObservationFixture(t)
	ctx := context.Background()
	root := filepath.Dir(f.file)
	absent, err := f.s.inventoryAbsence(ctx, verificationKey(f.library, 1), absenceRequest(root, "film.mkv"), absenceRoot(root), nil)
	if absent || !errors.Is(err, storage.ErrRemoteChanged) {
		t.Fatalf("missing verification row = absent=%v err=%v, want ErrRemoteChanged", absent, err)
	}
	if got := listingCount(t, f); got != 0 {
		t.Fatalf("verification started %d listings without a current epoch", got)
	}

	armSourceVerification(t, f, 2, true)
	absent, err = f.s.inventoryAbsence(ctx, verificationKey(f.library, 1), absenceRequest(root, "film.mkv"), absenceRoot(root), nil)
	if absent || !errors.Is(err, storage.ErrRemoteChanged) {
		t.Fatalf("stale epoch key = absent=%v err=%v, want ErrRemoteChanged", absent, err)
	}
	if got := listingCount(t, f); got != 0 {
		t.Fatalf("stale epoch started %d listings", got)
	}
}

// A pending or failed listing is never evidence, and a pending producer for an
// older epoch is awaited rather than duplicated for the newer one.
func TestRemoteAbsenceNeverProvesFromPendingOrFailedListing(t *testing.T) {
	f := newObservationFixture(t)
	ctx := context.Background()
	root := filepath.Dir(f.file)
	key := verificationKey(f.library, 1)
	armSourceVerification(t, f, 1, true)
	startVerificationListing(t, f, "pending", key, root)

	absent, err := f.s.inventoryAbsence(ctx, key, absenceRequest(root, "film.mkv"), absenceRoot(root), nil)
	if absent || !errors.Is(err, storage.ErrBusy) {
		t.Fatalf("pending listing = absent=%v err=%v, want ErrBusy", absent, err)
	}
	if got := listingCount(t, f); got != 1 {
		t.Fatalf("pending listing was duplicated: %d", got)
	}

	failed := newObservationFixture(t)
	ctxFailed := context.Background()
	rootFailed := filepath.Dir(failed.file)
	keyFailed := verificationKey(failed.library, 1)
	armSourceVerification(t, failed, 1, true)
	startVerificationListing(t, failed, "failed", keyFailed, rootFailed)
	failed.s.serveLife = ctxFailed
	if _, err := failed.s.db.Exec(`UPDATE remote_native_listings SET state='failed',error_code='invalid_configuration' WHERE listing_id='failed'`); err != nil {
		t.Fatal(err)
	}
	absent, err = failed.s.inventoryAbsence(ctxFailed, keyFailed, absenceRequest(rootFailed, "film.mkv"), absenceRoot(rootFailed), nil)
	if absent || !errors.Is(err, storage.ErrRemoteConfig) {
		t.Fatalf("failed listing = absent=%v err=%v, want ErrRemoteConfig", absent, err)
	}
}

// An outstanding producer for the same source and parent is awaited rather than
// duplicated for a newer health epoch.
func TestRemoteAbsenceCoalescesOutstandingParentProducer(t *testing.T) {
	f := newObservationFixture(t)
	ctx := context.Background()
	root := filepath.Dir(f.file)
	key := verificationKey(f.library, 2)
	armSourceVerification(t, f, 2, true)
	startVerificationListing(t, f, "pending", verificationKey(f.library, 1), root)
	f.s.nativeListings = map[string]*nativeListing{"pending": {}}

	absent, err := f.s.inventoryAbsence(ctx, key, absenceRequest(root, "film.mkv"), absenceRoot(root), nil)
	if absent || !errors.Is(err, storage.ErrBusy) {
		t.Fatalf("pending parent = absent=%v err=%v, want ErrBusy", absent, err)
	}
	if got := listingCount(t, f); got != 1 {
		t.Fatalf("verification launched %d listings beside the pending producer", got)
	}
}

// A root identity that names another source, path or generation is namespace
// confusion and is refused before any listing is consulted.
func TestRemoteAbsenceRejectsRootNamespaceConfusion(t *testing.T) {
	f := newObservationFixture(t)
	ctx := context.Background()
	root := filepath.Dir(f.file)
	key := verificationKey(f.library, 1)
	armSourceVerification(t, f, 1, true)
	readyAbsenceListing(t, f, "listing", key, root, "other.mkv")
	request := absenceRequest(root, "film.mkv")

	swapped := absenceRoot(root)
	swapped.ObjectIdentity = storage.RemoteDirectoryIdentity("another-source", root) + ":generation:1"
	absent, err := f.s.inventoryAbsence(ctx, key, request, swapped, nil)
	if absent || !errors.Is(err, storage.ErrInventoryChanged) {
		t.Fatalf("foreign root identity = absent=%v err=%v, want ErrInventoryChanged", absent, err)
	}

	replaced := absenceRoot(root)
	replaced.ObjectIdentity = storage.RemoteDirectoryIdentity("remote", root) + ":generation:2"
	absent, err = f.s.inventoryAbsence(ctx, key, request, replaced, nil)
	if absent || !errors.Is(err, storage.ErrInventoryChanged) {
		t.Fatalf("replacement generation = absent=%v err=%v, want ErrInventoryChanged", absent, err)
	}

	notDirectory := absenceRoot(root)
	notDirectory.Directory = false
	absent, err = f.s.inventoryAbsence(ctx, key, request, notDirectory, nil)
	if absent || !errors.Is(err, storage.ErrInventoryChanged) {
		t.Fatalf("non-directory root = absent=%v err=%v, want ErrInventoryChanged", absent, err)
	}
}

// A source whose last recorded contact failed credentials, or that is otherwise
// unavailable, cannot lend listing evidence authority.
func TestRemoteAbsenceRefusesUnhealthySource(t *testing.T) {
	for _, test := range []struct {
		name  string
		state string
		code  string
		want  error
	}{
		{"credentials", "credentials_required", "credentials_required", storage.ErrRemoteCredentials},
		{"offline", "offline", "offline", storage.ErrRemoteOffline},
		{"binary", "binary_changed", "binary_changed", storage.ErrRemoteBinary},
		{"throttled", "rate_limited", "rate_limited", storage.ErrBusy},
		{"changed", "degraded", "source_changed", storage.ErrRemoteChanged},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newObservationFixture(t)
			root := filepath.Dir(f.file)
			key := verificationKey(f.library, 1)
			armSourceVerification(t, f, 1, true)
			readyAbsenceListing(t, f, "listing", key, root, "other.mkv")
			if _, err := f.s.db.Exec(`UPDATE remote_sources SET state=?,error_code=? WHERE id='remote'`, test.state, test.code); err != nil {
				t.Fatal(err)
			}
			absent, err := f.s.inventoryAbsence(context.Background(), key, absenceRequest(root, "film.mkv"), absenceRoot(root), nil)
			if absent || !errors.Is(err, test.want) {
				t.Fatalf("unhealthy source = absent=%v err=%v, want %v", absent, err, test.want)
			}
		})
	}
}

// The accepting writer must revalidate the exact epoch listing: a moved scan
// generation, a replacement configuration or root identity, a different
// key/listing, an unready listing or the file's return all refuse the missing
// mark.
func TestRemoteAbsenceWriterFencesCapturedListingAndConfiguration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	setup := func(t *testing.T) (observationFixture, string, storage.InventoryRequest) {
		f := newObservationFixture(t)
		root := filepath.Dir(f.file)
		key := verificationKey(f.library, 1)
		armSourceVerification(t, f, 1, true)
		readyAbsenceListing(t, f, "listing", key, root, "other.mkv")
		request := absenceRequest(root, "film.mkv")
		absent, err := f.s.inventoryAbsence(context.Background(), key, request, absenceRoot(root), nil)
		if err != nil || !absent {
			t.Fatalf("setup proof = absent=%v err=%v", absent, err)
		}
		return f, key, request
	}

	t.Run("missing scan generation capture", func(t *testing.T) {
		f, key, request := setup(t)
		if _, err := f.s.db.Exec(`DELETE FROM remote_scan_generations WHERE job_id=?`, key); err != nil {
			t.Fatal(err)
		}
		if err := validateAbsence(t, f, key, request); !errors.Is(err, storage.ErrRemoteChanged) {
			t.Fatalf("writer accepted an uncaptured generation: %v", err)
		}
	})
	t.Run("configuration generation moved", func(t *testing.T) {
		f, key, request := setup(t)
		if _, err := f.s.db.Exec(`UPDATE remote_sources SET generation=2 WHERE id='remote'`); err != nil {
			t.Fatal(err)
		}
		if err := validateAbsence(t, f, key, request); !errors.Is(err, storage.ErrRemoteChanged) {
			t.Fatalf("writer accepted a replacement configuration: %v", err)
		}
	})
	t.Run("root path replaced", func(t *testing.T) {
		f, key, request := setup(t)
		if _, err := f.s.db.Exec(`UPDATE remote_sources SET root=? WHERE id='remote'`, filepath.Join(filepath.Dir(request.Root), "replacement")); err != nil {
			t.Fatal(err)
		}
		if err := validateAbsence(t, f, key, request); !errors.Is(err, storage.ErrRemoteChanged) {
			t.Fatalf("writer accepted a replacement root: %v", err)
		}
	})
	t.Run("root identity replaced", func(t *testing.T) {
		f, key, request := setup(t)
		if _, err := f.s.db.Exec(`UPDATE library_sources SET root_identity='remote-dir:other' WHERE id=?`, f.library); err != nil {
			t.Fatal(err)
		}
		if err := validateAbsence(t, f, key, request); !errors.Is(err, storage.ErrRemoteChanged) {
			t.Fatalf("writer accepted a replacement root identity: %v", err)
		}
	})
	t.Run("library incarnation moved", func(t *testing.T) {
		f, key, request := setup(t)
		if _, err := f.s.db.Exec(`UPDATE library_sources SET incarnation='replacement' WHERE id=?`, f.library); err != nil {
			t.Fatal(err)
		}
		if err := validateAbsence(t, f, key, request); !errors.Is(err, storage.ErrRemoteChanged) {
			t.Fatalf("writer accepted a replacement incarnation: %v", err)
		}
	})
	t.Run("health epoch moved", func(t *testing.T) {
		f, key, request := setup(t)
		if _, err := f.s.db.Exec(`UPDATE catalog_source_verification SET health_epoch=health_epoch+1 WHERE source_id=?`, f.library); err != nil {
			t.Fatal(err)
		}
		if err := validateAbsence(t, f, key, request); !errors.Is(err, storage.ErrRemoteChanged) {
			t.Fatalf("writer accepted a superseded health epoch: %v", err)
		}
	})
	t.Run("verification no longer pending", func(t *testing.T) {
		f, key, request := setup(t)
		if _, err := f.s.db.Exec(`UPDATE catalog_source_verification SET pending=0 WHERE source_id=?`, f.library); err != nil {
			t.Fatal(err)
		}
		if err := validateAbsence(t, f, key, request); !errors.Is(err, storage.ErrRemoteChanged) {
			t.Fatalf("writer accepted a closed walk: %v", err)
		}
	})
	t.Run("another verification key listing", func(t *testing.T) {
		f, _, request := setup(t)
		if err := validateAbsence(t, f, verificationKey(f.library, 2), request); !errors.Is(err, storage.ErrRemoteChanged) {
			t.Fatalf("writer accepted another epoch's listing: %v", err)
		}
	})
	t.Run("listing no longer complete", func(t *testing.T) {
		f, key, request := setup(t)
		if _, err := f.s.db.Exec(`UPDATE remote_listing_sessions SET ready=0 WHERE id='listing'`); err != nil {
			t.Fatal(err)
		}
		if err := validateAbsence(t, f, key, request); !errors.Is(err, storage.ErrRemoteChanged) {
			t.Fatalf("writer accepted partial evidence: %v", err)
		}
	})
	t.Run("file returned", func(t *testing.T) {
		f, key, request := setup(t)
		if _, err := f.s.db.Exec(`INSERT INTO remote_listing_entries(listing_id,name,snapshot) VALUES('listing',?,'{}')`, filepath.Join(request.Root, request.RelativePath)); err != nil {
			t.Fatal(err)
		}
		if err := validateAbsence(t, f, key, request); !errors.Is(err, storage.ErrInventoryChanged) {
			t.Fatalf("writer retired a returned file: %v", err)
		}
	})
	t.Run("foreign namespace listing", func(t *testing.T) {
		f, key, request := setup(t)
		if _, err := f.s.db.Exec(`UPDATE remote_listing_sessions SET source_id='another-source' WHERE id='listing'`); err != nil {
			t.Fatal(err)
		}
		if err := validateAbsence(t, f, key, request); !errors.Is(err, storage.ErrRemoteChanged) {
			t.Fatalf("writer accepted another source's listing: %v", err)
		}
	})
	t.Run("source failed after proof", func(t *testing.T) {
		f, key, request := setup(t)
		if _, err := f.s.db.Exec(`UPDATE remote_sources SET state='credentials_required',error_code='credentials_required' WHERE id='remote'`); err != nil {
			t.Fatal(err)
		}
		if err := validateAbsence(t, f, key, request); !errors.Is(err, storage.ErrRemoteCredentials) {
			t.Fatalf("writer accepted a failed source: %v", err)
		}
	})
}
