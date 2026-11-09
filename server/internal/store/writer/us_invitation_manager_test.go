package writer

import (
	"context"
)

// usTestInvitationManager is the ONLY implementation in this slice. It is
// deliberately test-only: Sol's invitation-manager recipe
// (us_invitation_authority.go) provides the production implementation and this
// type must never be exported, registered or used outside a test binary.
//
// Recheck records the exact dispatcher SITE named on the context, not a call
// count. A removed call therefore removes a site from the recording instead of
// silently shifting a dynamic "last call" selector.
type testInvitationManager struct {
	issuer string
	tiers  map[string]bool
	deny   map[usInvitationRecheckSite]bool
	sites  []usInvitationRecheckSite
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
	site, ok := usInvitationRecheckSiteOf(ctx)
	if ok {
		m.sites = append(m.sites, site)
		if m.deny[site] {
			return ErrDisclosureDenied
		}
	}
	return nil
}

// usTestInvitationManagerDenying builds a state/label-based manager whose
// Recheck denies at one exact named dispatcher site. It never counts calls.
func usTestInvitationManagerDenying(issuer string, site usInvitationRecheckSite, tiers ...string) *testInvitationManager {
	m := usTestInvitationManager(issuer, tiers...)
	m.deny = map[usInvitationRecheckSite]bool{site: true}
	return m
}

func (m *testInvitationManager) saw(site usInvitationRecheckSite) bool {
	for _, got := range m.sites {
		if got == site {
			return true
		}
	}
	return false
}

var _ usInvitationManager = (*testInvitationManager)(nil)
