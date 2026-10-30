package eventfeed

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type capacityRecipientKey struct{}
type CapacityRecipient func(context.Context, *sql.Tx, identity.Principal) error

// WithCapacityRecipient carries only the HTTP-admitted original reader's guard.
// An absent guard cannot receive or be woken by account-capacity hints.
func WithCapacityRecipient(ctx context.Context, check CapacityRecipient) context.Context {
	return context.WithValue(ctx, capacityRecipientKey{}, check)
}
func capacityRecipient(ctx context.Context) CapacityRecipient {
	check, _ := ctx.Value(capacityRecipientKey{}).(CapacityRecipient)
	return check
}
func (h *Hub) SubscribeForContext(ctx context.Context, p identity.Principal, administrative bool) (<-chan struct{}, func()) {
	return h.subscribeFor(p, administrative, capacityRecipient(ctx) != nil)
}
