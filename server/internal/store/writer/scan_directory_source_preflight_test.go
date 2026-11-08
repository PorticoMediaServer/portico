package writer

import "testing"

func TestScanDirectorySourcePreflightCannotBorrowLifecyclePoint(t *testing.T) {
	for _, purpose := range []CommandID{InventoryDirectoryDiscover, InventoryDirectoryEOF, InventoryDirectoryRefuse, InventoryTraversalComplete} {
		turn := &commandTurn{transaction: &transaction{descriptor: Descriptor{ID: purpose}}}
		value, err := readScanByteLengths(t.Context(), turn, scanRunByteLengths, "unrelated-run")
		if err != ErrInvalid || value != (scanByteVector{}) {
			t.Fatal("Directory borrowed a lifecycle byte reader", purpose, value, err)
		}
	}
}
