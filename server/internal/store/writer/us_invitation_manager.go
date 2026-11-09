package writer

import "context"

// usInvitationManager is the only authority input of the 550-552 invitation
// dispatcher. It is deliberately narrow: the dispatcher never re-derives a
// role, permission or session stamp of its own. Issuer names the already
// verified actor account; ManagesTier answers the same-tier rule for a role
// read in the same frame; Recheck proves the current source again and a denial
// replaces every body outcome.
//
// Sol's invitation-manager recipe (us_invitation_authority.go) provides the
// production implementation; this interface is the only authority input of the
// 550-552 dispatcher. Until that recipe lands, the only implementation in this
// slice is the test-only constructor in us_invitation_manager_test.go.
type usInvitationManager interface {
	// Issuer returns the verified issuer account id. The dispatcher compares it
	// with the frozen request's actor/created_by before any write and refuses a
	// mismatch with no retained receipt.
	Issuer() string
	// ManagesTier reports whether the proven manager may manage an invitation
	// carrying role. CREATE passes the requested tier; EDIT passes the row's
	// CURRENT tier and the requested tier; REVOKE passes the current target tier.
	ManagesTier(role string) bool
	// Recheck re-proves current authority in the same frame. The dispatcher calls
	// it before any retained payload is read or decoded and again at final
	// release.
	Recheck(ctx context.Context, turn *commandTurn) error
}
