package playback

import (
	"context"
	"testing"
)

func TestConversionLoadUsesOnlyActiveObservedExecution(t *testing.T) {
	db, s, p, _, item, _ := activityFixture(t)
	defer db.Close()
	absent, e := s.ConversionLoad(context.Background())
	if e != nil || absent.Available {
		t.Fatal(absent, e)
	}
	h := &HLS{active: map[string]context.CancelFunc{}, windows: map[string]*hlsWindow{}}
	s.ConfigureHLS(h)
	for _, execution := range []string{"hardware", "software", "unknown", "audio", "inactive"} {
		session, e := s.Create(p, item, "original", "load-"+execution)
		if e != nil {
			t.Fatal(e)
		}
		h.windows[session.ID] = &hlsWindow{videoConversion: execution != "audio", audioConversion: execution == "audio"}
		if execution != "inactive" {
			h.active[session.ID] = func() {}
		}
		if execution == "hardware" || execution == "software" {
			mustAudioExec(t, db, `INSERT INTO playback_hls_stages VALUES(?,?,?)`, session.ID, execution, `[{"operation":"encode","execution":"`+execution+`"}]`)
		}
	}
	out, e := s.ConversionLoad(context.Background())
	if e != nil || !out.Available || out.Hardware != 1 || out.Software != 2 || out.Unknown != 1 {
		t.Fatal(out, e)
	}
}
