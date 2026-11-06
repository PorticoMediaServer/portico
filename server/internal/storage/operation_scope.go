package storage

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/storage/sourceops"
)

// OperationScope aliases the concrete physical-debt owner. Existing helper
// APIs retain the same owner without a parallel scope or ledger.
type OperationScope = sourceops.Scope

func NewOperationScope() *OperationScope { return sourceops.New() }

type inventoryOperationsKey struct{}

// WithInventoryOperations gives portable directory streams a physical owner
// spanning the short contexts used to fetch individual scan pages.
func WithInventoryOperations(ctx context.Context, owner *OperationScope) context.Context {
	return context.WithValue(ctx, inventoryOperationsKey{}, owner)
}

// scopeOwnerLifetime checks direct owner cancellation even before AfterFunc has
// propagated it to a helper's wakeup context.
type scopeOwnerLifetime struct {
	context.Context
	scope context.Context
}

func (c *scopeOwnerLifetime) Err() error {
	if err := c.scope.Err(); err != nil {
		return err
	}
	return c.Context.Err()
}
