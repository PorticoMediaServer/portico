package streaming

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote/remotetest"
)

// A film on a WebDAV server plays as it is by byte range and is repackaged by the converter
// through the loopback bridge, both through the source's throttle; a second audio track is
// converted beside it the same way; a file whose size is not the one scanned is refused.
// Where this host has the decoder's isolated bridge, the test requires the sandbox, so a
// converter that kept the host network would be refused.
func TestARemoteFilePlaysDirectAndRepackaged(t *testing.T) {
	rig := newMediaRig(t)
	direct := rig.generate("direct.mp4", 4, "-c:v", "libx264", "-c:a", "aac", "-movflags", "+faststart")
	remux := rig.generate("remux.mkv", 12, "-map", "0:v", "-map", "1:a", "-map", "1:a", "-c:v", "libx264", "-c:a", "aac")
	if decoder.ProbeConfinement(context.Background()) == nil {
		t.Setenv("PORTICO_DECODER_SANDBOX", "required")
	}
	files := map[string]string{}
	for _, path := range []string{direct, remux} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		files["Films/"+filepath.Base(path)] = string(raw)
	}
	dav := remotetest.NewDAV(t, files)
	src, _ := remote.NewWebDAV(dav.URL, dav.User, dav.Password)
	storage := remote.Throttled(src, remote.NewThrottle(remote.Limits{Connections: 4, PerSecond: 50}))
	start := func(session, path string) (Stream, DeliveryPlan) {
		request, info := rig.request(path)
		plan, err := Plan(request)
		if err != nil {
			t.Fatal(err)
		}
		stream, err := rig.streams.Start(context.Background(), StartRequest{SessionID: session, Plan: plan, Request: &request,
			Source: RemoteFile(storage, "Films/"+filepath.Base(path), info.Size(), info.ModTime())})
		if err != nil {
			t.Fatalf("start %s: %v", path, err)
		}
		return stream, plan
	}

	stream, _ := start("s1", direct)
	if stream.Mode != "direct" {
		t.Fatalf("mode %s", stream.Mode)
	}
	rig.ready("s1")
	whole := files["Films/direct.mp4"]
	reads := dav.Reads.Load()
	response := rig.get(context.Background(), stream.Grant, "original", "bytes=100-299")
	if response.Code != http.StatusPartialContent || response.Body.String() != whole[100:300] {
		t.Fatalf("range answered %d with %d bytes", response.Code, response.Body.Len())
	}
	if got := dav.Reads.Load() - reads; got > 2 {
		t.Fatalf("one range took %d requests", got)
	}
	rig.streams.Stop("s1")

	stream, plan := start("s2", remux)
	if stream.Mode != "hls" || plan.VideoAction != "copy" {
		t.Fatalf("matroska for a browser: mode %s video %s", stream.Mode, plan.VideoAction)
	}
	rig.ready("s2")
	rig.requireContinuous(rig.packets(stream.Grant, 0, 99), plan, 0, 12)
	if len(plan.Renditions) < 2 {
		t.Fatalf("two audio tracks gave %d renditions", len(plan.Renditions))
	}
	renditions := 0
	for _, list := range rig.playlists(stream.Grant) {
		if strings.HasPrefix(list.name, "audio-") && len(list.segments) > 0 {
			if len(rig.fetch(stream.Grant, list.segments[0])) == 0 {
				t.Fatalf("%s: empty segment", list.name)
			}
			renditions++
		}
	}
	if renditions == 0 {
		t.Fatal("no audio rendition playlist was offered")
	}
	rig.streams.Stop("s2")

	request, info := rig.request(direct)
	_, err := rig.streams.Start(context.Background(), StartRequest{SessionID: "s3", Plan: DeliveryPlan{Mode: "direct", Strategy: DeliveryOriginal},
		Request: &request, Source: RemoteFile(storage, "Films/direct.mp4", info.Size()+1, info.ModTime())})
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("a file of another size: %v", err)
	}
}
