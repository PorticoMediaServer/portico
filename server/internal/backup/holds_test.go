package backup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

func holdRows(t *testing.T, db *sql.DB) (n int, state string) {
	t.Helper()
	if err := db.QueryRow(`SELECT count(*),COALESCE(max(state),'') FROM backup_blob_holds`).Scan(&n, &state); err != nil {
		t.Fatal(err)
	}
	return n, state
}

// The artwork hold exists from before the first database step until the
// backup is published or abandoned, and never outlives it.
func TestBackupHoldsArtworkUntilItEnds(t *testing.T) {
	s, _, db := testService(t)
	seen := ""
	s.SetCopyScrubber(func(context.Context, *dbwork.Candidate) error {
		n, state := holdRows(t, db)
		seen = fmt.Sprintf("%d %s", n, state)
		return nil
	})
	if _, err := s.Create(context.Background(), KindManual); err != nil {
		t.Fatal(err)
	}
	if seen != "1 copying" {
		t.Fatalf("hold while the copy was checked: %q", seen)
	}
	if n, _ := holdRows(t, db); n != 0 {
		t.Fatal("a published backup kept its hold")
	}
	failure := errors.New("scrub failed")
	s.SetCopyScrubber(func(context.Context, *dbwork.Candidate) error { return failure })
	if _, err := s.Create(context.Background(), KindManual); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if n, _ := holdRows(t, db); n != 0 {
		t.Fatal("an abandoned backup kept its hold")
	}
}

// A hold left by an earlier process is that process's interrupted backup:
// start-up removes it. This process's own hold is never touched.
func TestStartupReleasesOnlyAnInterruptedHold(t *testing.T) {
	s, _, db := testService(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO backup_blob_holds(singleton,backup_id,process_epoch,scratch_token,created_ms,state) VALUES(1,'old','earlier-process','scratch',0,'files')`); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseInterruptedHold(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := holdRows(t, db); n != 0 {
		t.Fatal("an interrupted backup's hold survived start-up")
	}
	if _, err := db.Exec(`INSERT INTO backup_blob_holds(singleton,backup_id,process_epoch,scratch_token,created_ms,state) VALUES(1,'mine',?,'scratch',0,'copying')`, s.epoch); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseInterruptedHold(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := holdRows(t, db); n != 1 {
		t.Fatal("start-up removed a running backup's hold")
	}
}

// P2-17: a backup carries owner artwork, not provider art. Restored where the
// provider files are absent (another machine, or a cleaned folder), a
// provider-selected source comes back file-less: the restore succeeds, the
// selection is intact, and opening it queues re-acquisition instead of
// failing, while the owner's upload opens from the files the backup carried.
func TestRestoreOfAFileLessArtworkSource(t *testing.T) {
	s, _, state := policyFixture(t)
	ctx := context.Background()
	c := catalogtest.New(t, s.db)
	lib := c.Library("films", "Films", "movie", "/fixture")
	item := c.Movie(lib, "/fixture/movie.mkv", "Fixture", 2020)
	artwork := filepath.Join(state, "Metadata", "artwork")
	m := metadata.New(s.db, "")
	if err := m.SetArtworkDirectory(artwork); err != nil {
		t.Fatal(err)
	}
	target := metadata.RepairTarget{Kind: "item", ID: item.Public}
	auth := func(*sql.Tx) error { return nil }
	before, err := m.RepairState(ctx, target, auth)
	if err != nil {
		t.Fatal(err)
	}
	picture := image.NewRGBA(image.Rect(0, 0, 24, 36))
	picture.Set(0, 0, color.RGBA{R: 33, A: 255})
	var encoded bytes.Buffer
	if err = png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	actor := metadata.MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}
	if _, err = m.UploadArtwork(ctx, target, "poster", "", before.Revision, encoded.Bytes(), actor, auth); err != nil {
		t.Fatal(err)
	}
	// Provider art for the backdrop: a source, its one file, and an ordinary
	// (unlocked, actor-less) selection.
	provider := fmt.Sprintf("%064x", 77)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,'image/jpeg',1,1,1,'now')`, provider)
	exec(`INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) VALUES(?,'image/jpeg',1,1,?,?,?,1920,0,0,1,'now')`, provider, provider, provider, provider)
	exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,subject,provider,image_id,origin,attribution,source_fence,observed_at) VALUES('provider-backdrop','item',?,'backdrop','','fixture','image','remote','fixture','fence','now')`, item.ID)
	exec(`INSERT INTO artwork_selections(kind,entity_id,role,subject,candidate_id,digest,locked,revision,actor,observed_at) VALUES('item',?,'backdrop','','provider-backdrop',?,0,1,'','now')`, item.ID, provider)
	if err = os.WriteFile(filepath.Join(artwork, provider+".img"), []byte("provider bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	id, err := s.Create(ctx, KindManual)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(state, BackupsDir, id, "Metadata", "artwork", provider+".img")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the backup carried provider art", err)
	}
	if _, err = s.Stage(ctx, Source{BackupID: id}); err != nil {
		t.Fatal(err)
	}
	if err = s.db.Close(); err != nil {
		t.Fatal(err)
	}
	// The provider file is not where the restore lands.
	if err = os.Remove(filepath.Join(artwork, provider+".img")); err != nil {
		t.Fatal(err)
	}
	if err = ApplyStaged(ctx, state, nil); err != nil {
		t.Fatal(err)
	}
	restored, err := persistence.Open(storagepaths.Path(state, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var selected string
	if err = restored.QueryRow(`SELECT digest FROM artwork_selections WHERE kind='item' AND entity_id=? AND role='backdrop'`, item.ID).Scan(&selected); err != nil || selected != provider {
		t.Fatalf("restored selection %q %v", selected, err)
	}
	after := metadata.New(restored, "")
	if err = after.SetArtworkDirectory(artwork); err != nil {
		t.Fatal(err)
	}
	if file, _, err := after.EntityArtwork(ctx, target, "backdrop", "", false); !errors.Is(err, metadata.ErrArtworkPending) {
		if file != nil {
			file.Close()
		}
		t.Fatalf("file-less provider art: %v", err)
	}
	file, _, err := after.EntityArtwork(ctx, target, "poster", "", false)
	if err != nil {
		t.Fatalf("owner upload after restore: %v", err)
	}
	file.Close()
}

// A hold whose release failed (same service, stale) never blocks the next
// backup: one backup runs at a time, so the next one replaces it.
func TestAStaleHoldDoesNotBlockTheNextBackup(t *testing.T) {
	s, _, db := testService(t)
	if _, err := db.Exec(`INSERT INTO backup_blob_holds(singleton,backup_id,process_epoch,scratch_token,created_ms,state) VALUES(1,'earlier','` + s.epoch + `','scratch',0,'files')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(context.Background(), KindManual); err != nil {
		t.Fatalf("a stale hold blocked the next backup: %v", err)
	}
	if n, _ := holdRows(t, db); n != 0 {
		t.Fatal("the backup left a hold")
	}
}

// A scheduled backup takes even its hold as a maintenance ticket within its
// no-progress budget: under constant user writes it gives up with
// ErrBackupPressure before copying, instead of waiting without end.
func TestScheduledBackupGivesUpOnItsHoldUnderConstantWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, _, db := testService(t)
	s.limits = func(kind Kind) copyLimits {
		limits := copyLimitsFor(kind)
		limits.noProgress = 300 * time.Millisecond
		return limits
	}
	if _, err := db.Exec(`CREATE TABLE churn(body BLOB)`); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				_ = dbwork.WithWriteTx(ctx, db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, `INSERT INTO churn VALUES(randomblob(64))`)
					time.Sleep(2 * time.Millisecond)
					return err
				})
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	_, err := s.Create(context.Background(), KindScheduled)
	stop()
	wg.Wait()
	if !errors.Is(err, ErrBackupPressure) {
		t.Fatalf("scheduled backup under constant writes: %v", err)
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Fatal("a failed backup was listed")
	}
}

// An artwork file the collector moved aside a moment before the backup's hold
// is still carried: the moved-aside file holds the same bytes.
func TestBackupCarriesOwnerArtworkACollectorMovedAside(t *testing.T) {
	s, _, state := policyFixture(t)
	ctx := context.Background()
	c := catalogtest.New(t, s.db)
	item := c.Movie(c.Library("films", "Films", "movie", "/fixture"), "/fixture/movie.mkv", "Fixture", 2020)
	artwork := filepath.Join(state, "Metadata", "artwork")
	m := metadata.New(s.db, "")
	if err := m.SetArtworkDirectory(artwork); err != nil {
		t.Fatal(err)
	}
	target := metadata.RepairTarget{Kind: "item", ID: item.Public}
	auth := func(*sql.Tx) error { return nil }
	before, err := m.RepairState(ctx, target, auth)
	if err != nil {
		t.Fatal(err)
	}
	picture := image.NewRGBA(image.Rect(0, 0, 24, 36))
	var encoded bytes.Buffer
	if err = png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	if _, err = m.UploadArtwork(ctx, target, "poster", "", before.Revision, encoded.Bytes(), metadata.MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}, auth); err != nil {
		t.Fatal(err)
	}
	var blob string
	if err = s.db.QueryRow(`SELECT s.small_blob FROM artwork_uploads u JOIN artwork_sources s ON s.digest=u.digest`).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	// Between the database copy and the file copy, the file is in transit.
	s.SetCopyScrubber(func(context.Context, *dbwork.Candidate) error {
		return os.Rename(filepath.Join(artwork, blob+".img"), filepath.Join(artwork, blob+".img.tomb-test"))
	})
	id, err := s.Create(ctx, KindManual)
	if err != nil {
		t.Fatalf("a moved-aside owner file failed the backup: %v", err)
	}
	if got := fileHashOrEmpty(t, filepath.Join(state, BackupsDir, id, "Metadata", "artwork", blob+".img")); got != blob {
		t.Fatal("the moved-aside owner file was not carried")
	}
}

// With the whole artwork folder included, the owner's artwork is still
// checked against the digest the database names: changed bytes under the
// same name fail the backup instead of being carried.
func TestFullArtworkBackupStillChecksOwnerArtwork(t *testing.T) {
	s, p, state := policyFixture(t)
	ctx := context.Background()
	c := catalogtest.New(t, s.db)
	item := c.Movie(c.Library("films", "Films", "movie", "/fixture"), "/fixture/movie.mkv", "Fixture", 2020)
	artwork := filepath.Join(state, "Metadata", "artwork")
	m := metadata.New(s.db, "")
	if err := m.SetArtworkDirectory(artwork); err != nil {
		t.Fatal(err)
	}
	target := metadata.RepairTarget{Kind: "item", ID: item.Public}
	auth := func(*sql.Tx) error { return nil }
	before, err := m.RepairState(ctx, target, auth)
	if err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err = png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 24, 36))); err != nil {
		t.Fatal(err)
	}
	if _, err = m.UploadArtwork(ctx, target, "poster", "", before.Revision, encoded.Bytes(), metadata.MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}, auth); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Update(ctx, SettingsPatch{IncludeArtwork: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create(ctx, KindManual); err != nil {
		t.Fatalf("a healthy full-artwork backup failed: %v", err)
	}
	var blob string
	if err = s.db.QueryRow(`SELECT s.small_blob FROM artwork_uploads u JOIN artwork_sources s ON s.digest=u.digest`).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(artwork, blob+".img"), []byte("not the uploaded picture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create(ctx, KindManual); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("changed owner artwork was carried: %v", err)
	}
}
