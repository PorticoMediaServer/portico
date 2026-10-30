package mediaanalysis

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

func TestSonicCueBindingsAndNormalizedVectorsKeepPerSongRanges(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	lib := c.Library("music", "Music", "music", "/music")
	album := c.Album(c.Artist(lib, "Artist"), "Album", 2026)
	one := c.Song(album, 1, "/music/album.flac", "One")
	two := c.Song(album, 2, "/music/other.flac", "Two")
	c.Exec(`UPDATE catalog_asset_links SET asset_id=? WHERE entity_id=?`, one.Asset, two.ID)
	c.Exec(`INSERT INTO music_virtual_tracks(asset_id,ordinal,entity_id,start_seconds,end_seconds) VALUES(?,1,?,0,1.0/75),(?,2,?,1.0/75,2)`, one.Token, one.ID, one.Token, two.ID)
	c.Drain()
	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	b := binding{AssetID: one.Token, DurationUS: 2000000}
	b.Source.LibraryID = "music"
	tracks, revision, err := sonicBindingsTx(context.Background(), tx, b)
	if err != nil || len(tracks) != 2 || tracks[0].EndUS != 13333 || tracks[1].StartUS != 13333 || tracks[1].EndUS != 2000000 {
		t.Fatal(tracks, err)
	}
	if _, err = tx.Exec(`UPDATE music_virtual_tracks SET start_seconds=2.0/75 WHERE ordinal=2`); err != nil {
		t.Fatal(err)
	}
	_, changed, err := sonicBindingsTx(context.Background(), tx, b)
	if err != nil || changed == revision {
		t.Fatal("changed cue reused prior analysis binding", err)
	}
	if _, err = tx.Exec(`INSERT INTO analysis_results(id,object_id,asset_id,source_revision,root_incarnation,configuration_generation,policy_revision,stage,algorithm,evidence,tool_digest,duration_us,summary_json,created_ms) VALUES('sonic','object',?,'r','root',1,1,'sonic','fixture','fixture','fixture',2000000,'{}',1)`, one.Token); err != nil {
		t.Fatal(err)
	}
	// Synthetic unit vectors exercise relational publication, not inference or
	// classification quality; actual model/runtime tests remain independent.
	items := []sonicItemAnalysis{}
	for index, track := range tracks {
		var values [sonic.Dimensions]float32
		values[index] = 1
		embedding, err := sonic.Normalize(values[:])
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, sonicItemAnalysis{Binding: track, Analysis: sonic.Analysis{Model: sonic.Identity{ID: sonic.ModelID, Version: sonic.ModelVersion, SHA256: sonic.ModelSHA256}, Embedding: embedding, Patches: 1}})
	}
	if err = persistSonic(context.Background(), tx, "sonic", items); err != nil {
		t.Fatal(err)
	}
	var vectors, tags int
	if err = tx.QueryRow(`SELECT count(*) FROM music_sonic_embeddings WHERE result_id='sonic'`).Scan(&vectors); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(`SELECT count(*) FROM music_sonic_tags WHERE result_id='sonic'`).Scan(&tags); err != nil || vectors != 2 || tags != 100 {
		t.Fatal(vectors, tags, err)
	}
	if _, err = tx.Exec(`DELETE FROM music_sonic_embeddings WHERE result_id='sonic' AND entity_id=?`, one.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(`SELECT count(*) FROM music_sonic_tags WHERE result_id='sonic'`).Scan(&tags); err != nil || tags != 50 {
		t.Fatal("cue tag FK crossed song identity", tags, err)
	}
}

func TestSonicMusepackClockRequiresFreshMeasuredRate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	lib := c.Library("music", "Music", "music", "/music")
	item := c.Song(c.Album(c.Artist(lib, "Artist"), "Album", 2026), 1, "/music/song.mpc", "Song")
	c.Exec(`UPDATE catalog_assets SET audio_codec='mpc8' WHERE id=?`, item.Asset)
	c.Exec(`INSERT INTO music_technical_facts(asset_id,size,modified_ns,codec,sample_rate,bit_depth,channels) SELECT token,size,modified_ns,'mpc8',44100,0,2 FROM catalog_assets WHERE id=?`, item.Asset)
	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	seconds := 22018.0 / 44100
	if _, err = exactUS(seconds); err == nil {
		t.Fatal("fixture is not a fractional sample clock")
	}
	actual, err := musepackDurationUS(context.Background(), tx, item.Token, "mpc8", seconds)
	if err != nil || actual != int64(math.Round(seconds*1e6)) {
		t.Fatal(actual, err)
	}
	if _, err = musepackDurationUS(context.Background(), tx, item.Token, "mpc8", seconds+.000001); !errors.Is(err, ErrInput) {
		t.Fatal("noninteger source-frame count accepted", err)
	}
	if _, err = tx.Exec(`UPDATE catalog_assets SET modified_ns=modified_ns+1 WHERE id=?`, item.Asset); err != nil {
		t.Fatal(err)
	}
	if _, err = musepackDurationUS(context.Background(), tx, item.Token, "mpc8", seconds); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("stale measured rate accepted", err)
	}
}
