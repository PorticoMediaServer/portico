package writer

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// IA-10 owns exactly two new command IDs. Both are NORMAL Background turns:
// neither grants access, and neither may be downgraded or relabelled by a
// caller. The persisted result purpose is the configuration/retirement
// receipt-reclaim kind the group C owner rows constrain.
func TestIAReclaimDescriptors(t *testing.T) {
	for _, tc := range []struct {
		id     CommandID
		number CommandID
		name   string
	}{
		{IAConfigureReclaim, 105, "ia_configuration_receipt_reclaim"},
		{IARetireReclaim, 106, "ia_retirement_receipt_reclaim"},
	} {
		if tc.id != tc.number {
			t.Fatal("IA-10 changed its packet command ID", tc.name, tc.id)
		}
		d, err := Describe(tc.number)
		if err != nil || d.ID != tc.number || d.Name != tc.name || d.Durability != Normal {
			t.Fatal("IA-10 descriptor", tc.number, d, err)
		}
		class, err := commandClass(tc.number)
		if err != nil || class != governor.Background {
			t.Fatal("IA-10 class", tc.number, class, err)
		}
	}
}
