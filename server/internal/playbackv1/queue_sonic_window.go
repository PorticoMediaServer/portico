package playbackv1

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

var errSonicWindow = errors.New("invalid captured Sonic window")

// The persisted key deliberately excludes model identity: a new model must
// not recapture a window already heard with an older model or ordinary order.
// Membership includes stable snapshot occurrence IDs AND their source ordinals,
// so an unrelated append or metadata change cannot invalidate an old window.
type sonicWindowKey struct {
	queue             string
	seed, lap, window int64
	contents          [sha256.Size]byte
}

type sonicWindowCapture struct {
	key      sonicWindowKey
	model    *sonic.Identity // nil freezes an ordinary-shuffle fallback
	ordinals []int64         // source ordinals, never item IDs or overrides
}

func sonicWindowBytes(ordinary, reordered []int64) ([]byte, error) {
	if len(ordinary) < 1 || len(ordinary) > sonic.SmoothWindowMax || len(ordinary) != len(reordered) {
		return nil, errSonicWindow
	}
	wanted := make(map[int64]bool, len(ordinary))
	for _, ordinal := range ordinary {
		if ordinal < 0 {
			return nil, errSonicWindow
		}
		if _, duplicate := wanted[ordinal]; duplicate {
			return nil, errSonicWindow
		}
		wanted[ordinal] = false
	}
	raw := make([]byte, len(reordered)*8)
	for n, ordinal := range reordered {
		seen, exists := wanted[ordinal]
		if !exists || seen {
			return nil, errSonicWindow
		}
		wanted[ordinal] = true
		binary.LittleEndian.PutUint64(raw[n*8:], uint64(ordinal))
	}
	return raw, nil
}

func sonicWindowOrdinals(raw []byte, ordinary []int64) ([]int64, error) {
	if len(raw) < 8 || len(raw) > sonic.SmoothWindowMax*8 || len(raw)%8 != 0 || len(raw)/8 != len(ordinary) {
		return nil, errSonicWindow
	}
	ordinals := make([]int64, len(ordinary))
	for n := range ordinals {
		value := binary.LittleEndian.Uint64(raw[n*8:])
		if value > math.MaxInt64 {
			return nil, errSonicWindow
		}
		ordinals[n] = int64(value)
	}
	if _, err := sonicWindowBytes(ordinary, ordinals); err != nil {
		return nil, err
	}
	return ordinals, nil
}

// Length prefixes keep snapshot occurrence identities unambiguous without
// depending on separators in their opaque IDs.
func sonicWindowMember(h hash.Hash, source int64, occurrence string) {
	var word [8]byte
	binary.LittleEndian.PutUint64(word[:], uint64(source))
	h.Write(word[:])
	binary.LittleEndian.PutUint64(word[:], uint64(len(occurrence)))
	h.Write(word[:])
	h.Write([]byte(occurrence))
}
