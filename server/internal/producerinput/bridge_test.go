package producerinput

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixtureSource struct {
	mu             sync.Mutex
	data           []byte
	reads          map[int64]int
	lost           bool
	block, entered chan struct{}
	once           sync.Once
}

func (s *fixtureSource) Metadata() (playback.PreparedInputMetadata, error) {
	return playback.PreparedInputMetadata{Reference: playback.SourceReference{Kind: playback.ObservedSourceReference, ID: "fixture-acquisition"}, Length: playback.KnownInt64{Known: true, Value: int64(len(s.data))}}, nil
}
func (s *fixtureSource) Validate(ctx context.Context) (playback.InputValidation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return playback.InputValidation{}, err
	}
	if s.lost {
		return playback.InputValidation{}, playback.ErrObservedContinuityLost
	}
	return playback.InputValidation{Evidence: playback.InputSourceEvidence{Reference: playback.SourceReference{Kind: playback.ObservedSourceReference, ID: "fixture-acquisition"}, ObservationInterval: &playback.ObservationInterval{First: 1, Last: 2}}}, nil
}
func (s *fixtureSource) ReadExtent(ctx context.Context, offset, length int64) (playback.ObservedExtent, error) {
	if s.block != nil {
		s.once.Do(func() { close(s.entered) })
		<-s.block
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lost {
		return playback.ObservedExtent{}, playback.ErrObservedContinuityLost
	}
	s.reads[offset]++
	return playback.ObservedExtent{Bytes: append([]byte(nil), s.data[offset:offset+length]...), Evidence: playback.InputSourceEvidence{Reference: playback.SourceReference{Kind: playback.ObservedSourceReference, ID: "fixture-acquisition"}, ObservationInterval: &playback.ObservationInterval{First: 1, Last: 2}}}, nil
}
func bridgeFixture(t *testing.T, source *fixtureSource) (*Bridge, context.CancelFunc) {
	t.Helper()
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	parent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal("loopback listener unavailable", err)
	}
	owner, cancel := context.WithCancel(context.Background())
	bridge, err := newBridge(owner, source, parent, listener, 16)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		bridge.Close()
		select {
		case <-bridge.Done():
		case <-time.After(2 * time.Second):
			t.Error("bridge handlers/cache did not physically drain")
		}
	})
	return bridge, cancel
}
func request(t *testing.T, url, method, rangeHeader string) (int, []byte, http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data, response.Header
}
func TestProducerBridgeRetainsOverlapsAndEvidence(t *testing.T) {
	source := &fixtureSource{data: []byte("0123456789abcdefghijklmnopqrstuvwxyzABCD"), reads: map[int64]int{}}
	bridge, _ := bridgeFixture(t, source)
	status, first, _ := request(t, bridge.URL(), "GET", "bytes=0-19")
	if status != 206 || string(first) != "0123456789abcdefghij" {
		t.Fatal(status, string(first))
	}
	// Model an upstream mutation hidden from ordinary observation. Already served
	// overlaps must remain the same closed bytes, without claiming a strong source.
	source.mu.Lock()
	copy(source.data[:32], []byte(strings.Repeat("x", 32)))
	source.mu.Unlock()
	status, overlap, _ := request(t, bridge.URL(), "GET", "bytes=8-23")
	if status != 206 || string(overlap) != "89abcdefghijklmn" {
		t.Fatal("overlap changed under producer URL", status, string(overlap))
	}
	source.mu.Lock()
	reads := source.reads[0] + source.reads[16]
	source.mu.Unlock()
	if reads != 2 {
		t.Fatal("closed overlaps reacquired", reads)
	}
	evidence, err := bridge.Finalize(context.Background())
	if err != nil || len(evidence.Chunks) != 2 || evidence.Reference.Kind != playback.ObservedSourceReference {
		t.Fatal("lost observation provenance", evidence, err)
	}
	for _, chunk := range evidence.Chunks {
		if chunk.Source.ObservationInterval == nil || chunk.Source.Reference != evidence.Reference || chunk.Object.Digest == "" {
			t.Fatal("incomplete retained evidence")
		}
	}
}
func TestProducerBridgeRangeHeadAndPrivateToken(t *testing.T) {
	source := &fixtureSource{data: []byte("0123456789abcdefghijklmnopqrstuvwxyzABCD"), reads: map[int64]int{}}
	bridge, _ := bridgeFixture(t, source)
	status, body, header := request(t, bridge.URL(), "HEAD", "bytes=-4")
	if status != 206 || len(body) != 0 || header.Get("Content-Length") != "4" {
		t.Fatal("HEAD contract", status, header)
	}
	status, body, _ = request(t, bridge.URL(), "GET", "bytes=-4")
	if status != 206 || string(body) != "ABCD" {
		t.Fatal("suffix range", status, string(body))
	}
	status, _, _ = request(t, bridge.URL(), "GET", "bytes=0-1,4-5")
	if status != 416 {
		t.Fatal("multipart range accepted", status)
	}
	status, _, _ = request(t, bridge.URL()+"wrong", "GET", "")
	if status != 404 {
		t.Fatal("wrong private token accepted", status)
	}
	status, _, _ = request(t, bridge.URL(), "POST", "")
	if status != 405 {
		t.Fatal("producer mutation method accepted", status)
	}
	source.mu.Lock()
	count := len(source.reads)
	source.mu.Unlock()
	if count != 1 {
		t.Fatal("rejected/HEAD request acquired data", count)
	}
}
func TestProducerBridgeContinuityLossPreventsFinalize(t *testing.T) {
	source := &fixtureSource{data: []byte("0123456789abcdefghijklmnopqrstuvwxyzABCD"), reads: map[int64]int{}}
	bridge, _ := bridgeFixture(t, source)
	request(t, bridge.URL(), "GET", "bytes=0-3")
	source.mu.Lock()
	source.lost = true
	source.mu.Unlock()
	if _, err := bridge.Finalize(context.Background()); err == nil {
		t.Fatal("lost acquisition became publishable evidence")
	}
	select {
	case <-bridge.Done():
	case <-time.After(time.Second):
		t.Fatal("failed source job retained endpoint/cache")
	}
	if _, err := os.Stat(bridge.cache); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed job cache not removed", err)
	}
}
func TestProducerBridgeCloseRetainsPhysicalHandlerDebt(t *testing.T) {
	source := &fixtureSource{data: []byte("0123456789abcdefghijklmnopqrstuvwxyzABCD"), reads: map[int64]int{}, block: make(chan struct{}), entered: make(chan struct{})}
	bridge, cancel := bridgeFixture(t, source)
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		client := &http.Client{Timeout: time.Second}
		response, err := client.Get(bridge.URL())
		if err == nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
	}()
	select {
	case <-source.entered:
	case <-time.After(time.Second):
		close(source.block)
		t.Fatal("source did not enter bounded fake blocked IO")
	}
	cancel()
	bridge.Close()
	select {
	case <-bridge.Done():
		close(source.block)
		t.Fatal("cancellation acknowledged unfinished source handler")
	case <-time.After(10 * time.Millisecond):
	}
	if _, err := os.Stat(bridge.cache); err != nil {
		close(source.block)
		t.Fatal("cache deleted before physical handler exit", err)
	}
	close(source.block)
	select {
	case <-bridge.Done():
	case <-time.After(time.Second):
		t.Fatal("released source handler did not drain")
	}
	<-requestDone
}

type failedResponseWriter struct {
	header  http.Header
	cancel  context.CancelFunc
	written bool
}

func (w *failedResponseWriter) Header() http.Header { return w.header }
func (w *failedResponseWriter) WriteHeader(int)     {}
func (w *failedResponseWriter) Write([]byte) (int, error) {
	w.written = true
	if w.cancel != nil {
		w.cancel()
	}
	return 0, io.ErrClosedPipe
}
func TestProducerBridgeTransferFailureAndDisconnect(t *testing.T) {
	for _, disconnect := range []bool{true, false} {
		func() {
			source := &fixtureSource{data: []byte("0123456789abcdefghijklmnopqrstuvwxyzABCD"), reads: map[int64]int{}}
			bridge, _ := bridgeFixture(t, source)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req := httptest.NewRequest("GET", bridge.URL(), nil).WithContext(ctx)
			req.Header.Set("Range", "bytes=0-7")
			writer := &failedResponseWriter{header: make(http.Header)}
			if disconnect {
				writer.cancel = cancel
			}
			bridge.ServeHTTP(writer, req)
			if !writer.written {
				t.Fatal("fixture never reached output transfer")
			}
			if disconnect {
				status, data, _ := request(t, bridge.URL(), "GET", "bytes=0-7")
				if status != 206 || string(data) != "01234567" {
					t.Fatal("normal disconnect destroyed retained overlap")
				}
				source.mu.Lock()
				reads := source.reads[0]
				source.mu.Unlock()
				if reads != 1 {
					t.Fatal("disconnect reacquired retained chunk")
				}
			} else if _, err := bridge.Finalize(context.Background()); err == nil {
				t.Fatal("non-cancellation transfer failure finalized")
			}
			deadline, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			if err := bridge.Shutdown(deadline); err != nil {
				t.Fatal(err)
			}
		}()
	}
}

func TestProducerBridgePairSharesRetentionAndDrains(t *testing.T) {
	source := &fixtureSource{data: []byte("0123456789abcdefghijklmnopqrstuvwxyzABCD"), reads: map[int64]int{}}
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	v4, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer v4.Close()
	v6, err := net.ListenTCP("tcp6", &net.TCPAddr{IP: net.IPv6loopback, Port: v4.Addr().(*net.TCPAddr).Port})
	if err != nil {
		t.Fatal(err)
	}
	defer v6.Close()
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	bridge, err := NewPair(owner, source, parent, v4, v6)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Shutdown(context.Background())
	_, a, _ := request(t, bridge.URL(), "GET", "bytes=0-15")
	_, b, _ := request(t, strings.Replace(bridge.URL(), "127.0.0.1", "[::1]", 1), "GET", "bytes=0-15")
	if string(a) != "0123456789abcdef" || string(b) != string(a) {
		t.Fatal("families served different bytes")
	}
	source.mu.Lock()
	reads := source.reads[0]
	source.mu.Unlock()
	if reads != 1 {
		t.Fatal("families reacquired same input chunk")
	}
	cancel()
	ctx, done := context.WithTimeout(context.Background(), 2*time.Second)
	defer done()
	if err = bridge.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{v4.Addr().String(), v6.Addr().String()} {
		listener, e := net.Listen("tcp", address)
		if e != nil {
			t.Fatal("server loop did not drain", e)
		}
		listener.Close()
	}
}
