package downloads

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"testing"
)

func TestDownloadOwnerCeilingUsesActualSourceFacts(t *testing.T) {
	cfg := playback.DefaultDeliveryConfiguration()
	cfg.DownloadMaxQuality = "720p-low"
	s := &Service{deliveryConfiguration: func() playback.DeliveryConfiguration { return cfg }}
	for _, f := range []struct {
		source source
		want   bool
	}{
		{source{Kind: "movie", Height: 1080, VideoBitrateBPS: 1000000}, false},
		{source{Kind: "movie", Height: 720, VideoBitrateBPS: 0}, false},
		{source{Kind: "movie", Height: 0, VideoBitrateBPS: 1000000}, false},
		{source{Kind: "movie", Height: 720, VideoBitrateBPS: 3000000}, false},
		{source{Kind: "movie", Height: 720, VideoBitrateBPS: 1000000}, true},
		{source{Kind: "song"}, true},
	} {
		if got := s.withinCeiling(QualityOriginal, f.source); got != f.want {
			t.Fatalf("%+v got %v", f, got)
		}
	}
	for _, q := range []string{"original", "automatic", "2160p", "1080p-high"} {
		got := s.resolveQuality(context.Background(), nil, playback.CapabilityDocument{}, q, source{Kind: "movie", Height: 1080, VideoBitrateBPS: 8000000})
		if got != "720p-low" {
			t.Fatalf("%s -> %s", q, got)
		}
	}
}

func TestDownloadAdmissionClampsStoredVideoQuality(t *testing.T) {
	h := newHarness(t)
	cfg := playback.DefaultDeliveryConfiguration()
	cfg.ConversionMode = "all"
	cfg.DownloadMaxQuality = "720p-low"
	h.service.deliveryConfiguration = func() playback.DeliveryConfiguration { return cfg }
	batch, err := h.service.Submit(context.Background(), h.viewer, Request{OperationID: "owner-cap-original", MediaID: h.item, Quality: QualityOriginal}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Items) != 1 {
		t.Fatalf("admission %+v", batch)
	}
	var quality string
	if err = h.db.QueryRow(`SELECT quality FROM download_preparations WHERE id=?`, batch.Items[0].ID).Scan(&quality); err != nil || quality != "720p-low" {
		t.Fatalf("stored=%s err=%v", quality, err)
	}
	view, err := h.service.Options(context.Background(), h.viewer, h.item, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range view.Options {
		if o.Quality == QualityOriginal && o.Available {
			t.Fatal("uncapped original offered")
		}
		if o.Quality == "1080p-high" && (o.Available || o.Reason != ReasonDownloadCeiling) {
			t.Fatal(o)
		}
	}
}
