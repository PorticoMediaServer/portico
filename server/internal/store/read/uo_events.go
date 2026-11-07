package read

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"sync"
)

var errUOEventResync = errors.New("event resume requires resync")

// These bindings are current read/disclosure facts, not bearer authority.
// Each reconnect/read/delivery still requires current authorization. Keys are
// process-local; restart/restore/audience drift intentionally requires resync.
type uoEventBinding struct {
	database         [16]byte
	viewer, audience [32]byte
}
type uoEventCursorCodec struct{ aead cipher.AEAD }

func newUOEventCursorCodec() (*uoEventCursorCodec, error) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &uoEventCursorCodec{aead: aead}, nil
}
func (c *uoEventCursorCodec) encode(binding uoEventBinding, after int64) (string, error) {
	if c == nil || c.aead == nil || after < 0 || binding.database == [16]byte{} || binding.viewer == [32]byte{} || binding.audience == [32]byte{} {
		return "", errUOEventResync
	}
	var payload [89]byte
	payload[0] = 1
	copy(payload[1:17], binding.database[:])
	copy(payload[17:49], binding.viewer[:])
	copy(payload[49:81], binding.audience[:])
	binary.BigEndian.PutUint64(payload[81:], uint64(after))
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, payload[:], []byte("portico-event-resume-v1"))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}
func (c *uoEventCursorCodec) decode(token string, binding uoEventBinding) (int64, error) {
	if c == nil || c.aead == nil || token == "" || len(token) > 256 {
		return 0, errUOEventResync
	}
	sealed, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(sealed) != c.aead.NonceSize()+89+c.aead.Overhead() {
		return 0, errUOEventResync
	}
	n := c.aead.NonceSize()
	payload, err := c.aead.Open(nil, sealed[:n], sealed[n:], []byte("portico-event-resume-v1"))
	if err != nil || len(payload) != 89 || payload[0] != 1 {
		return 0, errUOEventResync
	}
	if subtle.ConstantTimeCompare(payload[1:17], binding.database[:]) != 1 || subtle.ConstantTimeCompare(payload[17:49], binding.viewer[:]) != 1 || subtle.ConstantTimeCompare(payload[49:81], binding.audience[:]) != 1 {
		return 0, errUOEventResync
	}
	position := binary.BigEndian.Uint64(payload[81:])
	if position > uint64(^uint64(0)>>1) {
		return 0, errUOEventResync
	}
	return int64(position), nil
}

// One channel generation per admitted audience, not per subscriber. Publishing
// closes ONE channel and performs no subscriber enumeration. A subscription's
// external GV memory grant remains charged through its release. No DB snapshot
// or read connection belongs to this wait helper.
type uoEventWakeHub struct {
	mu        sync.Mutex
	audiences map[string]*uoEventWakeAudience
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
type uoEventWakePosition struct {
	generation uint64
	changed    <-chan struct{}
}

func (h *uoEventWakeHub) subscribe(audience string) (*uoEventSubscription, error) {
	if h == nil || audience == "" || len(audience) > 512 {
		return nil, ErrInput
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.audiences == nil {
		h.audiences = make(map[string]*uoEventWakeAudience)
	}
	e := h.audiences[audience]
	if e == nil {
		e = &uoEventWakeAudience{changed: make(chan struct{})}
		h.audiences[audience] = e
	}
	e.refs++
	return &uoEventSubscription{hub: h, audience: audience, entry: e}, nil
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

// Retention identity uses the durable high-water even if the retained ring is
// empty. Gaps in row IDs are legal; a cursor older than the retained floor must
// resync rather than claim a complete zero-event page.
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
