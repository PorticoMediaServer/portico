package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
)

func basicResultFixture(t *testing.T) (CommandIdentity, RequestDigest, BasicResult) {
	t.Helper()
	var identity CommandIdentity
	var request RequestDigest
	for i := range identity.Incarnation {
		identity.Incarnation[i], identity.OperationID[i] = 1, 2
	}
	identity.Sequence = 19
	for i := range request {
		request[i] = 3
	}
	return identity, request, BasicResult{Purpose: BasicPurposeStage, RunID: "basic-run", AcceptedRunRevision: 42, AppliedMS: 2000,
		SourceID: "basic-source", SourceIncarnation: "basic-incarnation", SourceRevision: "object:019", ChangeID: 51, ExpectedEpoch: 17,
		Members: []InventoryMember{}}
}

func TestBasicResultNativeVectorsAndCompleteWire(t *testing.T) {
	data, err := os.ReadFile("testdata/basic-result-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		SourceSHA          string `json:"source_sha256"`
		NextStageSourceSHA string `json:"next_stage_source_sha256"`
		Positive           []struct {
			Name     string
			Identity struct {
				Incarnation, OperationID string
				Sequence                 int64
			}
			Request string `json:"request_digest"`
			Result  BasicResult
			Bytes   int    `json:"canonical_bytes"`
			Hex     string `json:"canonical_hex"`
			SHA     string `json:"sha256"`
		} `json:"positive"`
	}
	if err = json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors.Positive) != 5 || vectors.SourceSHA != "36cc611ff9eb1dfb74bc12c49c7bbef14b2975d909c579b067151b4e87603be3" ||
		vectors.NextStageSourceSHA != "13be718c7d26a3dd73adcf8e54be30a2fc0f458ccd7eea1b4dceb503af428788" {
		t.Fatal("SC native vector source changed")
	}
	seen := make(map[ResultDigest]bool)
	for _, vector := range vectors.Positive {
		t.Run(vector.Name, func(t *testing.T) {
			var i CommandIdentity
			var q RequestDigest
			i.Sequence = vector.Identity.Sequence
			for _, raw := range []struct {
				hex  string
				dest []byte
			}{{vector.Identity.Incarnation, i.Incarnation[:]}, {vector.Identity.OperationID, i.OperationID[:]}, {vector.Request, q[:]}} {
				b, err := hex.DecodeString(raw.hex)
				if err != nil || len(b) != len(raw.dest) {
					t.Fatal("native vector raw width", err)
				}
				copy(raw.dest, b)
			}
			h := &recordedResultHash{Hash: sha256.New()}
			e := requestEncoder{h: h}
			if err := encodeCanonicalResult(&e, i, q, vector.Result); err != nil {
				t.Fatal(err)
			}
			if h.bytes.Len() != vector.Bytes || hex.EncodeToString(h.bytes.Bytes()) != vector.Hex {
				t.Fatal("independent complete Basic wire mismatch")
			}
			d, err := basicResultDigest(i, q, vector.Result)
			if err != nil || hex.EncodeToString(d[:]) != vector.SHA || !bytes.Equal(h.Sum(nil), d[:]) || seen[d] {
				t.Fatal(d, err)
			}
			payload := len(vector.Result.RunID) + len(vector.Result.SourceID) + len(vector.Result.SourceIncarnation) + len(vector.Result.SourceRevision)
			if basicResultFixedBytes(vector.Result.Purpose)+payload != vector.Bytes {
				t.Fatal("whole wire preflight omits framing")
			}
			seen[d] = true
		})
	}
}

func TestBasicResultStageContinuationBinding(t *testing.T) {
	i, q, base := basicResultFixture(t)
	seen := make(map[ResultDigest]bool)
	for _, tc := range []struct {
		name                string
		family, after, runR int64
	}{
		{"zero", 0, 0, 42},
		{"shape_pin_or_freeze", 1, 0, 42},
		{"first_owner", 0, 1, 42},
		{"owner_beyond_fixture", 0, 32, 42},
		{"largest_native_owner", 0, math.MaxInt64, 42},
		{"independent_run_revision", 0, 32, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := base
			v.Continuation.NextFamily, v.Continuation.AfterEntity = tc.family, tc.after
			v.AcceptedRunRevision = tc.runR // independent of ChangeID51 and object:019
			d, err := basicResultDigest(i, q, v)
			if err != nil || seen[d] {
				t.Fatal("Stage native prefix/revision refused or not bound", err)
			}
			seen[d] = true
		})
	}
}

func TestBasicResultContinuationRefusalsBeforeHashing(t *testing.T) {
	i, q, base := basicResultFixture(t)
	for _, tc := range []struct {
		name   string
		mutate func(*BasicResult)
	}{
		{"stage_negative_family", func(v *BasicResult) { v.Continuation.NextFamily = -1 }},
		{"stage_other_family", func(v *BasicResult) { v.Continuation.NextFamily = 2 }},
		{"stage_negative_owner", func(v *BasicResult) { v.Continuation.AfterEntity = -1 }},
		{"stage_family1_with_owner", func(v *BasicResult) { v.Continuation = BasicContinuation{NextFamily: 1, AfterEntity: 31} }},
		{"stage_directory_cursor", func(v *BasicResult) { v.Continuation.NextCursor = "next-directory" }},
		{"stage_copy_phase", func(v *BasicResult) { v.Continuation.NextCopyPhase = 1 }},
		{"stage_negative_copy_id", func(v *BasicResult) { v.Continuation.NextCopyID = -1 }},
		{"stage_copy_id", func(v *BasicResult) { v.Continuation.NextCopyID = 1 }},
		{"stage_copy_aux", func(v *BasicResult) { v.Continuation.NextCopyAux = 1 }},
		{"stage_copy_key", func(v *BasicResult) { v.Continuation.NextCopyKey = "copy" }},
		{"stage_copy_key2", func(v *BasicResult) { v.Continuation.NextCopyKey2 = "copy" }},
		{"stage_copy_key3", func(v *BasicResult) { v.Continuation.NextCopyKey3 = "copy" }},
		{"stage_family1_nil_members", func(v *BasicResult) { v.Continuation.NextFamily, v.Members = 1, nil }},
		{"stage_owner_result_count", func(v *BasicResult) { v.Continuation.AfterEntity, v.ResultCount = 32, 1 }},
		{"stage_owner_nonempty_members", func(v *BasicResult) { v.Continuation.AfterEntity, v.Members = 32, []InventoryMember{{}} }},
		{"stage_owner_nonpositive_epoch", func(v *BasicResult) { v.Continuation.AfterEntity, v.ExpectedEpoch = 32, 0 }},
		{"seal_family1", func(v *BasicResult) { v.Purpose, v.Continuation.NextFamily = BasicPurposeSeal, 1 }},
		{"seal_positive_owner", func(v *BasicResult) { v.Purpose, v.Continuation.AfterEntity = BasicPurposeSeal, 31 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := base
			tc.mutate(&v)
			assertBasicResultRefusedBeforeHashing(t, i, q, v)
		})
	}
}

func assertBasicResultRefusedBeforeHashing(t *testing.T, i CommandIdentity, q RequestDigest, v BasicResult) {
	t.Helper()
	h := &countedResultHash{Hash: sha256.New()}
	e := requestEncoder{h: h}
	if err := encodeCanonicalResult(&e, i, q, v); err == nil || h.writes != 0 || e.written != 0 {
		t.Fatal("unselected/malformed result reached hash", v.Purpose, err)
	}
	if d, err := basicResultDigest(i, q, v); err == nil || d != (ResultDigest{}) {
		t.Fatal("partial digest", err)
	}
}

func TestBasicResultOriginalAndNativeFieldBinding(t *testing.T) {
	i, q, base := basicResultFixture(t)
	want, err := basicResultDigest(i, q, base)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"RunID", "SourceID", "SourceIncarnation", "SourceRevision"} {
		v := base
		reflect.ValueOf(&v).Elem().FieldByName(field).SetString("different\x00native:e\u0301")
		if d, err := basicResultDigest(i, q, v); err != nil || d == want {
			t.Fatal("unrestricted native TEXT not bound", field, err)
		}
	}
	for _, field := range []string{"AcceptedRunRevision", "AppliedMS", "ChangeID", "ExpectedEpoch"} {
		v := base
		reflect.ValueOf(&v).Elem().FieldByName(field).SetInt(123)
		if d, err := basicResultDigest(i, q, v); err != nil || d == want {
			t.Fatal("actual post-state not bound", field, err)
		}
	}
	for n := range 4 {
		id, req := i, q
		switch n {
		case 0:
			id.Sequence++
		case 1:
			id.OperationID[0]++
		case 2:
			id.Incarnation[0]++
		case 3:
			req[0]++
		}
		if d, err := basicResultDigest(id, req, base); err != nil || d == want {
			t.Fatal("original binding lost", n, err)
		}
	}
	base.Outcome = ReceiptOutcome{State: Applied, Replayed: true}
	base.Digest[0] = 19
	if d, err := basicResultDigest(i, q, base); err != nil || d != want {
		t.Fatal("publication metadata entered saved digest", err)
	}
}

func TestBasicResultRefusesUnselectedAndMalformedBeforeHashing(t *testing.T) {
	i, q, base := basicResultFixture(t)
	for _, mutate := range []func(*BasicResult){
		func(v *BasicResult) { v.Purpose = "" },
		func(v *BasicResult) { v.Purpose = BasicPurposeAccept },
		func(v *BasicResult) { v.AcceptedRunRevision = 0 },
		func(v *BasicResult) { v.AppliedMS = -1 },
		func(v *BasicResult) { v.ChangeID = 0 },
		func(v *BasicResult) { v.ExpectedEpoch = 0 },
		func(v *BasicResult) { v.SourceRevision = "" },
		func(v *BasicResult) { v.SourceRevision = "\xff" },
		func(v *BasicResult) { v.Members = nil },
		func(v *BasicResult) { v.ResultCount = 1 },
		func(v *BasicResult) { v.Members = []InventoryMember{{}} },
		func(v *BasicResult) { v.Continuation.NextCopyAux = 2 },
		func(v *BasicResult) { v.Continuation.NextCopyKey = "unselected-copy" },
		func(v *BasicResult) { v.SourceRevision = strings.Repeat("r", maxCanonicalResultBytes) },
	} {
		v := base
		mutate(&v)
		assertBasicResultRefusedBeforeHashing(t, i, q, v)
	}
	base.Purpose = BasicPurposeAccept
	if _, err := basicResultDigest(i, q, base); !errors.Is(err, ErrNotImplemented) {
		t.Fatal("Accept map inferred", err)
	}
}
