package eventfeed

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Event cursors are opaque: a reader learns neither the raw ring position
// (which would count every other audience's events) nor anything it could
// carry to another profile, installation or role. Each cursor is the position,
// the reader's binding and an expiry, sealed with AES-GCM under a key that
// lives only in this process. A restart, a restore (which restarts) or a key
// that has aged out turns every outstanding cursor into one stream.resync,
// which is always a safe answer.
const (
	cursorPrefix    = "e1."
	cursorMaxLength = 128
	cursorLife      = 24 * time.Hour
	// Random 96-bit nonces stay far inside AES-GCM's bound when a key seals
	// at most 2^24 cursors.
	cursorKeySeals = 1 << 24
	bindingSize    = 16
	plainSize      = 8 + bindingSize + 8
)

var cursorAAD = []byte("portico event cursor v1")

type cursorKeys struct {
	mu       sync.Mutex
	current  cipher.AEAD
	previous cipher.AEAD
	born     time.Time
	seals    int
	now      func() time.Time
}

type binding [bindingSize]byte

// cursorBinding names everything a cursor may not cross. A bearer rotated in
// the same device family keeps it; another profile, account, installation or
// role does not, and resumes with a resync rather than borrowed history.
func cursorBinding(p identity.Principal, device string, administrative bool) binding {
	h := sha256.New()
	for _, part := range []string{"event-cursor", p.Authority, p.AccountID, p.ProfileID, device} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	if administrative {
		h.Write([]byte{1})
	}
	var out binding
	copy(out[:], h.Sum(nil))
	return out
}

func newAEAD() cipher.AEAD {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("event cursor key: " + err.Error())
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return aead
}

func (k *cursorKeys) clock() time.Time {
	if k.now != nil {
		return k.now()
	}
	return time.Now()
}

// sealing returns the key to seal with, rotating first when the current key
// is old or has sealed its share. The previous key still opens cursors issued
// before the rotation; anything older resyncs.
func (k *cursorKeys) sealing() cipher.AEAD {
	k.mu.Lock()
	defer k.mu.Unlock()
	now := k.clock()
	if k.current == nil || k.seals >= cursorKeySeals || now.Sub(k.born) >= cursorLife {
		k.previous, k.current, k.born, k.seals = k.current, newAEAD(), now, 0
	}
	k.seals++
	return k.current
}

func (k *cursorKeys) opening() (cipher.AEAD, cipher.AEAD) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.current, k.previous
}

func (k *cursorKeys) seal(position int64, b binding) string {
	aead := k.sealing()
	plain := make([]byte, plainSize)
	binary.BigEndian.PutUint64(plain, uint64(position))
	copy(plain[8:], b[:])
	binary.BigEndian.PutUint64(plain[8+bindingSize:], uint64(k.clock().Add(cursorLife).UnixMilli()))
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic("event cursor nonce: " + err.Error())
	}
	return cursorPrefix + base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, plain, cursorAAD))
}

// open returns the position for a cursor this process sealed for this
// binding and that has not expired. Every other cursor, including a numeric
// one from before opaque cursors, is not an error: the reader resyncs.
func (k *cursorKeys) open(raw string, b binding) (int64, bool) {
	if !strings.HasPrefix(raw, cursorPrefix) {
		return 0, false
	}
	sealed, err := base64.RawURLEncoding.DecodeString(raw[len(cursorPrefix):])
	if err != nil {
		return 0, false
	}
	current, previous := k.opening()
	for _, aead := range []cipher.AEAD{current, previous} {
		if aead == nil || len(sealed) != aead.NonceSize()+plainSize+aead.Overhead() {
			continue
		}
		plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], cursorAAD)
		if err != nil {
			continue
		}
		var got binding
		copy(got[:], plain[8:])
		expires := int64(binary.BigEndian.Uint64(plain[8+bindingSize:]))
		position := int64(binary.BigEndian.Uint64(plain))
		if got != b || expires <= k.clock().UnixMilli() || position < 0 {
			return 0, false
		}
		return position, true
	}
	return 0, false
}

// CursorWellFormed is the request-shape check: a bounded token of URL-safe
// characters. Only a malformed one is refused; one that merely fails to open
// is answered with a resync, never with a reason.
func CursorWellFormed(raw string) bool {
	if len(raw) > cursorMaxLength {
		return false
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}
