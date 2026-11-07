package scanbench

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/store/qual/scanclock"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// EC4Target is the first N1 slice of the harness, never a traversal-complete
// driver. The trusted opening supplies the sole opaque driver; this object
// cannot create authority, scopes, profiles, clocks, leases or observers.
// Advance performs one bounded stage. Keep it after errors and Close on the
// SAME object; do not start a replacement while physical retirement is pending.
type EC4Target struct {
	driver    *writer.ScanDriver
	protocol  ScanProtocol
	input     writer.ScanRunInput
	journal   *ReceiptJournal
	options   FilenameOptions
	manifest  scanclock.Manifest
	lifecycle *LifecycleTurn
	claim     writer.ScanRunReference
	lease     *writer.ScanRunLease
	n1        *N1Attempt
	stage     string
}

func NewEC4Target(driver *writer.ScanDriver, input writer.ScanRunInput, journal *ReceiptJournal, options FilenameOptions, manifest scanclock.Manifest) (*EC4Target, error) {
	if driver == nil || journal == nil || journal.root == nil || manifest.Run != "" {
		return nil, writer.ErrInvalid
	}
	// The N1 driver methods are W's agreed draft. A lifecycle-only issuer is
	// insufficient. Refuse before creating a run; never synthesize a disclosure.
	protocol, ok := any(driver).(ScanProtocol)
	if !ok {
		return nil, &writer.Refusal{Code: writer.RefusalUnqualified}
	}
	return &EC4Target{driver: driver, protocol: protocol, input: input, journal: journal, options: options, manifest: manifest, stage: "prepare_create"}, nil
}

func (r *EC4Target) Advance(ctx context.Context) error {
	if r == nil || ctx == nil {
		return writer.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	switch r.stage {
	case "prepare_create":
		var err error
		r.lifecycle, err = PrepareCreate(ctx, r.driver, r.input, r.journal)
		if err != nil {
			if r.lifecycle != nil {
				r.stage = "close_required"
			}
			return err
		}
		r.stage = "create"
	case "create":
		value, _, err := r.lifecycle.Resume(ctx)
		if err != nil {
			return err
		}
		r.claim = value.Original // saved create reference, not a lease
		r.manifest.Run = value.Original.Scan.RunID
		r.lifecycle = nil
		r.stage = "prepare_claim"
	case "prepare_claim":
		var err error
		r.lifecycle, err = PrepareClaim(ctx, r.driver, r.claim, r.journal)
		if err != nil {
			if r.lifecycle != nil {
				r.stage = "close_required"
			}
			return err
		}
		r.stage = "claim"
	case "claim":
		value, lease, err := r.lifecycle.Resume(ctx)
		if err != nil {
			return err
		}
		r.claim, r.lease, r.lifecycle = value.Original, lease, nil
		r.stage = "observe_n1"
	case "observe_n1":
		var err error
		r.n1, err = PrepareN1(ctx, r.driver, r.lease, r.claim, []byte("."), r.options, r.manifest, r.journal)
		if err != nil {
			if r.n1 != nil {
				r.stage = "close_required"
			}
			return err
		}
		r.stage = "submit_n1"
	case "submit_n1":
		// Mark before offering: any error keeps SAME original recovery. A zero
		// state with nil error cannot pass the exact-result consumption check.
		r.stage = "resolve_n1"
		if _, err := r.n1.SubmitAndConsume(ctx, r.journal); err != nil {
			return err
		}
		r.stage = "ack_n1"
	case "resolve_n1":
		if _, err := r.n1.ResolveAndConsume(ctx, r.protocol, r.journal); err != nil {
			return err
		}
		r.stage = "ack_n1"
	case "ack_n1":
		if _, err := r.n1.AckConsumed(ctx, r.protocol); err != nil {
			return err
		}
		r.stage = "retire_n1"
	case "retire_n1":
		if err := r.n1.Close(ctx); err != nil {
			return err
		}
		r.stage = "n1_target_consumed"
	default:
		return writer.ErrInvalid // no empty-stack/EOF/completion interpretation
	}
	return nil
}

func (r *EC4Target) Stage() string {
	if r == nil {
		return "unready"
	}
	return r.stage
}

func (r *EC4Target) Report() scanclock.Report {
	if r == nil || r.n1 == nil {
		return (*scanclock.Clock)(nil).Report()
	}
	return r.n1.Report()
}

func (r *EC4Target) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		return writer.ErrInvalid
	}
	if r.lifecycle != nil {
		if err := r.lifecycle.Close(ctx); err != nil {
			return err
		}
		r.lifecycle = nil
	}
	if r.n1 != nil {
		if err := r.n1.Close(ctx); err != nil {
			return err
		}
	}
	// Do not close the caller's shared driver or discard durable recovery files.
	r.lease, r.stage = nil, "closed"
	return nil
}
