package networking

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"time"
)

// TerminalClaimRevoker must revoke the exact installed Hosted sessions and
// playback authority in this transaction. Root supplies its actual repositories;
// a nil hook is refused. Its effects roll back with observation publication.
type TerminalClaimRevoker func(context.Context, *sql.Tx, Intent, string) error

// installedTupleTx checks the current pointer without consulting the observation
// itself, allowing authenticated terminal refresh while ordinary readers deny.
func installedTupleTx(ctx context.Context, tx *sql.Tx, expected Intent) (Intent, error) {
	id, e := readIdentity(ctx, tx)
	if e != nil {
		return Intent{}, e
	}
	v, e := loadIntentTx(ctx, tx, expected.OperationID)
	if e != nil {
		return Intent{}, e
	}
	if v.Stage != Installed || expected.Stage != Installed || !sameBinding(v.Binding, expected.Binding) || v.Revision != expected.Revision || v.ClaimGeneration != expected.ClaimGeneration || v.CredentialGeneration != expected.CredentialGeneration || id.server != v.ServerID || !bytes.Equal(id.key, v.PublicKey) || id.generation != v.LocalGeneration || !id.active.Valid || !id.installed.Valid || id.active.String != v.OperationID || id.installed.String != v.OperationID {
		return Intent{}, ErrStale
	}
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM networking_claim_credentials WHERE operation_id=? AND server_id=? AND local_generation=? AND claim_generation=? AND credential_generation=?`, v.OperationID, v.ServerID, v.LocalGeneration, v.ClaimGeneration, v.CredentialGeneration).Scan(&count); e != nil {
		return Intent{}, e
	}
	if count != 1 {
		return Intent{}, ErrStale
	}
	return v.Intent, nil
}

// GuardInstalledClaim permits claim control-plane recovery without an active
// observation; it does not grant membership, media or attachment readiness.
// A persisted terminal state denies indefinitely, regardless of envelope age.
func GuardInstalledClaim(ctx context.Context, tx *sql.Tx, v Intent) error {
	if _, e := installedTupleTx(ctx, tx, v); e != nil {
		return e
	}
	var state string
	e := tx.QueryRowContext(ctx, `SELECT state FROM networking_claim_authority WHERE operation_id=? AND server_id=? AND account_id=? AND local_generation=? AND claim_generation=? AND credential_generation=?`, v.OperationID, v.ServerID, v.AccountID, v.LocalGeneration, v.ClaimGeneration, v.CredentialGeneration).Scan(&state)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if terminalClaimState(state) {
		return ErrCancelled
	}
	if state != "active" {
		return ErrInvalid
	}
	return ctx.Err()
}

// RequireActiveClaimAuthority checks credential attachment readiness only. The
// caller must independently enforce the current member policy in this same tx.
// It must not replace the application's playback or offline policy lifetime.
func RequireActiveClaimAuthority(ctx context.Context, tx *sql.Tx, v Intent) error {
	if e := GuardInstalledClaim(ctx, tx, v); e != nil {
		return e
	}
	var raw string
	if e := tx.QueryRowContext(ctx, `SELECT expires_at FROM networking_claim_authority WHERE operation_id=?`, v.OperationID).Scan(&raw); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return ErrUnavailable
		}
		return e
	}
	expiry, e := time.Parse(time.RFC3339Nano, raw)
	if e != nil || !time.Now().Before(expiry) {
		return ErrUnavailable
	}
	return ctx.Err()
}
