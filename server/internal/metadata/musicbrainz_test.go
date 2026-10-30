package metadata

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

const mbRecord = "11111111-1111-1111-1111-111111111111"
const mbRelease = "22222222-2222-2222-2222-222222222222"
const mbGroup = "33333333-3333-3333-3333-333333333333"
const mbTrack = "44444444-4444-4444-4444-444444444444"
const mbArtistID = "55555555-5555-5555-5555-555555555555"

type mbFixture struct {
	searchRecord  func(context.Context, string, string) ([]metadataprovider.Recording, error)
	searchRelease func(context.Context, string, string) ([]metadataprovider.ReleaseCandidate, error)
	record        func(context.Context, string) (metadataprovider.RecordingLookup, error)
	release       func(context.Context, string) (metadataprovider.ReleaseLookup, error)
}

func (f mbFixture) SearchRecordings(c context.Context, t, a string) ([]metadataprovider.Recording, error) {
	return f.searchRecord(c, t, a)
}
func (f mbFixture) SearchReleases(c context.Context, t, a string) ([]metadataprovider.ReleaseCandidate, error) {
	return f.searchRelease(c, t, a)
}
func (f mbFixture) Recording(c context.Context, id string) (metadataprovider.RecordingLookup, error) {
	return f.record(c, id)
}
func (f mbFixture) Release(c context.Context, id string) (metadataprovider.ReleaseLookup, error) {
	return f.release(c, id)
}
func mbDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db")
	db, e := persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	c := catalogtest.New(t, db)
	lib := c.Library("lib", "Music", "music", "/music")
	artist := c.Artist(lib, "Artist")
	album := c.Album(artist, "Local Album", 2020)
	song := c.Song(album, 3, "/music/song.flac", "Local Song")
	c.Exec(`UPDATE mb_jobs SET album_id=? WHERE kind=? AND entity_id=?`, album.ID, "album", album.ID)
	c.Exec(`UPDATE mb_jobs SET item_id=? WHERE kind=? AND entity_id=?`, song.ID, "song", song.ID)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if err := compactcatalog.SetFactsTx(ctx, tx, song.ID, map[string]any{"disc_number": 2}); err != nil {
			return err
		}
		return compactcatalog.SetAssetTx(ctx, tx, song.Asset, map[string]any{"size": int64(1), "modified_ns": int64(1), "container": "flac", "video_codec": "", "audio_codec": "flac", "width": 0, "height": 0, "duration": 100.0})
	})
	c.Drain()
	settleMetadataCatalogue(t, db)
	return db, path
}

// mbIntID/mbPublicID look a fixture entity up by its title: catalogue ids
// are integers inside and public ids on the API, never the old text keys.
func mbIntID(t *testing.T, db *sql.DB, title string) int64 {
	t.Helper()
	var id int64
	if e := db.QueryRow(`SELECT id FROM catalog_entities WHERE title=?`, title).Scan(&id); e != nil {
		t.Fatal(e)
	}
	return id
}
func mbPublicID(t *testing.T, db *sql.DB, title string) string {
	t.Helper()
	var public string
	if e := db.QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE title=?`, title).Scan(&public); e != nil {
		t.Fatal(e)
	}
	return public
}
func mbAssetToken(t *testing.T, db *sql.DB) string {
	t.Helper()
	var token string
	if e := db.QueryRow(`SELECT a.token FROM catalog_entities e JOIN catalog_asset_links l ON l.entity_id=e.id JOIN catalog_assets a ON a.id=l.asset_id WHERE e.title='Local Song'`).Scan(&token); e != nil {
		t.Fatal(e)
	}
	return token
}
func mbRecordingFixture() metadataprovider.Recording {
	return metadataprovider.Recording{ID: mbRecord, Title: "Provider Song", ArtistCredit: []metadataprovider.ArtistCredit{{Name: "Artist", Artist: metadataprovider.Artist{ID: mbArtistID, Name: "Artist"}}}}
}
func mbReleaseFixture() metadataprovider.Release {
	return metadataprovider.Release{ID: mbRelease, Title: "Provider Edition", Date: "2020-01-01", Country: "CA", ReleaseGroup: metadataprovider.ReleaseGroup{ID: mbGroup, Title: "Album Group"}, Media: []metadataprovider.Medium{{Position: 2, Format: "CD", Tracks: []metadataprovider.ReleaseTrack{{ID: mbTrack, Position: 3, Number: "3", Title: "Local Song", ArtistCredit: mbRecordingFixture().ArtistCredit, Recording: mbRecordingFixture()}}}}}
}

// driveMBCatalogAdoptions drives the separately scheduled catalogue-domain
// jobs to terminal. Provider steps never execute that worker themselves, and
// an MB album adoption enqueues a further artist adoption while it runs, so the
// pump re-reads runnable jobs every turn instead of snapshotting roots once.
func driveMBCatalogAdoptions(t *testing.T, db *sql.DB) {
	t.Helper()
	drainCatalogMutations(t, db, catalog.New(db).MutationAdapter())
}

func TestMusicBrainzTagsReleaseRecordingRemainDistinctAndRestart(t *testing.T) {
	db, path := mbDB(t)
	defer func() { db.Close() }()
	asset := mbAssetToken(t, db)
	_, e := db.Exec(`INSERT INTO audio_tag_evidence VALUES('lib',?,'musicbrainz_trackid','embedded',?),('lib',?,'musicbrainz_albumid','embedded',?),('lib',?,'musicbrainz_releasetrackid','embedded',?)`, asset, mbRecord, asset, mbRelease, asset, mbTrack)
	if e != nil {
		t.Fatal(e)
	}
	s := New(db, "")
	calls := 0
	fixture := mbFixture{release: func(c context.Context, id string) (metadataprovider.ReleaseLookup, error) {
		calls++
		return metadataprovider.ReleaseLookup{RequestedID: id, Release: mbReleaseFixture()}, nil
	}, record: func(c context.Context, id string) (metadataprovider.RecordingLookup, error) {
		calls++
		return metadataprovider.RecordingLookup{RequestedID: id, Recording: mbRecordingFixture()}, nil
	}}
	s.mb = fixture
	if e = s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	state, e := s.MusicBrainzState("album", mbPublicID(t, db, "Local Album"))
	if e != nil || state.Status != "matched" {
		t.Fatal(state, e)
	}
	db.Close()
	db, e = persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	s = New(db, "")
	s.mb = fixture
	driveMBCatalogAdoptions(t, db)
	if e = s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	settleMetadataCatalogue(t, db)
	item, e := catalog.New(db).Get("p", mbPublicID(t, db, "Local Song"))
	if e != nil || item.Title != "Local Song" || item.Song.AlbumTitle != "Local Album" || item.Song.ProviderRecordingID != mbRecord || item.Song.ProviderReleaseID != mbRelease || item.Song.ProviderReleaseGroupID != mbGroup || item.Song.ProviderTrackID != mbTrack || item.Song.ProviderReleaseStatus != "matched" || *item.Song.DiscNumber != 2 || *item.Song.TrackNumber != 3 {
		t.Fatal(item, e)
	}
	if calls != 2 {
		t.Fatal("expected one release lookup and one independently verified recording lookup", calls)
	}
	var before string
	if e = db.QueryRow(`SELECT observed_at FROM mb_recording_evidence WHERE provider_id=?`, mbRecord).Scan(&before); e != nil {
		t.Fatal(e)
	}
	db.Exec(`UPDATE mb_jobs SET status='pending' WHERE kind='song'`)
	if e = s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	var after string
	if e = db.QueryRow(`SELECT observed_at FROM mb_recording_evidence WHERE provider_id=?`, mbRecord).Scan(&after); e != nil {
		t.Fatal(e)
	}
	if before != after || calls != 2 {
		t.Fatal("cache observation fabricated", before, after, calls)
	}
}
func TestMusicBrainzEditionsRequireReviewAndManualChoiceSurvivesRescan(t *testing.T) {
	db, _ := mbDB(t)
	defer db.Close()
	s := New(db, "")
	s.mb = mbFixture{searchRelease: func(context.Context, string, string) ([]metadataprovider.ReleaseCandidate, error) {
		return []metadataprovider.ReleaseCandidate{{ID: mbRelease, Title: "Local Album", Country: "CA", Date: "2020"}, {ID: mbGroup, Title: "Local Album", Country: "US", Date: "2020"}}, nil
	}, release: func(c context.Context, id string) (metadataprovider.ReleaseLookup, error) {
		return metadataprovider.ReleaseLookup{RequestedID: id, Release: mbReleaseFixture()}, nil
	}}
	if e := s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	state, e := s.MusicBrainzState("album", mbPublicID(t, db, "Local Album"))
	if e != nil || state.Status != "needs_selection" || state.SelectedID != "" || len(state.Candidates) != 2 {
		t.Fatal(state, e)
	}
	choice := MBSelection{ExpectedRevision: state.Revision, ProviderID: mbRelease, Actor: MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}}
	if e = s.SelectMusicBrainz("album", mbPublicID(t, db, "Local Album"), choice, nil); e != nil {
		t.Fatal(e)
	}
	if e = s.SelectMusicBrainz("album", mbPublicID(t, db, "Local Album"), choice, nil); !errors.Is(e, ErrMBConflict) {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE catalog_songs SET track_number=3 WHERE entity_id=?`, mbIntID(t, db, "Local Song")); e != nil {
		t.Fatal(e)
	}
	state, e = s.MusicBrainzState("album", mbPublicID(t, db, "Local Album"))
	if e != nil || !state.Manual || state.SelectedID != mbRelease {
		t.Fatal("manual choice lost", state, e)
	}
	if e = s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestMusicBrainzRecordingAmbiguityAndDurableCooldown(t *testing.T) {
	db, path := mbDB(t)
	defer func() { db.Close() }()
	db.Exec(`UPDATE mb_jobs SET status='needs_selection' WHERE kind='album'`)
	s := New(db, "")
	s.mb = mbFixture{searchRecord: func(context.Context, string, string) ([]metadataprovider.Recording, error) {
		one := mbRecordingFixture()
		one.Title = "Local Song"
		two := one
		two.ID = mbGroup
		return []metadataprovider.Recording{one, two}, nil
	}}
	if e := s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	state, e := s.MusicBrainzState("song", mbPublicID(t, db, "Local Song"))
	if e != nil || state.Status != "needs_selection" || state.SelectedID != "" {
		t.Fatal(state, e)
	}
	if e = s.RetryMusicBrainz("song", mbPublicID(t, db, "Local Song"), state.Revision, nil); e != nil {
		t.Fatal(e)
	}
	s.mb = mbFixture{searchRecord: func(context.Context, string, string) ([]metadataprovider.Recording, error) {
		return nil, &metadataprovider.Error{Provider: "musicbrainz", Status: 429, Code: "request_rejected", RetryAfter: time.Hour}
	}}
	if e = s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	db.Close()
	db, e = persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	s = New(db, "")
	s.mb = mbFixture{searchRecord: func(context.Context, string, string) ([]metadataprovider.Recording, error) {
		t.Fatal("restart bypassed cooldown")
		return nil, nil
	}}
	db.Exec(`UPDATE mb_jobs SET next_attempt='' WHERE kind='song'`)
	if e = s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestMusicBrainzCancellationAndAtomicReleaseFailure(t *testing.T) {
	db, _ := mbDB(t)
	defer db.Close()
	s := New(db, "")
	s.mb = mbFixture{searchRelease: func(c context.Context, _ string, _ string) ([]metadataprovider.ReleaseCandidate, error) {
		<-c.Done()
		return nil, c.Err()
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if e := s.MusicBrainzStep(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	state, e := s.MusicBrainzState("album", mbPublicID(t, db, "Local Album"))
	if e != nil || state.Attempts != 0 || state.Status != "pending" {
		t.Fatal(state, e)
	}
	db.Exec(`INSERT INTO audio_tag_evidence VALUES('lib',?,'musicbrainz_albumid','embedded',?)`, mbAssetToken(t, db), mbRelease)
	bad := mbReleaseFixture()
	bad.Media = append(bad.Media, bad.Media[0])
	s.mb = mbFixture{release: func(c context.Context, id string) (metadataprovider.ReleaseLookup, error) {
		return metadataprovider.ReleaseLookup{RequestedID: id, Release: bad}, nil
	}}
	if e = s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	var n int
	if e := db.QueryRow(`SELECT count(*) FROM mb_release_evidence`).Scan(&n); e != nil {
		t.Fatal(e)
	}
	if n != 0 {
		t.Fatal("partial release escaped transaction")
	}
}
func TestMusicBrainzRecordingConflictDoesNotBecomeReleaseTrackIdentity(t *testing.T) {
	db, _ := mbDB(t)
	defer db.Close()
	asset := mbAssetToken(t, db)
	_, e := db.Exec(`INSERT INTO audio_tag_evidence VALUES('lib',?,'musicbrainz_albumid','embedded',?),('lib',?,'musicbrainz_releasetrackid','embedded',?),('lib',?,'musicbrainz_trackid','embedded',?)`, asset, mbRelease, asset, mbTrack, asset, mbGroup)
	if e != nil {
		t.Fatal(e)
	}
	s := New(db, "")
	s.mb = mbFixture{release: func(c context.Context, id string) (metadataprovider.ReleaseLookup, error) {
		return metadataprovider.ReleaseLookup{RequestedID: id, Release: mbReleaseFixture()}, nil
	}, record: func(c context.Context, id string) (metadataprovider.RecordingLookup, error) {
		r := mbRecordingFixture()
		r.ID = mbGroup
		return metadataprovider.RecordingLookup{RequestedID: id, Recording: r}, nil
	}}
	for i := 0; i < 3; i++ {
		if e = s.MusicBrainzStep(context.Background()); e != nil {
			t.Fatal(e)
		}
		driveMBCatalogAdoptions(t, db)
	}
	settleMetadataCatalogue(t, db)
	item, e := catalog.New(db).Get("p", mbPublicID(t, db, "Local Song"))
	if e != nil || item.Song.ProviderRecordingID != mbGroup || item.Song.ProviderTrackID != "" || item.Song.ProviderReleaseStatus != "recording_conflict" {
		t.Fatal(item, e)
	}
}
func TestMusicBrainzLateReleaseCannotOverwriteManualSelection(t *testing.T) {
	db, _ := mbDB(t)
	defer db.Close()
	s := New(db, "")
	entered, release := make(chan struct{}), make(chan struct{})
	s.mb = mbFixture{searchRelease: func(context.Context, string, string) ([]metadataprovider.ReleaseCandidate, error) {
		return []metadataprovider.ReleaseCandidate{{ID: mbRelease, Title: "One"}, {ID: mbGroup, Title: "Two"}}, nil
	}, release: func(c context.Context, id string) (metadataprovider.ReleaseLookup, error) {
		close(entered)
		<-release
		return metadataprovider.ReleaseLookup{RequestedID: id, Release: mbReleaseFixture()}, nil
	}}
	if e := s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	state, _ := s.MusicBrainzState("album", mbPublicID(t, db, "Local Album"))
	if e := s.SelectMusicBrainz("album", mbPublicID(t, db, "Local Album"), MBSelection{ExpectedRevision: state.Revision, ProviderID: mbRelease, Actor: MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}}, nil); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- s.MusicBrainzStep(context.Background()) }()
	<-entered
	state, _ = s.MusicBrainzState("album", mbPublicID(t, db, "Local Album"))
	if e := s.SelectMusicBrainz("album", mbPublicID(t, db, "Local Album"), MBSelection{ExpectedRevision: state.Revision, ProviderID: mbGroup, Actor: MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}}, nil); e != nil {
		t.Fatal(e)
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	var n int
	if e := db.QueryRow(`SELECT count(*) FROM mb_release_evidence`).Scan(&n); e != nil {
		t.Fatal(e)
	}
	if n != 0 {
		t.Fatal("stale fetched edition published")
	}
	state, _ = s.MusicBrainzState("album", mbPublicID(t, db, "Local Album"))
	if state.SelectedID != mbGroup || !state.Manual {
		t.Fatal(state)
	}
}
func TestMusicBrainzUniqueRecordingSearchNeedsVerifiedLookup(t *testing.T) {
	db, _ := mbDB(t)
	defer db.Close()
	db.Exec(`UPDATE mb_jobs SET status='needs_selection' WHERE kind='album'`)
	s := New(db, "")
	lookups := 0
	s.mb = mbFixture{searchRecord: func(context.Context, string, string) ([]metadataprovider.Recording, error) {
		r := mbRecordingFixture()
		r.Title = "Local Song"
		return []metadataprovider.Recording{r}, nil
	}, record: func(c context.Context, id string) (metadataprovider.RecordingLookup, error) {
		lookups++
		return metadataprovider.RecordingLookup{RequestedID: id, Recording: mbRecordingFixture()}, nil
	}}
	if e := s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	state, e := s.MusicBrainzState("song", mbPublicID(t, db, "Local Song"))
	if e != nil || state.Status != "needs_selection" || state.SelectedID != "" || len(state.Candidates) != 1 || lookups != 0 {
		t.Fatal(state, e)
	}
	if e = s.SelectMusicBrainz("song", mbPublicID(t, db, "Local Song"), MBSelection{ExpectedRevision: state.Revision, ProviderID: mbRecord, Actor: MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}}, nil); e != nil {
		t.Fatal(e)
	}
	if e = s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	state, e = s.MusicBrainzState("song", mbPublicID(t, db, "Local Song"))
	if e != nil || state.Status != "matched" || lookups != 1 {
		t.Fatal(state, e, lookups)
	}
}
func TestMusicBrainzExpiredOrphanCacheCleanup(t *testing.T) {
	db, _ := mbDB(t)
	defer db.Close()
	song := mbIntID(t, db, "Local Song")
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	for _, id := range []string{mbRecord, mbGroup} {
		recording := mbRecordingFixture()
		recording.ID = id
		staged, err := stageMBRecording(metadataprovider.RecordingLookup{RequestedID: id, Recording: recording})
		if err != nil {
			t.Fatal(err)
		}
		revision, err := saveMBRecordingEvidence(context.Background(), tx, staged, "2000-01-01T00:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
		if id == mbGroup {
			if _, err = tx.Exec(`INSERT INTO mb_song_links(item_id,recording_revision,recording_id,observed_at) VALUES(?,?,?,'2000-01-01T00:00:00Z')`, song, revision, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}

	s := New(db, "")
	for quantum := 0; quantum < 8; quantum++ {
		if e = s.cleanupMusicBrainz(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	var n int
	if e = db.QueryRow(`SELECT count(*) FROM mb_recording_evidence`).Scan(&n); e != nil {
		t.Fatal(e)
	}
	if n != 1 {
		t.Fatal("linked provenance removed or orphan retained", n)
	}
}

func TestMusicBrainzAlternativeReviewPreservesPublishedManualChoice(t *testing.T) {
	db, _ := mbDB(t)
	defer db.Close()
	s := New(db, "")
	s.mb = mbFixture{release: func(c context.Context, id string) (metadataprovider.ReleaseLookup, error) {
		return metadataprovider.ReleaseLookup{RequestedID: id, Release: mbReleaseFixture()}, nil
	}, record: func(c context.Context, id string) (metadataprovider.RecordingLookup, error) {
		return metadataprovider.RecordingLookup{RequestedID: id, Recording: mbRecordingFixture()}, nil
	}, searchRelease: func(context.Context, string, string) ([]metadataprovider.ReleaseCandidate, error) {
		return []metadataprovider.ReleaseCandidate{{ID: mbGroup, Title: "Another edition"}}, nil
	}}
	selectManualFixtureRelease(t, s)
	if e := s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	driveMBCatalogAdoptions(t, db)
	// Finish the durable own-album reconciliation and song publication first.
	for n := 0; n < 2; n++ {
		if e := s.MusicBrainzStep(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	state, e := s.MusicBrainzState("album", mbPublicID(t, db, "Local Album"))
	if e != nil || state.Status != "matched" {
		t.Fatal(state, e)
	}
	if e = s.SearchMusicBrainzAlternatives("album", mbPublicID(t, db, "Local Album"), state.Revision, nil); e != nil {
		t.Fatal(e)
	}
	if e = s.SearchMusicBrainzAlternatives("album", mbPublicID(t, db, "Local Album"), state.Revision, nil); !errors.Is(e, ErrMBConflict) {
		t.Fatal(e)
	}
	if e = s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	state, e = s.MusicBrainzState("album", mbPublicID(t, db, "Local Album"))
	if e != nil || !state.Manual || state.SelectedID != mbRelease || state.Status != "needs_selection" {
		t.Fatal(state, e)
	}
	foundAlternative := false
	for _, candidate := range state.Candidates {
		if candidate.ID == mbGroup {
			foundAlternative = true
		}
	}
	if !foundAlternative {
		t.Fatal("alternative candidate missing", state)
	}
	var release, status string
	if e = db.QueryRow(`SELECT l.release_id,a.provider_match_status FROM mb_album_links l JOIN catalog_albums a ON a.entity_id=l.album_id WHERE l.album_id=?`, mbIntID(t, db, "Local Album")).Scan(&release, &status); e != nil || release != mbRelease || status != "matched" {
		t.Fatal(release, status, e)
	}
}

func TestMusicBrainzPublishedEditionReportsTrackOutcomeAndClearsOldBackoff(t *testing.T) {
	db, _ := mbDB(t)
	defer db.Close()
	db.Exec(`UPDATE mb_jobs SET attempts=3,next_attempt='2099-01-01T00:00:00Z',error='unavailable' WHERE kind='song'`)
	s := New(db, "")
	release := mbReleaseFixture()
	release.ArtistCredit = mbRecordingFixture().ArtistCredit
	s.mb = mbFixture{release: func(c context.Context, id string) (metadataprovider.ReleaseLookup, error) {
		return metadataprovider.ReleaseLookup{RequestedID: id, Release: release}, nil
	}, record: func(c context.Context, id string) (metadataprovider.RecordingLookup, error) {
		return metadataprovider.RecordingLookup{RequestedID: id, Recording: mbRecordingFixture()}, nil
	}}
	selectManualFixtureRelease(t, s)
	if e := s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	state, e := s.MusicBrainzState("album", mbPublicID(t, db, "Local Album"))
	if e != nil || state.Published == nil || state.Published.Title != "Provider Edition" || state.Published.Artist != "Artist" || state.Published.PendingTracks != 1 || state.Published.TotalTracks != 1 {
		t.Fatal(state, e)
	}
	driveMBCatalogAdoptions(t, db)
	if e = s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	song, e := s.MusicBrainzState("song", mbPublicID(t, db, "Local Song"))
	if e != nil || song.NextAttempt != "" || song.Attempts != 0 || song.Error != "" {
		t.Fatal("old backoff survived new edition", song, e)
	}
	if e = s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	state, e = s.MusicBrainzState("album", mbPublicID(t, db, "Local Album"))
	if e != nil || state.Published.MatchedTracks != 1 || state.Published.PendingTracks != 0 || state.Published.ReviewTracks != 0 {
		t.Fatal(state, e)
	}
	song, e = s.MusicBrainzState("song", mbPublicID(t, db, "Local Song"))
	if e != nil || song.Published == nil || song.Published.RecordingID != mbRecord || song.Published.TrackID != mbTrack || song.Published.Title != "Provider Song" || song.Published.TrackTitle != "Local Song" {
		t.Fatal(song, e)
	}
	db.Exec(`UPDATE mb_song_links SET release_status='recording_conflict' WHERE item_id=?`, mbIntID(t, db, "Local Song"))
	state, e = s.MusicBrainzState("album", mbPublicID(t, db, "Local Album"))
	if e != nil || state.Published.ReviewTracks != 1 || state.Published.MatchedTracks != 0 {
		t.Fatal(state, e)
	}
	song, e = s.MusicBrainzState("song", mbPublicID(t, db, "Local Song"))
	if e != nil || song.Published.TrackID != "" {
		t.Fatal("conflicted track identity exposed", song, e)
	}
	var title, artist, asset string
	if e = db.QueryRow(`SELECT e.title,ar.title,a.token FROM catalog_entities e JOIN catalog_song_artists sa ON sa.song_id=e.id JOIN catalog_entities ar ON ar.id=sa.artist_id JOIN catalog_asset_links al ON al.entity_id=e.id JOIN catalog_assets a ON a.id=al.asset_id WHERE e.id=?`, mbIntID(t, db, "Local Song")).Scan(&title, &artist, &asset); e != nil || title != "Local Song" || artist != "Artist" || asset != mbAssetToken(t, db) {
		t.Fatal("local data changed", title, artist, asset, e)
	}
}

func TestManualSongTitleNeverBecomesRecordingMatchingInput(t *testing.T) {
	db, _ := mbDB(t)
	defer db.Close()
	cat := catalog.New(db)
	allow := func(*sql.Tx) error { return nil }
	songPublic := mbPublicID(t, db, "Local Song")
	songID := mbIntID(t, db, "Local Song")
	current, e := cat.ManualMetadata(context.Background(), "server", "f", songPublic, allow)
	if e != nil {
		t.Fatal(e)
	}
	label := "Misleading Recording"
	if _, e = cat.SaveManualMetadata(context.Background(), "server", "f", songPublic, "owner", catalog.ManualMetadataMutation{ExpectedRevision: current.Revision, Title: catalog.MetadataEditValue{Present: true, Value: &label}}, allow); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`DELETE FROM mb_jobs WHERE kind!='song'`); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE mb_jobs SET status='pending',next_attempt='' WHERE kind='song' AND entity_id=?`, songID); e != nil {
		t.Fatal(e)
	}
	s := New(db, "")
	calls := 0
	s.mb = mbFixture{searchRecord: func(_ context.Context, title, artist string) ([]metadataprovider.Recording, error) {
		calls++
		if title != "Local Song" || artist != "Artist" {
			t.Fatalf("display label used to match: %q %q", title, artist)
		}
		return nil, nil
	}}
	if e = s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatal("matching provider was not exercised", calls)
	}
	var title string
	if e = db.QueryRow(`SELECT title FROM catalog_entities WHERE public_id=pid_blob(?)`, songPublic).Scan(&title); e != nil || title != label {
		t.Fatal(title, e)
	}
}

// Record a real observed candidate and accept it using the current evidence digest.
func selectManualFixtureRelease(t *testing.T, s *Service) {
	t.Helper()
	provider := s.mb.(mbFixture)
	initial := provider
	initial.searchRelease = func(context.Context, string, string) ([]metadataprovider.ReleaseCandidate, error) {
		return []metadataprovider.ReleaseCandidate{{ID: mbRelease, Title: "Provider Edition"}}, nil
	}
	s.mb = initial
	if err := s.MusicBrainzStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := s.MusicBrainzState("album", mbPublicID(t, s.db, "Local Album"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SelectMusicBrainz("album", mbPublicID(t, s.db, "Local Album"), MBSelection{ExpectedRevision: state.Revision, ProviderID: mbRelease, Actor: MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}}, nil); err != nil {
		t.Fatal(err)
	}
	s.mb = provider
}
