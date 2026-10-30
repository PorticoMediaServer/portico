package httpapi

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type pacedDeadlineWriter struct {
	*refusalDeadlineWriter
	chunkSizes []int
	pause      time.Duration
}

func (w *pacedDeadlineWriter) Write(p []byte) (int, error) {
	w.chunkSizes = append(w.chunkSizes, len(p))
	if w.deadline.IsZero() || !time.Now().Before(w.deadline) {
		return 0, fmt.Errorf("write lacked a live deadline")
	}
	time.Sleep(w.pause)
	return w.refusalDeadlineWriter.Write(p)
}

func TestResponseDeadlineIsLazyAndRollsWithinLargeWrites(t *testing.T) {
	w := &pacedDeadlineWriter{refusalDeadlineWriter: &refusalDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}, pause: 20 * time.Millisecond}
	r := withResponseDeadline(w)
	r.window, r.interval = 35*time.Millisecond, time.Millisecond
	if !w.deadline.IsZero() {
		t.Fatal("response deadline included pre-response database work")
	}
	payload := bytes.Repeat([]byte("x"), 4*(16<<10)+1)
	if n, err := r.Write(payload); err != nil || n != len(payload) {
		t.Fatalf("progressing large response failed: %d %v", n, err)
	}
	if len(w.chunkSizes) != 5 {
		t.Fatalf("encoder write was not chunked: %v", w.chunkSizes)
	}
	for _, chunk := range w.chunkSizes {
		if chunk > 16<<10 {
			t.Fatal("chunk exceeds bounded write size")
		}
	}
	r.finishResponse()
	if !w.flushedWithBudget || !w.deadline.IsZero() {
		t.Fatal("final buffered flush was not bounded and released")
	}
}

func TestAdmittedJSONFinalFlushBoundsGzipAndRestoresDeadline(t *testing.T) {
	for _, accept := range []string{"", "gzip"} {
		t.Run(accept, func(t *testing.T) {
			a := newAdmission()
			mux := http.NewServeMux()
			payload := `{"items":"` + strings.Repeat("content", 1000) + `"}`
			mux.HandleFunc("GET /v1/home", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, payload)
			})
			w := &refusalDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
			r := httptest.NewRequest("GET", "/v1/home", nil)
			r.Header.Set("Accept-Encoding", accept)
			a.wrap(mux, mux).ServeHTTP(w, r)
			if !w.wroteWithBudget || !w.flushedWithBudget || !w.deadline.IsZero() {
				t.Fatal("JSON body or trailer escaped the write budget")
			}
			body := w.Body.Bytes()
			if accept == "gzip" {
				reader, err := gzip.NewReader(bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				body, err = io.ReadAll(reader)
				if err != nil {
					t.Fatal(err)
				}
				_ = reader.Close()
			}
			if string(body) != payload {
				t.Fatal("response finalization changed the document")
			}
		})
	}
}

func TestAdmissionRefusalDoesNotDrainAnUnsentHTTP1Body(t *testing.T) {
	a := newAdmission()
	l := newLane(lanePlayback, laneSpec{capacity: 1})
	a.lanes[lanePlayback] = l
	if admitted, _ := l.begin("occupied"); !admitted {
		t.Fatal("failed to occupy lane")
	}
	defer l.leave("occupied")
	defer l.release("occupied")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/playback/sessions", func(w http.ResponseWriter, r *http.Request) { t.Error("refused request reached handler") })
	server := httptest.NewServer(a.wrap(mux, mux))
	defer server.Close()
	connection, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	_, err = fmt.Fprintf(connection, "POST /v1/playback/sessions HTTP/1.1\r\nHost: x\r\nContent-Length: 100\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(connection)
	if err != nil {
		t.Fatalf("refusal waited for an unsent request body: %v", err)
	}
	if !bytes.Contains(raw, []byte("503 Service Unavailable")) || !bytes.Contains(raw, []byte("Connection: close")) || !bytes.Contains(raw, []byte("server_busy")) {
		t.Fatalf("unexpected refusal: %s", raw)
	}
}
