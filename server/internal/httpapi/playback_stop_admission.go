package httpapi

import "context"

// Most homes never read session counts on Stop: 200 waiting slots already
// cover the target audience. Try the floor atomically with admission. Only an
// actual full queue needs a fresh count of the live-session partial index,
// outside the lane mutex and writer gate; then retry admission with that bound.
// A pre-check of waiters could race another Stop into a false full refusal.
func (a *admission) beginStop(ctx context.Context, l *lane, key string) (bool, bool) {
	floor := l.spec.queueCapacity
	admitted, queued := l.beginWithQueueLimit(key, floor, floor, false)
	if admitted || queued {
		return admitted, queued
	}
	capacity := floor
	if a.livePlaybackSessions != nil {
		if live, err := a.livePlaybackSessions(ctx); err == nil {
			capacity = max(floor, live)
		}
	}
	// Only this final attempt may record a queue-capacity refusal.
	return l.beginWithQueueLimit(key, capacity, capacity, true)
}
