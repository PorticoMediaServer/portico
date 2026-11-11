package live

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// The rewind folder of a channel never holds much more than its byte limit, however long the
// channel is watched. A clock that moves five seconds at every look cuts a new segment at
// every read, so the source's 13 MB become many segments in well under a second.
func TestTheRewindIsHeldToItsBytes(t *testing.T) {
	if most := rewindHeld(t, 1<<20); most > 1<<20+128<<10 {
		t.Fatalf("the rewind folder reached %d bytes against a limit of %d", most, 1<<20)
	}
	if most := rewindHeld(t, 0); most < 8<<20 {
		t.Fatalf("with no byte limit the folder held only %d bytes: the source's bytes never arrived and the test proves nothing", most)
	}
}

// rewindHeld streams about 13 MB through one channel and answers the most its rewind folder
// held at any moment.
func rewindHeld(t *testing.T, limit int64) int64 {
	sent := make(chan struct{})
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, packet*348) // about 64 KiB
		for n := 0; n < 200 && r.Context().Err() == nil; n++ {
			w.Write(chunk)
			w.(http.Flusher).Flush()
		}
		close(sent)
		<-r.Context().Done()
	}))
	t.Cleanup(src.Close)
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Add(int64(5*time.Second))) }
	dir := filepath.Join(t.TempDir(), "live")
	tuners := NewTuners(dir, src.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)), now,
		func() Tuning { return Tuning{Stretch: 24 * 365 * time.Hour, MaxBytes: limit, Retry: time.Minute} })
	defer tuners.Close()
	if _, err := tuners.Tune(1, 9, 1, src.URL, &Use{Name: "Living room"}); err != nil {
		t.Fatal(err)
	}
	var most int64
	measure := func() {
		var held int64
		filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				held += info.Size()
			}
			return nil
		})
		most = max(most, held)
	}
	for {
		select {
		case <-sent:
			time.Sleep(100 * time.Millisecond) // the last bytes reach the folder
			measure()
			return most
		case <-time.After(2 * time.Millisecond):
			measure()
		}
	}
}
