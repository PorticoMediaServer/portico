package hosted

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
)

// A15: an override or configured root ID must name the pinned key.
func TestRootIDMustNameThePinnedKey(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pin := base64.RawURLEncoding.EncodeToString(pub)
	if _, _, _, err = DefaultConfig("https://hosted.example", pin, "pin1"); err == nil {
		t.Fatal("override accepted a root ID for another key")
	}
	if _, _, id, err := DefaultConfig("https://hosted.example", pin, trust.KeyID(pub)); err != nil || id != trust.KeyID(pub) {
		t.Fatal("matching override refused", err)
	}
	if _, err = New(nil, nil, "https://hosted.example", pin, "pin1"); err == nil {
		t.Fatal("service accepted a root ID for another key")
	}
	if _, err = New(nil, nil, "https://hosted.example", pin, trust.KeyID(pub)); err != nil {
		t.Fatal(err)
	}
}
