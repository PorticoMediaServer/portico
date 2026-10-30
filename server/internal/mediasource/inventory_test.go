package mediasource

import "testing"

func TestInventoryRevisionSeparatesSourceIncarnationAndObservation(t *testing.T) {
	original := InventoryEvidence{Kind: "local", Scope: "source:incarnation", Object: "dev:inode", Size: 10, ModifiedNS: 11, ChangeToken: "ctime:12"}
	first, err := original.Revision()
	if err != nil || len(first) != 64 {
		t.Fatal(first, err)
	}
	same, _ := original.Revision()
	if same != first {
		t.Fatal("unstable revision")
	}
	for _, change := range []func(*InventoryEvidence){func(e *InventoryEvidence) { e.Scope = "source:new-root" }, func(e *InventoryEvidence) { e.Object = "other-inode" }, func(e *InventoryEvidence) { e.Size++ }, func(e *InventoryEvidence) { e.ModifiedNS++ }, func(e *InventoryEvidence) { e.ChangeToken = "changed" }, func(e *InventoryEvidence) { e.ProviderVersion = "provider-version" }} {
		e := original
		change(&e)
		revision, err := e.Revision()
		if err != nil || revision == first {
			t.Fatal("revision did not change", e, err)
		}
	}
	invalid := original
	invalid.Size = -1
	if _, err := invalid.Revision(); err == nil {
		t.Fatal("negative size accepted")
	}
}
