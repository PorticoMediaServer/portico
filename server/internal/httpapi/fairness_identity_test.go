package httpapi

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestInventedCredentialsCannotMintFairnessShares(t *testing.T) {
	a := newAdmission()
	r := httptest.NewRequest("GET", "/v1/home", nil)
	r.RemoteAddr = "198.51.100.42:12000"
	peer := a.clientKey(r)
	for i := 0; i < 1000; i++ {
		r.Header.Set("Authorization", "Bearer "+fmt.Sprintf("invented-credential-%043d", i))
		if a.clientKey(r) != peer {
			t.Fatal("invented token minted a client share")
		}
	}
	r.Header.Del("Authorization")
	r.Header.Set("Cookie", "portico_session=invented-cookie")
	if a.clientKey(r) != peer {
		t.Fatal("unverified cookie minted a client share")
	}
	r.Header.Del("Cookie")
	r.URL.Path = "/v1/media/invented-grant/original"
	if a.clientKey(r) != peer {
		t.Fatal("unverified grant minted a client share")
	}
	if a.clients.order.Len() != 0 || len(a.clients.entries) != 0 {
		t.Fatal("unverified credentials grew the registry")
	}
	a.rememberCredential("verified-grant", identity.Principal{})
	r.URL.Path = "/v1/media/verified-grant/original"
	if a.clientKey(r) == peer {
		t.Fatal("verified media grant did not get its share")
	}
	r.Header.Set("Authorization", "Bearer invented-header")
	if a.clientKey(r) != peer {
		t.Fatal("valid grant allowed an unrelated header to mint a share")
	}
}

func TestVerifiedDevicesBehindOneNATHaveIndependentShares(t *testing.T) {
	a := newAdmission()
	viewer := identity.Principal{Viewer: identity.Viewer{ServerID: "server", Authority: "local", AccountID: "household"}}
	one := httptest.NewRequest("GET", "/v1/home", nil)
	two := httptest.NewRequest("GET", "/v1/home", nil)
	one.RemoteAddr, two.RemoteAddr = "198.51.100.42:12000", "198.51.100.42:12001"
	one.Header.Set("Authorization", "Bearer phone")
	two.Header.Set("Authorization", "Bearer television")
	if a.clientKey(one) != a.clientKey(two) {
		t.Fatal("unknown credentials bypassed the shared peer bucket")
	}
	viewer.Hash = identity.Digest("phone")
	a.rememberCredential("phone", viewer)
	viewer.Hash = identity.Digest("television")
	a.rememberCredential("television", viewer)
	if a.clientKey(one) == a.clientKey(two) {
		t.Fatal("verified household devices shared a fairness key")
	}
	if strings.Contains(a.clientKey(one), "phone") || strings.Contains(a.clientKey(one), "household") {
		t.Fatal("accounting key disclosed identity or credential")
	}
}

func TestVerifiedTokenAndItsMediaGrantsShareOneFairnessIdentity(t *testing.T) {
	c := newFairnessIdentities()
	now := time.Now()
	p := identity.Principal{Viewer: identity.Viewer{ServerID: "server", Authority: "local", AccountID: "account"}, Hash: identity.Digest("token")}
	c.remember("token", p, now)
	want, _ := c.lookup("token", now)
	for i := 0; i < 100; i++ {
		grant := fmt.Sprintf("grant-%d", i)
		c.remember(grant, p, now)
		if key, known := c.lookup(grant, now); !known || key != want {
			t.Fatal("verified media grant minted another share for its token")
		}
	}
	p.AccountID = "another-account"
	c.remember("another-grant", p, now)
	if key, _ := c.lookup("another-grant", now); key == want {
		t.Fatal("different accounts collided on a verified authorization hash")
	}
}

func TestFairnessIdentityRegistryIsBoundedAndExpires(t *testing.T) {
	c := newFairnessIdentities()
	now := time.Now()
	p := identity.Principal{}
	for i := 0; i < fairnessIdentityCapacity; i++ {
		c.remember(fmt.Sprintf("secret-%d", i), p, now)
	}
	if _, ok := c.lookup("secret-0", now.Add(time.Minute)); !ok {
		t.Fatal("known credential was missing")
	}
	c.remember("new-secret", p, now)
	if _, ok := c.lookup("secret-1", now); ok {
		t.Fatal("least recently used credential survived eviction")
	}
	if _, ok := c.lookup("secret-0", now); !ok {
		t.Fatal("recently used credential was evicted")
	}
	if len(c.entries) != fairnessIdentityCapacity || c.order.Len() != fairnessIdentityCapacity {
		t.Fatal("credential registry exceeded its bound")
	}
	if _, ok := c.lookup("secret-0", now.Add(fairnessIdentityTTL)); ok {
		t.Fatal("lookup extended accounting beyond its verified TTL")
	}
	if len(c.entries) != fairnessIdentityCapacity-1 {
		t.Fatal("expired credential was not removed")
	}
}

func TestRememberedFairnessCredentialCannotBypassRevocation(t *testing.T) {
	f := newTL6V1Fixture(t, 1)
	phone, television := f.device("fairness-phone"), f.device("fairness-television")
	for _, token := range []string{phone.AccessToken, television.AccessToken} {
		if w := f.raw("GET", "/v1/items?limit=1", token, nil, nil); w.Code != 200 {
			t.Fatalf("new device behind shared peer: %d %s", w.Code, w.Body.String())
		}
	}
	if _, err := f.db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=(SELECT family_id FROM authorization_family_tokens WHERE token_hash=?)`, identity.Digest(phone.AccessToken)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if w := f.raw("GET", "/v1/items?limit=1", phone.AccessToken, nil, nil); w.Code != 401 {
			t.Fatalf("remembered revoked credential bypassed authentication: %d", w.Code)
		}
	}
	if w := f.raw("GET", "/v1/items?limit=1", television.AccessToken, nil, nil); w.Code != 200 {
		t.Fatalf("revocation affected another device: %d", w.Code)
	}
	if w := f.raw("GET", "/v1/items?limit=1", strings.Repeat("x", 64), nil, nil); w.Code != 401 {
		t.Fatalf("invented credential passed authentication: %d", w.Code)
	}
}
