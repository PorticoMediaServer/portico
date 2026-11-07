package writer

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func TestBasicCommandPurposesPreserveDistinctWireIdentities(t *testing.T) {
	for _, tc := range []struct {
		id      CommandID
		value   uint16
		purpose BasicPurpose
	}{
		{BasicAccept, 2, BasicPurposeAccept},
		{BasicStage, 21, BasicPurposeStage},
		{BasicSeal, 22, BasicPurposeSeal},
	} {
		purpose, err := basicPurposeForCommand(tc.id)
		descriptor, describeErr := Describe(tc.id)
		class, classErr := commandClass(tc.id)
		if err != nil || describeErr != nil || classErr != nil || uint16(tc.id) != tc.value || purpose != tc.purpose || descriptor.Name != string(tc.purpose) || descriptor.Durability != Normal || class != governor.ScanMinimum {
			t.Fatal(tc, purpose, descriptor, class, err, describeErr, classErr)
		}
	}
	for _, foreign := range []CommandID{0, InventoryAccept, InventoryClaim, SearchImageStage, BasicSeal + 1} {
		if purpose, err := basicPurposeForCommand(foreign); purpose != "" || !errors.Is(err, ErrInvalid) {
			t.Fatal("foreign kind acquired Basic purpose", foreign, purpose, err)
		}
	}
}
