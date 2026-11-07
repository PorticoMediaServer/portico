package writer

import (
	"strings"
	"testing"
)

func TestBasicResultFamily8TextCleanupCheckpoint(t *testing.T) {
	i, q, v := basicResultFixture(t)
	seen := map[ResultDigest]bool{}
	for _, c := range []BasicContinuation{
		{NextFamily: 8, AfterEntity: 31},                                   // phase0 zero input ordinal
		{NextFamily: 8, NextCopyID: 3, AfterEntity: 31},                    // unchanged phase0 input ordinal
		{NextFamily: 8, NextCopyPhase: 1, AfterEntity: 31},                 // initial text walk
		{NextFamily: 8, NextCopyPhase: 1, NextCopyAux: 1, AfterEntity: 31}, // actual empty native ID
		{NextFamily: 8, NextCopyPhase: 1, NextCopyAux: 1, NextCopyKey: "subtitle:é", AfterEntity: 31},
		{NextFamily: 8, NextCopyPhase: 1, NextCopyAux: 1, NextCopyKey: "subtitle:e\u0301", AfterEntity: 31},
		{NextFamily: 8, NextCopyPhase: 1, NextCopyAux: 1, NextCopyKey: "subtitle:\x00tail", AfterEntity: 31},
		{NextFamily: 9, AfterEntity: 31}, // exhausted text walk
	} {
		v.Continuation = c
		d, err := basicResultDigest(i, q, v)
		if err != nil || seen[d] {
			t.Fatalf("checkpoint %+v: duplicate=%t err=%v", c, seen[d], err)
		}
		seen[d] = true
	}
	// Every native key byte and the actual retained owner enter the SAME wire.
	base := v
	base.Continuation = BasicContinuation{NextFamily: 8, NextCopyPhase: 1, NextCopyAux: 1, NextCopyKey: "native-key", AfterEntity: 31}
	want, err := basicResultDigest(i, q, base)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*BasicResult){
		func(v *BasicResult) { v.Continuation.NextCopyKey += "2" },
		func(v *BasicResult) { v.Continuation.AfterEntity++ },
	} {
		v := base
		mutate(&v)
		if got, err := basicResultDigest(i, q, v); err != nil || got == want {
			t.Fatal("native text/owner not bound", err)
		}
	}
}

func TestBasicResultFamily8TextCleanupIsClosedBeforeHashing(t *testing.T) {
	i, q, base := basicResultFixture(t)
	base.Continuation = BasicContinuation{NextFamily: 8, NextCopyPhase: 1, NextCopyAux: 1, NextCopyKey: "native-key", AfterEntity: 31}
	for _, mutate := range []func(*BasicResult){
		func(v *BasicResult) { v.Continuation.NextCopyID = 1 },
		func(v *BasicResult) { v.Continuation.NextCopyAux = 0 }, // initial must have empty key
		func(v *BasicResult) { v.Continuation.NextCopyAux = -1 },
		func(v *BasicResult) { v.Continuation.NextCopyAux = 2 },
		func(v *BasicResult) { v.Continuation.NextCopyPhase = 0 },
		func(v *BasicResult) { v.Continuation.NextCopyPhase = 2 },
		func(v *BasicResult) { v.Continuation.NextFamily = 7 },
		func(v *BasicResult) { v.Continuation.NextFamily = 9 },
		func(v *BasicResult) { v.Continuation.AfterEntity = 0 },
		func(v *BasicResult) { v.Continuation.NextCopyKey2 = "other" },
		func(v *BasicResult) { v.Continuation.NextCopyKey3 = "other" },
		func(v *BasicResult) { v.Continuation.NextCursor = "other" },
		func(v *BasicResult) { v.Continuation.NextCopyKey = "\xff" },
		func(v *BasicResult) { v.Continuation.NextCopyKey = strings.Repeat("x", maxCanonicalResultBytes) },
		func(v *BasicResult) { v.Purpose = BasicPurposeSeal },
		func(v *BasicResult) { v.Purpose = BasicPurposeAccept },
	} {
		v := base
		mutate(&v)
		assertBasicResultRefusedBeforeHashing(t, i, q, v)
	}
}
