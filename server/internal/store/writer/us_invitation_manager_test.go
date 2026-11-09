package writer

import (
	"context"
)

// usTestInvitationManager is the ONLY implementation in this slice. It is
// deliberately test-only: Sol's invitation-manager recipe
// (us_invitation_authority.go) provides the production implementation and this
// type must never be exported, registered or used outside a test binary.
type testInvitationManager struct {
	issuer  string
	tiers   map[string]bool
	recheck func(context.Context, *commandTurn, int) error
	calls   int
}

func usTestInvitationManager(issuer string, tiers ...string) *testInvitationManager {
	m := &testInvitationManager{issuer: issuer, tiers: make(map[string]bool, len(tiers))}
	for _, tier := range tiers {
		m.tiers[tier] = true
	}
	return m
}

func (m *testInvitationManager) Issuer() string { return m.issuer }

func (m *testInvitationManager) ManagesTier(role string) bool {
	return m != nil && m.tiers[role]
}

func (m *testInvitationManager) Recheck(ctx context.Context, turn *commandTurn) error {
	if m == nil {
		return ErrDisclosureDenied
	}
	m.calls++
	if m.recheck != nil {
		return m.recheck(ctx, turn, m.calls)
	}
	return nil
}

// usTestInvitationManagerFailing builds a manager whose Recheck denies at one
// exact call number. It exercises entry, before-payload and release denial with
// the same seam the production recipe will implement.
func usTestInvitationManagerFailing(issuer string, failAt int, tiers ...string) *testInvitationManager {
	m := usTestInvitationManager(issuer, tiers...)
	m.recheck = func(_ context.Context, _ *commandTurn, call int) error {
		if call == failAt {
			return ErrDisclosureDenied
		}
		return nil
	}
	return m
}

var _ usInvitationManager = (*testInvitationManager)(nil)
