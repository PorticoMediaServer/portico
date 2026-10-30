package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

func acceptInventoryInfoForTest(t *testing.T, s *Service, ctx context.Context, job, object, revision string, policy int64, facts assets.Facts, refresh bool) (compactcatalog.InfoAcceptReceipt, error) {
	t.Helper()
	return runInfoProducerForTest(t, s, ctx, func() (compactcatalog.InfoAcceptReceipt, error) {
		return s.acceptInventoryInfo(ctx, job, object, revision, policy, facts, refresh)
	})
}
func acceptDiscAlternateForTest(t *testing.T, s *Service, ctx context.Context, job, object, revision string, policy int64, facts assets.Facts) (compactcatalog.InfoAcceptReceipt, error) {
	t.Helper()
	return runInfoProducerForTest(t, s, ctx, func() (compactcatalog.InfoAcceptReceipt, error) {
		return s.AcceptDiscAlternate(ctx, job, object, revision, policy, facts)
	})
}
func runInfoProducerForTest(t *testing.T, s *Service, ctx context.Context, produce func() (compactcatalog.InfoAcceptReceipt, error)) (compactcatalog.InfoAcceptReceipt, error) {
	t.Helper()
	worker := compactcatalog.NewWorker(s.db)
	for i := 0; i < 1000; i++ {
		receipt, err := produce()
		if !errors.Is(err, compactcatalog.ErrInfoInputDeferred) && !errors.Is(err, compactcatalog.ErrInfoInputBusy) {
			return receipt, err
		}
		if _, err = worker.Step(ctx, 8); err != nil {
			return receipt, err
		}
	}
	return compactcatalog.InfoAcceptReceipt{}, compactcatalog.ErrInfoInputDeferred
}

func TestInfoInventorySourceUsesLiveFenceAndExplicitReceipt(t *testing.T) {
	s, db, _, job, object, policy, _ := discPublicationFixture(t)
	var revision, token string
	var asset int64
	if err := db.QueryRow(`SELECT o.revision,o.asset_id,a.id FROM inventory_objects o JOIN catalog_assets a ON a.token=o.asset_id WHERE o.id=?`, object).Scan(&revision, &token, &asset); err != nil {
		t.Fatal(err)
	}
	facts := assets.Facts{Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Duration: 120, Streams: []assets.Stream{{Index: 0, Type: "video", Codec: "h264"}, {Index: 1, Type: "audio", Codec: "aac"}}, ChapterStatus: "known", Chapters: []assets.Chapter{{Title: "Opening", Start: 0, End: 60}}, SubtitleInventory: &subtitles.Inventory{Status: "known", Tracks: []subtitles.Track{{Origin: "embedded", StreamIndex: 2, Format: "vtt", Language: "en"}}}}
	receipt, err := acceptInventoryInfoForTest(t, s, context.Background(), job, object, revision, policy, facts, false)
	if err != nil || receipt.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatal("not accepted-pending", receipt, err)
	}
	var installed int
	if err := db.QueryRow(`SELECT count(*) FROM asset_streams WHERE asset_id=?`, token).Scan(&installed); err != nil || installed != 0 {
		t.Fatal("pending receipt claimed immediate canonical publication", installed, err)
	}
	catalogtest.New(t, db).Drain()
	if err := db.QueryRow(`SELECT count(*) FROM asset_streams WHERE asset_id=?`, token).Scan(&installed); err != nil || installed != 2 {
		t.Fatal("accepted technical source not applied", installed, err)
	}
	var epoch int64
	if err := db.QueryRow(`SELECT info_epoch FROM catalog_assets WHERE id=?`, asset).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	unchanged, err := acceptInventoryInfoForTest(t, s, context.Background(), job, object, revision, policy, facts, false)
	if err != nil || unchanged.State != compactcatalog.InfoInputApplied || unchanged.ChangeID != 0 {
		t.Fatal("unchanged source was not an applied no-op", unchanged, err)
	}
	var after int64
	if err := db.QueryRow(`SELECT info_epoch FROM catalog_assets WHERE id=?`, asset).Scan(&after); err != nil || after != epoch {
		t.Fatal("unchanged source advanced its head", epoch, after, err)
	}
	if _, err = db.Exec(`UPDATE jobs SET status='canceled' WHERE id=?`, job); err != nil {
		t.Fatal(err)
	}
	facts.Streams[1].Codec = "flac"
	if _, err = s.acceptInventoryInfo(context.Background(), job, object, revision, policy, facts, false); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled job admitted new source facts", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM catalog_info_changes`).Scan(&installed); err != nil || installed != 0 {
		t.Fatal("refused input left an accepted body", installed, err)
	}
}

// Facts hides these technical families from its public JSON. They must still
// distinguish accepted source bodies, including a successor queued before NEW1
// has materialized and an ordinary stream-field change with equal detail JSON.
func TestInfoInventorySourceFingerprintBindsHiddenTechnicalFamilies(t *testing.T) {
	s, db, _, job, object, policy, _ := discPublicationFixture(t)
	var revision, token string
	if err := db.QueryRow(`SELECT revision,asset_id FROM inventory_objects WHERE id=?`, object).Scan(&revision, &token); err != nil {
		t.Fatal(err)
	}
	facts := assets.Facts{Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Duration: 120, Streams: []assets.Stream{{Index: 0, Type: "video", Codec: "h264"}, {Index: 1, Type: "audio", Codec: "aac"}}, ChapterStatus: "known", Chapters: []assets.Chapter{{Title: "OLD chapter", Start: 0, End: 60}}, SubtitleInventory: &subtitles.Inventory{Status: "known", Tracks: []subtitles.Track{{Origin: "embedded", StreamIndex: 2, Format: "vtt", Language: "en"}}}}
	first, err := acceptInventoryInfoForTest(t, s, context.Background(), job, object, revision, policy, facts, false)
	if err != nil || first.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatal("first", first, err)
	}
	facts.Chapters[0].Title = "NEW chapter"
	second, err := acceptInventoryInfoForTest(t, s, context.Background(), job, object, revision, policy, facts, false)
	if err != nil || second.State != compactcatalog.InfoInputAcceptedPending || second.ChangeID == first.ChangeID {
		t.Fatal("chapter-only successor collapsed", first, second, err)
	}
	c := catalogtest.New(t, db)
	c.Drain()
	var title string
	if err = db.QueryRow(`SELECT title FROM asset_chapters WHERE asset_id=? AND chapter_index=1`, token).Scan(&title); err != nil || title != "NEW chapter" {
		t.Fatal("queued chapter not applied", title, err)
	}
	facts.SubtitleInventory.Tracks[0].Language = "ja"
	third, err := acceptInventoryInfoForTest(t, s, context.Background(), job, object, revision, policy, facts, false)
	if err != nil || third.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatal("subtitle-only input", third, err)
	}
	c.Drain()
	var language string
	if err = db.QueryRow(`SELECT language FROM asset_subtitles WHERE asset_id=?`, token).Scan(&language); err != nil || language != "ja" {
		t.Fatal("subtitle-only input lost", language, err)
	}
	facts.Streams[1].Codec = "flac"
	fourth, err := acceptInventoryInfoForTest(t, s, context.Background(), job, object, revision, policy, facts, false)
	if err != nil || fourth.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatal("stream base field input", fourth, err)
	}
	c.Drain()
	var codec string
	if err = db.QueryRow(`SELECT codec FROM asset_streams WHERE asset_id=? AND stream_index=1`, token).Scan(&codec); err != nil || codec != "flac" {
		t.Fatal("base stream field lost", codec, err)
	}
}
