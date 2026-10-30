package networking

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"time"
)

// Historical observation consumer is a test fixture; production authority uses
// the original-key capture and current policy/cleanup paths.
type ClaimAuthorityConsumer struct {
	store  *SQLiteStore
	verify ClaimAuthorityVerifier
	revoke TerminalClaimRevoker
}

func NewClaimAuthorityConsumer(s *SQLiteStore, v ClaimAuthorityVerifier, r TerminalClaimRevoker) (*ClaimAuthorityConsumer, error) {
	if s == nil || v == nil || r == nil {
		return nil, ErrInvalid
	}
	return &ClaimAuthorityConsumer{s, v, r}, nil
}

func (c *ClaimAuthorityConsumer) Apply(ctx context.Context, expected Intent, proof []byte) error {
	if _, e := claimAuthority(ctx); e != nil {
		return e
	}
	facts, e := c.verify(ctx, proof)
	if e != nil {
		return e
	}
	if facts.OperationID != expected.OperationID || facts.ServerID != expected.ServerID || facts.AccountID != expected.AccountID || facts.LocalGeneration != expected.LocalGeneration || facts.ClaimGeneration != expected.ClaimGeneration || facts.CredentialGeneration != expected.CredentialGeneration {
		return ErrStale
	}
	gated, e := c.store.tx(ctx)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	current, e := installedTupleTx(ctx, tx, expected)
	if e != nil {
		return e
	}
	var revision int64
	var state string
	var digest []byte
	e = tx.QueryRowContext(ctx, `SELECT observation_revision,state,payload_sha256 FROM networking_claim_authority WHERE operation_id=?`, current.OperationID).Scan(&revision, &state, &digest)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if e == nil {
		if facts.Revision < revision {
			return ErrStale
		}
		if facts.Revision == revision {
			if !bytes.Equal(digest, facts.Digest[:]) {
				return ErrStale
			}
			return ctx.Err()
		}
		if terminalClaimState(state) && facts.State != state {
			return ErrCancelled
		}
	}
	if facts.Revision < 1 || (facts.State != "active" && !terminalClaimState(facts.State)) || !time.Now().Before(facts.ExpiresAt) {
		return ErrUnavailable
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO networking_claim_authority(operation_id,server_id,account_id,local_generation,claim_generation,credential_generation,observation_revision,state,issued_at,expires_at,payload_sha256) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(operation_id) DO UPDATE SET observation_revision=excluded.observation_revision,state=excluded.state,issued_at=excluded.issued_at,expires_at=excluded.expires_at,payload_sha256=excluded.payload_sha256`, facts.OperationID, facts.ServerID, facts.AccountID, facts.LocalGeneration, facts.ClaimGeneration, facts.CredentialGeneration, facts.Revision, facts.State, facts.IssuedAt.UTC().Format(time.RFC3339Nano), facts.ExpiresAt.UTC().Format(time.RFC3339Nano), facts.Digest[:])
	if e != nil {
		return e
	}
	if terminalClaimState(facts.State) {
		if e = c.revoke(ctx, tx, current, facts.State); e != nil {
			return e
		}
	}
	return commitClaimTx(ctx, func() error {
		if !time.Now().Before(facts.ExpiresAt) {
			return ErrUnavailable
		}
		return gated.Commit()
	})
}
