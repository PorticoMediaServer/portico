package writer

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func basicCommandFixture() (basicStageRequest, basicSealRequest, CommandIdentity) {
	r, identity := inventoryRequestFixture()
	original := basicOriginalScope{r.scope, r.source, r.run}
	source := basicStreamFactStage{libraryID: r.scope.libraryID, assetID: 41, assetToken: "basic-asset", expectedEpoch: 17,
		runID: r.scope.runID, sourceID: r.source.id, incarnation: r.source.incarnation, objectID: "basic-object", objectRevision: "object:019",
		sourceGeneration: r.source.generation, policyRevision: r.run.policyRevision, witnessFingerprint: [32]byte{1},
		factRevision: 31, size: 42, modifiedNS: -11, detailVersion: 1, fingerprint: "probe-fingerprint"}
	begin := basicBeginEffects{source: source, shape: ScanShape{LibraryID: source.libraryID, OrdinaryOwners: 1,
		VersionAssetIDs: []int64{41}, Streams: []Stream{{Index: 0, Class: VideoStream}, {Index: 1, Class: AudioStream}}, RawChapterCount: 2,
		AuthoredParentIDs: []int64{31}}, counts: [9]int64{1, 2, 2, 1, 2, 1, 0, 0, 1}}
	stage := basicStageRequest{original: original, purpose: BasicPurposeStage, operation: basicStageBeginInput, begin: begin}
	source.changeID, source.inputRevision = 51, 7
	seal := basicSealRequest{original: original, purpose: BasicPurposeSeal, operation: basicSealStreamInput, source: source, changeRevision: 13}
	return stage, seal, identity
}

func TestBasicCommandPurposeAndClosedOperations(t *testing.T) {
	stageInput, sealInput, identity := basicCommandFixture()
	stage, err := newBasicStageCommand(identity, stageInput)
	if err != nil {
		t.Fatal(err)
	}
	seal, err := newBasicSealCommand(identity, sealInput)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		kind    CommandID
		request any
		digest  RequestDigest
	}{{BasicStage, stage.request, stage.digest}, {BasicSeal, seal.request, seal.digest}} {
		version := uint16(1)
		if test.kind == BasicStage {
			version = basicStageRequestVersion
		}
		digest, err := canonicalRequestDigest(test.kind, version, identity, test.request)
		alias, aliasErr := canonicalRequestDigest(BasicAccept, version, identity, test.request)
		if err != nil || aliasErr != nil || digest != test.digest || alias == test.digest {
			t.Fatal("distinct Basic kind or full request lost", err, aliasErr)
		}
	}
	stageInput.operation = 0
	if got, err := newBasicStageCommand(identity, stageInput); got != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal(got, err)
	}
	sealInput.operation = 255
	if got, err := newBasicSealCommand(identity, sealInput); got != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal(got, err)
	}
	stageInput.operation, stageInput.purpose = basicStageBeginInput, BasicPurposeSeal
	if got, err := newBasicStageCommand(identity, stageInput); got != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal(got, err)
	}
}

func TestBasicCommandCompleteFrozenRequest(t *testing.T) {
	stageInput, sealInput, identity := basicCommandFixture()
	stage, err := newBasicStageCommand(identity, stageInput)
	if err != nil {
		t.Fatal(err)
	}
	seal, err := newBasicSealCommand(identity, sealInput)
	if err != nil {
		t.Fatal(err)
	}
	// Reuse the established test-only native leaf walker. This is per-field
	// commitment coverage, not a combinatorial/native control matrix.
	for _, test := range []struct {
		kind    CommandID
		request any
		digest  RequestDigest
		fresh   func() any
	}{
		{BasicStage, &stageInput, stage.digest, func() any { s, _, _ := basicCommandFixture(); return &s }},
		{BasicSeal, &sealInput, seal.digest, func() any { _, s, _ := basicCommandFixture(); return &s }},
	} {
		leaves := 0
		inventoryTestLeaves(reflect.ValueOf(test.request).Elem(), nil, func(path []int, _ reflect.Value) {
			leaves++
			changed := reflect.ValueOf(test.fresh()).Elem()
			leaf := inventoryTestAt(changed, path)
			switch leaf.Kind() {
			case reflect.String:
				leaf.SetString(leaf.String() + "+")
			case reflect.Int, reflect.Int64:
				leaf.SetInt(leaf.Int() + 1)
			case reflect.Float64:
				leaf.SetFloat(leaf.Float() + 0.5)
			case reflect.Uint8:
				leaf.SetUint(leaf.Uint() ^ 1)
			case reflect.Bool:
				leaf.SetBool(!leaf.Bool())
			default:
				t.Fatal("uncovered Basic native field", leaf.Type())
			}
			digest, err := canonicalRequestDigest(test.kind, 1, identity, changed.Interface())
			if err != nil || digest == test.digest {
				t.Fatal("unhashed Basic field", path, err)
			}
		})
		if leaves < 70 {
			t.Fatal("sparse original Basic witness", leaves)
		}
	}
	stageRef, err := stage.receiptRequest()
	if err != nil || stageRef.Purpose != BasicPurposeStage || stageRef.Scan.Receipt.Identity != identity || stageRef.Scan.Receipt.RequestDigest != stage.digest {
		t.Fatal(stageRef, err)
	}
	sealRef, err := seal.receiptRequest()
	if err != nil || sealRef.Purpose != BasicPurposeSeal || sealRef.Scan.Receipt.RequestDigest != seal.digest {
		t.Fatal(sealRef, err)
	}
	stageInput.original.source.resolvedRoot[0] = '!'
	stageInput.begin.shape.VersionAssetIDs[0] = 99
	stageInput.begin.shape.Streams[0].Index = 99
	stageInput.begin.shape.AuthoredParentIDs[0] = 99
	if string(stage.request.original.source.resolvedRoot) != "/media" || stage.request.begin.shape.VersionAssetIDs[0] != 41 || stage.request.begin.shape.Streams[0].Index != 0 || stage.request.begin.shape.AuthoredParentIDs[0] != 31 {
		t.Fatal("Basic request retained caller buffers")
	}
	if again, err := stage.receiptRequest(); err != nil || again != stageRef {
		t.Fatal("saved original reference changed", again, err)
	}
	if got, err := (*basicSealCommand)(nil).receiptRequest(); err == nil || got != (BasicReceiptRequest{}) {
		t.Fatal("nil command exposed receipt", got, err)
	}
}

func TestBasicCommandCompleteBudgetAndNativeRootCause(t *testing.T) {
	stage, _, identity := basicCommandFixture()
	stage.original.source.resolvedRoot = []byte{0xff}
	if got, err := newBasicStageCommand(identity, stage); got != nil || !errors.Is(err, ErrFilenameUnrepresentable) {
		t.Fatal(got, err)
	}
	stage.original.source.resolvedRoot = []byte{'/', 0xff, 0}
	if got, err := newBasicStageCommand(identity, stage); got != nil || !errors.Is(err, ErrInvalid) || errors.Is(err, ErrFilenameUnrepresentable) {
		t.Fatal(got, err)
	}
	stage.begin.source.fingerprint = strings.Repeat("x", maxCanonicalRequestBytes+1)
	if got, err := newBasicStageCommand(identity, stage); got != nil || !errors.Is(err, ErrCap) {
		t.Fatal("complete byte budget did not precede root scanning", got, err)
	}
}
