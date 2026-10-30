package identity

import "testing"

// CD-03: every published ladder is ordered ascending by admission age. Clients
// (client-core parseRatingSystems) refuse a whole ladder whose minimumAge ever
// decreases, which left US TV ratings unusable for a restriction ceiling.
func TestRatingSystemsAscendByAdmissionAge(t *testing.T) {
	systems := RatingSystems()
	if len(systems) == 0 {
		t.Fatal("no rating systems")
	}
	for _, system := range systems {
		if len(system.Values) == 0 {
			t.Fatalf("%s: no values", system.ID)
		}
		seen := map[string]bool{}
		for i, value := range system.Values {
			if seen[value.Code] {
				t.Fatalf("%s: duplicate code %q", system.ID, value.Code)
			}
			seen[value.Code] = true
			if i > 0 && value.MinimumAge < system.Values[i-1].MinimumAge {
				t.Fatalf("%s: %s (%d) follows %s (%d); the ladder must ascend", system.ID, value.Code, value.MinimumAge, system.Values[i-1].Code, system.Values[i-1].MinimumAge)
			}
		}
	}
}
