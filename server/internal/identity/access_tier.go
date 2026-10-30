package identity

// Access tiers. The product has exactly three, ordered owner > admin > member,
// and this file is their single authority: every authorization helper in the
// server asks Grants or Administrative rather than comparing role strings.
//
//	owner  — the one account that holds server security, backups, connectivity
//	         policy, API keys and ownership transfer. Exactly one per server.
//	admin  — may manage libraries, metadata, invitations, member limits, device
//	         trust, diagnostics and any account below the admin tier. May not
//	         change server security, connectivity policy, backups, API keys, or
//	         any owner-only setting, and may not create or edit another admin.
//	member — a viewer with no administration.
//
// A Hosted token's role alone still never grants administration: ownerAuthorityTx
// and tierAuthorityTx in the HTTP layer re-check the live membership row.
const (
	TierOwner  = "owner"
	TierAdmin  = "admin"
	TierMember = "member"
)

// tierRank orders the tiers. An unknown role ranks below member so a future or
// corrupted value can never out-rank a real one.
func tierRank(role string) int {
	switch role {
	case TierOwner:
		return 3
	case TierAdmin:
		return 2
	case TierMember:
		return 1
	}
	return 0
}

// Grants is the single tier predicate. It answers whether an actor holding role
// satisfies a requirement expressed as the lowest tier that may proceed.
func Grants(role, needed string) bool {
	return tierRank(role) >= tierRank(needed) && tierRank(role) > 0
}

// Administrative reports whether a role administers the server at all, which is
// the owner tier and the admin tier. Library visibility follows it: an
// administrator sees every library rather than an allow list.
func Administrative(role string) bool { return Grants(role, TierAdmin) }

// ManagesTier reports whether an actor may create, edit or remove an account at
// the target tier. An admin manages strictly below itself, so admins never edit
// each other or the owner; the owner manages every tier but its own account,
// which only TransferDirectOwnership moves.
func ManagesTier(actor, target string) bool {
	if !Administrative(actor) {
		return false
	}
	if actor == TierOwner {
		return target != TierOwner
	}
	return tierRank(target) > 0 && tierRank(target) < tierRank(actor)
}

// ValidTier reports whether a role string may be stored on a membership row.
func ValidTier(role string) bool { return tierRank(role) > 0 }

// ValidDirectPassword publishes the direct account password policy so invitation
// acceptance (internal/access) creates an account under exactly the rule the
// owner's own member creation uses, rather than a second, weaker one.
func ValidDirectPassword(v string) bool { return directPassword(v) }
