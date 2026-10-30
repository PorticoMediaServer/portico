package httpapi

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The risk in a rolling deadline is getting it backwards and cutting off the
// client least able to recover: someone on a poor connection, part way through a
// long file. So this reads half a megabyte at a hundred kilobytes a second —
// five seconds of transfer, twenty-five refreshes of a two-second stall budget —
// and requires every byte to arrive.
func TestASlowButAliveClientIsNeverDropped(t *testing.T) {
	testtier.Media(t, "a slow client paced over real write deadlines (about 5 s)")
	restore := shortDeadlines(t, 2*time.Second, 200*time.Millisecond)
	defer restore()
	const total = 512 << 10
	payload := bytes.Repeat([]byte("p"), total)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := withRollingDeadline(w)
		defer body.release()
		http.ServeContent(body, r, "media.mp4", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	response, err := http.Get(server.URL + "/v1/media/grant")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	// 100 kB/s: eight kilobytes every eighty milliseconds.
	read := 0
	buffer := make([]byte, 8<<10)
	for read < total {
		time.Sleep(80 * time.Millisecond)
		n, err := io.ReadFull(response.Body, buffer)
		read += n
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			t.Fatalf("a client reading at 100 kB/s was dropped after %d of %d bytes: %v", read, total, err)
		}
	}
	if read != total {
		t.Fatalf("the slow client received %d of %d bytes", read, total)
	}
}

// The other half: a client that stops reading entirely must not hold the
// goroutine, its lane slot and its open file until the operating system gives
// up, which can be minutes.
func TestAClientThatStopsReadingIsReclaimedWithinTheDeadline(t *testing.T) {
	restore := shortDeadlines(t, 300*time.Millisecond, 50*time.Millisecond)
	defer restore()
	var returned atomic.Bool
	finished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		body := withRollingDeadline(w)
		defer body.release()
		// Far more than any socket buffer, so the write blocks once the client
		// stops reading.
		http.ServeContent(body, r, "media.mp4", time.Time{}, bytes.NewReader(bytes.Repeat([]byte("p"), 64<<20)))
		returned.Store(true)
	}))
	defer server.Close()
	connection, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err = fmt.Fprintf(connection, "GET /v1/media/grant HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	// Read nothing at all from here on.
	select {
	case <-finished:
		if !returned.Load() {
			t.Fatal("the handler ended without returning from the body write")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a client that stopped reading held the handler past the write deadline")
	}
}

// A frame deadline must not leak into the next response on a keep-alive
// connection, or an ordinary request minutes later inherits a deadline in the
// past and fails for no reason.
func TestTheRollingDeadlineIsReleasedWithTheBody(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	restore := shortDeadlines(t, 300*time.Millisecond, 50*time.Millisecond)
	defer restore()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/media/grant" {
			body := withRollingDeadline(w)
			body.Write([]byte("bytes"))
			body.release()
			return
		}
		time.Sleep(600 * time.Millisecond)
		w.Write([]byte("late but fine"))
	}))
	defer server.Close()
	client := &http.Client{}
	first, err := client.Get(server.URL + "/v1/media/grant")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, first.Body)
	first.Body.Close()
	second, err := client.Get(server.URL + "/v1/items/one")
	if err != nil {
		t.Fatalf("the next request on the same connection failed: %v", err)
	}
	defer second.Body.Close()
	raw, err := io.ReadAll(second.Body)
	if err != nil || string(raw) != "late but fine" {
		t.Fatalf("a deadline leaked into the next response: %q %v", raw, err)
	}
}

func shortDeadlines(t *testing.T, window, refresh time.Duration) func() {
	t.Helper()
	previousWindow, previousRefresh, previousFrame := mediaWriteDeadline, deadlineRefreshInterval, streamFrameDeadline
	mediaWriteDeadline, deadlineRefreshInterval, streamFrameDeadline = window, refresh, window
	return func() {
		mediaWriteDeadline, deadlineRefreshInterval, streamFrameDeadline = previousWindow, previousRefresh, previousFrame
	}
}

// frameDeadlineRecorder records the order in which a framed write arms and
// clears the write deadline, writes the frame and flushes it.
type frameDeadlineRecorder struct {
	http.ResponseWriter
	order    *[]string
	writeErr error
	flushErr error
}

func (r *frameDeadlineRecorder) note(step string) { *r.order = append(*r.order, step) }

func (r *frameDeadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	if deadline.IsZero() {
		r.note("deadline:clear")
	} else {
		r.note("deadline")
	}
	return nil
}

func (r *frameDeadlineRecorder) Write(p []byte) (int, error) {
	r.note("write")
	if r.writeErr != nil {
		return 0, r.writeErr
	}
	return len(p), nil
}

func (r *frameDeadlineRecorder) FlushError() error {
	r.note("flush")
	return r.flushErr
}

// The frame deadline bounds one frame, and is cleared once that frame has been
// written and flushed. Leaving it armed after the flush is what turns a quiet
// stream into a dead one: the events heartbeat is slower than the budget, and an
// expired write deadline resets the stream in HTTP/2 rather than waiting for it.
func TestAFrameDeadlineCoversTheFrameAndIsClearedAfterIt(t *testing.T) {
	for _, test := range []struct {
		name     string
		writeErr error
		flushErr error
		want     string
	}{
		{name: "written and flushed", want: "deadline,write,flush,deadline:clear"},
		{name: "write failed", writeErr: errors.New("peer gone"), want: "deadline,write,deadline:clear"},
		{name: "flush failed", flushErr: errors.New("peer gone"), want: "deadline,write,flush,deadline:clear"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var order []string
			recorder := &frameDeadlineRecorder{ResponseWriter: httptest.NewRecorder(), order: &order, writeErr: test.writeErr, flushErr: test.flushErr}
			err := frameWrite(recorder, func(out io.Writer) (int, error) {
				return io.WriteString(out, "event: message\ndata: {}\n\n")
			})
			wantErr := test.writeErr
			if wantErr == nil {
				wantErr = test.flushErr
			}
			if wantErr == nil && err != nil {
				t.Fatalf("frame error: %v", err)
			}
			if wantErr != nil && !errors.Is(err, wantErr) {
				t.Fatalf("frame error: %v, want %v", err, wantErr)
			}
			if got := strings.Join(order, ","); got != test.want {
				t.Fatalf("frame steps: %s, want %s", got, test.want)
			}
		})
	}
	// A writer that cannot take a deadline still streams: the deadline is a
	// reclamation mechanism, not a correctness one.
	plain := httptest.NewRecorder()
	if err := frameWrite(plain, func(out io.Writer) (int, error) { return io.WriteString(out, "event: message\n\n") }); err != nil {
		t.Fatal(err)
	}
	if plain.Body.String() != "event: message\n\n" || !plain.Flushed {
		t.Fatalf("undeadlined frame: %q flushed=%v", plain.Body.String(), plain.Flushed)
	}
}

// The other half of the same mechanism: a client that stops reading still fails
// its frame within the deadline, so the goroutine, its lane slot and its poller
// are reclaimed rather than held until the OS notices.
func TestAStreamWhoseClientStopsReadingIsReclaimedWithinTheFrameDeadline(t *testing.T) {
	previous := streamFrameDeadline
	streamFrameDeadline = 300 * time.Millisecond
	defer func() { streamFrameDeadline = previous }()
	type outcome struct {
		frames int
		err    error
	}
	finished := make(chan outcome, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		frame := strings.Repeat("e", 32<<10) + "\n\n"
		for sent := 0; sent < 1<<12; sent++ {
			if err := frameWrite(w, func(out io.Writer) (int, error) {
				return io.WriteString(out, frame)
			}); err != nil {
				finished <- outcome{sent, err}
				return
			}
		}
		finished <- outcome{1 << 12, nil}
	}))
	defer server.Close()
	connection, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err = fmt.Fprintf(connection, "GET /v1/events HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	// Read nothing at all from here on.
	select {
	case got := <-finished:
		if got.err == nil {
			t.Fatalf("a client that stopped reading accepted %d frames past the frame deadline", got.frames)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("a client that stopped reading held the stream past the frame deadline")
	}
}
