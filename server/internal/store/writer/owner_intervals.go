package writer

import (
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// ownerWindow contains observation-only monotonic timestamps. These samples
// never feed admission, leases, AppliedMS, the quantum or a governor ledger.
// A zero timestamp means the boundary was not reached, not a zero duration.
// No observer callback runs under writer custody. These use time.Now directly,
// independently of the admission clock (which protocol tests may freeze).
type ownerWindow struct {
	commandID            CommandID
	setupEnteredAt       time.Time
	setupReturnedAt      time.Time
	terminalEnteredAt    time.Time
	terminalReturnedAt   time.Time
	rollbackStartedAt    time.Time
	rollbackReturnedAt   time.Time
	connectionReturnedAt time.Time
	// Last required connection return/quarantine completed successfully. An
	// idle pooled connection may remain open: this is not a native Close fact.
	nativeReturnedAt            time.Time
	custodyRetirementStartedAt  time.Time
	custodyRetirementReturnedAt time.Time
	custodyRetiredAt            time.Time
	poolCloseStartedAt          time.Time
	poolCloseReturnedAt         time.Time
}

// The executor retains exactly one value-only terminal observation, including
// failed setup when begin returns no transaction. It retains no frame, grant,
// source, receipt, error payload or caller data. Readers must bind setupEnteredAt
// to their own invocation; a contending ErrBusy has no frame or new observation.
type ownerWindowObservation struct {
	ownerWindow
	state              commitState
	setupComplete      bool
	cleanupComplete    bool
	connectionReturned bool
	retirementComplete bool
	policyVerified     bool
	settings           sqliteconn.Settings
}

func (w *executor) lastOwnerWindow() (ownerWindowObservation, bool) {
	if observation := w.ownerObservation.Load(); observation != nil {
		return *observation, true
	}
	return ownerWindowObservation{}, false
}

func (t *transaction) enterTerminal() {
	if t.terminalEnteredAt.IsZero() {
		t.terminalEnteredAt = time.Now()
	}
}

// Called before releasing the writer mutex, even if terminal cleanup panics.
// The fixed-size copy does not retain the transaction through an interior
// pointer. A failed setup's return includes its entire cleanup and retirement.
func (t *transaction) observeTerminalReturn() {
	t.terminalReturnedAt = time.Now()
	if t.setupReturnedAt.IsZero() {
		t.setupReturnedAt = t.terminalReturnedAt
	}
	observation := &ownerWindowObservation{
		ownerWindow: t.ownerWindow, state: t.result.state,
		setupComplete: t.setupComplete, cleanupComplete: t.cleanupComplete,
		connectionReturned: t.connectionReturned, retirementComplete: t.retirementComplete,
		policyVerified: t.policyVerified, settings: t.settings,
	}
	t.writer.ownerObservation.Store(observation)
}
