package networking

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type requestContextRecorder struct {
	*httptest.ResponseRecorder
	ctx context.Context
}

func (w requestContextRecorder) RequestContext() context.Context { return w.ctx }

func TestCanceledIdentityProbeIsClientClosed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newRouteIdentityFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	body, err := json.Marshal(map[string]string{"baseUrl": "http://127.0.0.1:32400", "nonce": base64.RawURLEncoding.EncodeToString(make([]byte, 32))})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:32400/v1/networking/identity-proof", bytes.NewReader(body)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	w := requestContextRecorder{httptest.NewRecorder(), ctx}
	f.handler.ServeHTTP(w, r)
	if w.Code != 499 || w.Body.Len() != 0 {
		t.Fatalf("canceled probe recorded as %d %s", w.Code, w.Body.String())
	}
	lines := f.log.take()
	if len(lines) != 1 || !strings.Contains(lines[0], "499") || !strings.Contains(lines[0], "client closed") || strings.Contains(lines[0], "warn:") || strings.Contains(lines[0], "503") {
		t.Fatal("canceled probe polluted server failure logs", lines)
	}
}
