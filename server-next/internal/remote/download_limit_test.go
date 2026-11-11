package remote_test

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote/remotetest"
)

// Scanning rule 12: a source's download speed limit holds everything read from it together,
// a film streaming and a scan reading a file's facts at once. Every kind of source is read
// through its throttle (rclone and Drive too, over rclone's loopback WebDAV), so it holds for
// each. With no limit, the default, nothing waits.
func TestADownloadLimitHoldsEverythingReadFromASource(t *testing.T) {
	film := strings.Repeat("x", 300_000)
	dav := remotetest.NewDAV(t, map[string]string{"Films/Heat.mkv": film})
	src, _ := remote.NewWebDAV(dav.URL, dav.User, dav.Password)
	read := func(limits remote.Limits) time.Duration {
		storage := remote.Throttled(src, remote.NewThrottle(limits))
		began := time.Now()
		var wg sync.WaitGroup
		for _, ranged := range []bool{false, true} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				file, err := storage.Open(ctx, "Films/Heat.mkv")
				if err != nil {
					t.Error(err)
					return
				}
				defer file.Close()
				var got []byte
				if ranged {
					// A read of a block or more, as a scan's or the converter's.
					got = make([]byte, len(film))
					_, err = file.ReadAt(got, 0)
				} else {
					var body io.ReadCloser
					if body, err = remote.StreamFrom(file, 0); err == nil {
						got, err = io.ReadAll(body)
						body.Close()
					}
				}
				if err != nil || string(got) != film {
					t.Errorf("read %d bytes: %v", len(got), err)
				}
			}()
		}
		wg.Wait()
		return time.Since(began)
	}
	if took := read(remote.Limits{Connections: 4, PerSecond: 50}); took > time.Second {
		t.Fatalf("with no limit the reads took %v", took)
	}
	// 600 KB at 300 KB a second: about two seconds, less what the first read was let through.
	if took := read(remote.Limits{Connections: 4, PerSecond: 50, DownloadBytesPerSecond: 300_000}); took < 1500*time.Millisecond || took > 5*time.Second {
		t.Fatalf("600 KB at 300 KB a second took %v", took)
	}
}
