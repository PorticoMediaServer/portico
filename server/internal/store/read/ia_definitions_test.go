package read

import (
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestIAHarnessComposition(t *testing.T) {
	defs := IAHarnessDefinitions()
	if len(defs) != 33 {
		t.Fatal("closed IA definition inventory changed", len(defs))
	}
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
	var shared []stmt.Definition
	for _, family := range [][]stmt.Definition{IAAudioHarnessDefinitions(), IATVDefinitions(), IAExtraHarnessDefinitions()} {
		for _, d := range family {
			if d.ID == iaLinkOctetsID {
				d.Cohorts = nil
				shared = append(shared, d)
			}
		}
	}
	if len(shared) != 3 || !reflect.DeepEqual(shared[0], shared[1]) || !reflect.DeepEqual(shared[0], shared[2]) {
		t.Fatal("shared IA point drifted between families")
	}
}
