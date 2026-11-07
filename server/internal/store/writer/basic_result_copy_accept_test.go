package writer

import (
	"bytes"
	"crypto/sha256"
	"math"
	"strings"
	"testing"
)

func basicAcceptFixture(t *testing.T) (CommandIdentity, RequestDigest, BasicResult) {
	i, q, v := basicResultFixture(t)
	v.Purpose = BasicPurposeAccept
	v.Continuation.AfterEntity = 31
	v.ResultCount = 1
	v.Members = []InventoryMember{{ObjectID: "basic-object", LibraryID: 7, SubjectID: 31, AssetID: 41, AcceptedAssetRevision: 18}}
	return i, q, v
}

func TestBasicResultSelectedCopyCheckpoints(t *testing.T) {
	i, q, v := basicResultFixture(t)
	// Typed source projections, including every supplied copy checkpoint, then
	// native numeric family transitions. No parallel encoder or expected wire.
	seen := map[ResultDigest]bool{}
	for _, c := range []BasicContinuation{
		{NextFamily: 2, NextCopyID: 1, AfterEntity: 31},
		{NextFamily: 2, NextCopyPhase: 1, NextCopyID: 14, NextCopyAux: 1, AfterEntity: 31},
		{NextFamily: 5, AfterEntity: 31}, {AfterEntity: 31},
		{NextFamily: 2, NextCopyPhase: 1, AfterEntity: 31},
		{NextFamily: 3, NextCopyID: 1, AfterEntity: 31},
		{NextFamily: 3, NextCopyPhase: 1, NextCopyID: 14, NextCopyAux: 1, AfterEntity: 31},
		{NextFamily: 4, AfterEntity: 31},
		{NextFamily: 5, NextCopyPhase: 1, NextCopyID: 14, NextCopyAux: 1, AfterEntity: 31},
		{NextFamily: 6, AfterEntity: 31}, {NextFamily: 7, NextCopyID: 1, AfterEntity: 31},
		{NextFamily: 8, NextCopyID: 1, AfterEntity: 31}, {NextFamily: 9, AfterEntity: 31},
	} {
		v.Continuation = c
		d, err := basicResultDigest(i, q, v)
		if err != nil || seen[d] {
			t.Fatalf("checkpoint %+v: %v", c, err)
		}
		seen[d] = true
		h := &recordedResultHash{Hash: sha256.New()}
		e := requestEncoder{h: h}
		if err := encodeCanonicalResult(&e, i, q, v); err != nil || !bytes.Equal(h.Sum(nil), d[:]) {
			t.Fatal(err)
		}
	}
}

func TestBasicResultSelectedAcceptActualMember(t *testing.T) {
	i, q, v := basicAcceptFixture(t)
	want, err := basicResultDigest(i, q, v)
	if err != nil {
		t.Fatal(err)
	}
	h := &recordedResultHash{Hash: sha256.New()}
	e := requestEncoder{h: h}
	if err := encodeCanonicalResult(&e, i, q, v); err != nil {
		t.Fatal(err)
	}
	payload := len(v.RunID) + len(v.SourceID) + len(v.SourceIncarnation) + len(v.SourceRevision) + len(v.Members[0].ObjectID)
	if h.bytes.Len() != basicResultFixedBytes(v.Purpose)+64+payload || !bytes.Equal(h.Sum(nil), want[:]) {
		t.Fatal("member framing")
	}
	for _, mutate := range []func(*BasicResult){
		func(v *BasicResult) { v.ExpectedEpoch++; v.Members[0].AcceptedAssetRevision++ },
		func(v *BasicResult) { v.Members[0].ObjectID = "native\x00e\u0301" },
		func(v *BasicResult) { v.Members[0].LibraryID++ },
		func(v *BasicResult) { v.Members[0].SubjectID++; v.Continuation.AfterEntity++ },
		func(v *BasicResult) { v.Members[0].AssetID++ },
	} {
		_, _, v := basicAcceptFixture(t)
		mutate(&v)
		d, err := basicResultDigest(i, q, v)
		if err != nil || d == want {
			t.Fatal("member not bound", err)
		}
	}
	v.Outcome = ReceiptOutcome{State: Applied, Replayed: true}
	v.Digest[0] = 1
	if d, err := basicResultDigest(i, q, v); err != nil || d != want {
		t.Fatal("metadata", err)
	}
}

func TestBasicResultSelectedCopyAcceptRefusals(t *testing.T) {
	i, q, base := basicAcceptFixture(t)
	for _, mutate := range []func(*BasicResult){
		func(v *BasicResult) { v.Members = nil },
		func(v *BasicResult) { v.Members = append(v.Members, v.Members[0]); v.ResultCount = 2 },
		func(v *BasicResult) { v.ResultCount = 2 },
		func(v *BasicResult) { v.Members[0].InputOrdinal = 1 },
		func(v *BasicResult) { v.Members[0].ChildKind = 1 },
		func(v *BasicResult) { v.Members[0].ChildOrdinal = 1 },
		func(v *BasicResult) { v.Members[0].AcceptedAssetRevision = 17 },
		func(v *BasicResult) {
			v.ExpectedEpoch = math.MaxInt64
			v.Members[0].AcceptedAssetRevision = math.MinInt64
		},
		func(v *BasicResult) { v.Members[0].SubjectID = 32 },
		func(v *BasicResult) { v.Members[0].ObjectID = "\xff" },
		func(v *BasicResult) { v.Members[0].ObjectID = strings.Repeat("x", maxCanonicalResultBytes) },
		func(v *BasicResult) { v.Continuation.NextCopyKey = strings.Repeat("x", maxCanonicalResultBytes) },
		func(v *BasicResult) { v.Continuation.NextCopyKey2 = "\xff" },
		func(v *BasicResult) { v.Continuation.NextCopyKey3 = "\xff" },
		func(v *BasicResult) { v.Continuation.NextCursor = "\xff" },
	} {
		v := base
		v.Members = append([]InventoryMember{}, base.Members...)
		mutate(&v)
		assertBasicResultRefusedBeforeHashing(t, i, q, v)
	}
	_, _, v := basicResultFixture(t)
	for _, c := range []BasicContinuation{
		{NextFamily: 8, NextCopyPhase: 1, NextCopyKey: "text-pk", AfterEntity: 31},
		{NextFamily: 2, NextCopyPhase: 1, NextCopyID: 14, AfterEntity: 31},
		{NextFamily: 2, NextCopyPhase: 1, NextCopyAux: 1, AfterEntity: 31},
		{NextFamily: 7, NextCopyAux: 1, AfterEntity: 31},
		{NextFamily: 4, NextCopyID: 1, AfterEntity: 31},
		{NextFamily: 3, NextCopyID: -1, AfterEntity: 31},
		{NextFamily: 9}, {NextFamily: 10, AfterEntity: 31},
	} {
		v.Continuation = c
		assertBasicResultRefusedBeforeHashing(t, i, q, v)
	}
}

func TestBasicResultCopyScalarSensitivity(t *testing.T) {
	i, q, base := basicResultFixture(t)
	base.Continuation = BasicContinuation{NextFamily: 2, AfterEntity: 31}
	want, err := basicResultDigest(i, q, base)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []BasicContinuation{
		{NextFamily: 3, AfterEntity: 31},
		{NextFamily: 2, NextCopyPhase: 1, AfterEntity: 31},
		{NextFamily: 2, NextCopyID: 1, AfterEntity: 31},
		{NextFamily: 2, AfterEntity: 32},
	} {
		v := base
		v.Continuation = c
		d, err := basicResultDigest(i, q, v)
		if err != nil || d == want {
			t.Fatal("copy scalar not bound", err)
		}
	}
	// Aux is admitted only with the native phase1/nonzero ID combination.
	base.Continuation = BasicContinuation{NextFamily: 2, NextCopyID: 14, AfterEntity: 31}
	before, err := basicResultDigest(i, q, base)
	if err != nil {
		t.Fatal(err)
	}
	base.Continuation.NextCopyPhase = 1
	base.Continuation.NextCopyAux = 1
	after, err := basicResultDigest(i, q, base)
	if err != nil || after == before {
		t.Fatal("cleanup position not bound", err)
	}
	// All continuation TEXT fields remain unselected; none is newly admitted.
}
