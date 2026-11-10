package streaming

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote/remotetest"
)

// A film on a WebDAV server plays as it is by byte range and is repackaged by the converter
// through the loopback bridge, both through the source's throttle; a file whose size is not
// the one scanned is refused.
func TestARemoteFilePlaysDirectAndRepackaged(t *testing.T) {
	rig := newMediaRig(t)
	direct := rig.generate("direct.mp4", 4, "-c:v", "libx264", "-c:a", "aac", "-movflags", "+faststart")
	remux := rig.generate("remux.mkv", 12, "-c:v", "libx264", "-c:a", "aac")
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
	rig.streams.Stop("s2")

	request, info := rig.request(direct)
	_, err := rig.streams.Start(context.Background(), StartRequest{SessionID: "s3", Plan: DeliveryPlan{Mode: "direct", Strategy: DeliveryOriginal},
		Request: &request, Source: RemoteFile(storage, "Films/direct.mp4", info.Size()+1, info.ModTime())})
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("a file of another size: %v", err)
	}
}
