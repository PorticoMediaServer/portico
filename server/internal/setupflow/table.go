package setupflow

import (
	"context"
	"errors"
	"time"
)

// Event is something that may move setup between phases: a caller's request or
// something the server observes (time passing, Portico Services answering).
type Event string

const (
	// EventStart issues a setup token (start_browser_setup, start_app_setup).
	EventStart Event = "start"
	// EventInitializeHosted begins a Portico Account setup (setup_server, hosted).
	EventInitializeHosted Event = "initialize_hosted"
	// EventInitializeLocal creates the local owner (setup_server, local).
	EventInitializeLocal Event = "initialize_local"
	// EventRequestApproval prepares, signs and registers the claim; on an installed
	// setup whose admission lapsed it renews the start without a second approval.
	EventRequestApproval Event = "request_approval"
	// EventAbandon ends the caller's own unapproved Portico Account setup (Start
	// again). It is retired on the server alone: nothing is sent to Portico Services,
	// then or later, because the server may next be set up for local sign-in.
	EventAbandon Event = "abandon"
	// EventHostedApproved and EventHostedDenied are the Portico Account's decision.
	EventHostedApproved Event = "hosted_approved"
	EventHostedDenied   Event = "hosted_denied"
	// EventWindowPassed: the approval window ended unapproved.
	EventWindowPassed Event = "window_passed"
	// EventDeliveryProgress, EventDeliveryTransient, EventDeliveryStopped and
	// EventDeliveryRefused report applying an approval.
	EventDeliveryProgress  Event = "delivery_progress"
	EventDeliveryTransient Event = "delivery_transient"
	EventDeliveryStopped   Event = "delivery_stopped"
	EventDeliveryRefused   Event = "delivery_refused"
	// EventRetry re-arms an exhausted delivery.
	EventRetry Event = "retry"
	// EventInstalled: the claim installed and was acknowledged.
	EventInstalled Event = "installed"
	// EventExchange admits the owner from the original tab or app.
	EventExchange Event = "exchange"
	// EventAdmissionWindowPassed: an installed setup's owner admission window ended unused.
	EventAdmissionWindowPassed Event = "admission_window_passed"
)

// Events lists every event.
var Events = []Event{EventStart, EventInitializeHosted, EventInitializeLocal, EventRequestApproval, EventAbandon,
	EventHostedApproved, EventHostedDenied, EventWindowPassed, EventDeliveryProgress, EventDeliveryTransient,
	EventDeliveryStopped, EventDeliveryRefused, EventRetry, EventInstalled, EventExchange, EventAdmissionWindowPassed}

// Refusals. Each is what a caller is told when an event doesn't apply.
var (
	// ErrInProgress: another setup holds the server (setup_in_progress).
	ErrInProgress = errors.New("setup_in_progress")
	// ErrAlreadyComplete: the server has an owner (setup_already_complete).
	ErrAlreadyComplete = errors.New("setup_already_complete")
	// ErrPorticoSetup: a server being set up for a Portico Account never gets a
	// local owner. Start again, which returns it to open, comes first.
	ErrPorticoSetup = errors.New("setup_portico_mode")
	// ErrStateChanged: setup isn't in a phase this event applies to (setup_state_changed).
	ErrStateChanged = errors.New("setup_state_changed")
)

type edge struct{ from, to Phase }

// table holds every allowed transition: from phase, on event, to phase. Numbers are
// the rows of B-DESIGN.md §3.2. Ownership (the caller's own setup token and request)
// and each event's own guards are checked by the caller, not here.
var table = map[Event][]edge{
	// S1: a start from an expired setup supersedes it.
	EventStart: {{Open, Open}, {Expired, Open}},
	// S4, and its idempotent replay by the same request.
	EventInitializeHosted: {{Open, Started}, {Started, Started}, {AwaitingApproval, AwaitingApproval}},
	// S5
	EventInitializeLocal: {{Open, Completed}},
	// S6, S7, and the replay of a renewed start whose Hosted call failed. An expired
	// setup goes Back (S8) and starts again instead.
	EventRequestApproval: {{Started, AwaitingApproval}, {AwaitingApproval, AwaitingApproval},
		{InstalledLapsed, Installed}, {Installed, Installed}},
	// S8: Start again. A Portico Account setup never falls back to local (Justin,
	// 31 Oct); it ends and setup begins afresh with its free choice of kind.
	EventAbandon: {{Started, Open}, {AwaitingApproval, Open}, {Expired, Open}},
	// S9, S10
	EventHostedApproved: {{AwaitingApproval, Approved}},
	EventHostedDenied:   {{AwaitingApproval, Started}},
	// S11
	EventWindowPassed: {{Started, Expired}, {AwaitingApproval, Expired}},
	// S12–S16
	EventDeliveryProgress:  {{Approved, Finishing}, {Finishing, Finishing}},
	EventDeliveryTransient: {{Approved, Approved}, {Finishing, Finishing}},
	EventDeliveryStopped:   {{Approved, Exhausted}, {Finishing, Exhausted}},
	EventRetry:             {{Exhausted, Finishing}},
	// S16: a final refusal withdraws the claim. Until Portico Services acknowledges the
	// withdrawal, setup reads started and cancelling; Back retires it here at once.
	EventDeliveryRefused: {{Approved, Started}, {Finishing, Started}, {Exhausted, Started}},
	// S17–S19
	EventInstalled:             {{Finishing, Installed}},
	EventExchange:              {{Installed, Completed}},
	EventAdmissionWindowPassed: {{Installed, InstalledLapsed}},
}

// Next is the phase event moves setup to from phase from, or the refusal a caller gets.
func Next(from Phase, event Event) (Phase, error) {
	for _, e := range table[event] {
		if e.from == from {
			return e.to, nil
		}
	}
	switch {
	case from == Completed:
		// S20
		return from, ErrAlreadyComplete
	case event == EventStart && from != Open && from != Expired:
		// S2, S3
		return from, ErrInProgress
	case event == EventInitializeLocal:
		// S8r: every phase but open has a Portico Account setup row.
		return from, ErrPorticoSetup
	}
	return from, ErrStateChanged
}

// Require derives setup in the caller's transaction and checks that event applies.
// It returns the state it found and the phase the event moves setup to; on a
// refusal the state is still returned, so callers can say when to try again.
func Require(ctx context.Context, q Querier, now time.Time, delivery DeliveryLookup, event Event) (State, Phase, error) {
	state, err := Derive(ctx, q, now, delivery)
	if err != nil {
		return State{}, "", err
	}
	to, err := Next(state.Phase, event)
	return state, to, err
}
