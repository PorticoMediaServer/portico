package metadata

// Worker assertions address entities through their public ids at service
// boundaries and integer ids in MusicBrainz evidence and job tables.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

const (
	mbLaneRecord   = "11111111-1111-1111-1111-111111111111"
	mbLaneRelease  = "22222222-2222-2222-2222-222222222222"
	mbLaneGroup    = "33333333-3333-3333-3333-333333333333"
	mbLaneTrack    = "44444444-4444-4444-4444-444444444444"
	mbLaneArtistID = "55555555-5555-5555-5555-555555555555"
)

type mbLaneFixture struct {
	searchRecord  func(context.Context, string, string) ([]metadataprovider.Recording, error)
	searchRelease func(context.Context, string, string) ([]metadataprovider.ReleaseCandidate, error)
	record        func(context.Context, string) (metadataprovider.RecordingLookup, error)
	release       func(context.Context, string) (metadataprovider.ReleaseLookup, error)
}

func (f mbLaneFixture) SearchRecordings(ctx context.Context, title, artist string) ([]metadataprovider.Recording, error) {
	return f.searchRecord(ctx, title, artist)
}
func (f mbLaneFixture) SearchReleases(ctx context.Context, title, artist string) ([]metadataprovider.ReleaseCandidate, error) {
	return f.searchRelease(ctx, title, artist)
}
func (f mbLaneFixture) Recording(ctx context.Context, id string) (metadataprovider.RecordingLookup, error) {
	return f.record(ctx, id)
}
func (f mbLaneFixture) Release(ctx context.Context, id string) (metadataprovider.ReleaseLookup, error) {
	return f.release(ctx, id)
}

func mbLaneDB(t *testing.T) (*sql.DB, catalogtest.Names) {
	t.Helper()
	c := catalogtest.Open(t)
	library := c.Library("lib", "Music", "music", "/music")
	artist := c.Artist(library, "Artist")
	album := c.Album(artist, "Local Album", 2020)
	song := c.Song(album, 3, "/music/song.flac", "Local Song")
	c.Fields(song.ID, map[string]any{"album_id": album.ID, "disc_number": 2, "track_number": 3})
	c.Drain()
	var storedArtist int64
	if err := c.DB.QueryRow(`SELECT artist_id FROM catalog_albums WHERE entity_id=?`, album.ID).Scan(&storedArtist); err != nil || storedArtist != artist.ID {
		t.Fatalf("compact album fixture is incomplete: artist=%d want=%d err=%v", storedArtist, artist.ID, err)
	}
	return c.DB, catalogtest.Names{"artist": artist, "album": album, "song": song}
}

func mbLaneRecordingFixture() metadataprovider.Recording {
	return metadataprovider.Recording{ID: mbLaneRecord, Title: "Provider Song", ArtistCredit: []metadataprovider.ArtistCredit{{Name: "Artist", Artist: metadataprovider.Artist{ID: mbLaneArtistID, Name: "Artist"}}}}
}

func mbLaneReleaseFixture() metadataprovider.Release {
	return metadataprovider.Release{ID: mbLaneRelease, Title: "Provider Edition", Date: "2020-01-01", Country: "CA", ReleaseGroup: metadataprovider.ReleaseGroup{ID: mbLaneGroup, Title: "Album Group"}, Media: []metadataprovider.Medium{{Position: 2, Format: "CD", Tracks: []metadataprovider.ReleaseTrack{{ID: mbLaneTrack, Position: 3, Number: "3", Title: "Local Song", ArtistCredit: mbLaneRecordingFixture().ArtistCredit, Recording: mbLaneRecordingFixture()}}}}}
}

func mbCurrentExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, e := db.Exec(q, args...); e != nil {
		t.Fatal(e)
	}
}
func TestMBCurrentWorkerTypedPublicationAndCatalogPin(t *testing.T) {
	db, names := mbLaneDB(t)
	defer db.Close()
	mbCurrentExec(t, db, `INSERT INTO audio_tag_evidence VALUES('lib',?,'musicbrainz_trackid','embedded',?),('lib',?,'musicbrainz_albumid','embedded',?),('lib',?,'musicbrainz_releasetrackid','embedded',?)`, names["song"].Token, mbLaneRecord, names["song"].Token, mbLaneRelease, names["song"].Token, mbLaneTrack)
	s := New(db, "")
	calls := 0
	s.mb = mbLaneFixture{release: func(c context.Context, id string) (metadataprovider.ReleaseLookup, error) {
		calls++
		return metadataprovider.ReleaseLookup{RequestedID: id, Release: mbLaneReleaseFixture()}, nil
	}, record: func(c context.Context, id string) (metadataprovider.RecordingLookup, error) {
		calls++
		return metadataprovider.RecordingLookup{RequestedID: id, Recording: mbLaneRecordingFixture()}, nil
	}}
	// Album publication, durable own-album continuation, then song publication.
	// The album publication hands identity joining to the catalogue-domain
	// worker, and a song under that still-frozen album cannot be claimed; drive
	// the real jobs between provider steps instead of accepting a pending track.
	for n := 0; n < 3; n++ {
		if e := s.MusicBrainzStep(context.Background()); e != nil {
			t.Fatal(n, e)
		}
		driveMBCatalogAdoptions(t, db)
	}
	state, e := s.MusicBrainzState("album", names["album"].Public)
	if e != nil || state.Status != "matched" || state.Published == nil || state.Published.MatchedTracks != 1 || state.Published.ReconciliationPending {
		var songStatus, songError, operations string
		_ = db.QueryRow(`SELECT status,error FROM mb_jobs WHERE kind='song' AND entity_id=?`, names["song"].ID).Scan(&songStatus, &songError)
		_ = db.QueryRow(`SELECT group_concat(kind||':'||status||':'||reason,';') FROM mb_publication_operations`).Scan(&operations)
		t.Fatalf("album publication state=%+v published=%+v err=%v providerCalls=%d songJob=%s/%s operations=%s", state, state.Published, e, calls, songStatus, songError, operations)
	}
	settleMetadataCatalogue(t, db)
	item, e := catalog.New(db).Get("p", names["song"].Public)
	if e != nil || item.Song == nil || item.Song.ProviderRecordingID != mbLaneRecord || item.Song.ProviderTrackID != mbLaneTrack || item.Song.ProviderReleaseStatus != "matched" {
		t.Fatal(item, e)
	}
	oldTitle, oldArtist := item.Song.ProviderTitle, item.Song.ProviderArtist
	// Store a second complete immutable snapshot for the same provider IDs.
	// This tests the actual catalog consumer independently of worker selection.
	changed := mbLaneReleaseFixture()
	changed.Media[0].Tracks[0].Title = "Other album track"
	changed.Media[0].Tracks[0].Recording.Title = "Other album recording"
	changed.Media[0].Tracks[0].ArtistCredit[0].Name = "Other artist"
	stage, e := stageMBRelease(metadataprovider.ReleaseLookup{RequestedID: mbLaneRelease, Release: changed})
	if e != nil {
		t.Fatal(e)
	}
	other := catalogtest.New(t, db).Album(names["artist"], "Other", 0)
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	revision, e := saveMBReleaseEvidence(context.Background(), tx, stage, "2099-01-01T00:00:00Z")
	if e != nil {
		t.Fatal(e)
	}
	if e = checkMBEvidenceBudget(context.Background(), tx); e != nil {
		t.Fatal(e)
	}
	if e = mbFixturePinTx(tx, mbFixturePinIntent{Link: "album", Entity: other.ID, NewRelease: revision}, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO mb_album_links(album_id,release_revision,release_id,requested_id,observed_at) VALUES(?,?,?,?,'2099-01-01T00:00:00Z')`, other.ID, revision, mbLaneRelease, mbLaneRelease)
		return err
	}); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	settleMetadataCatalogue(t, db)
	item, e = catalog.New(db).Get("p", names["song"].Public)
	if e != nil || item.Song.ProviderTitle != oldTitle || item.Song.ProviderArtist != oldArtist || *item.Song.TrackNumber != 3 || *item.Song.DiscNumber != 2 {
		t.Fatal("shared provider revision changed first album song", item, e)
	}
	if calls < 1 || calls > 2 {
		t.Fatal("unexpected acquisition count", calls)
	}
}
func TestMBCurrentWorkerStaleProviderCannotPublish(t *testing.T) {
	db, names := mbLaneDB(t)
	defer db.Close()
	mbCurrentExec(t, db, `UPDATE mb_jobs SET status='needs_selection' WHERE kind='album';INSERT INTO audio_tag_evidence VALUES('lib',?,'musicbrainz_trackid','embedded',?)`, names["song"].Token, mbLaneRecord)
	// Tag scheduling also marks the album pending; keep this fixture song-only.
	mbCurrentExec(t, db, `UPDATE mb_jobs SET status='needs_selection' WHERE kind='album'`)
	s := New(db, "")
	s.mb = mbLaneFixture{record: func(c context.Context, id string) (metadataprovider.RecordingLookup, error) {
		catalogtest.New(t, db).Fields(names["song"].ID, map[string]any{"title": "Changed while provider was in flight"})
		return metadataprovider.RecordingLookup{RequestedID: id, Recording: mbLaneRecordingFixture()}, nil
	}}
	if e := s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	var links int
	if e := db.QueryRow(`SELECT count(*) FROM mb_song_links`).Scan(&links); e != nil || links != 0 {
		t.Fatal("stale result published", links, e)
	}
	state, e := s.MusicBrainzState("song", names["song"].Public)
	if e != nil || state.Status != "pending" {
		t.Fatal(state, e)
	}
	var stale int
	if e := db.QueryRow(`SELECT count(*) FROM mb_publication_operations WHERE status='stale' AND result_recording_revision IS NULL`).Scan(&stale); e != nil || stale != 1 {
		t.Fatal("missing compact stale receipt", stale, e)
	}
}

func TestMBCurrentWorkerExpiryDuringStageRollsBack(t *testing.T) {
	db, _ := mbLaneDB(t)
	defer db.Close()
	mbCurrentExec(t, db, `UPDATE mb_jobs SET status='needs_selection' WHERE kind='album'`)
	s := New(db, "")
	at := s.publicationTime()
	s.now = func() time.Time { return at }
	j, e := s.claimMB(context.Background())
	if e != nil || j == nil {
		t.Fatal(j, e)
	}
	calls := 0
	s.now = func() time.Time {
		calls++
		if calls > 1 {
			return at.Add(2 * time.Minute)
		}
		return at
	}
	e = s.mbPublishSong(context.Background(), *j, metadataprovider.RecordingLookup{RequestedID: mbLaneRecord, Recording: mbLaneRecordingFixture()}, at.Format(time.RFC3339))
	if !errors.Is(e, errTVDBStale) {
		t.Fatal("expired staging was accepted", e)
	}
	var n int
	if e = db.QueryRow(`SELECT count(*) FROM mb_recording_evidence`).Scan(&n); e != nil || n != 0 {
		t.Fatal("expired prospective evidence retained", n, e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM mb_song_links`).Scan(&n); e != nil || n != 0 {
		t.Fatal("expired staging published", n, e)
	}
}

func TestMBCurrentWorkerManualSelectionSurvivesInputRefresh(t *testing.T) {
	db, names := mbLaneDB(t)
	defer db.Close()
	s := New(db, "")
	searchCalls := 0
	s.mb = mbLaneFixture{searchRelease: func(context.Context, string, string) ([]metadataprovider.ReleaseCandidate, error) {
		searchCalls++
		return []metadataprovider.ReleaseCandidate{{ID: mbLaneRelease, Title: "Local Album", Date: "2020"}}, nil
	}, release: func(c context.Context, id string) (metadataprovider.ReleaseLookup, error) {
		return metadataprovider.ReleaseLookup{RequestedID: id, Release: mbLaneReleaseFixture()}, nil
	}}
	if e := s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	state, e := s.MusicBrainzState("album", names["album"].Public)
	if e != nil || len(state.Candidates) != 1 {
		t.Fatal(state, e, "search callback calls", searchCalls)
	}
	if e = s.SelectMusicBrainz("album", names["album"].Public, MBSelection{ExpectedRevision: state.Revision, ProviderID: mbLaneRelease, Actor: MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}}, nil); e != nil {
		t.Fatal(e)
	}
	var originalQuery string
	var originalGeneration int64
	if e = db.QueryRow(`SELECT query_digest,job_generation FROM mb_selection_receipts WHERE status='pending'`).Scan(&originalQuery, &originalGeneration); e != nil {
		t.Fatal(e)
	}
	catalogtest.New(t, db).Fields(names["album"].ID, map[string]any{"title": "Changed local album title"})
	if e = s.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	state, e = s.MusicBrainzState("album", names["album"].Public)
	if e != nil || state.Status != "matched" || !state.Manual || state.SelectedID != mbLaneRelease {
		t.Fatal(state, e)
	}
	var query, authority, account, profile, status string
	var generation, current int64
	if e = db.QueryRow(`SELECT query_digest,actor_authority,actor_account_id,actor_profile_id,status,job_generation FROM mb_selection_receipts`).Scan(&query, &authority, &account, &profile, &status, &generation); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(`SELECT generation FROM mb_jobs WHERE kind='album' AND entity_id=?`, names["album"].ID).Scan(&current); e != nil {
		t.Fatal(e)
	}
	if query != originalQuery || generation != originalGeneration || generation >= current || authority != "local" || account != "owner" || profile != "profile" || status != "applied" {
		t.Fatal("owner intention receipt was replaced or lost", query, generation, current, status)
	}
}
func TestMBCurrentWorkerDisplacedEvidenceBudgetRollsBackPin(t *testing.T) {
	db, names := mbLaneDB(t)
	defer db.Close()
	other := catalogtest.New(t, db).Album(names["artist"], "Other", 0)
	mbCurrentExec(t, db, `UPDATE mb_jobs SET status='needs_selection' WHERE kind='album' AND entity_id=?`, other.ID)
	ctx := context.Background()
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	save := func(title string) string {
		t.Helper()
		v := mbLaneReleaseFixture()
		v.Title = title
		stage, e := stageMBRelease(metadataprovider.ReleaseLookup{RequestedID: mbLaneRelease, Release: v})
		if e != nil {
			t.Fatal(e)
		}
		id, e := saveMBReleaseEvidence(ctx, tx, stage, "2099-01-01T00:00:00Z")
		if e != nil {
			t.Fatal(e)
		}
		return id
	}
	old := save("Accepted old")
	next := save("Already accepted elsewhere")
	for _, pin := range []struct {
		album   int64
		release string
	}{{names["album"].ID, old}, {other.ID, next}} {
		pin := pin
		if e = mbFixturePinTx(tx, mbFixturePinIntent{Link: "album", Entity: pin.album, NewRelease: pin.release}, func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO mb_album_links VALUES(?,?,?,?,'2099-01-01T00:00:00Z')`, pin.album, pin.release, mbLaneRelease, mbLaneRelease)
			return err
		}); e != nil {
			t.Fatal(e)
		}
	}
	// Every orphan is real validated, normalized and sealed evidence. The new
	// target already belongs to another album, so accepting it does not release
	// any cache charge while displacing our old accepted revision adds one.
	for n := 0; n < 512; n++ {
		save(fmt.Sprintf("Retained orphan %03d", n))
	}
	if e = checkMBEvidenceBudget(ctx, tx); e != nil {
		t.Fatal("initial exact capacity rejected", e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	s := New(db, "")
	j, e := s.claimMB(ctx)
	if e != nil || j == nil || j.id != names["album"].Public {
		t.Fatal(j, e)
	}
	value := mbLaneReleaseFixture()
	value.Title = "Already accepted elsewhere"
	e = s.mbPublishAlbum(ctx, *j, metadataprovider.ReleaseLookup{RequestedID: mbLaneRelease, Release: value}, "2099-01-01T00:00:00Z")
	if !errors.Is(e, errMBBudget) {
		t.Fatal("displaced old revision bypassed admission", e)
	}
	var pin string
	if e = db.QueryRow(`SELECT release_revision FROM mb_album_links WHERE album_id=?`, names["album"].ID).Scan(&pin); e != nil || pin != old {
		t.Fatal("budget refusal replaced accepted pin", pin, e)
	}
	var continuations int
	if e = db.QueryRow(`SELECT count(*) FROM mb_album_reconciliations WHERE album_id=?`, names["album"].ID).Scan(&continuations); e != nil || continuations != 0 {
		t.Fatal("rolled back publication left continuation", continuations, e)
	}
}
