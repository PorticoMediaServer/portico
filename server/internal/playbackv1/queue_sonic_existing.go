package playbackv1

import (
	"database/sql"
	"errors"
)

// Replay only, inside an already-authorized queue snapshot or its existing
// background job. Missing rows remain ordinary; this cannot activate analysis
// or create a capture for a monitoring reader or a failed mutation.
func (l *layout) loadExistingSonicWindows(windows []int64) ([]int64, error) {
	missing := []int64{}
	if !l.q.shuffle || l.waiting() {
		return missing, nil
	}
	if l.sonicWindows == nil {
		l.sonicWindows = map[int64]sonicWindowCapture{}
	}
	seen := map[int64]bool{}
	for _, window := range windows {
		if seen[window] {
			continue
		}
		seen[window] = true
		key, ordinary, _, _, err := l.sonicWindowSource(window, false)
		if err != nil {
			return nil, err
		}
		captured, err := readSonicWindowTx(l.ctx, l.tx, key, ordinary)
		if errors.Is(err, sql.ErrNoRows) {
			delete(l.sonicWindows, window)
			missing = append(missing, window)
		} else if err != nil {
			return nil, err
		} else {
			l.sonicWindows[window] = captured
		}
	}
	return missing, nil
}

func (l *layout) loadExistingSonicRange(from, count int64) ([]int64, error) {
	windows, err := l.sonicWindowsForRange(from, count)
	if err != nil {
		return nil, err
	}
	return l.loadExistingSonicWindows(windows)
}
