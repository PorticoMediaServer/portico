package read

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// TestUSSecurityPlans proves the complete inert statement set Captures on the
// actual reader fixture, including the five exact octet shapes. Goldens are
// recorded by the desk's train run, not here; this test proves the definitions,
// native keys and EQP recipe are executable as written.
func TestUSSecurityPlans(t *testing.T) {
	f := newForegroundSource(t)
	definitions := USSecurityDefinitions()
	if len(usSecurityDefinitions()) != 12 {
		t.Fatal("US security statement inventory changed")
	}
	want := map[string]bool{
		usSecuritySession: true, usSecurityFence: true,
		usSecurityStatus: true, usSecurityStatusOctets: true,
		usSecurityMembers: true, usSecurityMembersOctets: true,
		usSecurityMember: true, usSecurityMemberOctets: true,
		usSecurityProfiles: true, usSecurityProfilesOctets: true,
		usSecurityDevices: true, usSecurityDevicesOctets: true,
	}
	for _, definition := range usSecurityDefinitions() {
		if !want[string(definition.ID)] {
			t.Fatalf("unexpected US security statement %s", definition.ID)
		}
		if definition.Kind != stmt.Read || definition.Owner != usSecurityOwner || len(definition.Cohorts) != 2 {
			t.Fatalf("incomplete US security definition %s", definition.ID)
		}
		delete(want, string(definition.ID))
	}
	if len(want) != 0 {
		t.Fatalf("missing US security statements: %v", want)
	}
	if len(definitions) != len(browseDefinitions())+12 {
		t.Fatalf("composed US security inventory: %d", len(definitions))
	}
	fixtureHash := foregroundFixtureHash(t)
	for _, statistics := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if statistics == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden := captureBrowse(t, f, definitions, statistics, fixtureHash)
		if _, err := stmt.New(definitions, golden.Receipt()); err != nil {
			t.Fatal("actual US security Capture cannot authorize its exact definitions", err)
		}
	}
}
