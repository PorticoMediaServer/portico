package writer

import "context"

// usInvitationManager is the only authority input of the 550-552 invitation
// dispatcher. It is deliberately narrow: the dispatcher never re-derives a
// role, permission or session stamp of its own. Issuer is the already verified
// CURRENT issuer (the same eleven-field authority the entry witness carries);
// ManagesTier answers the same-tier rule for a role read in the same frame;
// Recheck proves the current source again and a denial replaces every body
// outcome.
//
// Sol's invitation-manager recipe (us_invitation_authority.go) provides the
// production implementation; this interface is the only authority input of the
// 550-552 dispatcher. Until that recipe lands, the only implementation in this
// slice is the test-only constructor in us_invitation_manager_test.go.
type usInvitationManager interface {
	// Issuer returns the verified CURRENT issuer. Its six stable columns
	// (server/account/member incarnation/profile/profile lifetime/installation)
	// address the retained owner row; family, token and generations are never an
	// owner predicate because a sign-in may renew them. The account is also
	// compared with a frozen request's actor/created_by on FRESH paths.
	Issuer() usPolicyIssuer
	// ManagesTier reports whether the proven manager may manage an invitation
	// carrying role. CREATE passes the requested tier; EDIT passes the row's
	// CURRENT tier and the requested tier; REVOKE passes the current target tier.
	ManagesTier(role string) bool
	// Recheck re-proves current authority in the same frame. The dispatcher calls
	// it before any retained payload is read or decoded and again at final
	// release.
	Recheck(ctx context.Context, turn *commandTurn) error
}
