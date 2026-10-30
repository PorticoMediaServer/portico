package playbackv1

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"math"
	"slices"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

func (l *layout) sonicBasePositionActive(position int64) bool {
	if len(l.spans) == 0 {
		return true
	}
	for _, span := range l.spans {
		if !span.source && position >= span.start && position-span.start < span.count {
			return true
		}
	}
	return false
}

func (l *layout) sonicWindowSource(window int64, readKeys bool) (sonicWindowKey, []int64, []int, []string, error) {
	key := sonicWindowKey{queue: l.q.id, seed: l.q.seed, lap: l.q.lap, window: window}
	if !validSonicWindowKey(key) || window > math.MaxInt64/sonic.SmoothWindowMax || window*sonic.SmoothWindowMax >= l.q.domain {
		return key, nil, nil, nil, errSonicWindow
	}
	start := window * sonic.SmoothWindowMax
	count := min(int64(sonic.SmoothWindowMax), l.q.domain-start)
	ordinary := make([]int64, count)
	positions, ids := []int{}, []string{}
	digest := sha256.New()
	for index := range ordinary {
		position := start + int64(index)
		source := l.ordinaryOrderToSource(position)
		segment, ordinal, ok := l.sourceAt(source)
		if !ok {
			return key, nil, nil, nil, errSonicWindow
		}
		snapshot := l.segments[segment].snapshot
		ordinary[index] = source
		sonicWindowMember(digest, source, snapshot+"-"+strconv.FormatInt(ordinal, 10))
		if !readKeys {
			continue
		}
		_, removed := slices.BinarySearch(l.removals[snapshot], ordinal)
		if removed || !l.sonicBasePositionActive(position) {
			continue // keep tombstones and explicit moved/source spans fixed
		}
		positions = append(positions, index)
	}
	if readKeys {
		// One keyed bulk read, even when a seeded window spans 256 distant chunks.
		if err := l.prefetchSources(ordinary); err != nil {
			return key, nil, nil, nil, err
		}
		for _, position := range positions {
			segment, ordinal, _ := l.sourceAt(ordinary[position])
			id, err := l.key(l.segments[segment].snapshot, ordinal)
			if err != nil {
				return key, nil, nil, nil, err
			}
			ids = append(ids, id)
		}
	}
	copy(key.contents[:], digest.Sum(nil))
	return key, ordinary, positions, ids, nil
}

// Must run inside the queue's admitted writer, before the occurrence is used.
// Replays never consult current analysis; missing rows use one bounded current
// profile/model read and atomically freeze either its order or ordinary order.
func (s *Service) captureMusicWindowTx(ctx context.Context, tx *sql.Tx, caller Caller, l *layout, window int64) error {
	readyMusic := l.musicQueue()
	if !l.q.shuffle || l.waiting() {
		return nil
	}
	// Before a background snapshot completes, even its future keys may be
	// unknown. Freeze the ordinary source ordinals without opening them or
	// inferring that the completed queue will be music-only.
	key, ordinary, positions, ids, err := l.sonicWindowSource(window, false)
	if err != nil {
		return err
	}
	captured, err := readSonicWindowTx(ctx, tx, key, ordinary)
	if errors.Is(err, sql.ErrNoRows) {
		if !readyMusic && (len(l.building) == 0 || !l.musicOnlySources()) {
			if l.sonicMisses == nil {
				l.sonicMisses = map[int64]bool{}
			}
			l.sonicMisses[window] = true
			return nil // a mixed queue cannot create new smoothed windows
		}
		err = nil
		if readyMusic {
			_, _, positions, ids, err = l.sonicWindowSource(window, true)
			if err != nil {
				return err
			}
		}
		proposed := append([]int64(nil), ordinary...)
		var model *sonic.Identity
		if len(ids) > 1 && s.SmoothOrder != nil {
			var order catalog.MusicSmoothOrder
			order, err = s.SmoothOrder(ctx, tx, caller.Principal, ids)
			if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrUnsupportedSelector) || errors.Is(err, sonic.ErrUnavailable) {
				err = nil // refused/unknown analysis cannot influence the ordinary order
			} else if err == nil && order.BySound {
				if order.Model == nil || len(order.Positions) != len(positions) || order.Positions[0] != 0 {
					return errSonicWindow
				}
				for index, from := range order.Positions {
					if from < 0 || from >= int64(len(positions)) {
						return errSonicWindow
					}
					proposed[positions[index]] = ordinary[positions[from]]
				}
				model = order.Model
			}
		}
		if err != nil {
			return err
		}
		captured, err = captureSonicWindowTx(ctx, tx, key, ordinary, proposed, model)
	}
	if err != nil {
		return err
	}
	if l.sonicWindows == nil {
		l.sonicWindows = map[int64]sonicWindowCapture{}
	}
	l.sonicWindows[window] = captured
	return nil
}
