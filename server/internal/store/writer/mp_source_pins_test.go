package writer

import "testing"

func TestMPSourcePinsDraftActualExitTuple(t *testing.T) {
	h := mpPrepReceiptHeader{identity: mpDraftIdentity(), request: RequestDigest{3}, kind: MPSourceInventoryRelease, appliedMS: 100}
	r := mpSourcePinEffect{media: "media", execution: "execution", asset: "asset", mediaIncarnation: [16]byte{4}, revision: 6, generation: 2, state: "stopped", kind: mpInventoryPin, released: true, physicalPending: true, physicalExit: mpPhysicalExitFacts{retiredGeneration: 2, exitedGeneration: 2, exitedMS: 90, retirementRequest: "retire", exitRequest: "retire", supervisorIncarnation: [16]byte{1}}}
	d, err := mpSourcePinsResultDigest(h, r)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*mpSourcePinEffect){func(r *mpSourcePinEffect) { r.physicalExit.supervisorIncarnation[0]++ }, func(r *mpSourcePinEffect) { r.physicalExit.exitedMS++ }, func(r *mpSourcePinEffect) {
		r.physicalExit.retirementRequest = "next"
		r.physicalExit.exitRequest = "next"
	}, func(r *mpSourcePinEffect) { r.physicalPending = false }} {
		q := r
		change(&q)
		next, e := mpSourcePinsResultDigest(h, q)
		if e != nil || d == next {
			t.Fatal("unbound actual exit", e)
		}
	}
	bad := r
	bad.physicalExit.exitRequest = "another-retirement"
	if _, err = mpSourcePinsResultDigest(h, bad); err == nil {
		t.Fatal("cross-retirement exit proof")
	}
	bad = r
	bad.physicalExit.supervisorIncarnation = [16]byte{}
	if _, err = mpSourcePinsResultDigest(h, bad); err == nil {
		t.Fatal("missing supervisor")
	}
}
