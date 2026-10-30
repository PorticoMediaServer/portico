package remotemedia

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"sync/atomic"
	"time"
)

// Bridge provides only a capability-gated local view of the admitted MP4 bytes.
// FFprobe is separately forced to MOV and disables external data references.
func (c *Client) Bridge(ctx context.Context) (string, func(), error) {
	resp, e := c.Open(ctx, "GET", "bytes=0-31")
	if e != nil {
		return "", nil, e
	}
	head := make([]byte, 32)
	n, readErr := io.ReadFull(resp.Body, head)
	resp.Body.Close()
	if readErr != nil && readErr != io.ErrUnexpectedEOF {
		return "", nil, ErrUnsupported
	}
	if n < 12 || !bytes.Equal(head[4:8], []byte("ftyp")) {
		return "", nil, ErrUnsupported
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return "", nil, e
	}
	raw := make([]byte, 32)
	if _, e = rand.Read(raw); e != nil {
		listener.Close()
		return "", nil, e
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	var used atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+token || r.URL.RawQuery != "" || (r.Method != "GET" && r.Method != "HEAD") {
			http.NotFound(w, r)
			return
		}
		if used.Load() >= 16<<20 {
			http.Error(w, "probe byte budget exhausted", 429)
			return
		}
		response, e := c.Open(ctx, r.Method, r.Header.Get("Range"))
		if e != nil {
			http.Error(w, "source unavailable", 502)
			return
		}
		defer response.Body.Close()
		CopyResponse(w, response, &budgetReader{reader: response.Body, used: &used, limit: 16 << 20})
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 2 * time.Second, IdleTimeout: 2 * time.Second}
	supervise.Go("remotemedia.bridge.serve", func() { _ = server.Serve(listener) })
	close := func() { _ = server.Close() }
	supervise.Go("remotemedia.bridge.stop", func() { <-ctx.Done(); close() })
	return "http://" + listener.Addr().String() + "/" + token, close, nil
}

type budgetReader struct {
	reader io.Reader
	used   *atomic.Int64
	limit  int64
}

func (r *budgetReader) Read(p []byte) (int, error) {
	for {
		used := r.used.Load()
		remaining := r.limit - used
		if remaining <= 0 {
			return 0, errors.New("probe byte budget exhausted")
		}
		reserve := min(int64(len(p)), remaining)
		if !r.used.CompareAndSwap(used, used+reserve) {
			continue
		}
		n, e := r.reader.Read(p[:reserve])
		r.used.Add(int64(n) - reserve)
		return n, e
	}
}
