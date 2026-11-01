package setupflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
)

// ReceiptWindow is how long after a setup starts its onboarding row keeps its
// receipt (a reload resumes it). A start never prepared at Hosted takes its
// approval window from receipt_until-ReceiptWindow.
const ReceiptWindow = 900 * time.Second

// Querier is a *sql.Tx or *sql.DB. Derive reads in the caller's transaction so a
// mutator sees the same rows it then changes.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Derive reads setup's phase from the onboarding row, the setup claim and its
// completion, at time now. delivery may be nil: approved and finishing setups are
// then taken to be delivering. A setup whose rows match no phase returns
// ErrInconsistent.
func Derive(ctx context.Context, q Querier, now time.Time, delivery DeliveryLookup) (State, error) {
	var out State
	var accounts int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM accounts`).Scan(&accounts); err != nil {
		return out, err
	}
	var ready, receiptUntil int64
	err := q.QueryRowContext(ctx, `SELECT operation_id,auth_mode,ready,revision,receipt_until FROM onboarding_state_v1 WHERE singleton=1`).
		Scan(&out.RequestID, &out.AuthMode, &ready, &out.Revision, &receiptUntil)
	noRow := errors.Is(err, sql.ErrNoRows)
	if err != nil && !noRow {
		return State{}, err
	}
	if accounts != 0 {
		out.Phase = Completed
		return out, nil
	}
	if noRow {
		return State{Phase: Open}, nil
	}
	if out.AuthMode != "hosted" || ready != 0 {
		return State{}, inconsistent("onboarding row %s ready=%d without an owner", out.AuthMode, ready)
	}

	var active, installed sql.NullString
	if err = q.QueryRowContext(ctx, `SELECT active_operation_id,installed_operation_id FROM networking_claim_identity WHERE singleton=1`).Scan(&active, &installed); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return State{}, err
	}
	if installed.Valid && (!active.Valid || installed.String != active.String) {
		return State{}, inconsistent("installed claim is not the active one")
	}

	// The approval window, exactly as a new start judges it: the newest signed start's
	// expiry, or (never signed) the start of setup plus the setup lifetime.
	var settled int
	var newest sql.NullInt64
	if err = q.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE phase IN('installed','consumed','completed')),max(expires_ms) FROM identity_setup_completions WHERE setup_request_id=?`, out.RequestID).Scan(&settled, &newest); err != nil {
		return State{}, err
	}
	window := time.Unix(receiptUntil, 0).Add(-ReceiptWindow).Add(claimweb.SetupLifetime())
	if newest.Valid {
		window = time.UnixMilli(newest.Int64)
	}
	byWindow := func(live Phase) Phase {
		if now.Before(window) {
			return live
		}
		return Expired
	}

	if !active.Valid {
		if settled != 0 {
			return State{}, inconsistent("a settled setup completion with no active claim")
		}
		out.Phase, out.ExpiresAt = byWindow(Started), window
		// The newest signed start says how the last request ended; a new request replaces it.
		var last sql.NullString
		if err = q.QueryRowContext(ctx, `SELECT phase FROM identity_setup_completions WHERE setup_request_id=? ORDER BY created_ms DESC LIMIT 1`, out.RequestID).Scan(&last); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return State{}, err
		}
		if out.Phase == Started && last.String == "denied" {
			out.Outcome = "denied"
		}
		return out, nil
	}

	op := Operation{ID: active.String}
	var setupOperation, purpose, account string
	var approvalRevision, acknowledged int64
	err = q.QueryRowContext(ctx, `SELECT setup_operation_id,purpose,stage,account_id,approval_revision,installation_acknowledged,local_generation,revision FROM networking_claim_intents WHERE operation_id=?`, op.ID).
		Scan(&setupOperation, &purpose, &op.Stage, &account, &approvalRevision, &acknowledged, &op.LocalGeneration, &op.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, inconsistent("active claim %s has no row", op.ID)
	}
	if err != nil {
		return State{}, err
	}
	if purpose == "setup" && setupOperation != out.RequestID && (op.Stage == "cancel_pending" || op.Stage == "cancelled") {
		// A setup started over: the earlier setup's claim is still being withdrawn at
		// Portico Services. This setup can't prepare its own claim until that ends.
		out.Phase, out.Cancelling = Started, true
		return out, nil
	}
	if purpose != "setup" || setupOperation != out.RequestID {
		return State{}, inconsistent("active claim belongs to another request")
	}
	out.Operation = &op
	if installed.Valid && op.Stage != "installed" {
		return State{}, inconsistent("claim %s is recorded as installed at stage %q", op.ID, op.Stage)
	}

	switch op.Stage {
	case "cancel_pending", "cancelled":
		// Its own unapproved claim is being withdrawn: no new start until that ends.
		out.Phase, out.Cancelling = Started, true
		return out, nil
	case "prepared":
		if account == "account-unbound" && approvalRevision == 0 {
			out.Phase, out.ExpiresAt = byWindow(AwaitingApproval), window
			return out, nil
		}
		return delivering(out, Approved, now, delivery), nil
	case "approved":
		return delivering(out, Approved, now, delivery), nil
	case "finalizing", "retrieving":
		return delivering(out, Finishing, now, delivery), nil
	case "installed":
		if !installed.Valid {
			return State{}, inconsistent("installed claim %s is not recorded as installed", op.ID)
		}
		if acknowledged == 0 {
			return delivering(out, Finishing, now, delivery), nil
		}
		return admission(ctx, q, out, now)
	}
	return State{}, inconsistent("claim stage %q", op.Stage)
}

// delivering is approved or finishing while something delivers the approval, and
// exhausted once nothing does.
func delivering(out State, phase Phase, now time.Time, lookup DeliveryLookup) State {
	out.Phase = phase
	if lookup == nil {
		return out
	}
	d, ok := lookup(out.Operation.ID)
	if ok {
		out.Delivery = &d
	}
	if !ok || !d.Running {
		out.Phase = Exhausted
	}
	return out
}

// admission reads an installed setup's owner admission window from its completion.
func admission(ctx context.Context, q Querier, out State, now time.Time) (State, error) {
	var phase, attempt string
	var expires int64
	err := q.QueryRowContext(ctx, `SELECT phase,expires_ms,attempt_id FROM identity_setup_completions WHERE operation_id=?`, out.Operation.ID).Scan(&phase, &expires, &attempt)
	if errors.Is(err, sql.ErrNoRows) {
		// Installed without a signed start: the setup token plus a fresh identity
		// completes it (post_setup_account_complete); no window applies.
		out.Phase = Installed
		return out, nil
	}
	if err != nil {
		return State{}, err
	}
	out.ExpiresAt = time.UnixMilli(expires)
	live := now.UnixMilli() < expires
	switch phase {
	case "prepared", "installed":
		if live {
			out.Phase = Installed
		} else {
			out.Phase, out.Renewable = InstalledLapsed, attempt == ""
		}
	case "consumed":
		// The original tab spent its attempt and is admitting the owner.
		if live {
			out.Phase = Installed
		} else {
			out.Phase = InstalledLapsed
		}
	case "cancelled", "expired", "superseded":
		out.Phase = InstalledLapsed
	default:
		return State{}, inconsistent("completion %q without an owner", phase)
	}
	return out, nil
}

func inconsistent(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInconsistent, fmt.Sprintf(format, args...))
}
