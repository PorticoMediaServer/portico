package metadata

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

type integrationScreenProvider struct {
	calls  int
	record metadataprovider.ScreenRecord
}

func (p *integrationScreenProvider) SearchScreen(context.Context, string, string, int, string, string) ([]metadataprovider.ScreenRecord, error) {
	p.calls++
	return []metadataprovider.ScreenRecord{p.record}, nil
}
func (p *integrationScreenProvider) ScreenDetails(context.Context, string, string, string, string) (metadataprovider.ScreenRecord, error) {
	p.calls++
	return p.record, nil
}
func integrationExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, e := db.Exec(q, args...); e != nil {
		t.Fatal(e)
	}
}
func integrationValue(t *testing.T, db *sql.DB, q string, args ...any) string {
	t.Helper()
	var v string
	if e := db.QueryRow(q, args...).Scan(&v); e != nil {
		t.Fatal(e)
	}
	return v
}

func integrationPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	im.Set(0, 0, color.RGBA{R: 200, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func integrationScreen(t *testing.T) (*Service, *sql.DB, RepairTarget, *integrationScreenProvider) {
	t.Helper()
	s, db, target := repairFixture(t)
	integrationExec(t, db, `UPDATE screen_metadata_policies SET providers='["tmdb"]';UPDATE screen_metadata_consent SET confirmed=1,revision=revision+1`)
	p := &integrationScreenProvider{record: metadataprovider.ScreenRecord{Identity: metadataprovider.ScreenID{Provider: "tmdb", Type: "movie", ID: "42"}, Title: "Before", Year: 2020, Overview: "Provider description", PosterPath: "/poster.jpg"}}
	s.screenProviders = map[string]ScreenProvider{"tmdb": p}
	return s, db, target, p
}

func integrationMovie(t *testing.T, db *sql.DB) catalogtest.Item {
	t.Helper()
	var item catalogtest.Item
	if err := db.QueryRow(`SELECT id,pid(public_id) FROM catalog_entities WHERE kind=? AND title='Before' ORDER BY id LIMIT 1`, int(compactcatalog.Movie)).Scan(&item.ID, &item.Public); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT a.id,a.token FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=? ORDER BY a.id LIMIT 1`, item.ID).Scan(&item.Asset, &item.Token); err != nil {
		t.Fatal(err)
	}
	return item
}
func TestW2I01StartupAndNFOCanonicalAssetForeignKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.sqlite")
	for n := 0; n < 2; n++ {
		db, e := persistence.Open(path)
		if e != nil {
			t.Fatal(e)
		}
		if n == 0 {
			c := catalogtest.New(t, db)
			library := c.Library("lib", "Movies", "movie", "/local")
			item := c.Movie(library, "/local/a.mp4", "Film", 2020)
			integrationExec(t, db, `INSERT INTO video_nfo_status VALUES('lib',?,'ready','');INSERT INTO video_nfo_evidence VALUES('lib',?,'local-nfo','movie','', 'digest','{}',1,1,'2026-09-07')`, item.Token, item.Token)
		}
		if got := integrationValue(t, db, `SELECT count(*) FROM video_nfo_evidence`); got != "1" {
			t.Fatal(got)
		}
		if got := integrationValue(t, db, `SELECT count(*) FROM pragma_foreign_key_check`); got != "0" {
			t.Fatal("invalid schema references", got)
		}
		db.Close()
	}
}
func TestW2I01GenericScreenIdentifyUsesActiveWorkerAndHistory(t *testing.T) {
	s, db, target, p := integrationScreen(t)
	item := integrationMovie(t, db)
	ctx := context.Background()
	actor := MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}
	state, e := s.RepairState(ctx, target, integrationOwner)
	if e != nil {
		t.Fatal(e)
	}
	title := "Owner title"
	state, e = s.Repair(ctx, target, RepairCommand{Action: "edit", ExpectedRevision: state.Revision, Fields: map[string]RepairFieldEdit{"title": {Value: &title}}}, actor, integrationOwner)
	if e != nil {
		t.Fatal(e)
	}
	state, e = s.Repair(ctx, target, RepairCommand{Action: "search", ExpectedRevision: state.Revision, Provider: "tmdb", Query: "Before"}, actor, integrationOwner)
	if e != nil {
		t.Fatal(e)
	}
	if p.calls != 0 {
		t.Fatal("interactive search performed provider IO")
	}
	if e = s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	state, e = s.RepairState(ctx, target, integrationOwner)
	if e != nil {
		t.Fatal(e)
	}
	if state.Snapshot.Identity.Engine != "screen" || len(state.Candidates) != 1 {
		t.Fatalf("disconnected generic review: %+v", state)
	}
	calls := p.calls
	cmd := RepairCommand{Action: "identify", ExpectedRevision: state.Revision, Provider: "tmdb", CandidateID: state.Candidates[0].ID, Confirm: true}
	state, e = s.Repair(ctx, target, cmd, actor, integrationOwner)
	if e != nil {
		t.Fatal(e)
	}
	if p.calls != calls || !state.Snapshot.Identity.Locked || state.Snapshot.Identity.ID != "42" {
		t.Fatal("identify not durable/fenced", state)
	}
	if _, e = s.Repair(ctx, target, cmd, actor, integrationOwner); !errors.Is(e, ErrRepairConflict) {
		t.Fatal("stale identify replay", e)
	}
	if e = s.ScreenStep(ctx); e != nil {
		t.Fatal(e)
	}
	if got := integrationValue(t, db, `SELECT accepted_publication FROM screen_metadata_work WHERE target_id=?`, item.ID); got == "" {
		t.Fatal("active worker did not publish")
	}
	if got := integrationValue(t, db, `SELECT title FROM catalog_entities WHERE id=?`, item.ID); got != title {
		t.Fatal("owner title lost", got)
	}
	if got := integrationValue(t, db, `SELECT count(*) FROM metadata_owner_history WHERE trigger='identify' AND actor='local:owner:profile'`); got != "1" {
		t.Fatal("identify actor/history missing", got)
	}
	if got := integrationValue(t, db, `SELECT count(*) FROM artwork_dirty WHERE kind='item' AND entity_id=?`, item.ID); got != "1" {
		t.Fatal("publication did not dirty artwork")
	}
	if e = s.seedArtwork(ctx); e != nil {
		t.Fatal(e)
	}
	if got := integrationValue(t, db, `SELECT identity FROM artwork_discovery WHERE entity_id=? AND provider='tmdb'`, item.ID); got != "42" {
		t.Fatal(got)
	}
	// A replacement is desired but not yet accepted: never relabel old artwork.
	integrationExec(t, db, `UPDATE screen_metadata_work SET provider_id='43',selection_revision=selection_revision+1,generation=generation+1 WHERE target_id=?`, item.ID)
	if e = s.seedArtwork(ctx); e != nil {
		t.Fatal(e)
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	r, has, e := acceptedScreenArtwork(ctx, tx, target)
	tx.Rollback()
	if e != nil || !has || r != nil {
		t.Fatal("stale accepted artwork became current", r, has, e)
	}
	integrationExec(t, db, `UPDATE screen_metadata_consent SET confirmed=0,revision=revision+1`)
	tx, e = db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	enabled, e := s.artworkProviderEnabled(ctx, tx, target, "tmdb")
	tx.Rollback()
	if e != nil || enabled {
		t.Fatal("artwork bypassed screen consent", e)
	}
}
func TestW2I01CascadeTerminalReceiptAndSupersession(t *testing.T) {
	for _, superseded := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "superseded"}[superseded], func(t *testing.T) {
			s, db, target, _ := integrationScreen(t)
			ctx := context.Background()
			if e := s.ScreenStep(ctx); e != nil {
				t.Fatal(e)
			}
			now := time.Now().UTC()
			s.now = func() time.Time { return now }
			state, e := s.RepairState(ctx, target, integrationOwner)
			if e != nil {
				t.Fatal(e)
			}
			_, e = s.Repair(ctx, target, RepairCommand{Action: "cascade", ExpectedRevision: state.Revision, Intent: "refresh_unlocked", Confirm: true}, MBActor{Authority: "local", AccountID: "owner"}, integrationOwner)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.RepairCascadeStep(ctx); e != nil {
				t.Fatal(e)
			}
			if got := integrationValue(t, db, `SELECT status FROM metadata_repair_cascade_items`); got != "queued" {
				t.Fatal("admission mislabeled completion", got)
			}
			if superseded {
				item := integrationMovie(t, db)
				integrationExec(t, db, `UPDATE screen_metadata_work SET generation=generation+1,provider_id='99' WHERE target_id=?`, item.ID)
			} else if e = s.ScreenStep(ctx); e != nil {
				t.Fatal(e)
			}
			now = now.Add(3 * time.Second)
			if e = s.RepairCascadeStep(ctx); e != nil {
				t.Fatal(e)
			}
			want := "complete"
			parent := "complete"
			if superseded {
				want = "superseded"
				parent = "partial"
			}
			if got := integrationValue(t, db, `SELECT status FROM metadata_repair_cascade_items`); got != want {
				t.Fatal(got, want)
			}
			if got := integrationValue(t, db, `SELECT status FROM metadata_repair_cascades`); got != parent {
				t.Fatal(got, parent)
			}
		})
	}
}
func TestW2I01ArtworkReaderRejectsSymlinkAndInstallerRepairsIt(t *testing.T) {
	s, _, _ := repairFixture(t)
	raw := integrationPNG(t, 20, 30)
	a, e := s.installArtwork(raw, 20, 30)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(s.cacheRoot, a.digest+".img")
	outside := filepath.Join(t.TempDir(), "private")
	if e = os.WriteFile(outside, []byte("PRIVATE"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(outside, path); e != nil {
		t.Skip("symlink unavailable:", e)
	}
	if f, e := s.openArtworkObject(a.digest); e == nil {
		f.Close()
		t.Fatal("followed cache symlink")
	}
	if _, e = s.installArtwork(raw, 20, 30); e != nil {
		t.Fatal(e)
	}
	if f, e := s.openArtworkObject(a.digest); e != nil {
		t.Fatal(e)
	} else {
		f.Close()
	}
	b, e := os.ReadFile(outside)
	if e != nil || string(b) != "PRIVATE" {
		t.Fatal("installer changed outside file", e)
	}
	if _, e = s.openArtworkObject(strings.Repeat("../", 22)); e == nil {
		t.Fatal("invalid digest accepted")
	}
}

func integrationOwner(*sql.Tx) error { return nil }

func TestW2I01CoverArtUsesAcceptedCanonicalReleaseNotPendingIdentity(t *testing.T) {
	s, db, _ := repairFixture(t)
	ctx := context.Background()
	c := catalogtest.New(t, db)
	music := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Album", 2020)
	canonical := "22222222-2222-2222-2222-222222222222"
	requested := "11111111-1111-1111-1111-111111111111"
	staged, e := stageMBRelease(metadataprovider.ReleaseLookup{RequestedID: requested, Release: metadataprovider.Release{ID: canonical, Title: "Album", ReleaseGroup: metadataprovider.ReleaseGroup{ID: "33333333-3333-3333-3333-333333333333", Title: "Album"}}})
	if e != nil {
		t.Fatal(e)
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	revision, e := saveMBReleaseEvidence(ctx, tx, staged, "2026-09-07")
	if e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = mbFixturePinTx(tx, mbFixturePinIntent{Link: "album", Entity: album.ID, NewRelease: revision}, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO mb_album_links VALUES(?,?,?,?,?)`, album.ID, revision, canonical, requested, "2026-09-07")
		return err
	}); e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if _, e = tx.ExecContext(ctx, `UPDATE mb_jobs SET selected_id=? WHERE kind='album' AND entity_id=?`, requested, album.ID); e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if e = s.seedArtwork(ctx); e != nil {
		t.Fatal(e)
	}
	if got := integrationValue(t, db, `SELECT identity FROM artwork_discovery WHERE kind='album' AND entity_id=?`, album.ID); got != canonical {
		t.Fatal("alias was not resolved to accepted release", got)
	}
	integrationExec(t, db, `DELETE FROM artwork_discovery WHERE kind='album';UPDATE mb_jobs SET selected_id='44444444-4444-4444-4444-444444444444',generation=generation+1 WHERE kind='album' AND entity_id=?`, album.ID)
	if e = s.seedArtwork(ctx); e != nil {
		t.Fatal(e)
	}
	if got := integrationValue(t, db, `SELECT count(*) FROM artwork_discovery WHERE kind='album' AND entity_id=?`, album.ID); got != "0" {
		t.Fatal("pending replacement used old accepted CAA identity", got)
	}
}

func TestW2I01ArtworkCascadeTracksInstalledChild(t *testing.T) {
	s, db, target := repairFixture(t)
	item := integrationMovie(t, db)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "local.png")
	if e := os.WriteFile(path, integrationPNG(t, 30, 40), 0600); e != nil {
		t.Fatal(e)
	}
	s.LocalArtwork = func(string) (*os.File, string, error) { f, e := os.Open(path); return f, "image/png", e }
	catalogtest.New(t, db).Fields(item.ID, map[string]any{"poster_url": "local:fixture"})
	if e := s.ArtworkStep(ctx); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	state, e := s.RepairState(ctx, target, integrationOwner)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Repair(ctx, target, RepairCommand{Action: "cascade", Intent: "repair_assets", Confirm: true, ExpectedRevision: state.Revision}, MBActor{Authority: "local", AccountID: "owner"}, integrationOwner)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RepairCascadeStep(ctx); e != nil {
		t.Fatal(e)
	}
	if got := integrationValue(t, db, `SELECT status FROM metadata_repair_cascade_items`); got != "queued" {
		t.Fatal(got)
	}
	if e = s.ArtworkStep(ctx); e != nil {
		t.Fatal(e)
	}
	now = now.Add(3 * time.Second)
	if e = s.RepairCascadeStep(ctx); e != nil {
		t.Fatal(e)
	}
	if got := integrationValue(t, db, `SELECT status FROM metadata_repair_cascades`); got != "complete" {
		t.Fatal(got)
	}
}
