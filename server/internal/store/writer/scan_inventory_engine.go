package writer

import (
	"context"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// The qualified profile names the exact pinned native implementation. Observe
// it again on THIS held connection before BEGIN; Capture/startup values cannot
// stand in for a replacement's engine. This adds no SQL or engine override.
func attestInventoryDriverEngine(ctx context.Context, tx *transaction, expected sqliteconn.EngineIdentity) (out sqliteconn.EngineIdentity, err error) {
	var life context.Context
	if tx != nil {
		life = tx.ctx
	}
	defer func() { out, err = finishCommand(ctx, life, out, err) }()
	if ctx == nil || tx == nil || tx.conn == nil || tx.descriptor.ID != InventoryAccept || !tx.policyVerified || tx.begun || tx.done || expected.Version == "" || expected.SourceID == "" {
		return out, ErrInvalid
	}
	out, err = sqliteconn.AttestEngine(ctx, tx.conn)
	if err != nil {
		return out, err
	}
	if out != expected {
		return out, errors.Join(sqliteconn.ErrPolicy, &Refusal{Code: RefusalUnqualified})
	}
	return out, nil
}
