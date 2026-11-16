package live

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

type retryTransport func(*http.Request) (*http.Response, error)

func (f retryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Every fourth connection sends data. The three intervening empty connections
// fit inside the retry window; new data must restart that window each time.
func TestASourceThatDropsAndComesBackKeepsTheChannel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		connected := make(chan int, 1)
		client := &http.Client{Transport: retryTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			var data []byte
			if (calls-1)%4 == 0 {
				data = bytes.Repeat([]byte{0x47}, packet*4)
			}
			connected <- calls
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data)), Request: r}, nil
		})}
		tuners := NewTuners(t.TempDir()+"/live", client, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now,
			func() Tuning { return Tuning{Stretch: time.Minute, Retry: 4 * time.Second} })
		in, err := tuners.Tune(1, 9, 1, "http://fixture/stream", &Use{Name: "Living room"})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { tuners.Close(); <-in.done }()
		for want := 1; want <= 10; want++ {
			select {
			case got := <-connected:
				if got != want {
					t.Fatalf("connection %d, want %d", got, want)
				}
			case <-in.done:
				t.Fatal("the channel stopped before reconnecting", in.Stopped())
			case <-time.After(30 * time.Second):
				t.Fatal("the channel did not reconnect")
			}
		}
		if err := in.Stopped(); err != nil {
			t.Fatal("the channel gave up despite new data", err)
		}
	})
}
