package governor

import "errors"

var ErrComposition = errors.New("governor composition mismatch")

// VerifyComposition checks trusted writer wiring without admission, I/O, locks
// or mutation. A successful grant Check alone cannot prove that the driver was
// supplied the ledger's actual WAL gate. Sample freshness, resource forecasts
// and physical inventory remain separate obligations.
func (a *Authority) VerifyComposition(g Governor, wal *WALGate) error {
	if a == nil || a.ledger == nil || g == nil || wal == nil || g != a.ledger || a.ledger.config.WAL != wal {
		return ErrComposition
	}
	return nil
}
