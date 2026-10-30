package mediaanalysis

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

func TestSonicStatusCountsCurrentCueSongsAndSeparatesRuntimeAndPause(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("music", "Music", "music", "/music")
	album := c.Album(c.Artist(lib, "Artist"), "Album", 2026)
	one := c.Song(album, 1, "/music/album.flac", "One")
	two := c.Song(album, 2, "/music/unused.flac", "Two")
	c.Exec(`UPDATE catalog_asset_links SET asset_id=? WHERE entity_id=?`, one.Asset, two.ID)
	c.Exec(`UPDATE catalog_assets SET audio_codec='flac',duration=2 WHERE id=?`, one.Asset)
	c.Exec(`INSERT INTO music_virtual_tracks(asset_id,ordinal,entity_id,start_seconds,end_seconds) VALUES(?,1,?,0,1),(?,2,?,1,2)`, one.Token, one.ID, one.Token, two.ID)
	c.Exec(`INSERT INTO library_sources(id,library_id,configured_root,root,incarnation) VALUES('sonic-source','music','/music/status','/music/status','root')`)
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) SELECT 'sonic-object','sonic-source',token,'root','album.flac','r','{}',size,modified_ns FROM catalog_assets WHERE id=?`, one.Asset)
	c.Exec(`INSERT INTO analysis_results(id,object_id,asset_id,source_revision,root_incarnation,configuration_generation,policy_revision,stage,algorithm,evidence,tool_digest,duration_us,summary_json,created_ms) VALUES('sonic-result','sonic-object',?,'r','root',1,1,'sonic',?,'fixture','fixture',2000000,'{}',1)`, one.Token, Algorithm+":sonic:fixture")
	c.Exec(`INSERT INTO analysis_heads(object_id,source_revision,stage,result_id) VALUES('sonic-object','r','sonic','sonic-result')`)
	// These synthetic vectors test persisted facts and fencing, not inference.
	var values [sonic.Dimensions]float32
	values[0] = 1
	embedding, err := sonic.Normalize(values[:])
	if err != nil {
		t.Fatal(err)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return persistSonic(ctx, tx, "sonic-result", []sonicItemAnalysis{
			{Binding: sonicTrackBinding{EntityID: one.ID, EndUS: 1000000}, Analysis: sonic.Analysis{Model: sonic.Identity{ID: sonic.ModelID, Version: sonic.ModelVersion, SHA256: sonic.ModelSHA256}, Embedding: embedding, Patches: 1}},
			{Binding: sonicTrackBinding{EntityID: two.ID, StartUS: 1000000, EndUS: 2000000}, Analysis: sonic.Analysis{Model: sonic.Identity{ID: sonic.ModelID, Version: sonic.ModelVersion, SHA256: sonic.ModelSHA256}, Embedding: embedding, Patches: 1}},
		})
	})
	s := &Service{db: c.DB}
	ctx := context.Background()
	at := time.Date(2026, 10, 24, 2, 0, 0, 0, time.UTC)
	authorize := func(*sql.Tx) error { return nil }
	read := func(total, analyzed int64) SonicStatus {
		t.Helper()
		status, err := s.SonicStatus(ctx, "music", at, authorize)
		if err != nil || status.Total != total || status.Analyzed != analyzed {
			t.Fatalf("current count: %+v %v", status, err)
		}
		return status
	}
	first := read(2, 2)
	if first.Runtime.State != "unavailable" || !first.Settings.Enabled || !first.PolicyAllows {
		t.Fatal("stored results fabricated runtime health", first)
	}
	c.Exec(`UPDATE maintenance_policy SET paused_until_ms=?,revision=revision+1 WHERE singleton=1`, at.Add(time.Hour).UnixMilli())
	paused := read(2, 2)
	if paused.Work.PausedUntil != at.Add(time.Hour).UnixMilli() || paused.Work.NextAllowedAt == nil || !paused.Work.NextAllowedAt.Equal(at.Add(time.Hour)) {
		t.Fatal("existing pause policy lost", paused)
	}
	if _, err = catalog.New(c.DB).UpdateSonicSettings(ctx, "music", false, 1, authorize); err != nil {
		t.Fatal(err)
	}
	off := read(2, 2)
	if off.Settings.Enabled || off.PolicyAllows {
		t.Fatal("disabled setting did not stop future work", off)
	}
	c.Exec(`UPDATE music_virtual_tracks SET start_seconds=1.1 WHERE ordinal=2`)
	read(2, 1)
	c.Exec(`UPDATE music_virtual_tracks SET start_seconds=1 WHERE ordinal=2`)
	c.Exec(`UPDATE music_sonic_embeddings SET model_sha256='old' WHERE entity_id=?`, one.ID)
	read(2, 1)
	c.Exec(`UPDATE music_sonic_embeddings SET model_sha256=? WHERE entity_id=?`, sonic.ModelSHA256, one.ID)
	c.Exec(`UPDATE library_sources SET generation=2 WHERE id='sonic-source'`)
	read(2, 0)
	c.Exec(`UPDATE library_sources SET generation=1 WHERE id='sonic-source'`)
	c.Exec(`UPDATE inventory_objects SET revision='replacement' WHERE id='sonic-object'`)
	read(2, 0)
	c.Exec(`UPDATE inventory_objects SET revision='r',modified_ns=modified_ns+1 WHERE id='sonic-object'`)
	read(0, 0)
	c.Exec(`UPDATE inventory_objects SET modified_ns=modified_ns-1 WHERE id='sonic-object'`)
	c.Exec(`UPDATE library_sources SET incarnation='re-rooted' WHERE id='sonic-source'`)
	read(0, 0)
	denied := errors.New("owner required")
	if _, err = s.SonicStatus(ctx, "music", at, func(*sql.Tx) error { return denied }); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	if _, err = s.SonicStatus(ctx, "music", at, nil); !errors.Is(err, ErrInput) {
		t.Fatal("missing owner gate admitted", err)
	}
}
