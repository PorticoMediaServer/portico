package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
)

type resultFixture struct {
	SourceSHA string `json:"source_sha256"`
	Positive  []struct {
		Name  string
		Input map[string]json.RawMessage
		Bytes int    `json:"canonical_bytes"`
		Hex   string `json:"canonical_hex"`
		SHA   string `json:"sha256"`
	} `json:"positive"`
	Fields []struct {
		Field string
		Value json.RawMessage
		SHA   string `json:"sha256"`
	} `json:"field_sensitivity"`
	Raw []struct {
		Field string
		Index int
		SHA   string `json:"sha256"`
	} `json:"raw_byte_sensitivity"`
	Swaps []struct {
		Left, Right string
		SHA         string `json:"sha256"`
	} `json:"distinct_integer_swaps"`
}

func resultAnswers(t *testing.T) resultFixture {
	t.Helper()
	b, err := os.ReadFile("testdata/effective-begin-result-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var f resultFixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if f.SourceSHA != "8fb712b8e252d1d8bbda94a6b67c4ba2349fe4b14a1d1fee5898234e3d63aadb" || len(f.Positive) != 6 || len(f.Fields) != 11 || len(f.Raw) != 64 || len(f.Swaps) != 4 {
		t.Fatal("independent fixture changed")
	}
	return f
}

func resultFixtureValues(t *testing.T, input map[string]json.RawMessage) (CommandIdentity, RequestDigest, effectiveBeginResult) {
	t.Helper()
	read := func(key string, out any) {
		t.Helper()
		if err := json.Unmarshal(input[key], out); err != nil {
			t.Fatal(key, err)
		}
	}
	var kind, variant, outcome string
	var version int
	read("kind", &kind)
	read("variant", &variant)
	read("outcome", &outcome)
	read("version", &version)
	if version != 1 || kind != "effective_stage" || variant != "begin" || outcome != "applied" {
		t.Fatal("fixture is not the closed BEGIN schema")
	}
	var identity CommandIdentity
	var request RequestDigest
	var v effectiveBeginResult
	for key, dest := range map[string][]byte{"command_incarnation_hex": identity.Incarnation[:], "operation_id_hex": identity.OperationID[:], "request_digest_hex": request[:]} {
		var s string
		read(key, &s)
		b, err := hex.DecodeString(s)
		if err != nil || len(b) != len(dest) {
			t.Fatal("fixture raw width", key, err)
		}
		copy(dest, b)
	}
	read("command_sequence", &identity.Sequence)
	read("provider_operation_id", &v.ProviderOperationID)
	read("entity_id", &v.EntityID)
	read("observed_catalog_revision", &v.ObservedCatalogRevision)
	read("applied_ms", &v.AppliedMS)
	read("after_item", &v.AfterItem)
	read("next_ordinal", &v.NextOrdinal)
	read("publication_id", &v.PublicationID)
	read("image_id", &v.ImageID)
	read("image_revision", &v.ImageRevision)
	read("result_count", &v.ResultCount)
	read("member_cohort_length", &v.MemberCohortLength)
	return identity, request, v
}

type recordedResultHash struct {
	hash.Hash
	bytes bytes.Buffer
}

func (h *recordedResultHash) Write(p []byte) (int, error) { h.bytes.Write(p); return h.Hash.Write(p) }

func TestCanonicalResultIndependentKnownAnswers(t *testing.T) {
	for _, f := range resultAnswers(t).Positive {
		t.Run(f.Name, func(t *testing.T) {
			i, q, v := resultFixtureValues(t, f.Input)
			h := &recordedResultHash{Hash: sha256.New()}
			e := requestEncoder{h: h}
			if err := encodeCanonicalResult(&e, i, q, v); err != nil {
				t.Fatal(err)
			}
			if h.bytes.Len() != f.Bytes || hex.EncodeToString(h.bytes.Bytes()) != f.Hex {
				t.Fatal("canonical bytes differ from independent reference")
			}
			d, err := effectiveBeginResultDigest(i, q, v)
			if err != nil || hex.EncodeToString(d[:]) != f.SHA || !bytes.Equal(h.Sum(nil), d[:]) {
				t.Fatalf("digest %x %v", d, err)
			}
			common, err := canonicalResultDigest(i, q, v)
			if err != nil || common != d {
				t.Fatal("typed helper diverges", err)
			}
		})
	}
}

func TestCanonicalResultIndependentSensitivities(t *testing.T) {
	f := resultAnswers(t)
	check := func(input map[string]json.RawMessage, want string) {
		t.Helper()
		i, q, v := resultFixtureValues(t, input)
		d, err := effectiveBeginResultDigest(i, q, v)
		if err != nil || hex.EncodeToString(d[:]) != want {
			t.Fatalf("sensitivity %x want%s %v", d, want, err)
		}
	}
	copyInput := func() map[string]json.RawMessage {
		out := map[string]json.RawMessage{}
		for k, v := range f.Positive[0].Input {
			out[k] = v
		}
		return out
	}
	for _, c := range f.Fields {
		t.Run(c.Field, func(t *testing.T) { v := copyInput(); v[c.Field] = c.Value; check(v, c.SHA) })
	}
	for _, c := range f.Raw {
		t.Run(c.Field, func(t *testing.T) {
			v := copyInput()
			var raw string
			json.Unmarshal(v[c.Field], &raw)
			b, err := hex.DecodeString(raw)
			if err != nil {
				t.Fatal(err)
			}
			b[c.Index] ^= 1
			v[c.Field], _ = json.Marshal(hex.EncodeToString(b))
			check(v, c.SHA)
		})
	}
	for _, c := range f.Swaps {
		t.Run(c.Left+"_swap_"+c.Right, func(t *testing.T) { v := copyInput(); v[c.Left], v[c.Right] = v[c.Right], v[c.Left]; check(v, c.SHA) })
	}
}

func TestCanonicalResultEveryFieldCovered(t *testing.T) {
	f := resultAnswers(t)
	identity, request, base := resultFixtureValues(t, f.Positive[0].Input)
	original, err := effectiveBeginResultDigest(identity, request, base)
	if err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*effectiveBeginResult){
		"ProviderOperationID":     func(v *effectiveBeginResult) { v.ProviderOperationID += "x" },
		"EntityID":                func(v *effectiveBeginResult) { v.EntityID++ },
		"ObservedCatalogRevision": func(v *effectiveBeginResult) { v.ObservedCatalogRevision++ },
		"AppliedMS":               func(v *effectiveBeginResult) { v.AppliedMS++ },
		"AfterItem":               func(v *effectiveBeginResult) { v.AfterItem += "x" },
		"NextOrdinal":             func(v *effectiveBeginResult) { v.NextOrdinal++ },
		"PublicationID":           func(v *effectiveBeginResult) { s := ""; v.PublicationID = &s },
		"ImageID":                 func(v *effectiveBeginResult) { v.ImageID++ },
		"ImageRevision":           func(v *effectiveBeginResult) { v.ImageRevision++ },
		"ResultCount":             func(v *effectiveBeginResult) { v.ResultCount++ },
		"MemberCohortLength":      func(v *effectiveBeginResult) { v.MemberCohortLength++ },
	}
	typ := reflect.TypeOf(base)
	if typ.NumField() != len(mutations) {
		t.Fatal("add encoding/refusal coverage for every new result field")
	}
	for n := range typ.NumField() {
		name := typ.Field(n).Name
		change, ok := mutations[name]
		if !ok {
			t.Fatal("uncovered result field", name)
		}
		v := base
		change(&v)
		d, err := effectiveBeginResultDigest(identity, request, v)
		if err == nil && d == original {
			t.Fatal("unbound result field", name)
		}
		if err != nil && (!errors.Is(err, ErrInvalid) || d != (ResultDigest{})) {
			t.Fatal("invalid mutation manufactured a digest", name, err)
		}
	}
	// A new common identity field must get its own fixed-wire/mutation proof.
	if reflect.TypeOf(identity).NumField() != 3 {
		t.Fatal("extend result identity coverage")
	}
}

func TestCanonicalResultRefusesMalformedShapesAndRanges(t *testing.T) {
	f := resultAnswers(t)
	i, q, base := resultFixtureValues(t, f.Positive[0].Input)
	for _, name := range []string{"EntityID", "ObservedCatalogRevision", "ImageID", "ImageRevision", "NextOrdinal", "ResultCount", "MemberCohortLength"} {
		for _, n := range []int64{-1, 0, 1, math.MaxInt64} {
			v := base
			reflect.ValueOf(&v).Elem().FieldByName(name).SetInt(n)
			valid := n > 0
			if name == "NextOrdinal" || name == "ResultCount" || name == "MemberCohortLength" {
				valid = n == 0
			}
			d, err := effectiveBeginResultDigest(i, q, v)
			if (err == nil) != valid || (err != nil && (!errors.Is(err, ErrInvalid) || d != (ResultDigest{}))) {
				t.Fatal(name, n, err)
			}
		}
	}
	for _, change := range []func(*effectiveBeginResult){
		func(v *effectiveBeginResult) { v.ProviderOperationID = "" },
		func(v *effectiveBeginResult) { v.ProviderOperationID = string([]byte{0xff}) },
		func(v *effectiveBeginResult) { v.AfterItem = string([]byte{0xff}) },
		func(v *effectiveBeginResult) { v.AppliedMS = -1 },
		func(v *effectiveBeginResult) { s := "published"; v.PublicationID = &s },
	} {
		v := base
		change(&v)
		d, err := effectiveBeginResultDigest(i, q, v)
		if !errors.Is(err, ErrInvalid) || d != (ResultDigest{}) {
			t.Fatal("malformed result accepted", v, err)
		}
	}
	for _, seq := range []int64{-1, 0} {
		bad := i
		bad.Sequence = seq
		if _, err := effectiveBeginResultDigest(bad, q, base); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid sequence", err)
		}
	}
	for _, unknown := range []any{nil, &base, struct{}{}, RequestDigest{}, map[string]any{"image_id": 1}} {
		if _, err := canonicalResultDigest(i, q, unknown); !errors.Is(err, ErrInvalid) {
			t.Fatal("unknown result schema accepted", err)
		}
	}
	for _, change := range []func(*effectiveBeginResult){
		func(v *effectiveBeginResult) { v.AfterItem = strings.Repeat("x", maxCanonicalRequestBytes) },
		func(v *effectiveBeginResult) {
			s := strings.Repeat("x", maxCanonicalRequestBytes/2)
			v.ProviderOperationID = s
			v.AfterItem = s
		},
	} {
		v := base
		change(&v)
		if d, err := effectiveBeginResultDigest(i, q, v); !errors.Is(err, ErrInvalid) || d != (ResultDigest{}) {
			t.Fatal("result preparation envelope accepted", err)
		}
	}
}

func TestEffectiveStagePurposeAndOriginalRequestBinding(t *testing.T) {
	d, err := Describe(EffectiveStage)
	if err != nil || d.Name != "effective_stage" || d.Durability != Normal || EffectiveStage != SearchSeal+1 {
		t.Fatal("closed purpose changed", d, err)
	}
	f := resultAnswers(t)
	i, q, v := resultFixtureValues(t, f.Positive[0].Input)
	result, err := effectiveBeginResultDigest(i, q, v)
	if err != nil {
		t.Fatal(err)
	}
	request, err := canonicalRequestDigest(EffectiveStage, 1, i, struct{ Variant string }{"begin"})
	if err != nil || ResultDigest(request) == result {
		t.Fatal("request and result domains overlap", err)
	}
	other, err := canonicalRequestDigest(CreditStage, 1, i, struct{ Variant string }{"begin"})
	if err != nil || other == request {
		t.Fatal("effective purpose aliases credit stage", err)
	}
}
