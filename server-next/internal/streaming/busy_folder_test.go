package streaming

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// A second audio track asked for while the stream's folder is busy for a moment (another
// window is making it, or it is being moved) is not a failure of that track: once the folder
// is free the track plays. It used to be taken for a full disk, which marked the track
// failed for the rest of the stream, and every request holding it was told the playback had
// ended.
func TestABusyFolderDoesNotFailAnAudioTrack(t *testing.T) {
	rig := newMediaRig(t)
	film := rig.generate("two-tracks.mkv", 12, "-map", "0:v", "-map", "1:a", "-map", "1:a", "-c:v", "libx264", "-c:a", "aac")
	stream, plan := rig.start("busy", film, 0, nil)
	if stream.Mode != "hls" || len(plan.Renditions) < 2 {
		t.Fatalf("mode %s with %d audio renditions", stream.Mode, len(plan.Renditions))
	}
	rig.ready("busy")
	h := rig.streams.hls
	h.mu.Lock()
	id := ""
	for key, s := range h.streams {
		if s.session == "busy" {
			id = key
		}
	}
	if id == "" {
		h.mu.Unlock()
		t.Fatal("the stream is not running")
	}
	if h.outputBusy == nil {
		h.outputBusy = map[string]int{}
	}
	h.outputBusy[id]++
	h.mu.Unlock()
	slot := renditionSegmentFile(plan.Renditions[1], 0)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if code := rig.get(ctx, stream.Grant, slot, "").Code; code == http.StatusOK {
		t.Fatal("the slot was made while its folder was busy")
	}
	h.mu.Lock()
	h.outputBusy[id]--
	failure := h.streams[id].plan.Renditions[1].Failure
	h.mu.Unlock()
	if failure != "" {
		t.Fatalf("a busy folder failed the track: %s", failure)
	}
	if len(rig.fetch(stream.Grant, slot)) == 0 {
		t.Fatal("the track did not play once the folder was free")
	}
	rig.streams.Stop("busy")
}
