package trust

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestSharedCertificateVector(t *testing.T) {
	raw, err := os.ReadFile("testdata/shared_vector.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		RootPublic string   `json:"rootPublic"`
		Envelope   Envelope `json:"envelope"`
	}
	if err = json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	root, err := base64.RawURLEncoding.Strict().DecodeString(v.RootPublic)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := v.Envelope.Verify(root, "documents", time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || string(payload) != `{"kind":"portico.trust.test-vector","value":1}` {
		t.Fatal(string(payload), err)
	}
	v.Envelope.Signature = "broken"
	if _, err = v.Envelope.Verify(root, "documents", time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("tampered document accepted")
	}
}
