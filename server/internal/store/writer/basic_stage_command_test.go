package writer

import (
	"errors"
	"testing"
)

func basicStageFactCommandFixture() (basicStageRequest, CommandIdentity) {
	begin, seal, identity := basicCommandFixture()
	return basicStageRequest{original: begin.original, purpose: BasicPurposeStage, operation: basicStageFactInput,
		fact: seal.source, changeRevision: seal.changeRevision}, identity
}

func TestBasicStageFactClosedRequestAndRevisions(t *testing.T) {
	if basicStageBeginInput == 0 || basicStageFactInput == 0 || basicStageBeginInput == basicStageFactInput || basicSealStreamInput == 0 {
		t.Fatal("closed operations must have distinct nonzero identities")
	}
	for _, tc := range []struct {
		name   string
		change func(*basicStageRequest)
	}{
		{"zero_operation", func(r *basicStageRequest) { r.operation = 0 }},
		{"unknown_nonzero_operation", func(r *basicStageRequest) { r.operation = 255 }},
		{"empty_purpose", func(r *basicStageRequest) { r.purpose = "" }},
		{"foreign_purpose", func(r *basicStageRequest) { r.purpose = BasicPurposeSeal }},
		{"unallocated_change", func(r *basicStageRequest) { r.fact.changeID = 0 }},
		{"unallocated_input", func(r *basicStageRequest) { r.fact.inputRevision = 0 }},
		{"zero_change_revision", func(r *basicStageRequest) { r.changeRevision = 0 }},
		{"negative_change_revision", func(r *basicStageRequest) { r.changeRevision = -1 }},
		{"inactive_begin", func(r *basicStageRequest) { r.begin.shape.Streams = []Stream{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, identity := basicStageFactCommandFixture()
			tc.change(&in)
			if got, err := newBasicStageCommand(identity, in); got != nil || !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid closed request produced command %v, error %v", got, err)
			}
		})
	}
}

func TestBasicStageBeginRefusesInactiveFactAndCallerRevisions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*basicStageRequest)
	}{
		{"inactive_fact", func(r *basicStageRequest) { r.fact.witnessFingerprint[0] = 1 }},
		{"caller_change_revision", func(r *basicStageRequest) { r.changeRevision = 1 }},
		{"caller_change_id", func(r *basicStageRequest) { r.begin.source.changeID = 51 }},
		{"caller_input_revision", func(r *basicStageRequest) { r.begin.source.inputRevision = 7 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, _, identity := basicCommandFixture()
			tc.change(&in)
			if got, err := newBasicStageCommand(identity, in); got != nil || !errors.Is(err, ErrInvalid) {
				t.Fatalf("BEGIN accepted caller allocation or inactive fact: %v, %v", got, err)
			}
		})
	}
}

func TestBasicStageFactFreezesExactRevisionFactAndReference(t *testing.T) {
	in, identity := basicStageFactCommandFixture()
	wantFact, wantRevision := in.fact, in.changeRevision
	// The CJR revision is independent of both the input and inventory run.
	if wantRevision == in.fact.inputRevision || wantRevision == in.original.run.revision {
		t.Fatal("fixture aliases independent revisions")
	}
	command, err := newBasicStageCommand(identity, in)
	if err != nil {
		t.Fatal(err)
	}
	if !command.valid || command.request.operation != basicStageFactInput || command.request.fact != wantFact || command.request.changeRevision != wantRevision {
		t.Fatal("StageFact lost its exact closed payload")
	}
	wantRef := BasicReceiptRequest{Purpose: BasicPurposeStage, Scan: ScanReceiptRequest{
		RunID: in.original.scope.runID, LibraryID: in.original.scope.libraryID, LibraryKey: in.original.scope.libraryKey,
		Receipt: ReceiptReference{Identity: identity, RequestDigest: command.digest},
	}}
	if got, err := command.receiptRequest(); err != nil || got != wantRef {
		t.Fatal("incomplete original StageFact reference", got, err)
	}

	// Two targeted identity falsifiers: a different CJR revision and a
	// different fact must never resolve through the saved request reference.
	for _, tc := range []struct {
		name   string
		change func(*basicStageRequest)
	}{
		{"change_revision", func(r *basicStageRequest) { r.changeRevision++ }},
		{"fact_fingerprint", func(r *basicStageRequest) { r.fact.fingerprint += "+" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed, identity := basicStageFactCommandFixture()
			tc.change(&changed)
			other, err := newBasicStageCommand(identity, changed)
			if err != nil {
				t.Fatal(err)
			}
			if other.digest == command.digest {
				t.Fatal("distinct StageFact payload shared a request digest")
			}
			if ref, err := other.receiptRequest(); err != nil || ref == wantRef {
				t.Fatal("distinct StageFact payload shared the saved reference", ref, err)
			}
		})
	}

	in.original.source.resolvedRoot[0] = '!'
	in.original.scope.runID = "other-run"
	in.fact.factRevision++
	in.fact.fingerprint = "other-probe"
	in.fact.witnessFingerprint[0]++
	in.changeRevision++
	if command.request.fact != wantFact || command.request.changeRevision != wantRevision || string(command.request.original.source.resolvedRoot) != "/media" {
		t.Fatal("StageFact retained mutable caller input")
	}
	if digest, err := canonicalRequestDigest(BasicStage, basicStageRequestVersion, identity, command.request); err != nil || digest != command.digest {
		t.Fatal("frozen StageFact no longer matches its committed digest", err)
	}
	if got, err := command.receiptRequest(); err != nil || got != wantRef {
		t.Fatal("caller mutation changed the saved original reference", got, err)
	}
	for _, invalid := range []*basicStageCommand{nil, {}} {
		if got, err := invalid.receiptRequest(); got != (BasicReceiptRequest{}) || !errors.Is(err, ErrInvalid) {
			t.Fatal("unconstructed StageFact exposed a reference", got, err)
		}
	}
}
