package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

func inventoryStreamsFixture(t *testing.T) (*Service, *sql.DB, InventoryStreamInput) {
	t.Helper()
	s, db, _, job, object, policy, _ := discPublicationFixture(t)
	var revision string
	if err := db.QueryRow(`SELECT revision FROM inventory_objects WHERE id=?`, object).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	facts := assets.Facts{Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 120, Streams: []assets.Stream{{Index: 0, Type: "video", Codec: "h264", Detail: assets.StreamDetail{StreamBreadth: assets.StreamBreadth{ChromaSubsampling: "420"}}}, {Index: 1, Type: "audio", Codec: "aac", Language: "en"}}, ChapterStatus: "known", Chapters: []assets.Chapter{{Title: "Opening", Start: 0, End: 60}}, SubtitleInventory: &subtitles.Inventory{Status: "known", Tracks: []subtitles.Track{{Origin: "embedded", StreamIndex: 2, Format: "vtt", Language: "en"}}}}
	if _, err := acceptInventoryInfoForTest(t, s, context.Background(), job, object, revision, policy, facts, false); err != nil {
		t.Fatal(err)
	}
	catalogtest.New(t, db).Drain()
	if _, err := db.Exec(`UPDATE inventory_objects SET analysis_revision=revision WHERE id=?`, object); err != nil {
		t.Fatal(err)
	}
	input, err := s.InventoryStreams(context.Background(), job, object, revision, policy)
	if err != nil {
		t.Fatal(err)
	}
	input.Streams[0].Detail.InterlaceAnalysis = assets.InterlaceDecodedInterlaced
	input.Streams[0].Detail.FrameRateMode = "constant"
	return s, db, input
}

func TestInventoryStreamsPublishesOnlyStreamsAndKeepsPendingReceipt(t *testing.T) {
	s, db, input := inventoryStreamsFixture(t)
	ctx := context.Background()
	var before string
	unchanged := `SELECT json_array(container,video_codec,audio_codec,width,height,duration,(SELECT group_concat(title) FROM asset_chapters WHERE asset_id=a.token),(SELECT analysis_revision FROM inventory_objects WHERE id=?),(SELECT revision FROM asset_chapter_facts WHERE asset_id=a.token),(SELECT revision FROM asset_subtitle_facts WHERE asset_id=a.token),(SELECT group_concat(language) FROM asset_subtitles WHERE asset_id=a.token)) FROM catalog_assets a WHERE id=?`
	if err := db.QueryRow(unchanged, input.object, input.frame.asset).Scan(&before); err != nil {
		t.Fatal(err)
	}
	// A separate scalar-only accepted input occupies slot 0. Streams must queue
	// and survive ordinary promotion without borrowing or changing its mask.
	if err := compactcatalogTestWrite(db, func(tx *sql.Tx) error {
		var epoch int64
		if err := tx.QueryRow(`SELECT info_epoch FROM catalog_assets WHERE id=?`, input.frame.asset).Scan(&epoch); err != nil {
			return err
		}
		in, err := compactcatalog.BeginInfoInputTx(ctx, tx, compactcatalog.InfoInputSpec{AssetID: input.frame.asset, Token: input.frame.token, ExpectedEpoch: epoch, Fingerprint: [32]byte{9}, Witnesses: []compactcatalog.InfoWitness{{Kind: compactcatalog.InfoWitnessAsset, Fingerprint: [32]byte{9}}}})
		if err != nil {
			return err
		}
		_, err = compactcatalog.AcceptInfoInputTx(ctx, tx, in.Ref)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.AcceptInventoryStreams(ctx, input)
	if err != nil || receipt.State != compactcatalog.InfoInputAcceptedPending || receipt.Slot != 1 {
		t.Fatal(receipt, err)
	}
	var mask int64
	if err = db.QueryRow(`SELECT family_mask FROM catalog_info_changes WHERE id=?`, receipt.ChangeID).Scan(&mask); err != nil || mask != 7 {
		t.Fatal("partial mask", mask, err)
	}
	again, err := s.AcceptInventoryStreams(ctx, input)
	if err != nil || again.ChangeID != receipt.ChangeID || again.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatal("pending input was replaced", again, err)
	}
	var mode string
	if err = db.QueryRow(`SELECT frame_rate_mode FROM asset_stream_breadth WHERE asset_id=? AND stream_index=0`, input.frame.token).Scan(&mode); err != nil || mode != "" {
		t.Fatal("pending input published early", mode, err)
	}
	catalogtest.New(t, db).Drain()
	var after string
	if err = db.QueryRow(unchanged, input.object, input.frame.asset).Scan(&after); err != nil || after != before {
		t.Fatal("unrelated metadata changed", before, after, err)
	}
	current, err := s.InventoryStreams(ctx, input.job, input.object, input.revision, input.policy)
	if err != nil || current.headRevision != input.headRevision+1 || current.Streams[0].Detail.InterlaceAnalysis != assets.InterlaceDecodedInterlaced || current.Streams[0].Detail.ChromaSubsampling != "420" || current.Streams[1].Language != "en" {
		t.Fatal("stream set or head lost", current, err)
	}
	noop, err := s.AcceptInventoryStreams(ctx, current)
	if err != nil || noop.State != compactcatalog.InfoInputApplied || noop.ChangeID != 0 {
		t.Fatal("canonical fingerprint disagrees", noop, err)
	}
}

func compactcatalogTestWrite(db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func TestInventoryStreamsRejectsChangedAdmissionBeforeSeal(t *testing.T) {
	for _, point := range []struct{ name, trigger string }{
		{"staging", "AFTER INSERT ON catalog_info_new_streams"},
		{"acceptance", "AFTER UPDATE OF sealed ON catalog_info_input_families WHEN NEW.family=3 AND NEW.sealed=1"},
	} {
		t.Run(point.name, func(t *testing.T) {
			for _, test := range []struct {
				name, mutation string
				want           error
			}{
				{"job", `UPDATE jobs SET status='canceled'`, context.Canceled},
				{"source", `UPDATE library_sources SET enabled=0`, context.Canceled},
				{"policy", `UPDATE library_scan_policies SET revision=revision+1`, context.Canceled},
				{"object", `UPDATE inventory_objects SET revision='replacement'`, sql.ErrNoRows},
				{"streams", `UPDATE asset_stream_facts SET revision=revision+1`, compactcatalog.ErrInfoInputDrift},
			} {
				t.Run(test.name, func(t *testing.T) {
					s, db, input := inventoryStreamsFixture(t)
					// Invalidate after a committed row batch, leaving subsequent family
					// seals and the final accept to repeat admission. No timing race.
					if _, err := db.Exec(`CREATE TRIGGER stream_input_test_drift ` + point.trigger + ` BEGIN ` + test.mutation + `; END`); err != nil {
						t.Fatal(err)
					}
					_, err := s.AcceptInventoryStreams(context.Background(), input)
					if !errors.Is(err, test.want) {
						t.Fatalf("expected %v, got %v", test.want, err)
					}
					var accepted int
					if err = db.QueryRow(`SELECT count(*) FROM catalog_info_changes WHERE phase BETWEEN 1 AND 3`).Scan(&accepted); err != nil || accepted != 0 {
						t.Fatal("invalidated input accepted", accepted, err)
					}
					catalogtest.New(t, db).Drain()
					var mode string
					if err = db.QueryRow(`SELECT frame_rate_mode FROM asset_stream_breadth WHERE asset_id=? AND stream_index=0`, input.frame.token).Scan(&mode); err != nil || mode != "" {
						t.Fatal("abandoned input applied", mode, err)
					}
				})
			}
		})
	}
}

func TestInventoryStreamsRefusesCompetingPendingStreams(t *testing.T) {
	s, _, input := inventoryStreamsFixture(t)
	first, err := s.AcceptInventoryStreams(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.Streams[0].Detail.Telecine = true
	if _, err = s.AcceptInventoryStreams(context.Background(), input); !errors.Is(err, compactcatalog.ErrInfoInputBusy) {
		t.Fatal("stale canonical observation queued behind replacement", first, err)
	}
}

func TestInventoryStreamsRetryCannotResetAbandonedReclaimCursor(t *testing.T) {
	s, db, input := inventoryStreamsFixture(t)
	if _, err := db.Exec(`CREATE TRIGGER stream_input_test_cancel AFTER INSERT ON catalog_info_new_streams BEGIN UPDATE jobs SET status='canceled'; END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptInventoryStreams(context.Background(), input); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TRIGGER stream_input_test_cancel; UPDATE jobs SET status='running'`); err != nil {
		t.Fatal(err)
	}
	var change, revision, cursor int64
	if err := db.QueryRow(`SELECT id,input_revision,copy_family FROM catalog_info_changes WHERE asset_id=? AND copy_aux=-1`, input.frame.asset).Scan(&change, &revision, &cursor); err != nil {
		t.Fatal("canceled body not marked for reclaim", err)
	}
	if _, err := s.AcceptInventoryStreams(context.Background(), input); !errors.Is(err, compactcatalog.ErrInfoInputBusy) {
		t.Fatal("late producer reclaimed canceled input", err)
	}
	var unchanged bool
	if err := db.QueryRow(`SELECT copy_aux=-1 AND input_revision=? AND copy_family=? FROM catalog_info_changes WHERE id=?`, revision, cursor, change).Scan(&unchanged); err != nil || !unchanged {
		t.Fatal("retry reset recovery ownership", unchanged, err)
	}
	catalogtest.New(t, db).Drain()
	receipt, err := s.AcceptInventoryStreams(context.Background(), input)
	if err != nil || receipt.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatal("fresh body after reclaim", receipt, err)
	}
	catalogtest.New(t, db).Drain()
	var mode string
	if err = db.QueryRow(`SELECT frame_rate_mode FROM asset_stream_breadth WHERE asset_id=? AND stream_index=0`, input.frame.token).Scan(&mode); err != nil || mode != "constant" {
		t.Fatal("recovered publication lost", mode, err)
	}
}
