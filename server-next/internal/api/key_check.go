package api

import (
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A key, personal token or app password is turned into its person and profile here, and only
// here, so every route, the assistant address and the compatible routes get the same rule:
// a key is refused whenever and wherever its person could not sign in. Using a key is not a
// sign-in: it adds no device and raises no "signed in" event.

// keyRate is how many requests one key may make in a minute before it is made to wait.
// Apps signed in as a person are never slowed this way.
const keyRate = 600

// keyCheck refuses a key whose person could not sign in now and from here, and answers the
// caller it acts as. scope is what the route needs; "" skips the scope check (an app
// password has no scopes; its tool gets what its person may do).
func (s *Server) keyCheck(r *http.Request, key db.KeyCaller, err error, scope Scope) (Caller, error) {
	limits := s.sourceLimit(r)
	switch {
	case errors.Is(err, db.ErrNotFound):
		// A wrong key counts as a wrong guess from its network, like a wrong password.
		if err := s.db.RecordFailure(r.Context(), s.now(), limits...); err != nil {
			return Caller{}, err
		}
		return Caller{}, ErrUnauthorized()
	case errors.Is(err, db.ErrAccountUnavailable):
		return Caller{}, refusal(http.StatusForbidden, "account_unavailable", "This key's account is turned off.", false)
	case err != nil:
		return Caller{}, err
	}
	// A network blocked for wrong guesses is blocked for keys too.
	if wait, err := s.db.LimitWait(r.Context(), s.now(), limits...); err != nil {
		return Caller{}, err
	} else if wait > 0 {
		return Caller{}, errTooManyAttempts(wait)
	}
	// The account's viewing hours and home-only limit are checked here too, like a sign-in;
	// they arrive with the viewing-hours and home-only work (#2316).
	switch {
	case scope == ScopeSession:
		return Caller{}, refusal(http.StatusForbidden, "session_required", "This needs a signed-in person, not a key.", false)
	case scope != "" && !slices.Contains(key.Scopes, string(scope)) && !slices.Contains(key.Scopes, string(ScopeAdmin)):
		return Caller{}, refusal(http.StatusForbidden, "insufficient_scope", "This key was not given the scope this needs.", false)
	}
	if wait := s.keyRequests.take(key.KeyID, s.now()); wait > 0 {
		return Caller{}, ErrTooMany("key_rate_limited", "This key is making too many requests. Wait a moment.", wait)
	}
	return Caller{UserID: key.UserID, ProfileID: key.ProfileID, Role: key.Role, KeyScopes: key.Scopes}, nil
}

// keyRequests counts each key's requests in memory: a bucket of keyRate that refills over a
// minute. A restart forgets the counts, which costs nothing.
type keyRequests struct {
	mu      sync.Mutex
	buckets map[int64]*bucket
}

type bucket struct {
	left float64
	at   time.Time
}

// take spends one request of the key's bucket, or says how long to wait for one.
func (k *keyRequests) take(keyID int64, now time.Time) time.Duration {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.buckets == nil {
		k.buckets = map[int64]*bucket{}
	}
	b := k.buckets[keyID]
	if b == nil {
		if len(k.buckets) > 10_000 {
			clear(k.buckets) // only keys that are in use come back
		}
		b = &bucket{left: keyRate, at: now}
		k.buckets[keyID] = b
	}
	perSecond := float64(keyRate) / 60
	b.left = min(keyRate, b.left+now.Sub(b.at).Seconds()*perSecond)
	b.at = now
	if b.left < 1 {
		return time.Duration((1 - b.left) / perSecond * float64(time.Second))
	}
	b.left--
	return 0
}

// keyHash is how a key's secret is looked up.
func keyHash(secret string) string { return auth.Hash(secret) }
