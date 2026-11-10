package remote_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote/remotetest"
)

var ctx = context.Background()

// A WebDAV folder is listed with one request, names as they are, and a file is read by
// range; a wrong password and a "slow down" are told apart, and the throttle waits out the
// slow-down and carries on.
func TestWebDAV(t *testing.T) {
	dav := remotetest.NewDAV(t, map[string]string{"Films/Amélie (2001)/Amélie (2001).mkv": "0123456789", "Films/Heat #2.mkv": "abc"})
	src, err := remote.NewWebDAV(dav.URL, dav.User, dav.Password)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := src.List(ctx, "Films")
	if err != nil || len(listed) != 2 || dav.Listings.Load() != 1 {
		t.Fatalf("listing: %v %+v after %d requests", err, listed, dav.Listings.Load())
	}
	if e := listed[0]; e.Name != "Amélie (2001)" || !e.Dir {
		t.Fatalf("folder: %+v", e)
	}
	if e := listed[1]; e.Name != "Heat #2.mkv" || e.Dir || e.Size != 3 || e.ModifiedNS == 0 {
		t.Fatalf("file: %+v", e)
	}

	file, err := src.Open(ctx, "Films/Amélie (2001)/Amélie (2001).mkv")
	if err != nil || file.Size() != 10 {
		t.Fatalf("open: %v", err)
	}
	part := make([]byte, 4)
	if n, err := file.ReadAt(part, 8); n != 2 || err != io.EOF || string(part[:n]) != "89" {
		t.Fatalf("read at the end: %d %v %q", n, err, part[:n])
	}
	if n, err := file.ReadAt(part, 3); n != 4 || err != nil || string(part) != "3456" {
		t.Fatalf("read: %d %v %q", n, err, part)
	}
	if _, err := src.List(ctx, "../outside"); err == nil {
		t.Fatal("a path climbing out of the source was asked for")
	}

	wrong, _ := remote.NewWebDAV(dav.URL, dav.User, "wrong")
	if _, err := wrong.List(ctx, ""); !errors.Is(err, remote.ErrSignIn) {
		t.Fatalf("a wrong password: %v", err)
	}
	dav.SlowDowns.Store(1)
	var slow *remote.SlowDown
	if _, err := src.List(ctx, ""); !errors.As(err, &slow) || slow.After != time.Second {
		t.Fatalf("a slow-down: %v", err)
	}
	dav.SlowDowns.Store(1)
	start := time.Now()
	throttled := remote.Throttled(src, remote.NewThrottle(remote.Limits{Connections: 1, PerSecond: 50}))
	if listed, err := throttled.List(ctx, ""); err != nil || len(listed) != 1 || time.Since(start) < time.Second {
		t.Fatalf("behind the throttle: %v %+v after %s", err, listed, time.Since(start))
	}

	dav.NoRanges.Store(true)
	if _, err := src.Open(ctx, "Films/Heat #2.mkv"); !errors.Is(err, remote.ErrNoRanges) {
		t.Fatalf("a server that sends whole files: %v", err)
	}
}

// A name a WebDAV server lists that could point somewhere else is dropped, and the rest of
// the listing, a name with two dots in a row included, is kept.
func TestWebDAVDropsUnsafeNames(t *testing.T) {
	dav := remotetest.NewDAV(t, map[string]string{"Films/Heat.mkv": "x", "Films/Film..mkv": "y"})
	dav.BadNames = []string{".", "..", "%2E", "%2E%2E", `back\slash.mkv`, "back%5Cslash.mkv", "slash%2Fname.mkv", "%2E%2E%2Fescape.mkv", "nul%00.mkv"}
	src, _ := remote.NewWebDAV(dav.URL, dav.User, dav.Password)
	listed, err := src.List(ctx, "Films")
	if err != nil || len(listed) != 2 || listed[0].Name != "Film..mkv" || listed[1].Name != "Heat.mkv" {
		t.Fatalf("listing with unsafe names: %v %+v", err, listed)
	}
}
