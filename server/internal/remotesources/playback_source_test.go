package remotesources

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

func subtitlePlaybackSourceFixture(t *testing.T) (observationFixture, subtitles.PlaybackSourceFacts) {
	t.Helper()
	f, _, request, _ := episodeReadFixture(t)
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration)
 SELECT 'subtitle-play','login','account','profile',id,'asset',1,'playing','hash','grant','2099-01-01T00:00:00Z','request',60 FROM catalog_entities WHERE public_id=pid_blob(?)`, []any{request.Target.EpisodeID}},
		{`INSERT INTO playback_physical_source_pins SELECT 'subtitle-play',id,incarnation,generation FROM library_sources`, nil},
	} {
		if _, err := f.s.db.Exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return f, subtitles.PlaybackSourceFacts{ItemID: request.Target.EpisodeID, AssetID: "asset", SessionID: "subtitle-play", SessionGeneration: 1, SourceSize: 16, SourceModifiedNS: 23}
}

// The factual resolver is deliberately exercised without a sealed invocation:
// these facts cannot authorize a decoder or a provider process on their own.
func TestSubtitlePlaybackSourceFactsBindPhysicalPinAndObservation(t *testing.T) {
	f, facts := subtitlePlaybackSourceFixture(t)
	view, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.s.readPlaybackSourceTx(context.Background(), view.Tx(), facts)
	view.Rollback()
	if err != nil || got.file != f.file || got.source.ID != f.library || got.observation.SourceID != "remote" || got.scan.ObjectIdentity == "" {
		t.Fatal("physical selected object was not resolved", got, err)
	}
	if len(f.s.serveLeases) != 0 || len(f.s.readLeases) != 0 {
		t.Fatal("factual resolver started source I/O")
	}
	if input, err := f.s.OpenSubtitlePlaybackInput(context.Background(), facts.ItemID, facts.AssetID, f.file, 16, 23); input != nil || err == nil {
		t.Fatal("facts or playback purpose replaced the sealed Select invocation", input, err)
	}
	if len(f.s.serveLeases) != 0 || len(f.s.readLeases) != 0 {
		t.Fatal("missing selected invocation started source I/O")
	}
}

func TestSubtitlePlaybackSourceFactsRefuseChangedPhysicalSelection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, test := range []struct{ name, sql string }{
		{"missing-pin", `DELETE FROM playback_physical_source_pins`},
		{"other-pin-source", `UPDATE playback_physical_source_pins SET source_id='another'`},
		{"pin-generation", `UPDATE playback_physical_source_pins SET configuration_generation=configuration_generation+1`},
		{"pin-incarnation", `UPDATE playback_physical_source_pins SET incarnation='replacement'`},
		{"source-generation", `UPDATE library_sources SET generation=generation+1`},
		{"source-root", `UPDATE library_sources SET root_identity='replacement'`},
		{"unconfirmed-root", `UPDATE library_sources SET identity_confirmed=0`},
		{"disabled", `UPDATE library_sources SET enabled=0`},
		{"unlinked", `UPDATE catalog_asset_links SET available=0`},
		{"asset-unavailable", `UPDATE catalog_assets SET available=0`},
		{"inventory-retired", `UPDATE inventory_objects SET retired=1`},
		{"inventory-revision", `UPDATE inventory_objects SET revision='replacement'`},
		{"inventory-path", `UPDATE inventory_objects SET relative_path='another.mkv'`},
		{"observation-generation", `UPDATE remote_sources SET generation=generation+1`},
		{"observation-removed", `DELETE FROM remote_scan_observations`},
		{"unknown-range", `UPDATE remote_sources SET range_support='unknown'`},
		{"sequential-range", `UPDATE remote_sources SET range_support='unsupported'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, facts := subtitlePlaybackSourceFixture(t)
			if _, err := f.s.db.Exec(test.sql); err != nil {
				t.Fatal(err)
			}
			view, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.s.readPlaybackSourceTx(context.Background(), view.Tx(), facts)
			view.Rollback()
			if err == nil {
				t.Fatal("changed selected source was accepted")
			}
			if len(f.s.serveLeases) != 0 || len(f.s.readLeases) != 0 {
				t.Fatal("refusal started source I/O")
			}
		})
	}
	for _, field := range []string{"item", "asset", "session"} {
		t.Run("other-"+field, func(t *testing.T) {
			f, facts := subtitlePlaybackSourceFixture(t)
			switch field {
			case "item":
				facts.ItemID = "foreign"
			case "asset":
				facts.AssetID = "foreign"
			case "session":
				facts.SessionID = "foreign"
			}
			view, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.s.readPlaybackSourceTx(context.Background(), view.Tx(), facts)
			view.Rollback()
			if err == nil || len(f.s.serveLeases) != 0 || len(f.s.readLeases) != 0 {
				t.Fatal("foreign selected target reached source I/O", err)
			}
		})
	}
}
