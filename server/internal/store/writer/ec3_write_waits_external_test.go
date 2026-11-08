package writer_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual/workload"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

const foregroundWriteBudget = 50 * time.Millisecond
const foregroundWriteLimit = 4096

// Foreground is user intent: security's actual physical class remains Recovery.
// The timestamps concern THIS original mutation, never later Resolve/ACK.
type foregroundWriteWait struct {
	Method, ID                                                                     string
	Operation                                                                      workload.EC2Operation
	ScheduledAt, SubmittedAt                                                       time.Time
	SubmissionSource                                                               string
	HoldStartedAt, CommitReturnedAt, PhysicalReturnedAt                            *time.Time
	DriverLagNS, QueueWaitNS, HoldToCommitNS, SubmissionToCommitNS, PhysicalHoldNS *time.Duration
	ActualPurpose                                                                  *writer.CommandID
	ActualClass                                                                    *governor.Class
	ActualDurability                                                               *writer.Durability
	Complete                                                                       bool
	Stub, Refused, CallError                                                       bool
	Problems                                                                       []string
}

type foregroundWriteResult struct {
	Original any
	Wait     foregroundWriteWait
}

// Pre-HOLD wait includes preparation/admission/lock waiting. There is no
// independent mutex-only clock. Queue + HoldToCommit equals submission→COMMIT
// return; full physical HOLD can include a later terminal/retirement tail.
func foregroundWait(op workload.EC2Operation, offer workload.Offer, submitted time.Time, o workload.Observation) foregroundWriteWait {
	f := foregroundWriteWait{Method: "ec3-foreground-write-wait-v1", ID: fmt.Sprintf("%s/%s/%d/foreground-write", offer.Stream, op, offer.Slot), Operation: op, ScheduledAt: offer.ScheduledAt, SubmittedAt: submitted, SubmissionSource: "actual driver Action entry before preparation; scheduled offer lag retained separately", Stub: o.Stub, Refused: o.RefusalError, CallError: o.CallError != "", Problems: []string{}}
	if !offer.ScheduledAt.IsZero() && !submitted.Before(offer.ScheduledAt) {
		d := submitted.Sub(offer.ScheduledAt)
		f.DriverLagNS = &d
	}
	e := o.Evidence
	if o.Stub || e == nil {
		f.Problems = append(f.Problems, "native owner evidence unavailable; no queue/hold/commit cost invented")
		return f
	}
	purpose, class, durability := writer.ProgressAccept, governor.Foreground, writer.Normal
	if op == workload.EC2Security {
		purpose, class, durability = writer.SecurityAccept, governor.Recovery, writer.Full
	}
	f.ActualPurpose, f.ActualClass, f.ActualDurability = &e.Purpose, &e.Class, &e.Durability
	expectedSync := 1
	if durability == writer.Full {
		expectedSync = 2
	}
	if e.Purpose != purpose || e.Class != class || e.Durability != durability || e.Policy == nil || e.Policy.Engine == "" || e.Policy.Synchronous != expectedSync {
		f.Problems = append(f.Problems, "actual owner purpose/class/durability/policy binding invalid")
		return f
	}
	if e.HoldStartedAt.IsZero() || e.HoldStartedAt.Before(submitted) || o.CallReturned == nil || o.CallReturned.Before(e.HoldStartedAt) {
		f.Problems = append(f.Problems, "owner HOLD start unavailable/outside original submission")
		return f
	}
	f.HoldStartedAt = &e.HoldStartedAt
	queue := e.HoldStartedAt.Sub(submitted)
	f.QueueWaitNS = &queue
	if e.PhysicalReturnedAt != nil && e.PhysicalReturnedAt.After(e.HoldStartedAt) && !e.PhysicalReturnedAt.After(*o.CallReturned) {
		f.PhysicalReturnedAt = e.PhysicalReturnedAt
		d := e.PhysicalReturnedAt.Sub(e.HoldStartedAt)
		f.PhysicalHoldNS = &d
	} else {
		f.Problems = append(f.Problems, "physical HOLD/retirement end unavailable")
	}
	if e.CommitAttemptedAt != nil && e.CommitReturnedAt != nil && e.CommitReturnedAt.After(e.HoldStartedAt) && !e.CommitAttemptedAt.Before(e.HoldStartedAt) && !e.CommitReturnedAt.Before(*e.CommitAttemptedAt) && !e.CommitReturnedAt.After(*o.CallReturned) && e.PhysicalReturnedAt != nil && !e.CommitReturnedAt.After(*e.PhysicalReturnedAt) {
		f.CommitReturnedAt = e.CommitReturnedAt
		hold, end := e.CommitReturnedAt.Sub(e.HoldStartedAt), e.CommitReturnedAt.Sub(submitted)
		f.HoldToCommitNS, f.SubmissionToCommitNS = &hold, &end
	} else {
		f.Problems = append(f.Problems, "actual COMMIT return unavailable; no Resolve or facade-return reconstruction")
	}
	if e.Commit != writer.CommitSucceeded || o.Outcome == nil || o.Outcome.Replayed || o.Outcome.State != writer.Applied || o.Requested == nil || o.Outcome.Reference != *o.Requested {
		f.Problems = append(f.Problems, "fresh physical commit/original outcome not proved")
	}
	f.Problems = append(f.Problems, o.EvidenceProblems...)
	f.Complete = len(f.Problems) == 0 && !f.Stub && !f.Refused && !f.CallError && f.SubmissionToCommitNS != nil && f.PhysicalHoldNS != nil
	return f
}

type foregroundWriteLedger struct {
	facts        []foregroundWriteWait
	overflow     bool
	actionErrors int
}

func newForegroundWriteLedger() *foregroundWriteLedger {
	return &foregroundWriteLedger{facts: make([]foregroundWriteWait, 0, foregroundWriteLimit)}
}
func (l *foregroundWriteLedger) action(op workload.EC2Operation, action workload.Action) workload.Action {
	return func(ctx context.Context, offer workload.Offer, emit func(workload.Observation) error) error {
		submitted := time.Now()
		count := 0
		recorded := false
		var first workload.Observation
		err := action(ctx, offer, func(o workload.Observation) error {
			count++
			if count == 1 {
				first = o
			}
			// Preparation SNAPSHOT, replay, ACK and WAL observations are not the user's
			// fresh commit. Preserve them raw; bind the wait fact to ACCEPT only.
			selected := o.Phase == "ACCEPT" || (o.Phase == "" && o.Evidence != nil && o.Outcome != nil)
			if selected && !recorded {
				if len(l.facts) == foregroundWriteLimit {
					l.overflow = true
					return errors.Join(emit(o), errors.New("foreground write ledger overflow; incomplete"))
				}
				fact := foregroundWait(op, offer, submitted, o)
				l.facts = append(l.facts, fact)
				recorded = true
				o.Result = foregroundWriteResult{Original: o.Result, Wait: fact}
			}
			return emit(o)
		})
		if !recorded {
			o := first
			if count == 0 {
				if err == nil {
					err = errors.New("foreground Action emitted no original observation")
				}
				o = workload.Observation{Phase: "FOREGROUND_SUBMISSION", CallError: err.Error()}
			}
			fact := foregroundWait(op, offer, submitted, o)
			if len(l.facts) < foregroundWriteLimit {
				l.facts = append(l.facts, fact)
			} else {
				l.overflow = true
			}
			// The original observation has already been emitted when present. Record a
			// separate offered-but-no-fresh-commit fact, never manufacture a commit.
			o.Phase = "FOREGROUND_NO_ACCEPT"
			o.Result = foregroundWriteResult{Original: o.Result, Wait: fact}
			o.Notice = "no fresh ACCEPT observed; preparation/coalescing/refusal remain incomplete for write-wait coverage"
			err = errors.Join(err, emit(o))
		}
		if err != nil {
			l.actionErrors++
		}
		return err
	}
}

type writeWaitDistribution struct {
	Count               int
	P50NS, P99NS, MaxNS time.Duration
}

func writeWaitStats(xs []time.Duration) *writeWaitDistribution {
	if len(xs) == 0 {
		return nil
	}
	xs = slices.Clone(xs)
	slices.Sort(xs)
	rank99 := (99*len(xs) + 99) / 100
	return &writeWaitDistribution{len(xs), xs[(len(xs)+1)/2-1], xs[rank99-1], xs[len(xs)-1]}
}

type foregroundWriteClassReport struct {
	Observed, Incomplete                                      int
	QueueWait, HoldToCommit, SubmissionToCommit, PhysicalHold *writeWaitDistribution
}
type foregroundWriteReport struct {
	Method                                                    string
	TargetNS                                                  time.Duration
	ExpectedOffers, ObservedOffers, Incomplete, ActionErrors  int
	Qualification                                             bool
	Overflow, ScheduleComplete, CoverageComplete              bool
	QueueWait, HoldToCommit, SubmissionToCommit, PhysicalHold *writeWaitDistribution
	ObservedP99WithinTarget                                   *bool
	ByOperation                                               map[workload.EC2Operation]foregroundWriteClassReport
	ReadsUnaffected                                           *bool
	ReadsUnavailableReason, Scope                             string
}

func (l *foregroundWriteLedger) report(expected int, scheduleComplete bool) foregroundWriteReport {
	r := foregroundWriteReport{Method: "ec3-foreground-write-wait-v1", TargetNS: foregroundWriteBudget, ExpectedOffers: expected, ObservedOffers: len(l.facts), ActionErrors: l.actionErrors, Overflow: l.overflow, ScheduleComplete: scheduleComplete, Scope: "known observed intervals incl failed tails; partial distributions never whole-run/qualification evidence", ReadsUnavailableReason: "original paired read evidence retained; independent foreground-only baseline/QL comparison required"}
	var queue, hold, end, physical []time.Duration
	for _, f := range l.facts {
		if !f.Complete {
			r.Incomplete++
		}
		if f.QueueWaitNS != nil {
			queue = append(queue, *f.QueueWaitNS)
		}
		if f.HoldToCommitNS != nil {
			hold = append(hold, *f.HoldToCommitNS)
		}
		if f.SubmissionToCommitNS != nil {
			end = append(end, *f.SubmissionToCommitNS)
		}
		if f.PhysicalHoldNS != nil {
			physical = append(physical, *f.PhysicalHoldNS)
		}
	}
	r.ByOperation = map[workload.EC2Operation]foregroundWriteClassReport{}
	for _, op := range []workload.EC2Operation{workload.EC2ProgressCapture, workload.EC2ProgressDrain, workload.EC2Security} {
		group := foregroundWriteClassReport{}
		var q, h, e, p []time.Duration
		for _, f := range l.facts {
			if f.Operation != op {
				continue
			}
			group.Observed++
			if !f.Complete {
				group.Incomplete++
			}
			if f.QueueWaitNS != nil {
				q = append(q, *f.QueueWaitNS)
			}
			if f.HoldToCommitNS != nil {
				h = append(h, *f.HoldToCommitNS)
			}
			if f.SubmissionToCommitNS != nil {
				e = append(e, *f.SubmissionToCommitNS)
			}
			if f.PhysicalHoldNS != nil {
				p = append(p, *f.PhysicalHoldNS)
			}
		}
		if group.Observed > 0 {
			group.QueueWait, group.HoldToCommit, group.SubmissionToCommit, group.PhysicalHold = writeWaitStats(q), writeWaitStats(h), writeWaitStats(e), writeWaitStats(p)
			r.ByOperation[op] = group
		}
	}
	r.QueueWait, r.HoldToCommit, r.SubmissionToCommit, r.PhysicalHold = writeWaitStats(queue), writeWaitStats(hold), writeWaitStats(end), writeWaitStats(physical)
	r.CoverageComplete = expected > 0 && expected == len(l.facts) && r.Incomplete == 0 && l.actionErrors == 0 && !l.overflow && scheduleComplete
	if r.CoverageComplete && r.SubmissionToCommit != nil {
		ok := r.SubmissionToCommit.P99NS <= r.TargetNS
		for _, group := range r.ByOperation {
			if group.SubmissionToCommit == nil || group.SubmissionToCommit.P99NS > r.TargetNS {
				ok = false
			}
		}
		r.ObservedP99WithinTarget = &ok
	}
	return r
}

// Independent request submission shares the real owner with scan. No driver
// lock/retry/priority queue is added: actual owner refusals stay failed requests.
func foregroundWriteStream(every time.Duration, pattern []workload.EC2Operation, bindings workload.EC2Bindings) (workload.Stream, *foregroundWriteLedger, error) {
	ledger := newForegroundWriteLedger()
	stream := workload.Stream{Name: "foreground-writes", Every: every}
	if every < time.Millisecond || every > time.Minute || len(pattern) == 0 || len(pattern) > 3 {
		return stream, ledger, errors.New("invalid bounded foreground write cadence")
	}
	for _, op := range pattern {
		var fallback workload.Action
		switch op {
		case workload.EC2ProgressCapture:
			fallback = workload.ProgressCapture()
		case workload.EC2ProgressDrain:
			fallback = workload.ProgressDrain()
		case workload.EC2Security:
			fallback = workload.Security()
		default:
			return stream, ledger, errors.New("closed foreground progress/security pattern required")
		}
		action := bindings[op]
		if action == nil {
			action = fallback
		}
		stream.Pattern = append(stream.Pattern, workload.Step{Name: string(op), Run: ledger.action(op, action)})
	}
	return stream, ledger, nil
}
