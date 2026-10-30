package storage

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RemoteProbeBridge exposes a capability-gated loopback view of a captured source
// to an allowlisted demuxer. No origin URL, credentials or signed locator reach
// FFprobe. Each returned response has exactly its declared finite extent.
func RemoteProbeBridge(ctx context.Context, object RemoteObject) (string, func(), error) {
	if object == nil || object.Snapshot().Size <= 0 {
		return "", nil, ErrRemoteRange
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	var key [32]byte
	if _, err = rand.Read(key[:]); err != nil {
		listener.Close()
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(key[:])
	var used, requests atomic.Int64
	size := object.Snapshot().Size
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+token || r.URL.RawQuery != "" || (r.Method != "GET" && r.Method != "HEAD") {
			http.NotFound(w, r)
			return
		}
		if requests.Add(1) > 64 {
			http.Error(w, "probe request budget exhausted", 429)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Accept-Ranges", "bytes")
		if r.Method == "HEAD" {
			w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
			return
		}
		start, end, ok := probeRange(r.Header.Get("Range"), size)
		if !ok {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
			w.WriteHeader(416)
			return
		}
		length := end - start + 1
		// Bound open-ended requests without lying about the returned range/length.
		if length > 2<<20 {
			if r.Header.Get("Range") == "" {
				http.Error(w, "range required", 416)
				return
			}
			length = 2 << 20
			end = start + length - 1
		}
		if used.Add(length) > 16<<20 {
			http.Error(w, "probe byte budget exhausted", 429)
			return
		}
		readCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		body, err := object.OpenRange(readCtx, start, length)
		if err != nil {
			http.Error(w, "source unavailable", 502)
			return
		}
		defer body.Close()
		w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
		if r.Header.Get("Range") != "" {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
			w.WriteHeader(206)
		}
		_, _ = io.CopyN(w, body, length)
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 2 * time.Second, IdleTimeout: 2 * time.Second, WriteTimeout: 20 * time.Second}
	supervise.Go("storage.remote-bridge.serve", func() { _ = server.Serve(listener) })
	var once sync.Once
	shutdown := func() { once.Do(func() { _ = server.Close() }) }
	stop := context.AfterFunc(ctx, shutdown)
	close := func() { stop(); shutdown() }
	return "http://" + listener.Addr().String() + "/" + token, close, nil
}
func probeRange(raw string, size int64) (int64, int64, bool) {
	if size <= 0 {
		return 0, 0, false
	}
	if raw == "" {
		return 0, size - 1, true
	}
	if len(raw) > 128 || !strings.HasPrefix(raw, "bytes=") || strings.Contains(raw, ",") {
		return 0, 0, false
	}
	a, b, ok := strings.Cut(strings.TrimPrefix(raw, "bytes="), "-")
	if !ok {
		return 0, 0, false
	}
	if a == "" {
		n, e := strconv.ParseInt(b, 10, 64)
		if e != nil || n <= 0 {
			return 0, 0, false
		}
		return max(int64(0), size-n), size - 1, true
	}
	start, e := strconv.ParseInt(a, 10, 64)
	if e != nil || start < 0 || start >= size {
		return 0, 0, false
	}
	end := size - 1
	if b != "" {
		end, e = strconv.ParseInt(b, 10, 64)
		if e != nil || end < start {
			return 0, 0, false
		}
		end = min(end, size-1)
	}
	return start, end, true
}
