package httpapi

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

const fairnessIdentityCapacity = 4096
const fairnessIdentityTTL = 5 * time.Minute

type fairnessIdentity struct {
	digest, key string
	expires     time.Time
}
type fairnessIdentities struct {
	mu      sync.Mutex
	order   *list.List
	entries map[string]*list.Element
}

func newFairnessIdentities() *fairnessIdentities {
	return &fairnessIdentities{order: list.New(), entries: make(map[string]*list.Element)}
}

// This registry assigns load to a previously checked credential. It is never
// consulted by authentication, and stores neither secrets nor authorization.
// A revoked credential may retain an accounting key until expiry; the actual
// handler still applies exactly the same live authority checks as before.
func (c *fairnessIdentities) remember(secret string, p identity.Principal, now time.Time) {
	if c == nil || secret == "" {
		return
	}
	digest := identity.Digest(secret)
	// Verified devices retain one share through access-token rotation, profile
	// selection and media-grant creation. Legacy/synthetic principals without
	// this metadata retain the issuing-token fallback; neither key authorizes.
	authorization := "d:" + p.DeviceID
	if p.DeviceID == "" {
		authorization = "t:" + p.Hash
		if p.Hash == "" {
			authorization = "g:" + digest
		}
	}
	encoded, _ := json.Marshal([]string{p.ServerID, p.Authority, p.AccountID, authorization})
	sum := sha256.Sum256(encoded)
	value := fairnessIdentity{digest: digest, key: "c:" + hex.EncodeToString(sum[:]), expires: now.Add(fairnessIdentityTTL)}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[digest]; ok {
		element.Value = value
		c.order.MoveToFront(element)
		return
	}
	c.entries[digest] = c.order.PushFront(value)
	for c.order.Len() > fairnessIdentityCapacity {
		old := c.order.Back()
		delete(c.entries, old.Value.(fairnessIdentity).digest)
		c.order.Remove(old)
	}
}

func (c *fairnessIdentities) lookup(secret string, now time.Time) (string, bool) {
	if c == nil || secret == "" {
		return "", false
	}
	digest := identity.Digest(secret)
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[digest]
	if !ok {
		return "", false
	}
	value := element.Value.(fairnessIdentity)
	if !now.Before(value.expires) {
		delete(c.entries, digest)
		c.order.Remove(element)
		return "", false
	}
	c.order.MoveToFront(element)
	return value.key, true
}

func (a *admission) rememberCredential(secret string, p identity.Principal) {
	if a != nil {
		a.clients.remember(secret, p, time.Now())
	}
}

func (a *admission) clientKey(r *http.Request) string {
	if key, known := a.clients.lookup(presentedCredential(r), time.Now()); known {
		return key
	}
	// Inventing another token, cookie or grant is free. Until a handler checks
	// it, it must stay in the same peer share rather than minting a new client.
	return fairnessKey(r, a.trustedProxies)
}
