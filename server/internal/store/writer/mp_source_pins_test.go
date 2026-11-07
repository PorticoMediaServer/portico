package writer

import "testing"

func TestMPSourcePinsDraftActualExitTuple(t *testing.T) {
	r := mpSourcePinEffect{media: "media", execution: "execution", asset: "asset", mediaIncarnation: [16]byte{4}, revision: 6, generation: 2, state: "stopped", kind: mpInventoryPin, released: true, physicalPending: true, physicalExit: mpPhysicalExitFacts{retiredGeneration: 2, exitedGeneration: 2, exitedMS: 90, retirementRequest: "retire", exitRequest: "retire", supervisorIncarnation: [16]byte{1}}}

	if !r.physicalExit.valid(r.generation, r.physicalPending) {
		t.Fatal("valid native exit tuple")
	}
	bad := r
	bad.physicalExit.exitRequest = "another-retirement"
	if bad.physicalExit.valid(bad.generation, bad.physicalPending) {
		t.Fatal("cross-retirement exit proof")
	}
	bad = r
	bad.physicalExit.supervisorIncarnation = [16]byte{}
	if bad.physicalExit.valid(bad.generation, bad.physicalPending) {
		t.Fatal("missing supervisor")
	}
}
