package playbackv1

import "github.com/PorticoMediaServer/portico/server/internal/sonic"

// Spans keep their existing meaning: only the seeded base permutation maps
// through a captured row. Explicit source spans and appended tails stay intact.
func (l *layout) baseOrderToSource(position int64) int64 {
	if position >= 0 && position < l.q.domain {
		if err := l.prepareSonicWindow(position / sonic.SmoothWindowMax); err != nil {
			return -1
		}
	}
	if position >= 0 && position < l.q.domain && l.q.shuffle {
		if window, ok := l.sonicWindows[position/sonic.SmoothWindowMax]; ok {
			index := position % sonic.SmoothWindowMax
			if index < int64(len(window.ordinals)) {
				return window.ordinals[index]
			}
		}
	}
	return l.ordinaryOrderToSource(position)
}

func (l *layout) baseSourceToOrder(source int64) int64 {
	position := l.ordinarySourceToOrder(source)
	if position >= 0 && position < l.q.domain {
		if err := l.prepareSonicWindow(position / sonic.SmoothWindowMax); err != nil {
			return -1
		}
	}
	if position >= 0 && position < l.q.domain && l.q.shuffle {
		if window, ok := l.sonicWindows[position/sonic.SmoothWindowMax]; ok {
			for index, ordinal := range window.ordinals {
				if ordinal == source {
					return position/sonic.SmoothWindowMax*sonic.SmoothWindowMax + int64(index)
				}
			}
		}
	}
	return position
}

func (l *layout) prepareSonicWindow(window int64) error {
	if l.sonicErr != nil {
		return l.sonicErr
	}
	if l.sonicPrepare == nil || !l.q.shuffle || l.waiting() {
		return nil
	}
	if _, captured := l.sonicWindows[window]; !captured && !l.sonicMisses[window] {
		l.sonicErr = l.sonicPrepare(l, window)
	}
	return l.sonicErr
}
