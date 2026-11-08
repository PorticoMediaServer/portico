package writer

import "context"

// uoRootBinding is the only door from a domain command into the shared lifetime
// root's retained owner/receipt/payload custody. It mints no authority and adds
// no issuer: the sole non-test owner mint remains authorizeUSOwnerPolicy.
//
// Followers UO-02, UO-03, UO-05 and later copy exactly this rule: bind once at
// entry, check before the first retained lookup, and recheck before releasing a
// payload or digest. A NORMAL parent purpose uses its own current fence; a FULL
// security consumer must present US-01's current owner stamp for this SAME turn.
type uoRootBinding struct {
	security *usOwnerAuthorization
}

func bindUORoot(turn *commandTurn, security *usOwnerAuthorization) (out uoRootBinding, err error) {
	if turn == nil || turn.transaction == nil || !turn.transaction.begun || turn.transaction.done {
		return out, ErrInvalid
	}
	descriptor := turn.transaction.descriptor
	registered, err := Describe(descriptor.ID)
	if err != nil || registered != descriptor {
		return out, ErrInvalid
	}
	switch descriptor.Durability {
	case Normal:
		// Ordinary user roots owe no US security stamp. Their parent command
		// already owns the current profile/device fence.
		if security != nil {
			return out, ErrInvalid
		}
	case Full:
		// A FULL security consumer is exactly the case this stamp exists for.
		if security == nil || security.turn != turn {
			return out, ErrDisclosureDenied
		}
	default:
		return out, ErrInvalid
	}
	return uoRootBinding{security: security}, nil
}

func (b *uoRootBinding) check(ctx context.Context, turn *commandTurn) error {
	if b == nil || turn == nil || turn.transaction == nil || turn.transaction.done {
		return ErrDisclosureDenied
	}
	if turn.transaction.descriptor.Durability == Full {
		if b.security == nil || b.security.turn != turn {
			return ErrDisclosureDenied
		}
		// US-01 re-runs every independent current source check on this turn.
		return b.security.recheck(ctx, turn)
	}
	if turn.transaction.descriptor.Durability != Normal || b.security != nil {
		return ErrDisclosureDenied
	}
	return turn.checkLifetimes(ctx)
}
