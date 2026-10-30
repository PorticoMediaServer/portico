package httpapi

import (
	"container/list"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Resolving the Authorization header costs a read transaction and four or five
// statements, and every request pays it — including the fifty artwork requests
// a poster grid makes and the heartbeat every playing session sends every ten
// seconds. The old server's answer was a bounded cache with a fifteen-second
// ceiling and a thousand-odd entries (audit §4.3), and this is that, with the
// same discipline and one improvement.
//
// The discipline, which matters more than the saving:
//
//   - A denial is never cached. A viewer who was refused must be asked about
//     again, every time, because the reason may have been transient and because
//     caching a refusal is the one error this cache could make that a viewer
//     would notice.
//   - Fifteen seconds, never longer. That is the window a revocation can be late
//     by if every other signal fails.
//   - The entry carries the principal's epoch, so a session whose epoch moved
//     cannot be served from it.
//   - The entry carries the authority generation (`dbwork.AuthorityGeneration`),
//     which moves whenever an authority-bearing transaction commits *or* any
//     statement writes a table that decides authority. An entry made before such
//     a write simply stops matching — there is no window between the write
//     committing and the cache being told, because there is nothing to tell.
//   - Security-fence and playback lanes revalidate strictly: they skip the cache
//     entirely. So does a long-lived transport before it parks, which is what
//     stops a durable revocation leaving an event stream authorised until the
//     TTL expires.

const (
	principalCacheTTL     = 15 * time.Second
	principalCacheEntries = 1024
)

type principalEntry struct {
	principal identity.Principal
	expires   time.Time
	authority uint64
}

type principalCache struct {
	mu      sync.Mutex
	order   *list.List
	entries map[string]*list.Element
	hits    uint64
	misses  uint64
}

type principalNode struct {
	key   string
	entry principalEntry
}

func newPrincipalCache() *principalCache {
	return &principalCache{order: list.New(), entries: map[string]*list.Element{}}
}

func (c *principalCache) lookup(key string, now time.Time) (identity.Principal, bool) {
	if c == nil {
		return identity.Principal{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		c.misses++
		return identity.Principal{}, false
	}
	node := element.Value.(*principalNode)
	if !now.Before(node.entry.expires) || node.entry.authority != dbwork.AuthorityGeneration() {
		c.order.Remove(element)
		delete(c.entries, key)
		c.misses++
		return identity.Principal{}, false
	}
	c.order.MoveToFront(element)
	c.hits++
	return node.entry.principal, true
}

// store takes the authority generation read BEFORE the check that produced p. Reading it here
// instead would stamp an answer computed before a revocation with the generation after it, and
// the revoked session would be served from cache until the entry expired.
func (c *principalCache) store(key string, p identity.Principal, now time.Time, generation uint64) {
	if c == nil {
		return
	}
	expires := now.Add(principalCacheTTL)
	if !p.ExpiresAt.IsZero() && p.ExpiresAt.Before(expires) {
		expires = p.ExpiresAt
	}
	entry := principalEntry{principal: p, expires: expires, authority: generation}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[key]; ok {
		element.Value.(*principalNode).entry = entry
		c.order.MoveToFront(element)
		return
	}
	c.entries[key] = c.order.PushFront(&principalNode{key: key, entry: entry})
	for c.order.Len() > principalCacheEntries {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*principalNode).key)
	}
}

// PrincipalCacheStats is what the cache is doing.
type PrincipalCacheStats struct {
	Entries  int    `json:"entries"`
	Capacity int    `json:"capacity"`
	TTLMs    int64  `json:"ttlMillis"`
	Hits     uint64 `json:"hits"`
	Misses   uint64 `json:"misses"`
}

func (c *principalCache) stats() PrincipalCacheStats {
	out := PrincipalCacheStats{Capacity: principalCacheEntries, TTLMs: principalCacheTTL.Milliseconds()}
	if c == nil {
		return out
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out.Entries = c.order.Len()
	out.Hits, out.Misses = c.hits, c.misses
	return out
}

// strictPrincipal reports whether this request must resolve its principal
// against live state rather than from the cache. Everything whose delay leaves a
// principal authorised longer than policy allows is here: the security-fence and
// auth lanes, and playback, where a session must not outlive its authority by
// even a few seconds.
func strictPrincipal(class dbwork.Class) bool {
	switch class {
	case dbwork.ClassSecurityFence, dbwork.ClassEstablishedPlayback, dbwork.ClassPlaybackStart, dbwork.ClassProtectedCapture:
		return true
	}
	return false
}

// --- library authority -----------------------------------------------------

// The other half of the per-request authorisation cost is "may this viewer see
// this library?", asked once per request on most routes and once per row on the
// listings. It depends on nothing but the principal and the library, so it
// caches on exactly the same terms as the principal itself: bounded, fifteen
// seconds, fenced on the authority epoch, denials never stored, and skipped
// entirely on the lanes that must resolve live.
//
// Storing only grants is what makes the fence sufficient. A grant that should
// have become a denial is invalidated by the epoch — every path that revokes
// membership, narrows a profile's libraries or bumps an account epoch writes one
// of the authority tables — and a denial that should have become a grant is
// never cached at all, so a viewer who has just been given access sees it
// immediately.
type accessCache struct {
	mu      sync.Mutex
	order   *list.List
	entries map[string]*list.Element
}

type accessNode struct {
	key       string
	expires   time.Time
	authority uint64
}

const accessCacheEntries = 4096

func newAccessCache() *accessCache {
	return &accessCache{order: list.New(), entries: map[string]*list.Element{}}
}

func accessKey(p identity.Principal, library string) string {
	return p.Hash + "\x00" + p.AccountID + "\x00" + p.ProfileID + "\x00" + p.Authority + "\x00" + p.Role + "\x00" + library
}

func (c *accessCache) allowed(key string, now time.Time) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return false
	}
	node := element.Value.(*accessNode)
	if !now.Before(node.expires) || node.authority != dbwork.AuthorityGeneration() {
		c.order.Remove(element)
		delete(c.entries, key)
		return false
	}
	c.order.MoveToFront(element)
	return true
}

// grant takes the generation read before the policy check (see principalCache.store).
func (c *accessCache) grant(key string, now time.Time, generation uint64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[key]; ok {
		node := element.Value.(*accessNode)
		node.expires, node.authority = now.Add(principalCacheTTL), generation
		c.order.MoveToFront(element)
		return
	}
	c.entries[key] = c.order.PushFront(&accessNode{key: key, expires: now.Add(principalCacheTTL), authority: generation})
	for c.order.Len() > accessCacheEntries {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*accessNode).key)
	}
}

func (c *accessCache) size() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

// --- viewer restrictions ---------------------------------------------------

// A viewer's content restriction is read once per surface that can show a
// title, and a composite handler has several: the detail page reads it three
// times, home twice, search twice. It is one row keyed by profile, it changes
// only through a restriction publication — a security-fence write — and it is
// already folded into every viewer fence, so it caches on exactly the terms the
// authority caches do.
//
// It is cached whatever the answer, unlike the authority caches, because there
// is no such thing as a restriction denial: the value *is* the answer, and the
// default (no restriction) is as much a fact as any other.
type restrictionCache struct {
	mu      sync.Mutex
	order   *list.List
	entries map[string]*list.Element
}

type restrictionNode struct {
	key        string
	value      identity.ContentRestrictions
	expires    time.Time
	generation uint64
}

const restrictionCacheEntries = 1024

func newRestrictionCache() *restrictionCache {
	return &restrictionCache{order: list.New(), entries: map[string]*list.Element{}}
}

func (c *restrictionCache) lookup(profile string, now time.Time) (identity.ContentRestrictions, bool) {
	if c == nil || profile == "" {
		return identity.ContentRestrictions{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[profile]
	if !ok {
		return identity.ContentRestrictions{}, false
	}
	node := element.Value.(*restrictionNode)
	if !now.Before(node.expires) || node.generation != dbwork.AuthorityGeneration() {
		c.order.Remove(element)
		delete(c.entries, profile)
		return identity.ContentRestrictions{}, false
	}
	c.order.MoveToFront(element)
	return node.value, true
}

// store takes the generation read before the restrictions were read (see principalCache.store).
func (c *restrictionCache) store(profile string, value identity.ContentRestrictions, now time.Time, generation uint64) {
	if c == nil || profile == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[profile]; ok {
		node := element.Value.(*restrictionNode)
		node.value, node.expires, node.generation = value, now.Add(principalCacheTTL), generation
		c.order.MoveToFront(element)
		return
	}
	c.entries[profile] = c.order.PushFront(&restrictionNode{key: profile, value: value, expires: now.Add(principalCacheTTL), generation: generation})
	for c.order.Len() > restrictionCacheEntries {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*restrictionNode).key)
	}
}
