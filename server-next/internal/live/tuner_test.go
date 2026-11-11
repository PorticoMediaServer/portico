package live

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// countingSource sends MPEG-TS packets whose first payload byte counts up, forever.
func countingSource(t *testing.T) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for n := 0; r.Context().Err() == nil; n++ {
			p := make([]byte, packet)
			p[0], p[1] = 0x47, byte(n)
			if _, err := w.Write(p); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(time.Millisecond)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

type firstBytes struct {
	bytes.Buffer
	want int
	done context.CancelFunc
}

func (f *firstBytes) Write(p []byte) (int, error) {
	f.Buffer.Write(p)
	if f.Len() >= f.want {
		f.done()
	}
	return len(p), nil
}

// Viewers of one channel share one connection; a second channel on a one-tuner source is
// refused naming what uses it; reading follows live, and a reader can start back at the
// oldest point kept.
func TestAChannelsInputIsSharedAndCanBeRewound(t *testing.T) {
	src := countingSource(t)
	tuners := NewTuners(t.TempDir()+"/live", src.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now,
		func() Tuning { return DefaultTuning })
	ctx, stop := context.WithCancel(context.Background())
	go tuners.Run(ctx)
	t.Cleanup(stop) // ends every input before the fake source closes
	first, second := &Use{Name: "Living room"}, &Use{Name: "Kitchen"}
	in, err := tuners.Tune(1, 9, 1, src.URL, first)
	if err != nil {
		t.Fatal(err)
	}
	again, err := tuners.Tune(1, 9, 1, src.URL, second)
	if err != nil || again != in {
		t.Fatalf("a second viewer did not share the input: %v", err)
	}
	var busy *NoTuner
	if _, err := tuners.Tune(2, 9, 1, src.URL, &Use{Record: true}); !errors.As(err, &busy) || len(busy.Using) != 2 {
		t.Fatalf("a second channel on a one-tuner source answered %v", err)
	}

	read := func(at time.Time) []byte {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out := &firstBytes{want: packet * 4, done: cancel}
		in.Read(ctx, at, out)
		if out.Len() < packet*4 || out.Bytes()[0] != 0x47 || out.Len()%packet != 0 && out.Len() < packet*4 {
			t.Fatalf("read %d bytes", out.Len())
		}
		return out.Bytes()
	}
	time.Sleep(300 * time.Millisecond)
	oldest, _ := in.Window()
	fromStart := read(oldest)
	if fromStart[1] != 0 {
		t.Fatalf("reading from the oldest point began at packet %d", fromStart[1])
	}
	if live := read(time.Time{}); live[0] != 0x47 {
		t.Fatal("reading live did not begin on a packet")
	}
	in.Leave(first)
	in.Leave(second)
}
