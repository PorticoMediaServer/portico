package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestSignInRefusedRetriesAndFreshSourceHideUnknownNames(t *testing.T) {
	d, _ := completedLocalFoundationFixture(t)
	const peer = "192.0.2.77:443"
	ctx := identity.WithSignInSource(context.Background(), peer)
	for _, name := range []string{"owner", "unlisted"} {
		for range 5 {
			_, _ = d.Identity.DirectLogin(ctx, name, "wrong")
		}
	}
	until := time.Now().Unix() + 30
	for _, table := range []string{"identity_credential_attempts", "identity_unknown_attempts"} {
		if _, err := d.DB.Exec("UPDATE "+table+" SET locked_until=?", until); err != nil {
			t.Fatal(err)
		}
	}
	h := New(d)
	request := func(peer, name string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]string{"username": name, "password": "wrong"})
		r := httptest.NewRequest("POST", "/v1/direct/sign-in", bytes.NewReader(raw))
		r.RemoteAddr = peer
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	body := func(w *httptest.ResponseRecorder) string {
		var v map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		delete(v, "requestId")
		raw, _ := json.Marshal(v)
		return string(raw)
	}
	for range 3 {
		real, synthetic := request(peer, "owner"), request(peer, "unlisted")
		if real.Code != 429 || synthetic.Code != real.Code || body(real) != body(synthetic) {
			t.Fatalf("refusal differs: known=%d %s unknown=%d %s", real.Code, body(real), synthetic.Code, body(synthetic))
		}
		a, err := strconv.Atoi(real.Header().Get("Retry-After"))
		if err != nil {
			t.Fatal(err)
		}
		b, err := strconv.Atoi(synthetic.Header().Get("Retry-After"))
		if err != nil {
			t.Fatal(err)
		}
		if diff := a - b; diff < -1 || diff > 1 {
			t.Fatalf("refused retry extended synthetic lock: %d versus %d", a, b)
		}
	}
	real, synthetic := request("192.0.3.77:443", "owner"), request("192.0.3.77:443", "unlisted")
	if real.Code != 401 || synthetic.Code != real.Code || body(real) != body(synthetic) || real.Header().Get("Retry-After") != "" || synthetic.Header().Get("Retry-After") != "" {
		t.Fatalf("fresh-source response differs: known=%d %s unknown=%d %s", real.Code, body(real), synthetic.Code, body(synthetic))
	}
}
