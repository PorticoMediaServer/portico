// Package setupflow is the one owner of first-run setup's state on the server. It
// derives the setup phase from the durable rows other packages write (onboarding,
// the setup claim and its completion) and holds the table of which events may
// move setup from one phase to another. It writes nothing itself.
package setupflow

import (
	"errors"
	"time"
)

// Phase is where this server's first-run setup is. It is derived, never stored.
type Phase string

const (
	// Open: no owner and no setup started.
	Open Phase = "open"
	// Started: a Portico Account setup exists with no live claim being approved.
	Started Phase = "started"
	// AwaitingApproval: its claim is prepared and waits for a Portico Account to approve it.
	AwaitingApproval Phase = "awaiting_approval"
	// Expired: a started or awaiting setup whose window passed unapproved; the next start supersedes it.
	Expired Phase = "expired"
	// Approved: Portico Services approved the claim; the server is applying it.
	Approved Phase = "approved"
	// Finishing: the server is finalizing, retrieving or acknowledging the installed claim.
	Finishing Phase = "finishing"
	// Exhausted: approved or finishing, but nothing is delivering it any more.
	Exhausted Phase = "exhausted"
	// Installed: the claim is installed and the owner can be admitted from the original tab or app.
	Installed Phase = "installed"
	// InstalledLapsed: installed, but the owner admission window passed unused.
	InstalledLapsed Phase = "installed_lapsed"
	// Completed: the server has an owner.
	Completed Phase = "completed"
)

// Phases lists every phase, in the order setup normally moves through them.
var Phases = []Phase{Open, Started, AwaitingApproval, Expired, Approved, Finishing, Exhausted, Installed, InstalledLapsed, Completed}

// Coarse is what the server tells a caller without setup credentials.
type Coarse string

const (
	CoarseReady      Coarse = "ready"
	CoarseInProgress Coarse = "in_progress"
	CoarseComplete   Coarse = "complete"
)

// Coarse folds a phase into the three values anyone may see. An expired setup reads
// ready: the next start replaces it.
func (p Phase) Coarse() Coarse {
	switch p {
	case Open, Expired:
		return CoarseReady
	case Completed:
		return CoarseComplete
	}
	return CoarseInProgress
}

// ErrInconsistent means the durable rows don't describe any phase (for example a
// setup claim belonging to another request). Callers refuse, as a conflict.
var ErrInconsistent = errors.New("setup rows are inconsistent")

// Operation names the setup's current claim operation.
type Operation struct {
	ID              string
	LocalGeneration int64
	Revision        int64
	Stage           string
}

// Delivery is what the server knows about applying an approval it was given.
type Delivery struct {
	// Running is true while something is still trying to apply the approval.
	Running bool
	// Since is when the approval arrived.
	Since time.Time
	// Attempts counts failed tries so far.
	Attempts int
	// NextRetryAt is when the next try starts; zero when none is scheduled.
	NextRetryAt time.Time
}

// SlowAfter is how long after an approval delivery reads as slow.
const SlowAfter = 2 * time.Minute

// Slow reports whether delivery has been running at least SlowAfter.
func (d Delivery) Slow(now time.Time) bool { return !d.Since.IsZero() && now.Sub(d.Since) >= SlowAfter }

// DeliveryLookup reports the in-memory delivery for an operation; ok is false when
// nothing tracks it.
type DeliveryLookup func(operationID string) (d Delivery, ok bool)

// State is one consistent reading of setup.
type State struct {
	Phase Phase
	// RequestID, Revision and AuthMode describe the onboarding row when one exists.
	RequestID string
	Revision  int64
	AuthMode  string
	// ExpiresAt ends the current window: the approval window while started or
	// awaiting approval, the owner admission window while installed. Zero when none applies.
	ExpiresAt time.Time
	// Operation is the setup's active claim operation, when it has one.
	Operation *Operation
	// Delivery is set while approved, finishing or exhausted, when tracked.
	Delivery *Delivery
	// Cancelling is true while the setup's own unapproved claim is being withdrawn.
	Cancelling bool
	// Outcome says how the setup's last request on Portico Services ended, while started
	// with no new request since: "denied" when the Portico Account declined it.
	Outcome string
	// Renewable is true when an installed setup's lapsed admission window may be
	// renewed without a second approval.
	Renewable bool
}
