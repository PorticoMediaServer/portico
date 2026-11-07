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

type sealResultAnswer struct {
	Kind     string
	Identity struct {
		Incarnation string
		Sequence    int64
		OperationID string
	}
	Request         string
	Facts           sealResultFacts
	WireHex, SHA256 string
}

func sealResultAnswers(t *testing.T) []sealResultAnswer {
	t.Helper()
	b, err := os.ReadFile("testdata/enrichment-seal-result-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256.Sum256(b); hex.EncodeToString(got[:]) != "ffb2ebbc8696fd910055d7152f736033a4f7da74d835e819f2b9eadcbe3b4439" {
		t.Fatal("independent struct/hashlib result answers changed")
	}
	var a []sealResultAnswer
	if err = json.Unmarshal(b, &a); err != nil || len(a) != 2 {
		t.Fatal("independent answer shape", err)
	}
	return a
}
func sealResultInputs(t *testing.T, a sealResultAnswer) (CommandIdentity, RequestDigest, any) {
	t.Helper()
	i := CommandIdentity{Sequence: a.Identity.Sequence}
	var q RequestDigest
	for text, dst := range map[string][]byte{a.Identity.Incarnation: i.Incarnation[:], a.Identity.OperationID: i.OperationID[:], a.Request: q[:]} {
		b, err := hex.DecodeString(text)
		if err != nil || len(b) != len(dst) {
			t.Fatal("fixed identity width", err)
		}
		copy(dst, b)
	}
	if a.Kind == "evidence_seal" {
		return i, q, evidenceSealResult{a.Facts}
	}
	if a.Kind == "credit_source_seal" {
		return i, q, creditSourceSealResult{a.Facts}
	}
	t.Fatal("unknown answer kind")
	return i, q, nil
}
func TestEnrichmentSealResultIndependentBytes(t *testing.T) {
	for _, a := range sealResultAnswers(t) {
		t.Run(a.Kind, func(t *testing.T) {
			i, q, v := sealResultInputs(t, a)
			h := &recordedResultHash{Hash: sha256.New()}
			if err := encodeCanonicalResult(&requestEncoder{h: h}, i, q, v); err != nil {
				t.Fatal(err)
			}
			if hex.EncodeToString(h.bytes.Bytes()) != a.WireHex {
				t.Fatal("independent exact wire changed")
			}
			digest, err := canonicalResultDigest(i, q, v)
			if err != nil || hex.EncodeToString(digest[:]) != a.SHA256 || !bytes.Equal(h.Sum(nil), digest[:]) {
				t.Fatal("independent digest changed", err)
			}
		})
	}
}
func TestEnrichmentSealResultEveryNativeField(t *testing.T) {
	for _, a := range sealResultAnswers(t) {
		t.Run(a.Kind, func(t *testing.T) {
			i, q, v := sealResultInputs(t, a)
			original, err := canonicalResultDigest(i, q, v)
			if err != nil {
				t.Fatal(err)
			}
			check := func(f sealResultFacts) {
				t.Helper()
				var v any = evidenceSealResult{f}
				if a.Kind == "credit_source_seal" {
					v = creditSourceSealResult{f}
				}
				d, err := canonicalResultDigest(i, q, v)
				if err == nil && d == original || err != nil && d != (ResultDigest{}) {
					t.Fatal("field unbound or partial digest", err)
				}
			}
			typ := reflect.TypeOf(a.Facts)
			if typ.NumField() != 34 {
				t.Fatal("new native field needs explicit result review", typ.NumField())
			}
			for n := 0; n < typ.NumField(); n++ {
				t.Run(typ.Field(n).Name, func(t *testing.T) {
					f := a.Facts
					field := reflect.ValueOf(&f).Elem().Field(n)
					switch field.Kind() {
					case reflect.String:
						field.SetString(field.String() + "x")
					case reflect.Int64:
						field.SetInt(field.Int() + 1)
					case reflect.Array:
						field.Index(0).SetUint(field.Index(0).Uint() ^ 1)
					case reflect.Struct:
						present := field.FieldByName("Present")
						if !present.IsValid() {
							t.Fatal("unhandled nullable type")
						}
						present.SetBool(!present.Bool())
					default:
						t.Fatal("new native result type needs coverage")
					}
					check(f)
				})
			}
			changed := q
			changed[0] ^= 1
			d, err := canonicalResultDigest(i, changed, v)
			if err != nil || d == original {
				t.Fatal("request unbound", err)
			}
			changedI := i
			changedI.OperationID[0] ^= 1
			d, err = canonicalResultDigest(changedI, q, v)
			if err != nil || d == original {
				t.Fatal("identity unbound", err)
			}
			f := a.Facts
			f.OwnerGeneration, f.ContentRevision = f.ContentRevision, f.OwnerGeneration
			check(f)
		})
	}
}
func TestEnrichmentSealResultPurposeNullsAndPreflight(t *testing.T) {
	a := sealResultAnswers(t)
	for _, answer := range a {
		i, q, _ := sealResultInputs(t, answer)
		var wrong any = creditSourceSealResult{answer.Facts}
		if answer.Kind == "credit_source_seal" {
			wrong = evidenceSealResult{answer.Facts}
		}
		if d, err := canonicalResultDigest(i, q, wrong); !errors.Is(err, ErrInvalid) || d != (ResultDigest{}) {
			t.Fatal("purpose NULL groups aliased", err)
		}
		f := answer.Facts
		f.AfterItem = "a\x00b"
		if err := preflightEnrichmentSealResult(EvidenceSeal, f); err == nil {
			t.Fatal("NUL accepted")
		}
		f = answer.Facts
		f.AfterItem = strings.Repeat("x", MaxSealControlStringBytes+1)
		var value any = evidenceSealResult{f}
		if answer.Kind == "credit_source_seal" {
			value = creditSourceSealResult{f}
		}
		h := &recordedResultHash{Hash: sha256.New()}
		if err := encodeCanonicalResult(&requestEncoder{h: h}, i, q, value); !errors.Is(err, ErrCap) || h.bytes.Len() != 0 {
			t.Fatal("over-limit started hashing", err)
		}
	}
	i, q, _ := sealResultInputs(t, a[1])
	f := a[1].Facts
	f.VerifiedRows = 0
	f.NextOrdinal = 0
	if _, err := creditSourceSealResultDigest(i, q, creditSourceSealResult{f}); err != nil {
		t.Fatal("complete empty source refused", err)
	}
	if _, err := canonicalResultDigest(i, q, f); !errors.Is(err, ErrInvalid) {
		t.Fatal("arbitrary output accepted", err)
	}
}
func TestEnrichmentSealPurposesAreDistinctNormal(t *testing.T) {
	for id, name := range map[CommandID]string{EvidenceSeal: "evidence_seal", CreditSourceSeal: "credit_source_seal"} {
		d, err := Describe(id)
		if err != nil || d.Name != name || d.Durability != Normal {
			t.Fatal(d, err)
		}
	}
	if EvidenceSeal != BasicSeal+1 || CreditSourceSeal != EvidenceSeal+1 {
		t.Fatal("prior purposes renumbered")
	}
}
