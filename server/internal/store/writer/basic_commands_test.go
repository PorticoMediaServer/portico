package writer

import (
	"errors"
	"testing"
)

func basicPrivateCommandFixture() (basicStageRequest, basicSealRequest, CommandIdentity) {
	in, id := inventoryRequestFixture()
	source := basicStreamFactStage{changeID: 51, inputRevision: 7, libraryID: in.scope.libraryID, assetID: 41, expectedEpoch: 17, assetToken: "basic-asset",
		runID: in.scope.runID, sourceID: in.source.id, incarnation: in.source.incarnation, objectID: "basic-object", objectRevision: "object:019", sourceGeneration: in.source.generation,
		policyRevision: in.run.policyRevision, witnessFingerprint: [32]byte{1}, factRevision: 31, size: 42, modifiedNS: -11, detailVersion: 1, fingerprint: "native-probe"}
	original := basicOriginalScope{in.scope, in.source, in.run}
	return basicStageRequest{original: original, purpose: BasicPurposeStage, operation: basicStageFactInput, fact: source, changeRevision: 13},
		basicSealRequest{original: original, purpose: BasicPurposeSeal, operation: basicSealStreamInput, source: source, changeRevision: 13}, id
}

func TestBasicPrivateCommandsFreezeOriginalPurposeAndReferences(t *testing.T) {
	stage, seal, identity := basicPrivateCommandFixture()
	first, err := newBasicStageCommand(identity, stage)
	if err != nil {
		t.Fatal(err)
	}
	last, err := newBasicSealCommand(identity, seal)
	if err != nil {
		t.Fatal(err)
	}
	q, err := first.receiptRequest()
	if err != nil || q.Purpose != BasicPurposeStage || q.Scan.Receipt.Identity != identity || q.Scan.Receipt.RequestDigest != first.digest {
		t.Fatal(q, err)
	}
	sq, err := last.receiptRequest()
	if err != nil || sq.Purpose != BasicPurposeSeal || sq.Scan.Receipt.RequestDigest != last.digest || first.digest == last.digest {
		t.Fatal(sq, err)
	}
	stage.original.source.resolvedRoot[0] = '!'
	stage.fact.objectRevision = "changed"
	if string(first.request.original.source.resolvedRoot) != "/media" || first.request.fact.objectRevision != "object:019" {
		t.Fatal("caller retained original backing")
	}
	if again, err := first.receiptRequest(); err != nil || again != q {
		t.Fatal(again, err)
	}
	changed, _, identity := basicPrivateCommandFixture()
	changed.changeRevision++
	other, err := newBasicStageCommand(identity, changed)
	if err != nil || other.digest == first.digest {
		t.Fatal("independent change revision was unhashed", err)
	}
}

func TestBasicPrivateCommandVariantsAndPathPolicyAreClosed(t *testing.T) {
	for _, operation := range []basicStageOperation{0, 1, 255} {
		in, _, identity := basicPrivateCommandFixture()
		in.operation = operation
		if got, err := newBasicStageCommand(identity, in); got != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal(got, err)
		}
	}
	in, _, identity := basicPrivateCommandFixture()
	in.purpose = BasicPurposeSeal
	if got, err := newBasicStageCommand(identity, in); got != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal(got, err)
	}
	for _, tc := range []struct {
		root []byte
		want error
	}{{[]byte{0xff}, ErrFilenameUnrepresentable}, {[]byte{'/', 0}, ErrInvalid}} {
		in, _, identity := basicPrivateCommandFixture()
		in.original.source.resolvedRoot = tc.root
		got, err := newBasicStageCommand(identity, in)
		if got != nil || !errors.Is(err, tc.want) {
			t.Fatal(got, err)
		}
	}
	q := BasicReceiptRequest{Purpose: BasicPurposeAccept}
	if _, err := (&basicInvocation{request: q}).validate(); !errors.Is(err, ErrInvalid) {
		t.Fatal("activation borrowed Stage/Seal", err)
	}
}
