package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Original target comparisons, never source-delete or issuer authority. The
// independently authorized retirement parent owns its current worker custody,
// fresh original classification, full physical quota/receipt/tombstone protocol
// and terminal. Only ended key/app-password LEAF sources with a revoked,
// acknowledged credential-origin family are supported by this first recipe.
// It never reads ciphertext, decrypts, deletes the credential/family or refunds
// quota. The returned actual removed bytes are an input to the parent's SAME
// transaction ledger, not an ACK or a committed refund.
type usCredentialSecretDrainScope struct {
	credential, kind, account, profile, member, lifetime, family, server, secretHash string
	familyIncarnation                                                                [16]byte
	familyGeneration, acknowledged                                                   int64
}
type usCredentialSecretDrainEffect struct {
	removed       bool
	variableBytes int64
}

func (usCredentialSecretDrainScope) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usCredentialSecretDrainScope")
}

func drainUSCredentialSecret(ctx context.Context, turn *commandTurn, s usCredentialSecretDrainScope) (out usCredentialSecretDrainEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil {
		return out, ErrInvalid
	}
	tx := turn.transaction
	// Future US-26 authority/result adoption is still required; this private
	// component registers no accepting descriptor or general delete command.
	if tx.descriptor.ID != 582 || tx.descriptor.Durability != Full || !tx.begun || tx.done || !tx.policyVerified || tx.settings.Synchronous != 2 {
		return out, ErrInvalid
	}
	for _, v := range []string{s.credential, s.account, s.profile, s.family, s.server} {
		if !usIdentifier(v) {
			return out, ErrInvalid
		}
	}
	if (s.kind != "key" && s.kind != "app_password") || !usHex(s.member, 32) || !usHex(s.lifetime, 32) || !usIdentityDigest(s.secretHash) || s.familyIncarnation == ([16]byte{}) || s.familyGeneration <= 0 || s.acknowledged < 0 {
		return out, ErrInvalid
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	row, e := wc008DraftQuery(ctx, turn, usSecretDrainSource, s.credential, s.kind, s.account, s.profile, s.family, s.secretHash, s.server, s.member, s.lifetime, s.familyIncarnation[:], s.familyGeneration, s.acknowledged, s.acknowledged)
	if e != nil {
		return out, e
	}
	var yes int64
	if e = row.Scan(&yes); e != nil {
		return out, usSecretDrainScanError(e)
	}
	if yes != 1 {
		return out, ErrConflict
	}
	// PK preflight returns only exact generated variable bytes or a mismatched
	// original-binding sentinel. A foreign row must not masquerade as absence.
	row, e = wc008DraftQuery(ctx, turn, usSecretDrainBytes, s.kind, s.account, s.member, s.profile, s.lifetime, s.family, s.familyIncarnation[:], s.secretHash, s.credential)
	if e != nil {
		return out, e
	}
	var bytes int64
	if e = row.Scan(&bytes); errors.Is(e, sql.ErrNoRows) {
		return out, turn.checkLifetimes(ctx)
	} else if e != nil {
		return out, e
	}
	if bytes <= 0 {
		return out, ErrConflict
	}
	// Native schema bounds identifiers/key/ciphertext; admit only its bounded
	// source shape, never copy a hostile body merely to compute its size.
	if bytes > 4096 {
		return out, &Refusal{Code: RefusalCapacity}
	}
	row, e = wc008DraftQuery(ctx, turn, usSecretDrainDelete, s.credential, s.kind, s.account, s.member, s.profile, s.lifetime, s.family, s.familyIncarnation[:], s.secretHash, bytes)
	if e != nil {
		return out, e
	}
	var removed int64
	if e = row.Scan(&removed); e != nil {
		return out, usSecretDrainScanError(e)
	}
	if removed != bytes {
		return out, ErrConflict
	}
	out = usCredentialSecretDrainEffect{removed: true, variableBytes: removed}
	// A final child check detects a mismatched registry step or a changed
	// composed prefix. Any error rolls back source/child/ledger effects together.
	row, e = wc008DraftQuery(ctx, turn, usSecretDrainAbsent, s.credential)
	if e != nil {
		return out, e
	}
	if e = row.Scan(&yes); !errors.Is(e, sql.ErrNoRows) {
		if e != nil {
			return out, e
		}
		return out, ErrConflict
	}
	return out, turn.checkLifetimes(ctx)
}
func usSecretDrainScanError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	return err
}
