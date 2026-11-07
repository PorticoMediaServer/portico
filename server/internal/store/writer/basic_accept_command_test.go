package writer

import (
	"database/sql"
	"errors"
	"testing"
)

func TestBasicAcceptCommandFreezesCompleteNativePayload(t *testing.T) {
	stage, seal, identity := basicCommandFixture()
	s := seal.source
	analysis, err := prepareBasicObjectAnalysis(s, stage.original.scope.libraryKey, "actual-probe-algorithm", `["probe"]`, `["probe"]`, false)
	if err != nil {
		t.Fatal(err)
	}
	in := basicAcceptRequest{original: stage.original, purpose: BasicPurposeAccept, operation: basicAcceptFirstOrdinaryLeaf, activation: basicActivationEffects{source: s, changeRevision: 13, familyMask: 511, shapeID: 61, fileSizeRevision: 1, invalidationRevision: 1}, entityID: 31, duration: 100, height: 1080, analysis: *analysis, oldScalars: []basicFirstOldScalar{{field: 4, kind: 3, text: sql.NullString{String: "old", Valid: true}}}}
	command, err := newBasicAcceptCommand(identity, in)
	if err != nil {
		t.Fatal(err)
	}
	q, err := command.receiptRequest()
	if err != nil {
		t.Fatal(err)
	}
	in.original.source.resolvedRoot[0] = '!'
	in.oldScalars[0].text.String = "mutated"
	if command.request.oldScalars[0].text.String != "old" || command.request.original.source.resolvedRoot[0] == '!' {
		t.Fatal("caller storage retained")
	}
	again, err := canonicalRequestDigest(BasicAccept, 1, identity, command.request)
	if err != nil || again != q.Scan.Receipt.RequestDigest {
		t.Fatal(again, err)
	}
	wrong := command.request
	wrong.purpose = BasicPurposeStage
	if got, err := newBasicAcceptCommand(identity, wrong); got != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal(got, err)
	}
	wrong = command.request
	wrong.oldScalars = []basicFirstOldScalar{{field: 3, kind: 1, integer: sql.NullInt64{Int64: 1, Valid: true}}}
	if got, err := newBasicAcceptCommand(identity, wrong); got != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("unowned OLD captured", got, err)
	}
	wrong = command.request
	wrong.analysis.completedJSON = `["probe","local_metadata"]`
	wrong.analysis.policyJSON = wrong.analysis.completedJSON
	if got, err := newBasicAcceptCommand(identity, wrong); got != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("missing local evidence admitted", got, err)
	}
}
