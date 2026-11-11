package live

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A source that drops now and then but connects again at once keeps the channel going: the
// retry time counts from the latest drop, not from the first one an hour ago.
func TestASourceThatDropsAndComesBackKeepsTheChannel(t *testing.T) {
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for n := 0; n < 100 && r.Context().Err() == nil; n++ { // about 300 ms, then the connection drops
			p := make([]byte, packet)
			p[0] = 0x47
			w.Write(p)
			w.(http.Flusher).Flush()
			time.Sleep(3 * time.Millisecond)
		}
	}))
	t.Cleanup(src.Close)
	tuners := NewTuners(t.TempDir()+"/live", src.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now,
		func() Tuning { return Tuning{Stretch: time.Minute, Retry: 2 * time.Second} })
	t.Cleanup(tuners.Close)
	in, err := tuners.Tune(1, 9, 1, src.URL, &Use{Name: "Living room"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Second) // several drops, each followed by a good connection
	if err := in.Stopped(); err != nil {
		t.Fatalf("a source that came back after every drop was given up on: %v", err)
	}
}
