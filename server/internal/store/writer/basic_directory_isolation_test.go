package writer

import (
	"errors"
	"reflect"
	"testing"
)

func TestBasicDispatcherRejectsMixedDirectoryPurpose(t *testing.T) {
	w, e, authority, in, _ := basicDispatchFixture(t)
	disclosure := inventoryTestDisclosure(t, w, in.request.Scan)
	before := authority.Usage()
	for _, directory := range []CommandID{InventoryDirectoryDiscover, InventoryDirectoryEOF, InventoryDirectoryRefuse, InventoryTraversalComplete} {
		out, ack, err := w.effective.runCommand(t.Context(), w, nil, EnrichmentReceiptRequest{}, nil, false,
			&inventoryInvocation{disclosure: disclosure, request: in.request.Scan, basic: &in, directory: directory})
		if !errors.Is(err, ErrInvalid) || !reflect.ValueOf(out).IsZero() || !reflect.ValueOf(ack).IsZero() {
			t.Fatal("mixed domain escaped its closed arm", directory, out, ack, err)
		}
		if authority.Usage() != before || e.db.Stats().InUse != 0 {
			t.Fatal("invalid mixed domain acquired custody", directory, authority.Usage())
		}
	}
}
