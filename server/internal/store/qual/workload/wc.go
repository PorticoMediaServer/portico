package workload

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

type WCPhase string

const (
	ProgressCapturePhase WCPhase = "PROGRESS_CAPTURE"
	ProgressDrainPhase   WCPhase = "PROGRESS_DRAIN"
	SecurityPhase        WCPhase = "SECURITY"
)

// Intent is scheduling metadata only. It NEVER substitutes for the actual
// owner policy/class/physical outcome or makes a coalesced/check read a write.
type WCIntent struct {
	Phase      WCPhase
	Purpose    writer.CommandID
	Class      governor.Class
	Durability writer.Durability
	Variant    string
}

func WCIntentFor(phase WCPhase) (WCIntent, error) {
	id := writer.ProgressAccept
	if phase == SecurityPhase {
		id = writer.SecurityAccept
	} else if phase != ProgressCapturePhase && phase != ProgressDrainPhase {
		return WCIntent{}, errors.New("unknown WC cadence phase")
	}
	descriptor, err := writer.Describe(id)
	if err != nil {
		return WCIntent{}, err
	}
	class := governor.Foreground
	if id == writer.SecurityAccept {
		class = governor.Recovery
	}
	variant := "capture_report"
	if phase == ProgressDrainPhase {
		variant = "drain_piece"
	}
	if phase == SecurityPhase {
		variant = "admin_revoke"
	}
	return WCIntent{phase, id, class, descriptor.Durability, variant}, nil
}

var ErrWCComposition = errors.New("WC native composition unavailable: real source/original Resolve/ACK/current-snapshot fixture required")

// WCCadence builds ONE bounded serial writer pattern, with NORMAL progress and
// intended FULL security mixed explicitly. No catch-up/retry or parallel writer.
// The unbound mixed cadence stays a metadata gate. Bound ProgressCapture and
// ProgressDrain actions use the real owner protocol; security requires its own
// separately reviewed FULL binding.
func WCCadence(name string, every, offset time.Duration, pattern []WCPhase) (Stream, error) {
	if name == "" || len(name) > 128 || every < time.Millisecond || offset < 0 || len(pattern) < 2 || len(pattern) > 32 {
		return Stream{}, errors.New("invalid WC cadence")
	}
	stream := Stream{Name: name, Every: every, Offset: offset}
	normal, full := false, false
	for _, phase := range pattern {
		intent, err := WCIntentFor(phase)
		if err != nil {
			return Stream{}, err
		}
		if phase == SecurityPhase {
			full = true
		} else {
			normal = true
		}
		// Values-only gate; caller SQL, a bool, or an injected callback cannot open
		// this driver. New native fixture wiring will be reviewed separately.
		stream.Pattern = append(stream.Pattern, Step{Name: string(phase), Run: wcGatedAction(intent)})
	}
	if !normal || !full {
		return Stream{}, errors.New("mixed WC cadence requires progress and security phases")
	}
	return stream, nil
}

// No binding remains an explicit closed gate. A real binding uses only the
// opaque owner driver and the typed facade; report callbacks supply data only.
func ProgressCapture(bindings ...ProgressBinding) Action {
	if len(bindings) != 1 {
		intent, _ := WCIntentFor(ProgressCapturePhase)
		return wcGatedAction(intent)
	}
	return progressAction(bindings[0], false)
}
func ProgressDrain(bindings ...ProgressBinding) Action {
	if len(bindings) != 1 {
		intent, _ := WCIntentFor(ProgressDrainPhase)
		return wcGatedAction(intent)
	}
	return progressAction(bindings[0], true)
}
func Security(bindings ...SecurityBinding) Action {
	if len(bindings) != 1 {
		intent, _ := WCIntentFor(SecurityPhase)
		return wcGatedAction(intent)
	}
	return securityAction(bindings[0])
}
func wcGatedAction(intent WCIntent) Action {
	return func(_ context.Context, _ Offer, emit func(Observation) error) error {
		return errors.Join(ErrWCComposition, emit(Observation{Phase: "STUB", Stub: true, Notice: ErrWCComposition.Error(), Result: intent}))
	}
}
