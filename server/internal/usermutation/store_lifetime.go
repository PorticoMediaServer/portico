package usermutation

// P2-62 receipt lifetime binding.
//
// A receipt is only a replay of the request that produced it while the same
// owner, the same profile lifetime, the same installation lifetime and the same
// mutation root are still current. Those values are resolved from the accepting
// transaction, never supplied by the caller, so a domain route that knows
// nothing about lifetimes cannot accidentally grant an old result to a different
// one. Existing non-generic domain receipts stay rootless: their installation
// and root bindings are empty, and an empty binding never matches a live root.
//
// The one root table belongs to P2-51's receipt store; there is no second
// receipt, result or acknowledgement store here.

import (
	"context"
	"database/sql"
	"errors"
)

// ErrRootGone means the request names a mutation root that is not the live owner
// for the authenticated scope: it was deleted, its profile lifetime or
// membership incarnation rotated, or the family, profile or installation no
// longer match. A request under a gone root must not apply and its old result
// must not be returned, so callers roll the transaction back instead of treating
// it as an absent target.
var ErrRootGone = errors.New("mutation root is gone")

// ValidRootID applies the 22-character root grammar at protocol boundaries.
func ValidRootID(v string) bool {
	if len(v) != 22 {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// lifetime is what the receiving transaction says the request is bound to.
// profileLive is false for a profile row the server keeps only as a sanitized
// erased record, which denies replay without pretending the lifetime matches.
type lifetime struct {
	profile      string
	profileLive  bool
	account      string
	installation string
	root         string
	rootState    string
}

// resolveLifetime reads the exact current lifetimes for the binding. It uses the
// receipt store's own reader so a Check/Save/Lookup inside a transaction sees
// that transaction's rows, and opens at most three primary-key reads.
func resolveLifetime(ctx context.Context, q reader, b Binding) (lifetime, error) {
	l := lifetime{profileLive: true, root: b.RootID}
	if b.Authority == "local" {
		var profile string
		var deleted int
		err := q.QueryRowContext(ctx, `SELECT lifetime,deleted FROM direct_profiles WHERE account_id=? AND id=?`, b.AccountID, b.ProfileID).Scan(&profile, &deleted)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// A local effect cannot acquire an empty synthetic lifetime.
			l.profileLive = false

		case err != nil:
			return l, err
		case deleted != 0:
			// An erased profile keeps a sanitized row for history. It has no
			// live lifetime, so nothing committed under its old one replays.
			l.profileLive = false
		default:
			l.profile = profile
		}
		var incarnation string
		var disabled int
		err = q.QueryRowContext(ctx, `SELECT incarnation,disabled FROM direct_memberships WHERE account_id=?`, b.AccountID).Scan(&incarnation, &disabled)
		if errors.Is(err, sql.ErrNoRows) {
			l.profileLive = false
		} else if err != nil {
			return l, err
		}
		l.account = incarnation
		if disabled != 0 {
			l.profileLive = false
		}
	}
	if b.RootID == "" {
		return l, nil
	}
	if err := bindRoot(ctx, q, b, &l); err != nil {
		return l, err
	}
	return l, nil
}

// bindRoot proves the root is the live owner for this exact scope. Any mismatch
// is "gone": the receipt that names it must not apply and must not be replayed.
func bindRoot(ctx context.Context, q reader, b Binding, l *lifetime) error {
	if !l.profileLive {
		return ErrRootGone
	}
	var account, profile, accountIncarnation, profileIncarnation, family, installation, state string
	err := q.QueryRowContext(ctx, `SELECT account_id,profile_id,account_incarnation,profile_incarnation,family_id,installation_incarnation,state FROM user_mutation_roots WHERE id=?`, b.RootID).Scan(&account, &profile, &accountIncarnation, &profileIncarnation, &family, &installation, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRootGone
	}
	if err != nil {
		return err
	}
	if state != "active" && state != "draining" {
		return ErrRootGone
	}
	if account != b.AccountID || profile != b.ProfileID || family != b.FamilyID || profileIncarnation != l.profile || accountIncarnation != l.account {
		return ErrRootGone
	}
	var retired int
	if err := q.QueryRowContext(ctx, `SELECT retired FROM identity_installations WHERE incarnation=?`, installation).Scan(&retired); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRootGone
		}
		return err
	}
	if retired != 0 {
		return ErrRootGone
	}
	l.installation = installation
	l.rootState = state
	return nil
}

// bound reports whether a stored receipt was committed under these exact
// lifetimes. Empty stored bindings only match empty current ones, so a rootless
// domain receipt can never be reused as a root's result and a receipt from
// before a lifetime rotation can never be replayed.
func bound(r *stored, id int64, b Binding, l lifetime) bool {
	return l.profileLive && r.scope == id && r.profile == b.ProfileID && r.family == b.FamilyID &&
		r.profileIncarnation == l.profile && r.rootID == l.root && r.installationIncarnation == l.installation
}
