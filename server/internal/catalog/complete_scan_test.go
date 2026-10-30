package catalog

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"testing"
)

func TestCompleteScanAvoidsUnconsumedPassesButCustomKeepsThem(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("film", "Films", "movie", "/films")
	// A stored Complete profile is read through its named profile semantics.
	c.Exec(`UPDATE library_scan_policies SET tier='complete',operations_json='["probe","checksum","fingerprint"]' WHERE library_id='film'`)
	p, err := New(c.DB).ScanPolicy(context.Background(), "film")
	if err != nil {
		t.Fatal(err)
	}
	if p.Allows("checksum") || p.Allows("fingerprint") || !p.Allows("trickplay") || !p.Allows("loudness") {
		t.Fatal(p)
	}
	custom, err := ValidateScanPolicy(ScanPolicy{Tier: "custom", Operations: []string{"probe", "checksum", "fingerprint"}})
	if err != nil || !custom.Allows("checksum") || !custom.Allows("fingerprint") {
		t.Fatal(custom, err)
	}
}

// Intro and credits detection was a scan stage before it became its own
// background work. A library saved back then still has it in its stored
// policy; it reads as the stages this build runs, so nothing waits on it.
func TestAStoredPolicyDropsTheRetiredSegmentStage(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("shows", "Shows", "tv", "/shows")
	c.Library("films", "Films", "movie", "/films")
	c.Exec(`UPDATE library_scan_policies SET tier='custom',operations_json='["probe","local_metadata","subtitles","trickplay","segment_detection"]' WHERE library_id='shows'`)
	c.Exec(`UPDATE library_scan_policies SET tier='complete',operations_json='["chapter_images","trickplay","waveform","loudness","segment_detection"]' WHERE library_id='films'`)
	svc := New(c.DB)
	custom, err := svc.ScanPolicy(context.Background(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	if custom.Allows("segment_detection") || !custom.Allows("trickplay") || len(custom.Operations) != 4 {
		t.Fatalf("%+v", custom)
	}
	complete, err := svc.ScanPolicy(context.Background(), "films")
	if err != nil {
		t.Fatal(err)
	}
	if complete.Allows("segment_detection") || !complete.Allows("trickplay") {
		t.Fatalf("%+v", complete)
	}
}
