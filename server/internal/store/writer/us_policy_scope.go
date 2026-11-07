package writer

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// Receipt composition consumes, never mints, CURRENT independent authority.
// There is deliberately no production constructor yet. W must issue this stamp
// only AFTER same-turn native token/family/member/profile/installation, narrow
// grant, PIN/fence and owner/admin target checks; absence fails closed.
// It is scoped to THIS frame, not a cached principal or reusable permission.
type usPolicyAuthorityStamp struct{ frame *transaction }
type usPolicyNativePermit struct{ frame *transaction } // test constructor only

type usPolicyAuthorization struct {
	turn            *commandTurn
	stamp           *usPolicyAuthorityStamp
	issuer          usPolicyIssuer
	lifetime        context.Context
	authorizedUntil time.Time
	memoryBytes     uint64 // one private partition of the SAME W admission, not a ledger
	receiptBytes    int64  // exact existing quota reservation made by W, not a caller quota
	nativeFixture   *usPolicyNativePermit
}

func (a *usPolicyAuthorization) check(turn *commandTurn) error {
	if a == nil || turn == nil || turn.transaction == nil || a.turn != turn || a.stamp == nil || a.stamp.frame != turn.transaction || a.lifetime == nil || a.lifetime.Err() != nil {
		return ErrDisclosureDenied
	}
	tx := turn.transaction
	if tx.descriptor.ID != USMemberLibraries && tx.descriptor.ID != USProfileLibraries || tx.descriptor.Durability != Full || tx.settings.Synchronous != int(sqliteconn.Full) || !tx.policyVerified {
		return ErrInvalid
	}
	if !tx.begun || tx.done {
		return ErrClosed
	}
	if a.authorizedUntil.IsZero() || a.memoryBytes == 0 {
		return &Refusal{Code: RefusalUnqualified}
	}
	return nil
}

func (a *usPolicyAuthorization) requireMemory(ctx context.Context, turn *commandTurn, actualVariableBytes int64) error {
	if err := a.check(turn); err != nil {
		return err
	}
	if actualVariableBytes < 0 || actualVariableBytes > maxCanonicalResultBytes {
		return &Refusal{Code: RefusalCapacity}
	}
	// Actual native bytes, plus bounded decoder/maps/scalars/output ownership.
	// A numerical envelope is not qualified from the command's descriptor.
	required := uint64(64<<10) + uint64(actualVariableBytes)*32
	if required > a.memoryBytes {
		return &Refusal{Code: RefusalCapacity}
	}
	if c, ok := turn.transaction.custody.(*governedTurn); ok {
		if err := c.check(turn.transaction.descriptor.ID, turn.transaction.workLimit); err != nil {
			return err
		}
		if c.expected.Need[governor.MemoryBytes] <= 0 || a.memoryBytes > uint64(c.expected.Need[governor.MemoryBytes]) {
			return &Refusal{Code: RefusalUnqualified}
		}
	} else if a.nativeFixture == nil || a.nativeFixture.frame != turn.transaction {
		return &Refusal{Code: RefusalUnqualified}
	}
	return turn.checkLifetimes(ctx)
}

func usPolicyTimeMS(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization) (nowMS int64, err error) {
	defer func() { nowMS, err = finishCommand(ctx, commandLifetime(turn), nowMS, err) }()
	if err = a.check(turn); err != nil {
		return 0, err
	}
	tx := turn.transaction
	if tx.writer == nil || tx.writer.now == nil {
		return 0, ErrInvalid
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	now := tx.writer.now()
	if err = tx.observeTime(now); err != nil {
		return 0, err
	}
	if !now.Before(a.authorizedUntil) {
		return 0, ErrDisclosureDenied
	}
	deadline, ok := tx.ctx.Deadline()
	if !ok {
		return 0, ErrInvalid
	}
	if !now.Before(deadline) {
		return 0, context.DeadlineExceeded
	}
	ms := now.UnixMilli()
	if ms < 0 || !time.UnixMilli(ms).Equal(now.Truncate(time.Millisecond)) {
		return 0, ErrInvalid
	}
	return ms, nil
}

func finishUSPolicy(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, v usPolicyComposition, err error) (usPolicyComposition, error) {
	if a == nil {
		return usPolicyComposition{}, errors.Join(err, ErrDisclosureDenied)
	}
	err = errors.Join(err, a.check(turn))
	if err == nil {
		_, err = usPolicyTimeMS(ctx, turn, a)
	}
	return finishCommand(ctx, a.lifetime, v, err)
}
