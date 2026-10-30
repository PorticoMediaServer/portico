package catalog

import (
	"container/list"
	"sync"
	"time"
)

// Two caches in this package were the shape the old server had already learned
// not to use, and both cost more the busier the server got.
//
// The count cache was a 128-entry slice scanned linearly under a mutex on every
// home row. At seventeen rows and two hundred viewers that is a hot lock held
// for a scan, to avoid a query that takes a millisecond.
//
// The facet cache had no single-flight and evicted by throwing itself away: at
// 512 entries it emptied, so a busy server with more than 512 live keys kept
// discarding the work it had just done, and a cold key was computed once per
// concurrent viewer rather than once. The old server's rule (audit principle 21)
// is the one applied here: every cache is bounded in entries and in time, and is
// paired with a single-flight map.

// countKey identifies an exact count for one library, profile, revision and
// mode. It is a comparable struct, so it is a map key.
type countKey struct {
	library, profile string
	revision         ContentRevision
	mode             string
}

func (s *Service) cachedCount(key countKey) (int, bool) {
	s.state.countMu.Lock()
	defer s.state.countMu.Unlock()
	value, ok := s.state.countCache[key.string()]
	return value, ok
}

func (s *Service) storeCount(key countKey, count int) {
	s.state.countMu.Lock()
	defer s.state.countMu.Unlock()
	if s.state.countCache == nil {
		s.state.countCache = map[string]int{}
	}
	// The cap is a memory bound, not a hit-rate strategy: the key carries the
	// revision, so a stale entry can never be served and clearing is safe.
	if len(s.state.countCache) >= countCacheEntries {
		s.state.countCache = map[string]int{}
	}
	s.state.countCache[key.string()] = count
}

const countCacheEntries = 512

func (k countKey) string() string {
	return k.library + "\x00" + k.profile + "\x00" + k.mode + "\x00" + itoa(k.revision.Catalog) + "\x00" + itoa(k.revision.Viewer)
}

// --- bounded LRU with single-flight ----------------------------------------

type facetCacheEntry struct {
	expires  time.Time
	page     FacetPage
	revision int64
	// publications records the write-gate publication count the entry was built
	// at. It is carried for diagnostics only: it is tempting to use it to skip
	// the revision check when nothing has been committed since, and that would be
	// wrong, because the counter sees gated transactions and a direct `db.Exec`
	// is not one. The fence stays the exact per-library revision.
	publications uint64
	// cost is the entry's approximate retained bytes, counted against the
	// cache's byte budget.
	cost int
}

// facetCache is a bounded LRU paired with a single-flight map. The LRU evicts
// the least recently used entry rather than the whole table, and the flight map
// means N concurrent viewers who all want the same cold facet produce one
// computation and N readers of its result.
type facetCache struct {
	mu       sync.Mutex
	capacity int
	order    *list.List
	entries  map[string]*list.Element
	flights  map[string]*flight
	// used is the cost of the retained entries; puts evict the least recently
	// used entries until it is within facetCacheBytes.
	used int
}

type facetNode struct {
	key   string
	entry facetCacheEntry
}

type flight struct {
	done chan struct{}
	page FacetPage
	err  error
}

const facetCacheEntries = 512

// facetCacheBytes bounds what whole value lists may retain in memory. A
// library-wide actor list can hold hundreds of thousands of names.
var facetCacheBytes = 64 << 20

// facetPageCost approximates a page's retained bytes.
func facetPageCost(page FacetPage) int {
	cost := 128
	for _, value := range page.Values {
		cost += 64 + len(value.Value) + len(value.Label)
	}
	return cost
}

func newFacetCache(capacity int) *facetCache {
	return &facetCache{capacity: capacity, order: list.New(), entries: map[string]*list.Element{}, flights: map[string]*flight{}}
}

// get returns a live entry and promotes it. An entry whose revision has moved or
// whose TTL has passed is not live and is dropped.
func (c *facetCache) get(key string, revision int64) (FacetPage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return FacetPage{}, false
	}
	node := element.Value.(*facetNode)
	if node.entry.revision != revision || !time.Now().Before(node.entry.expires) {
		c.order.Remove(element)
		delete(c.entries, key)
		c.used -= node.entry.cost
		return FacetPage{}, false
	}
	c.order.MoveToFront(element)
	return node.entry.page, true
}

func (c *facetCache) put(key string, entry facetCacheEntry) {
	entry.cost = facetPageCost(entry.page)
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry.cost > facetCacheBytes/4 {
		// One list may not crowd out every other entry; it is recomputed.
		if element, ok := c.entries[key]; ok {
			c.order.Remove(element)
			delete(c.entries, key)
			c.used -= element.Value.(*facetNode).entry.cost
		}
		return
	}
	if element, ok := c.entries[key]; ok {
		node := element.Value.(*facetNode)
		c.used += entry.cost - node.entry.cost
		node.entry = entry
		c.order.MoveToFront(element)
	} else {
		c.entries[key] = c.order.PushFront(&facetNode{key: key, entry: entry})
		c.used += entry.cost
	}
	for c.order.Len() > c.capacity || c.used > facetCacheBytes {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		node := oldest.Value.(*facetNode)
		delete(c.entries, node.key)
		c.used -= node.entry.cost
	}
}

// do runs compute for key unless another caller is already running it, in which
// case it waits for that one and shares its result. The leader's error is shared
// too: a stampede of two hundred viewers must not become two hundred failures
// against a database that is already struggling.
func (c *facetCache) do(key string, compute func() (FacetPage, error)) (FacetPage, error) {
	c.mu.Lock()
	if existing, ok := c.flights[key]; ok {
		c.mu.Unlock()
		<-existing.done
		return existing.page, existing.err
	}
	current := &flight{done: make(chan struct{})}
	c.flights[key] = current
	c.mu.Unlock()

	current.page, current.err = compute()

	c.mu.Lock()
	delete(c.flights, key)
	c.mu.Unlock()
	close(current.done)
	return current.page, current.err
}

// --- ordered results of lists across libraries ----------------------------

// acrossOrdered is one list across libraries in its order, up to one row past
// the counted maximum: ids, each row's continuation key and title letter.
type acrossOrdered struct {
	ids     []int64
	keys    []string
	letters []string
	// capped says the list holds only its first acrossMaximum+1 rows.
	capped bool
}

func (o *acrossOrdered) cost() int {
	cost := 128
	for i := range o.ids {
		cost += 48 + len(o.keys[i]) + len(o.letters[i])
	}
	return cost
}

// acrossOrderCache keeps recent ordered lists by their complete revision, so
// a group's count, letter rail and pages share one expansion of its members.
// It is bounded in entries, bytes and time.
type acrossOrderCache struct {
	mu      sync.Mutex
	order   *list.List
	entries map[string]*list.Element
	used    int
}

type acrossOrderNode struct {
	key     string
	list    *acrossOrdered
	expires time.Time
	cost    int
}

const acrossOrderEntries = 64

var acrossOrderBytes = 16 << 20

func newAcrossOrderCache() *acrossOrderCache {
	return &acrossOrderCache{order: list.New(), entries: map[string]*list.Element{}}
}

func (c *acrossOrderCache) get(key string) (*acrossOrdered, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	node := element.Value.(*acrossOrderNode)
	if !time.Now().Before(node.expires) {
		c.order.Remove(element)
		delete(c.entries, key)
		c.used -= node.cost
		return nil, false
	}
	c.order.MoveToFront(element)
	return node.list, true
}

func (c *acrossOrderCache) put(key string, ordered *acrossOrdered, ttl time.Duration) {
	node := &acrossOrderNode{key: key, list: ordered, expires: time.Now().Add(ttl), cost: ordered.cost()}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[key]; ok {
		c.used -= element.Value.(*acrossOrderNode).cost
		c.order.Remove(element)
		delete(c.entries, key)
	}
	if node.cost > acrossOrderBytes/4 {
		return
	}
	c.entries[key] = c.order.PushFront(node)
	c.used += node.cost
	for c.order.Len() > acrossOrderEntries || c.used > acrossOrderBytes {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		evicted := oldest.Value.(*acrossOrderNode)
		delete(c.entries, evicted.key)
		c.used -= evicted.cost
	}
}
