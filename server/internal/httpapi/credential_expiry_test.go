package httpapi

import (
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestAuthenticationCachesStopAtCredentialExpiry(t *testing.T) {
	now := time.Now()
	for _, lifetime := range []time.Duration{time.Second, principalCacheTTL * 2} {
		t.Run(lifetime.String(), func(t *testing.T) {
			expires := now.Add(lifetime)
			deadline := now.Add(principalCacheTTL)
			if expires.Before(deadline) {
				deadline = expires
			}
			principals := newPrincipalCache()
			principals.store("session", identity.Principal{ExpiresAt: expires}, now, dbwork.AuthorityGeneration())
			keys := newCredentialGate()
			keys.remember("key", access.ResolvedCredential{ID: "key", ExpiresAt: expires}, now, dbwork.AuthorityGeneration())
			if _, ok := principals.lookup("session", deadline.Add(-time.Nanosecond)); !ok {
				t.Fatal("live session was not cached")
			}
			if _, ok := keys.lookup("key", deadline.Add(-time.Nanosecond)); !ok {
				t.Fatal("live key was not cached")
			}
			if _, ok := principals.lookup("session", deadline); ok {
				t.Fatal("session accepted at its deadline")
			}
			if _, ok := keys.lookup("key", deadline); ok {
				t.Fatal("key accepted at its deadline")
			}
		})
	}
}

func TestAuthenticationCacheUsesVerifiedSessionDeadline(t *testing.T) {
	f := administrationFixtureNew(t)
	expires := time.Now().UTC().Add(5 * time.Second).Truncate(time.Second)
	if _, err := f.deps.DB.Exec(`UPDATE authorization_family_tokens SET expires_at=? WHERE token_hash=?`, expires.Format(time.RFC3339), identity.Digest(f.member)); err != nil {
		t.Fatal(err)
	}
	p, err := f.deps.Identity.Authenticate(f.member)
	if err != nil {
		t.Fatal(err)
	}
	if !p.ExpiresAt.Equal(expires) {
		t.Fatalf("verified expiry: %s, want %s", p.ExpiresAt, expires)
	}
	cache := newPrincipalCache()
	cache.store(p.Hash, p, time.Now(), dbwork.AuthorityGeneration())
	if _, ok := cache.lookup(p.Hash, expires); ok {
		t.Fatal("verified session was cached beyond its token expiry")
	}
}
