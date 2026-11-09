package read

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math"
	"sync"
	"time"
)

var errUOEventResync = errors.New("event resume requires resync")

// errUOEventWakeCapacity is the named refusal for a hub map that would exceed
// its bounded number of admitted audience entries.
var errUOEventWakeCapacity = errors.New("event wake hub capacity exhausted")

const (
	// uoEventWakeMaxActive bounds concurrent streams admitted through one hub.
	// A parent may configure a smaller cap on the hub itself.
	uoEventWakeMaxActive = 8
	// uoEventWakeMaxAudiences bounds the hub map. One stream may hold up to
	// uoEventTransportAudiences entries, so the map stays bounded even when
	// every stream is addressed by many audiences.
	uoEventWakeMaxAudiences = 512
)

// These bindings are current read/disclosure facts, not bearer authority.
// Each reconnect/read/delivery still requires current authorization. Keys are
// process-local; restart/restore/audience drift intentionally requires resync.
type uoEventBinding struct {
	database         [16]byte
	viewer, audience [32]byte
}

const uoEventCursorLife = 24 * time.Hour
const uoEventCursorKeySeals = 1 << 24

type uoEventCursorCodec struct {
	mu                sync.Mutex
	current, previous cipher.AEAD
	born              time.Time
	seals             int
	now               func() time.Time
}

func newUOEventAEAD() (cipher.AEAD, error) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func newUOEventCursorCodec() (*uoEventCursorCodec, error) {
	aead, err := newUOEventAEAD()
	if err != nil {
		return nil, err
	}
	return &uoEventCursorCodec{current: aead, born: time.Now()}, nil
}
func (c *uoEventCursorCodec) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}
func uoEventBindingDigest(b uoEventBinding) [16]byte {
	h := sha256.New()
	h.Write([]byte("portico event binding v1"))
	h.Write(b.database[:])
	h.Write(b.viewer[:])
	h.Write(b.audience[:])
	var out [16]byte
	copy(out[:], h.Sum(nil))
	return out
}
func (c *uoEventCursorCodec) encode(binding uoEventBinding, after int64) (string, error) {
	if c == nil || after < 0 || binding.database == [16]byte{} || binding.viewer == [32]byte{} || binding.audience == [32]byte{} {
		return "", errUOEventResync
	}
	c.mu.Lock()
	now := c.clock()
	ms := now.UnixMilli()
	if ms <= 0 || ms > math.MaxInt64-uoEventCursorLife.Milliseconds() {
		c.mu.Unlock()
		return "", errUOEventResync
	}
	if c.current == nil || c.seals >= uoEventCursorKeySeals || now.Sub(c.born) >= uoEventCursorLife {
		aead, err := newUOEventAEAD()
		if err != nil {
			c.mu.Unlock()
			return "", err
		}
		c.previous, c.current, c.born, c.seals = c.current, aead, now, 0
	}
	c.seals++
	aead := c.current
	c.mu.Unlock()
	// Compact binding hash keeps the opaque public cursor below its128-byte cap.
	var p [33]byte
	p[0] = 1
	digest := uoEventBindingDigest(binding)
	copy(p[1:17], digest[:])
	binary.BigEndian.PutUint64(p[17:25], uint64(after))
	binary.BigEndian.PutUint64(p[25:], uint64(ms+uoEventCursorLife.Milliseconds()))
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return "e1." + base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, p[:], []byte("portico-event-resume-v1"))), nil
}
func (c *uoEventCursorCodec) decode(token string, binding uoEventBinding) (int64, error) {
	if c == nil || len(token) < 3 || len(token) > 128 || token[:3] != "e1." || binding.database == [16]byte{} || binding.viewer == [32]byte{} || binding.audience == [32]byte{} {
		return 0, errUOEventResync
	}
	sealed, err := base64.RawURLEncoding.Strict().DecodeString(token[3:])
	if err != nil {
		return 0, errUOEventResync
	}
	c.mu.Lock()
	current, previous, now := c.current, c.previous, c.clock().UnixMilli()
	c.mu.Unlock()
	digest := uoEventBindingDigest(binding)
	for _, aead := range []cipher.AEAD{current, previous} {
		if aead == nil || len(sealed) != aead.NonceSize()+33+aead.Overhead() {
			continue
		}
		n := aead.NonceSize()
		p, err := aead.Open(nil, sealed[:n], sealed[n:], []byte("portico-event-resume-v1"))
		if err != nil || len(p) != 33 || p[0] != 1 {
			continue
		}
		if subtle.ConstantTimeCompare(p[1:17], digest[:]) != 1 {
			return 0, errUOEventResync
		}
		position, expiry := binary.BigEndian.Uint64(p[17:25]), binary.BigEndian.Uint64(p[25:])
		if position > math.MaxInt64 || expiry > math.MaxInt64 || int64(expiry) <= now || now <= 0 {
			return 0, errUOEventResync
		}
		return int64(position), nil
	}
	return 0, errUOEventResync
}

// One channel generation per admitted audience, not per subscriber. Publishing
// closes ONE channel and performs no subscriber enumeration. A subscription's
// external GV memory grant remains charged through its release. No DB snapshot
// or read connection belongs to this wait helper.
type uoEventWakeHub struct {
	mu           sync.Mutex
	audiences    map[string]*uoEventWakeAudience
	active       int
	maxActive    int
	maxAudiences int
}
type uoEventWakeAudience struct {
	changed    chan struct{}
	generation uint64
	refs       int
}
type uoEventSubscription struct {
	hub      *uoEventWakeHub
	audience string
	entry    *uoEventWakeAudience
	released bool
}

// uoEventSubscriptionSet is one stream's subscriptions to every addressed
// audience key. Release is idempotent and drops each refcount once.
type uoEventSubscriptionSet struct {
	subs     []*uoEventSubscription
	released bool
}
type uoEventWakePosition struct {
	generation uint64
	changed    <-chan struct{}
}

func (h *uoEventWakeHub) maxActiveStreams() int {
	if h.maxActive > 0 {
		return h.maxActive
	}
	return uoEventWakeMaxActive
}
func (h *uoEventWakeHub) maxAudienceEntries() int {
	if h.maxAudiences > 0 {
		return h.maxAudiences
	}
	return uoEventWakeMaxAudiences
}

// acquireStream enforces the configured concurrent-stream cap with a named
// refusal before any subscription, map entry or SQL work exists.
func (h *uoEventWakeHub) acquireStream() error {
	if h == nil {
		return ErrInput
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.active >= h.maxActiveStreams() {
		return errUOEventTransportAtCapacity
	}
	h.active++
	return nil
}
func (h *uoEventWakeHub) releaseStream() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.active > 0 {
		h.active--
	}
}

func (h *uoEventWakeHub) subscribe(audience string) (*uoEventSubscription, error) {
	set, err := h.subscribeAll([]string{audience})
	if err != nil {
		return nil, err
	}
	return set.subs[0], nil
}

// subscribeAll admits one refcount per audience under a single lock, so a
// failure leaves no partial subscription behind. A key that would grow the map
// past the hub bound is a named refusal, never an unbounded entry.
func (h *uoEventWakeHub) subscribeAll(audiences []string) (*uoEventSubscriptionSet, error) {
	if h == nil || len(audiences) == 0 {
		return nil, ErrInput
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.audiences == nil {
		h.audiences = make(map[string]*uoEventWakeAudience)
	}
	fresh := 0
	for _, audience := range audiences {
		if audience == "" || len(audience) > 512 {
			return nil, ErrInput
		}
		if h.audiences[audience] == nil {
			fresh++
		}
	}
	if len(h.audiences)+fresh > h.maxAudienceEntries() {
		return nil, errUOEventWakeCapacity
	}
	set := &uoEventSubscriptionSet{subs: make([]*uoEventSubscription, 0, len(audiences))}
	for _, audience := range audiences {
		e := h.audiences[audience]
		if e == nil {
			e = &uoEventWakeAudience{changed: make(chan struct{})}
			h.audiences[audience] = e
		}
		e.refs++
		set.subs = append(set.subs, &uoEventSubscription{hub: h, audience: audience, entry: e})
	}
	return set, nil
}
func (s *uoEventSubscriptionSet) observe() ([]uoEventWakePosition, error) {
	if s == nil {
		return nil, ErrInput
	}
	out := make([]uoEventWakePosition, len(s.subs))
	for i, sub := range s.subs {
		position, err := sub.observe()
		if err != nil {
			return nil, err
		}
		out[i] = position
	}
	return out, nil
}
func (s *uoEventSubscriptionSet) release() {
	if s == nil || s.released {
		return
	}
	s.released = true
	for _, sub := range s.subs {
		sub.release()
	}
}
func (s *uoEventSubscription) observe() (uoEventWakePosition, error) {
	if s == nil || s.hub == nil {
		return uoEventWakePosition{}, ErrInput
	}
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	if s.released {
		return uoEventWakePosition{}, ErrInput
	}
	return uoEventWakePosition{s.entry.generation, s.entry.changed}, nil
}
func (h *uoEventWakeHub) publish(audience string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if e := h.audiences[audience]; e != nil {
		e.generation++
		close(e.changed)
		e.changed = make(chan struct{})
	}
}

// Observe BEFORE the durable read. A publish between read completion and wait
// closes the captured channel, while this same-lock generation test also avoids
// parking. The caller's existing heartbeat may trigger a durable safety recheck.
func (s *uoEventSubscription) wait(ctx context.Context, before uoEventWakePosition) error {
	if ctx == nil {
		return ErrInput
	}
	now, err := s.observe()
	if err != nil {
		return err
	}
	if now.generation != before.generation || now.changed != before.changed {
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-before.changed:
		return ctx.Err()
	}
}
func (s *uoEventSubscription) release() {
	if s == nil || s.hub == nil {
		return
	}
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	if s.released {
		return
	}
	s.released = true
	s.entry.refs--
	if s.entry.refs == 0 {
		close(s.entry.changed)
		delete(s.hub.audiences, s.audience)
	}
}

// Gaps in row IDs are legal. The sole WC append/oldest-delete owner retains
// the newest row; an empty-ring restore changes the database binding. A cursor
// older than the retained floor must resync, never claim a complete empty page.
func uoEventResumeWindow(after, oldestRetained, highWater int64) error {
	if after < 0 || oldestRetained < 0 || highWater < 0 || after > highWater || oldestRetained > highWater {
		return errUOEventResync
	}
	if oldestRetained == 0 {
		if after < highWater {
			return errUOEventResync
		}
		return nil
	}
	if after < oldestRetained-1 {
		return errUOEventResync
	}
	return nil
}
