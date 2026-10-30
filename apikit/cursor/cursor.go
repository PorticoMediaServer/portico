// Package cursor signs bounded continuations and fences them to the viewer and query.
package cursor

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/apikit/apierror"
)

type Binding struct {
	Principal string `json:"principal"`
	Filter    string `json:"filter"`
	Sort      string `json:"sort"`
	Snapshot  string `json:"snapshot"`
	Authority string `json:"authority"`
}
type envelope struct {
	Version  int             `json:"v"`
	Expires  int64           `json:"expires"`
	Binding  Binding         `json:"binding"`
	Position json.RawMessage `json:"position"`
}
type Signer struct {
	mu            sync.RWMutex
	rotateMu      sync.Mutex
	key           []byte
	previous      []byte
	previousUntil time.Time
	statePath     string
	createdAt     time.Time
	rotateEvery   time.Duration
	ttl           time.Duration
	now           func() time.Time
}

func New(key []byte, ttl time.Duration) (*Signer, error) {
	if len(key) < 32 || ttl <= 0 || ttl > 24*time.Hour {
		return nil, errors.New("cursor key or lifetime invalid")
	}
	return &Signer{key: append([]byte{}, key...), ttl: ttl, now: time.Now}, nil
}
func (s *Signer) Seal(binding Binding, position any) (string, error) {
	if binding.Principal == "" || binding.Authority == "" || binding.Snapshot == "" {
		return "", errors.New("cursor binding required")
	}
	s.mu.RLock()
	key := s.key
	s.mu.RUnlock()
	raw, err := json.Marshal(position)
	if err != nil {
		return "", err
	}
	raw, err = json.Marshal(envelope{1, s.now().Add(s.ttl).Unix(), binding, raw})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(raw)
	token := base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(token) > 4096 {
		return "", errors.New("cursor too large")
	}
	return token, nil
}
func (s *Signer) Open(token string, binding Binding, position any) error {
	invalid := &apierror.Error{Code: "invalid_cursor"}
	if len(token) > 4096 {
		return invalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return invalid
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return invalid
	}
	s.mu.RLock()
	key, previous, previousUntil := s.key, s.previous, s.previousUntil
	s.mu.RUnlock()
	valid := validMAC(key, raw, sig)
	if !valid && len(previous) > 0 && s.now().Before(previousUntil) {
		valid = validMAC(previous, raw, sig)
	}
	if !valid {
		return invalid
	}
	var e envelope
	if json.Unmarshal(raw, &e) != nil || e.Version != 1 {
		return invalid
	}
	if e.Binding != binding {
		return invalid
	}
	if e.Expires <= s.now().Unix() {
		return &apierror.Error{Code: "cursor_expired"}
	}
	if err = json.Unmarshal(e.Position, position); err != nil {
		return invalid
	}
	return nil
}

func validMAC(key, raw, sig []byte) bool {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(raw)
	return hmac.Equal(sig, mac.Sum(nil))
}
