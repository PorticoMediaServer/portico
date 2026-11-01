package catalog

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

const (
	homeScanFreshness    = 3 * time.Second
	homeScanStaleLimit   = 2 * homeScanFreshness
	homeScanCacheBytes   = 32 << 20
	homeScanCacheEntries = 4096
	homeScanValueBytes   = 1 << 20
)

// Home may lag a scan's additions by a few seconds. Store immutable wire
// values, not request callbacks, contexts, authorization decisions or offers.
// Decoding gives each delivery its own projection to decorate.
type homeScanCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	order   list.List
	bytes   int
	flights map[string]*homeScanFlight
}

type homeScanEntry struct {
	key   string
	data  []byte
	at    time.Time
	bytes int
}

type homeScanFlight struct {
	done chan struct{}
	data []byte
	err  error
}

// Coalesce cold callers; while refreshing an expired value, peers may keep
// serving it within the separate stale bound. The caller doing the refresh
// owns the work, so there is no background goroutine to outlive its request.
func (c *homeScanCache) load(ctx context.Context, key string, now time.Time, build func() ([]byte, error)) ([]byte, bool, error) {
	c.mu.Lock()
	var old *homeScanEntry
	if element := c.entries[key]; element != nil {
		old = element.Value.(*homeScanEntry)
		c.order.MoveToFront(element)
		if age := now.Sub(old.at); age >= 0 && age < homeScanFreshness {
			c.mu.Unlock()
			return old.data, true, nil
		}
	}
	if flight := c.flights[key]; flight != nil {
		if old != nil {
			if age := now.Sub(old.at); age >= 0 && age < homeScanStaleLimit {
				c.mu.Unlock()
				return old.data, true, nil
			}
		}
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-flight.done:
			return flight.data, true, flight.err
		}
	}
	if c.flights == nil {
		c.flights = map[string]*homeScanFlight{}
	}
	flight := &homeScanFlight{done: make(chan struct{})}
	c.flights[key] = flight
	c.mu.Unlock()
	settled := false
	defer func() {
		if !settled {
			// A request panic must not strand other callers on its flight.
			c.mu.Lock()
			delete(c.flights, key)
			flight.err = errors.New("Home composition interrupted")
			close(flight.done)
			c.mu.Unlock()
		}
	}()
	data, err := build()
	c.mu.Lock()
	delete(c.flights, key)
	if element := c.entries[key]; element != nil {
		c.bytes -= element.Value.(*homeScanEntry).bytes
		c.order.Remove(element)
		delete(c.entries, key)
	}
	if err == nil && len(data) <= homeScanValueBytes {
		if c.entries == nil {
			c.entries = map[string]*list.Element{}
		}
		entry := &homeScanEntry{key: key, data: data, at: now, bytes: len(key) + len(data) + 256}
		c.entries[key] = c.order.PushFront(entry)
		c.bytes += entry.bytes
		for c.bytes > homeScanCacheBytes || len(c.entries) > homeScanCacheEntries {
			last := c.order.Back()
			entry := last.Value.(*homeScanEntry)
			c.bytes -= entry.bytes
			delete(c.entries, entry.key)
			c.order.Remove(last)
		}
	}
	flight.data, flight.err = data, err
	close(flight.done)
	settled = true
	c.mu.Unlock()
	return data, false, err
}

// A scan's ordinary library revision deliberately does not enter this key.
// Policy/classification, viewer and saved-resource epochs still do: stale
// additions are acceptable; a different authority or policy is a new build.
func (s *Service) homeScanKey(r HomeRequest, surface string, page HomeRowPage) (string, error) {
	var active bool
	var policy, classification, viewer, resources, generation int64
	var retaining bool
	err := s.read().QueryRow(`SELECT
 EXISTS(SELECT 1 FROM json_each(?) requested CROSS JOIN jobs j INDEXED BY jobs_library_status_directory
  ON j.library_id=requested.value WHERE j.status='running' LIMIT 1),
 (SELECT policy_epoch FROM catalog_facet_source_state WHERE singleton=1),
 (SELECT revision FROM catalog_summary_classification_revision WHERE singleton=1),
 COALESCE((SELECT sum(revision) FROM viewer_revisions WHERE profile_id=? AND library_id IN(SELECT value FROM json_each(?))),0),
 (SELECT revision FROM saved_resource_generation WHERE id=1),
 generation,retaining FROM catalog_generations WHERE singleton=1`, idsJSON(r.Libraries), r.Profile, idsJSON(r.Libraries)).Scan(&active, &policy, &classification, &viewer, &resources, &generation, &retaining)
	if err != nil || !active {
		return "", err
	}
	raw, err := json.Marshal([]any{surface, r.ServerID, r.Profile, r.ViewerFence, r.PrivacyFence,
		r.Libraries, r.Restrictions, r.RowOrder, r.HiddenRowIDs, r.LayoutRevision,
		r.CommunityActivity, r.LiveAvailable, r.ReadingPersonalAuthorize != nil,
		r.Limit, r.Rows, page.Limit, page.Group, page.SourceID, r.Now.UTC().Format("2006-01-02"),
		policy, classification, viewer, resources, generation, retaining})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (s *Service) homeScanNow() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}
