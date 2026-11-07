package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
		SourceSHA string `json:"source_sha256"`
		Positive  []struct {
			Result BasicResult
			Bytes  int    `json:"canonical_bytes"`
			Hex    string `json:"canonical_hex"`
			SHA    string `json:"sha256"`
		} `json:"positive"`
	}
	if err = json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors.Positive) != 2 || vectors.SourceSHA != "36cc611ff9eb1dfb74bc12c49c7bbef14b2975d909c579b067151b4e87603be3" {
		t.Fatal("SC native vector source changed")
	}
	i, q, _ := basicResultFixture(t)
	var previous ResultDigest
	for _, vector := range vectors.Positive {
		h := &recordedResultHash{Hash: sha256.New()}
		e := requestEncoder{h: h}
		if err = encodeCanonicalResult(&e, i, q, vector.Result); err != nil {
			t.Fatal(err)
		}
		if h.bytes.Len() != vector.Bytes || hex.EncodeToString(h.bytes.Bytes()) != vector.Hex {
			t.Fatal("independent complete Basic wire mismatch")
		}
		d, err := basicResultDigest(i, q, vector.Result)
		if err != nil || hex.EncodeToString(d[:]) != vector.SHA || !bytes.Equal(h.Sum(nil), d[:]) || d == previous {
			t.Fatal(d, err)
		}
		payload := len(vector.Result.RunID) + len(vector.Result.SourceID) + len(vector.Result.SourceIncarnation) + len(vector.Result.SourceRevision)
		if basicResultFixedBytes(vector.Result.Purpose)+payload != vector.Bytes {
			t.Fatal("whole wire preflight omits framing")
		}
		previous = d
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
		h := &countedResultHash{Hash: sha256.New()}
		e := requestEncoder{h: h}
		if err := encodeCanonicalResult(&e, i, q, v); err == nil || h.writes != 0 || e.written != 0 {
			t.Fatal("unselected/malformed result reached hash", v.Purpose, err)
		}
		if d, err := basicResultDigest(i, q, v); err == nil || d != (ResultDigest{}) {
			t.Fatal("partial digest", err)
		}
	}
	base.Purpose = BasicPurposeAccept
	if _, err := basicResultDigest(i, q, base); !errors.Is(err, ErrNotImplemented) {
		t.Fatal("Accept map inferred", err)
	}
}
