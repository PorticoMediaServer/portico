package dbwork

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

// These windows are scheduler cooperation for starved cold exact catalogue
// classes. They retain no query, result, identity or durable job. Only explicitly
// tagged background catalogue-source writers wait; the ordinary gate continues
// to serve every foreground class and untagged derivation.
const CatalogIngestWindowLimit = time.Minute
const catalogIngestWindowPeriod = 5 * time.Minute
const CatalogIngestOverallLimit = 10 * time.Minute

type catalogIngestKey struct{}

func WithCatalogIngest(ctx context.Context) context.Context {
	return context.WithValue(ctx, catalogIngestKey{}, true)
}

type catalogIngestWindow struct {
	token       uint64
	until, next time.Time
	changed     chan struct{}
	began       time.Time
}

var catalogIngestWindows = struct {
	sync.Mutex
	next uint64
	byDB map[*sql.DB]*catalogIngestWindow
}{byDB: make(map[*sql.DB]*catalogIngestWindow)}

// RequestCatalogIngestWindow opens one minute of opportunity. Only measured
// build progress may extend it, up to ten minutes total. Windows cannot overlap;
// ingest then gets at least four minutes before another window can start.
func RequestCatalogIngestWindow(db *sql.DB, duration time.Duration) (uint64, bool) {
	if db == nil {
		return 0, false
	}
	duration = min(duration, CatalogIngestWindowLimit)
	if duration <= 0 {
		return 0, false
	}
	now := time.Now()
	catalogIngestWindows.Lock()
	defer catalogIngestWindows.Unlock()
	w := catalogIngestWindows.byDB[db]
	if w == nil {
		w = &catalogIngestWindow{changed: make(chan struct{})}
		catalogIngestWindows.byDB[db] = w
	}
	if now.Before(w.until) {
		return w.token, false
	}
	if now.Before(w.next) {
		return 0, false
	}
	close(w.changed)
	w.changed = make(chan struct{})
	catalogIngestWindows.next++
	w.token = catalogIngestWindows.next
	w.until, w.next, w.began = now.Add(duration), now.Add(catalogIngestWindowPeriod), now
	return w.token, true
}

// ProgressCatalogIngestWindow renews a live window only after a committed base
// step advanced its durable cursor. A stalled producer cannot keep ingest held.
func ProgressCatalogIngestWindow(db *sql.DB, token uint64) {
	now := time.Now()
	catalogIngestWindows.Lock()
	defer catalogIngestWindows.Unlock()
	w := catalogIngestWindows.byDB[db]
	if w == nil || w.token != token || !now.Before(w.until) {
		return
	}
	until := now.Add(CatalogIngestWindowLimit)
	cap := w.began.Add(CatalogIngestOverallLimit)
	if until.After(cap) {
		until = cap
	}
	if until.After(w.until) {
		w.until = until
	}
	recovery := w.until.Add(catalogIngestWindowPeriod - CatalogIngestWindowLimit)
	if recovery.After(w.next) {
		w.next = recovery
	}
}
func CatalogIngestWindowActive(db *sql.DB) bool {
	catalogIngestWindows.Lock()
	defer catalogIngestWindows.Unlock()
	w := catalogIngestWindows.byDB[db]
	return w != nil && time.Now().Before(w.until)
}
func ReleaseCatalogIngestWindow(db *sql.DB, token uint64) {
	catalogIngestWindows.Lock()
	defer catalogIngestWindows.Unlock()
	w := catalogIngestWindows.byDB[db]
	if w == nil || w.token != token || w.until.IsZero() {
		return
	}
	w.until = time.Time{}
	close(w.changed)
	w.changed = make(chan struct{})
}

// ForgetCatalogIngestWindow is process-worker cleanup, never a persistent lock.
func ForgetCatalogIngestWindow(db *sql.DB, token uint64) {
	catalogIngestWindows.Lock()
	defer catalogIngestWindows.Unlock()
	w := catalogIngestWindows.byDB[db]
	if w != nil && w.token == token {
		close(w.changed)
		delete(catalogIngestWindows.byDB, db)
	}
}
func catalogIngestTagged(ctx context.Context, class Class) bool {
	tagged, _ := ctx.Value(catalogIngestKey{}).(bool)
	return tagged && class.Valid() && class.Background()
}
func catalogIngestHeld(ctx context.Context, db *sql.DB, class Class) bool {
	return catalogIngestTagged(ctx, class) && CatalogIngestWindowActive(db)
}
func WaitCatalogIngest(ctx context.Context, db *sql.DB, class Class) error {
	if !catalogIngestTagged(ctx, class) {
		return ctx.Err()
	}
	for {
		catalogIngestWindows.Lock()
		w := catalogIngestWindows.byDB[db]
		if w == nil || !time.Now().Before(w.until) {
			catalogIngestWindows.Unlock()
			return ctx.Err()
		}
		until, changed := w.until, w.changed
		catalogIngestWindows.Unlock()
		timer := time.NewTimer(time.Until(until))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// Recheck after ordinary gate admission too: a source already waiting when the
// window opened yields its slot before BEGIN. An already running bounded source
// transaction may finish; the exact producer fence observes that last commit.
func acquireCatalogWrite(ctx context.Context, db *sql.DB, class Class) (func(), error) {
	for {
		if err := WaitCatalogIngest(ctx, db, class); err != nil {
			return nil, err
		}
		release, err := gate.Acquire(ctx, class)
		if err != nil {
			return nil, err
		}
		if !catalogIngestHeld(ctx, db, class) {
			return release, nil
		}
		release()
	}
}
