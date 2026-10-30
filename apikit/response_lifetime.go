package apikit

import "context"

// AfterResponse retains a handler-owned delivery lease until typed response
// encoding/writing (or error handling) finishes. Register synchronously inside
// a typed Handler. It returns false without taking ownership outside one.
func AfterResponse(ctx context.Context, release func()) bool {
	r, ok := ctx.Value(replyKey{}).(*reply)
	if !ok || release == nil {
		return false
	}
	r.releases = append(r.releases, release)
	return true
}

func (r *reply) release() {
	for i := len(r.releases) - 1; i >= 0; i-- {
		r.releases[i]()
	}
	r.releases = nil
}
