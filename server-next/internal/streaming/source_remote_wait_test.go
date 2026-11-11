package streaming

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote/remotetest"
)

// Rule 14: when a source says slow down while a film plays, the film carries on from what is
// being read, asking nothing more of the source, and a read that needs a new request (a seek)
// waits as long as the source asked and then carries on, rather than failing.
func TestARemoteFilePlayingCarriesOnThroughASlowDown(t *testing.T) {
	film := strings.Repeat("0123456789", 1000)
	dav := remotetest.NewDAV(t, map[string]string{"Films/Heat.mkv": film})
	src, _ := remote.NewWebDAV(dav.URL, dav.User, dav.Password)
	throttle := remote.NewThrottle(remote.Limits{Connections: 2, PerSecond: 50})
	file, size, _, err := RemoteFile(remote.Throttled(src, throttle), "Films/Heat.mkv", int64(len(film)), time.Unix(1700000000, 0)).Open(context.Background())
	if err != nil || size != int64(len(film)) {
		t.Fatal(size, err)
	}
	defer file.Close()
	start := make([]byte, 100)
	if _, err := io.ReadFull(file, start); err != nil || string(start) != film[:100] {
		t.Fatal(err)
	}

	// Every new request is now told to slow down: the rest of the film still comes.
	dav.SlowDowns.Store(1000)
	reads := dav.Reads.Load()
	rest, err := io.ReadAll(file)
	if err != nil || string(rest) != film[100:] || dav.Reads.Load() != reads {
		t.Fatalf("the playing film stopped: %v, %d bytes, %d new requests", err, len(rest), dav.Reads.Load()-reads)
	}

	// A seek needs a new request: it waits out the source's one-second rest and carries on.
	dav.SlowDowns.Store(1)
	if _, err := file.Seek(5000, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	part := make([]byte, 10)
	if _, err := io.ReadFull(file, part); err != nil || string(part) != film[5000:5010] {
		t.Fatalf("after the rest: %v %q", err, part)
	}
	if waited := time.Since(began); waited < 900*time.Millisecond {
		t.Fatalf("the read did not wait for the rest the source asked: %v", waited)
	}
}
