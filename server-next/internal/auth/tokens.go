// Package auth holds what sign-in needs besides storage: token minting and checking, password
// hashing and the shape of the guessing limits. It runs no SQL.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// AccessLifetime is how long an access token works before the app must renew it.
const AccessLifetime = 15 * time.Minute

// ErrInvalidToken means the token was not made by this server, or has expired.
var ErrInvalidToken = errors.New("invalid token")

// Claims is what an access token says about its bearer. Requests are checked against it
// without reading the database.
type Claims struct {
	SessionID int64 `json:"s"`
	UserID    int64 `json:"u"`
	// ProfileID is 0 until a profile has been selected.
	ProfileID int64 `json:"p,omitempty"`
	// Role is what the bearer may do on the server: owner, admin, member, or account for a
	// sign-in that has not selected a profile yet.
	Role string `json:"r"`
	// Manage says the bearer may change the account itself: its profiles, devices and sessions.
	Manage bool `json:"m,omitempty"`
	// Boot and Seq place the token in the server's order of session changes, so a token read
	// before a change to its session can be told from one read after. See db.TokenStamp.
	Boot      int64 `json:"b"`
	Seq       int64 `json:"v"`
	ExpiresAt int64 `json:"e"`
}

// Tokens mints and checks tokens with the server's key.
type Tokens struct{ key []byte }

// NewKey returns a new signing key in the text form kept in settings.
func NewKey() string {
	var b [32]byte
	rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// NewTokens returns a minter for the key made by NewKey.
func NewTokens(key string) (*Tokens, error) {
	raw, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil || len(raw) != 32 {
		return nil, errors.New("the stored signing key is damaged")
	}
	return &Tokens{key: raw}, nil
}

const (
	kindAccess  = 'a'
	kindRefresh = 'r'
	macSize     = 32
)

func (t *Tokens) seal(kind byte, payload []byte) string {
	body := append([]byte{kind}, payload...)
	mac := hmac.New(sha256.New, t.key)
	mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(body))
}

func (t *Tokens) open(kind byte, token string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) < 1+macSize || raw[0] != kind {
		return nil, ErrInvalidToken
	}
	body, sum := raw[:len(raw)-macSize], raw[len(raw)-macSize:]
	mac := hmac.New(sha256.New, t.key)
	mac.Write(body)
	if !hmac.Equal(sum, mac.Sum(nil)) {
		return nil, ErrInvalidToken
	}
	return body[1:], nil
}

// Access mints an access token for c, valid from issued for AccessLifetime or until the
// sign-in itself ends, whichever is sooner. The same claims and time give the same token.
func (t *Tokens) Access(c Claims, issued, sessionEnds time.Time) (string, time.Time) {
	expires := issued.Add(AccessLifetime)
	if sessionEnds.Before(expires) {
		expires = sessionEnds
	}
	c.ExpiresAt = expires.UnixMilli()
	payload, _ := json.Marshal(c)
	return t.seal(kindAccess, payload), time.UnixMilli(c.ExpiresAt)
}

// CheckAccess returns the claims of an access token this server minted that has not expired.
func (t *Tokens) CheckAccess(token string, now time.Time) (Claims, error) {
	c, err := t.ExpiredAccess(token)
	if err != nil || c.ExpiresAt <= now.UnixMilli() {
		return Claims{}, ErrInvalidToken
	}
	return c, nil
}

// ExpiredAccess returns the claims of an access token this server minted, even after it has
// expired. Its one use is signing out: an app whose token ran out can still end its session.
func (t *Tokens) ExpiredAccess(token string) (Claims, error) {
	var c Claims
	payload, err := t.open(kindAccess, token)
	if err != nil || json.Unmarshal(payload, &c) != nil {
		return Claims{}, ErrInvalidToken
	}
	return c, nil
}

// Refresh is what a refresh token says: which sign-in it belongs to and which renewal made it.
type Refresh struct {
	SessionID  int64
	Generation int64
}

func (t *Tokens) refresh(r Refresh, nonce []byte) string {
	payload := make([]byte, 16, 32)
	binary.BigEndian.PutUint64(payload, uint64(r.SessionID))
	binary.BigEndian.PutUint64(payload[8:], uint64(r.Generation))
	return t.seal(kindRefresh, append(payload, nonce[:16]...))
}

// FirstRefresh mints the refresh token a new sign-in starts with.
func (t *Tokens) FirstRefresh(sessionID int64) string {
	var nonce [16]byte
	rand.Read(nonce[:])
	return t.refresh(Refresh{sessionID, 1}, nonce[:])
}

// NextRefresh returns the token that replaces current when it is renewed with requestID.
// It is worked out from the two, not drawn at random, so an app that lost the reply and
// repeats the renewal with the request id it saved is given the very same token again,
// and the server never has to keep a usable token on disk.
func (t *Tokens) NextRefresh(current string, r Refresh, requestID string) string {
	mac := hmac.New(sha256.New, t.key)
	mac.Write([]byte("next\x00" + current + "\x00" + requestID))
	return t.refresh(Refresh{r.SessionID, r.Generation + 1}, mac.Sum(nil))
}

// CheckRefresh says which sign-in and renewal a refresh token belongs to, if this server
// minted it. Whether it is still the current one is for the database to say.
func (t *Tokens) CheckRefresh(token string) (Refresh, error) {
	payload, err := t.open(kindRefresh, token)
	if err != nil || len(payload) != 32 {
		return Refresh{}, ErrInvalidToken
	}
	return Refresh{int64(binary.BigEndian.Uint64(payload)), int64(binary.BigEndian.Uint64(payload[8:]))}, nil
}

// Hash is how a token or code is stored: the database never holds one that could be used.
func Hash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
