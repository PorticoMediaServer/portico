package dbwork

import (
	"database/sql"
	"log"
)

// Diagnostic only: model the conservative debit balance reaching the last
// observed free space while actual disk and WAL remain unchanged. Only the
// production checkpoint owner's next real probe may refresh that observation.
func DiagnosticStaleBackupFreeSpace(db *sql.DB) {
	l := ledgerFor(db)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.spent = l.probedSpent + l.free
	log.Printf("REDS-STALE-FREE seeded cached_free=%d spent=%d probed_spent=%d admission=%d physical=%d", l.free, l.spent, l.probedSpent, l.stats.AdmissionBytes, l.stats.PhysicalBytes)
}
