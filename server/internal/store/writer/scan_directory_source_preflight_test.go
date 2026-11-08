package writer

import (
	"errors"
	"testing"
)

func TestScanDirectorySourcePreflightCannotBorrowLifecyclePoint(t *testing.T) {
	for _, purpose := range []CommandID{InventoryDirectoryDiscover, InventoryDirectoryEOF, InventoryDirectoryRefuse, InventoryTraversalComplete} {
		turn := &commandTurn{transaction: &transaction{ctx: t.Context(), descriptor: Descriptor{ID: purpose}}}
		value, err := readScanByteLengths(t.Context(), turn, scanRunByteLengths, "unrelated-run")
		var policy *PolicyError
		if !errors.Is(err, ErrInvalid) || errors.As(err, &policy) || value != (scanByteVector{}) {
			t.Fatal("Directory borrowed a lifecycle byte reader", purpose, value, err)
		}
	}
}
