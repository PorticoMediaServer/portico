package metadata

import (
	"context"
	"errors"
	"net"
)

// providerOutage reports whether a provider's failure is plainly the provider's own and not
// about the one request: no answer at all (a network error, a timeout), or an error that says
// so itself (fetch's Status and this package's StatusError for a 5xx, 401, 403 or 429, and
// fetch's SlowDown, a pause during which every request fails at once). Anything else, such
// as a refusal of one request (400, 422), is about that request: the caller records it against
// the one title or song and goes on.
func providerOutage(err error) bool {
	var says interface{ ProviderOutage() bool }
	if errors.As(err, &says) {
		return says.ProviderOutage()
	}
	var network net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network)
}
