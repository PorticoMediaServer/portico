package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Original target comparisons only, never issuer/retirement authority. The
// trusted parent owns its independently authorized retirement, complete quota
// forecast, original result/tombstone retention and physical terminal. Cleanup
// may continue after principal revocation; US-01's session stamp grants none of
// that authority. This component only removes bounded dependent witnesses and
// cannot delete/authorize the family, issue Applied or admit purpose 582.
type usFamilyFenceDrainScope struct {
	family, server, account, profile, member, lifetime, device, lineage string
	incarnation                                                         [16]byte
	generation, acknowledged                                            int64
}
type usFamilyFenceDrainEffect struct{ removed int64 }

func (usFamilyFenceDrainScope) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usFamilyFenceDrainScope")
}

func drainUSFamilyFenceWitnesses(ctx context.Context, turn *commandTurn, s usFamilyFenceDrainScope) (out usFamilyFenceDrainEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil {
		return out, ErrInvalid
	}
	tx := turn.transaction
	// Future reviewed US-26 retirement recipe only. No descriptor is registered
	// here; current main cannot open this unadopted accepting command.
	if tx.descriptor.ID != 582 || tx.descriptor.Durability != Full || !tx.begun || tx.done || !tx.policyVerified || tx.settings.Synchronous != 2 {
		return out, ErrInvalid
	}
	for _, v := range []string{s.family, s.server, s.account, s.profile, s.device, s.lineage} {
		if !usIdentifier(v) {
			return out, ErrInvalid
		}
	}
	if !usHex(s.member, 32) || !usHex(s.lifetime, 32) || s.incarnation == ([16]byte{}) || s.generation <= 0 || s.acknowledged < 0 {
		return out, ErrInvalid
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	row, e := wc008DraftQuery(ctx, turn, usFenceDrainFamily, s.family, s.server, s.account, s.profile, s.member, s.lifetime, s.incarnation[:], s.generation, s.acknowledged, s.acknowledged, s.device, s.lineage)
	if e != nil {
		return out, e
	}
	var yes int64
	if e = row.Scan(&yes); e != nil {
		return out, usFenceDrainScanError(e)
	}
	if yes != 1 {
		return out, ErrConflict
	}
	for _, scope := range [...]string{"device", "profile_pin", "profile_remember", "profile_lineage"} {
		row, e = wc008DraftQuery(ctx, turn, usFenceDrainChild, s.family, scope)
		if e != nil {
			return out, e
		}
		var fence, generation int64
		if e = row.Scan(&fence, &generation); errors.Is(e, sql.ErrNoRows) {
			continue
		} else if e != nil {
			return out, e
		}
		if fence <= 0 || generation <= 0 {
			return out, ErrConflict
		}
		profile, lifetime, device, lineage := s.profile, s.lifetime, "", ""
		if scope == "device" {
			profile, lifetime, device = "", "", s.device
		}
		if scope == "profile_lineage" {
			lineage = s.lineage
		}
		row, e = wc008DraftQuery(ctx, turn, usFenceDrainFence, fence, scope, s.account, s.member, profile, lifetime, device, lineage)
		if e != nil {
			return out, e
		}
		if e = row.Scan(&yes); e != nil {
			return out, usFenceDrainScanError(e)
		}
		if yes != 1 {
			return out, ErrConflict
		}
		// confirmed_generation may be stale relative to the current deny fence: it
		// is exactly the obsolete child's value being removed, never a new grant.
		row, e = wc008DraftQuery(ctx, turn, usFenceDrainDelete, s.family, scope, fence, generation)
		if e != nil {
			return out, e
		}
		if e = row.Scan(&yes); e != nil {
			return out, usFenceDrainScanError(e)
		}
		if yes != 1 {
			return out, ErrConflict
		}
		out.removed++
	}
	// A future/corrupt extra scope must block parent retirement, not be silently
	// skipped or turned into an unbounded scan/delete. The PK seek probes one row.
	row, e = wc008DraftQuery(ctx, turn, usFenceDrainRemaining, s.family)
	if e != nil {
		return out, e
	}
	if e = row.Scan(&yes); errors.Is(e, sql.ErrNoRows) {
		return out, turn.checkLifetimes(ctx)
	} else if e != nil {
		return out, e
	}
	return out, ErrConflict
}
func usFenceDrainScanError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	return err
}
