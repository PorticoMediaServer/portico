package playback

import (
	"context"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
)

func TestVideoBreadthRelationalReadback(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "state"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	_, _, asset := catalogFixture(t, db, "lib", "/test", compactcatalog.Movie, "film", "Film", compactcatalog.Asset{Path: "/test/film.mkv", Size: 100, ModifiedNS: 1, Container: "matroska", VideoCodec: "hevc", AudioCodec: "truehd", Duration: 10})
	breadth := assets.StreamBreadth{AudioDescription: true, AudioVariant: "truehd_atmos", ChromaSubsampling: "4:2:2", FrameRateNumerator: 24000, FrameRateDenominator: 1001, DolbyVisionRPU: true, DolbyVisionBaseLayer: true, DolbyVisionMetadataKnown: true, DolbyVisionEnhancementType: "fel", DolbyVisionCMVersion: "4.0"}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	e = assets.PersistStreams(tx, asset, 100, 1, []assets.Stream{{Index: 0, Type: "video", Codec: "hevc", Detail: assets.StreamDetail{StreamBreadth: breadth, DolbyVisionProfile: 7}}, {Index: 1, Type: "audio", Codec: "truehd", Channels: 8, Detail: assets.StreamDetail{StreamBreadth: breadth, ObjectAudio: "atmos", SampleRate: 96000, BitsPerSample: 24}}})
	if e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	facts, e := loadDeliverySource(context.Background(), db, asset)
	if e != nil {
		t.Fatal(e)
	}
	if facts.Video.StreamBreadth != breadth || len(facts.Audio) != 1 || !facts.Audio[0].AudioDescription || facts.Audio[0].AudioVariant != "truehd_atmos" {
		t.Fatalf("facts lost: %+v", facts)
	}
	var raw string
	if e = db.QueryRow(`SELECT detail_json FROM asset_streams WHERE asset_id=? AND stream_index=0`, asset).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var st assets.Stream
	st.DecodeDetail(raw)
	if st.Detail.StreamBreadth != (assets.StreamBreadth{}) {
		t.Fatal("relational facts duplicated in JSON")
	}
	tx, e = db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = assets.PersistStreams(tx, asset, 100, 2, []assets.Stream{{Index: 0, Type: "video", Codec: "h264"}}); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	rows, e := assets.ReadStreamBreadth(context.Background(), db, asset)
	if e != nil || len(rows) != 1 || rows[0] != (assets.StreamBreadth{}) {
		t.Fatal("stale stream facts survived", rows, e)
	}
}

func TestObservedInterlaceNormalizedReadback(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "state"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	_, _, asset := catalogFixture(t, db, "lib", "/test", compactcatalog.Movie, "film", "Film", compactcatalog.Asset{Path: "/test/film.mkv", Size: 100, ModifiedNS: 1, Container: "matroska", VideoCodec: "h264", Duration: 10})
	for _, state := range []string{assets.InterlaceFieldOrderProgressive, assets.InterlaceFieldOrderInterlaced, assets.InterlaceDecodedInterlaced, "decoded_frames", ""} {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		// Deliberately opposite legacy JSON flag: normalized actual evidence wins
		// for safe projection; false alone cannot establish progressive.
		err = assets.PersistStreams(tx, asset, 100, 1, []assets.Stream{{Index: 0, Type: "video", Codec: "h264", Detail: assets.StreamDetail{Interlaced: true, StreamBreadth: assets.StreamBreadth{InterlaceAnalysis: state}}}})
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		facts, err := loadDeliverySource(context.Background(), db, asset)
		if err != nil {
			t.Fatal(err)
		}
		observed := facts.Video.ObservedInterlaced()
		if (observed != nil) != (state == assets.InterlaceFieldOrderProgressive || state == assets.InterlaceFieldOrderInterlaced || state == assets.InterlaceDecodedInterlaced) {
			t.Fatal(state, observed)
		}
		if observed != nil && (facts.Video.Interlaced != *observed || *observed != (state != assets.InterlaceFieldOrderProgressive)) {
			t.Fatal(state, *observed)
		}
	}
}
