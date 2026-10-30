package playbackv1

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

type sonicWindowSelection func(*layout) ([]int64, error)

func sonicCurrentWindow(l *layout) ([]int64, error) {
	return l.sonicWindowsForRange(l.q.current, 1)
}

func sonicEntryWindow(entry string) sonicWindowSelection {
	return func(l *layout) ([]int64, error) {
		source, _, ok := l.sourceIndexOf(entry)
		if !ok {
			return nil, ErrNotFound
		}
		position := l.ordinarySourceToOrder(source)
		if !l.q.shuffle || position < 0 || position >= l.q.domain {
			return nil, nil
		}
		return []int64{position / sonic.SmoothWindowMax}, nil
	}
}

func (l *layout) sonicWindowAt(position int64) (int64, bool) {
	if !l.q.shuffle || position < 0 {
		return 0, false
	}
	base := position
	if len(l.spans) > 0 {
		base = -1
		for _, span := range l.spans {
			if position < span.count {
				if span.source {
					return 0, false
				}
				base = span.start + position
				break
			}
			position -= span.count
		}
	}
	return base / sonic.SmoothWindowMax, base >= 0 && base < l.q.domain
}

func (l *layout) sonicWindowsForRange(from, count int64) ([]int64, error) {
	if count < 0 || count > saveStepEntries {
		return nil, ErrQueueTooLarge
	}
	from = max(0, min(from, l.total()))
	count = min(count, l.total()-from)
	seen := map[int64]bool{}
	windows := []int64{}
	for index := int64(0); index < count; index++ {
		if window, ok := l.sonicWindowAt(from + index); ok && !seen[window] {
			seen[window] = true
			windows = append(windows, window)
		}
	}
	return windows, nil
}

// fn must only project the admitted read snapshot. A missing row is captured
// after closing that snapshot, never by upgrading its connection to a writer.
func (s *Service) withMusicLayout(ctx context.Context, caller Caller, id string, selectWindows sonicWindowSelection, fn func(*layout) error) error {
	for attempt := 0; attempt < 3; attempt++ {
		var q queueRow
		missing := []int64{}
		err := func() error {
			tx, done, err := dbwork.BeginRead(ctx, s.DB)
			if err != nil {
				return err
			}
			defer done()
			q, err = loadQueue(ctx, tx, id)
			if err != nil {
				return err
			}
			if !ownsQueue(caller, q) {
				return ErrNotFound
			}
			if err = s.castQueueAuthorityTx(ctx, tx, caller, id); err != nil {
				return err
			}
			l, err := s.readLayout(ctx, tx, q)
			if err != nil {
				return err
			}
			if q.shuffle && !l.waiting() {
				canCapture := l.musicQueue() || len(l.building) > 0 && l.musicOnlySources()
				windows, err := selectWindows(l)
				if err != nil {
					return err
				}
				if len(windows) > saveStepEntries {
					return ErrQueueTooLarge
				}
				l.sonicWindows = map[int64]sonicWindowCapture{}
				seen := map[int64]bool{}
				for _, window := range windows {
					if seen[window] {
						continue
					}
					seen[window] = true
					key, ordinary, _, _, err := l.sonicWindowSource(window, false)
					if err != nil {
						return err
					}
					captured, err := readSonicWindowTx(ctx, tx, key, ordinary)
					if errors.Is(err, sql.ErrNoRows) {
						if canCapture {
							missing = append(missing, window)
						}
					} else if err != nil {
						return err
					} else {
						l.sonicWindows[window] = captured
					}
				}
			}
			if len(missing) > 0 {
				return nil
			}
			return fn(l)
		}()
		if err != nil || len(missing) == 0 {
			return err
		}
		for _, window := range missing {
			err = dbwork.WithWriteTxContext(ctx, s.DB, dbwork.ClassInteractive, func(ctx context.Context, tx *sql.Tx) error {
				fresh, err := loadQueue(ctx, tx, id)
				if err != nil {
					return err
				}
				if !ownsQueue(caller, fresh) {
					return ErrNotFound
				}
				if err = s.castQueueAuthorityTx(ctx, tx, caller, id); err != nil {
					return err
				}
				if fresh.revision != q.revision {
					return ErrRevision
				}
				l, err := s.readLayout(ctx, tx, fresh)
				if err != nil {
					return err
				}
				return s.captureMusicWindowTx(ctx, tx, caller, l, window)
			})
			if errors.Is(err, ErrRevision) {
				break // reselect against the changed queue, within the same bound
			}
			if err != nil {
				return err
			}
		}
	}
	return ErrRevision
}

// withMusicLayoutStart resolves a start's selected entry before any session
// exists. A queue that already carries a session keeps the generic adopted-
// occurrence guard; an initial queue (no session) is admitted by the narrow
// start authority instead, with the exact entry and resolved item bound at the
// read, repeated on every missing-window capture write, and preserved for the
// accepting start writer. Returns the resolved item and whether this was the
// initial (sessionless) path.
func (s *Service) withMusicLayoutStart(ctx context.Context, caller Caller, id, entry string, selectWindows sonicWindowSelection, fn func(*layout) error) (string, bool, int64, error) {
	for attempt := 0; attempt < 3; attempt++ {
		var q queueRow
		var item string
		var initial bool
		var revision int64
		missing := []int64{}
		err := func() error {
			tx, done, err := dbwork.BeginRead(ctx, s.DB)
			if err != nil {
				return err
			}
			defer done()
			q, err = loadQueue(ctx, tx, id)
			if err != nil {
				return err
			}
			if !ownsQueue(caller, q) {
				return ErrNotFound
			}
			initial = q.session == ""
			if !initial {
				if err = s.castQueueAuthorityTx(ctx, tx, caller, id); err != nil {
					return err
				}
			}
			l, err := s.readLayout(ctx, tx, q)
			if err != nil {
				return err
			}
			p, ok := l.positionOf(entry)
			if !ok {
				return ErrNotFound
			}
			_, item, err = l.entryAtErr(p)
			if err != nil {
				return err // ErrQueueBuilding: this entry's key is still being snapshotted
			}
			if initial {
				if err = s.castQueueStartAuthorityTx(ctx, tx, caller, id, entry, item); err != nil {
					return err
				}
			}
			if q.shuffle && !l.waiting() {
				canCapture := l.musicQueue() || len(l.building) > 0 && l.musicOnlySources()
				windows, err := selectWindows(l)
				if err != nil {
					return err
				}
				if len(windows) > saveStepEntries {
					return ErrQueueTooLarge
				}
				l.sonicWindows = map[int64]sonicWindowCapture{}
				seen := map[int64]bool{}
				for _, window := range windows {
					if seen[window] {
						continue
					}
					seen[window] = true
					key, ordinary, _, _, err := l.sonicWindowSource(window, false)
					if err != nil {
						return err
					}
					captured, err := readSonicWindowTx(ctx, tx, key, ordinary)
					if errors.Is(err, sql.ErrNoRows) {
						if canCapture {
							missing = append(missing, window)
						}
					} else if err != nil {
						return err
					} else {
						l.sonicWindows[window] = captured
					}
				}
			}
			if len(missing) > 0 {
				return nil
			}
			if err = fn(l); err != nil {
				return err
			}
			revision = q.revision
			return nil
		}()
		if err != nil || len(missing) == 0 {
			return item, initial, revision, err
		}
		for _, window := range missing {
			err = dbwork.WithWriteTxContext(ctx, s.DB, dbwork.ClassInteractive, func(ctx context.Context, tx *sql.Tx) error {
				fresh, err := loadQueue(ctx, tx, id)
				if err != nil {
					return err
				}
				if !ownsQueue(caller, fresh) {
					return ErrNotFound
				}
				// An initial start has no occurrence to adopt; if the queue gained a
				// session meanwhile, reselect. An adopted queue keeps the generic
				// occurrence authority for its capture write.
				if initial {
					if fresh.session != "" {
						return ErrRevision
					}
				} else if err = s.castQueueAuthorityTx(ctx, tx, caller, id); err != nil {
					return err
				}
				if fresh.revision != q.revision {
					return ErrRevision
				}
				l, err := s.readLayout(ctx, tx, fresh)
				if err != nil {
					return err
				}
				p, ok := l.positionOf(entry)
				if !ok {
					return ErrNotFound
				}
				_, current, err := l.entryAtErr(p)
				if err != nil {
					return err
				}
				if current != item {
					return ErrRevision
				}
				if initial {
					if err = s.castQueueStartAuthorityTx(ctx, tx, caller, id, entry, item); err != nil {
						return err
					}
				}
				return s.captureMusicWindowTx(ctx, tx, caller, l, window)
			})
			if errors.Is(err, ErrRevision) {
				break
			}
			if err != nil {
				return item, initial, revision, err
			}
		}
	}
	return "", false, 0, ErrRevision
}
