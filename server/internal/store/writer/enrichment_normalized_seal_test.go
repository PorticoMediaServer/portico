package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// DATA ONLY: EN froze these bytes independently from the written approved
// spec, before W implementation. No expected stream/digest is codec-generated.
const normalizedVectorSHA = "5dbaf784b70d0d46ae6b66574ac7221ac84ba62d879acd8fd0220af6632aa681"
const normalizedSpecSHA = "f39cdeae12596bfbd1e9d941968806d94ec0a6db67f68e561997b018443ce55e"

type normalizedVector struct {
	Name, Kind  string
	NativeInput json.RawMessage `json:"native_input"`
	Expected    struct {
		Version        int64
		Algorithm      string
		VerifiedRows   int64  `json:"verified_rows"`
		CanonicalBytes int64  `json:"canonical_bytes"`
		CommitmentHex  string `json:"commitment_hex"`
	}
	CanonicalHex string `json:"canonical_hex"`
}

type normalizedVectors struct {
	SpecSHA  string `json:"approved_spec_sha256"`
	Positive []normalizedVector
	Refusals []struct {
		Name, Base      string
		ReplacePath     string `json:"replace_path"`
		Replacement     json.RawMessage
		ExpectedRefusal string `json:"expected_refusal"`
	}
	Primitives []struct {
		Type  string
		Value json.RawMessage
		Hex   string
	} `json:"primitive_known_answers"`
	Admission []struct {
		Name      string
		Lengths   []int `json:"repeated_ascii_lengths"`
		Aggregate uint64
		Admitted  bool
	} `json:"writer_admission_cases"`
	Framing [][]string `json:"distinct_framing_pair"`
}

func loadNormalizedVectors(t *testing.T) normalizedVectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/normalized-seal-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != normalizedVectorSHA {
		t.Fatalf("independent vector bytes changed: %s", got)
	}
	var d normalizedVectors
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if d.SpecSHA != normalizedSpecSHA || len(d.Positive) != 6 || len(d.Refusals) != 23 || len(d.Primitives) != 13 {
		t.Fatal("incomplete frozen vectors")
	}
	return d
}

func vectorDeclaration(v normalizedVector) (NormalizedSealSummary, error) {
	b, err := hex.DecodeString(v.Expected.CommitmentHex)
	if err != nil || len(b) != 32 {
		return NormalizedSealSummary{}, errors.New("bad expected digest")
	}
	out := NormalizedSealSummary{VerifiedRows: v.Expected.VerifiedRows, CanonicalBytes: v.Expected.CanonicalBytes}
	copy(out.Commitment[:], b)
	return out, nil
}

// This is a strict native-value fixture adapter, not a production JSON codec.
// It rejects bool/float/string -> INTEGER and TEXT -> BLOB substitutions and
// missing/unlisted columns before passing typed values to the real codec.
func decodeNormalizedNative(raw json.RawMessage, out any) error {
	return fillNormalizedNative(raw, reflect.ValueOf(out).Elem())
}
func fillNormalizedNative(raw json.RawMessage, v reflect.Value) error {
	if v.Type() == reflect.TypeOf(EvidenceInteger{}) {
		if bytes.Equal(raw, []byte("null")) {
			v.Set(reflect.ValueOf(EvidenceInteger{}))
			return nil
		}
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil {
			return err
		}
		v.Set(reflect.ValueOf(EvidenceInteger{Value: n, Present: true}))
		return nil
	}
	if v.Type() == reflect.TypeOf(EvidenceText{}) {
		if bytes.Equal(raw, []byte("null")) {
			v.Set(reflect.ValueOf(EvidenceText{}))
			return nil
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		v.Set(reflect.ValueOf(EvidenceText{Value: s, Present: true}))
		return nil
	}
	switch v.Kind() {
	case reflect.String:
		if len(raw) > 0 && raw[0] == '{' {
			var x struct {
				Hex string `json:"native_text_hex"`
			}
			if err := json.Unmarshal(raw, &x); err != nil {
				return err
			}
			b, err := hex.DecodeString(x.Hex)
			if err != nil {
				return err
			}
			v.SetString(string(b))
			return nil
		}
		if bytes.Equal(raw, []byte("null")) {
			return errors.New("NULL TEXT")
		}
		return json.Unmarshal(raw, v.Addr().Interface())
	case reflect.Int64:
		if bytes.Equal(raw, []byte("null")) {
			return errors.New("NULL INTEGER")
		}
		return json.Unmarshal(raw, v.Addr().Interface())
	case reflect.Array:
		if len(raw) == 0 || raw[0] != '{' {
			return errors.New("native BLOB required")
		}
		var x struct {
			Hex string `json:"blob_hex"`
		}
		if err := json.Unmarshal(raw, &x); err != nil {
			return err
		}
		b, err := hex.DecodeString(x.Hex)
		if err != nil || len(b) != v.Len() {
			return errors.New("BLOB width")
		}
		reflect.Copy(v, reflect.ValueOf(b))
		return nil
	case reflect.Slice:
		if bytes.Equal(raw, []byte("null")) {
			return nil
		}
		var rows []json.RawMessage
		if err := json.Unmarshal(raw, &rows); err != nil {
			return err
		}
		v.Set(reflect.MakeSlice(v.Type(), len(rows), len(rows)))
		for j := range rows {
			if err := fillNormalizedNative(rows[j], v.Index(j)); err != nil {
				return err
			}
		}
		return nil
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		if len(fields) != v.NumField() {
			return errors.New("incomplete or extra native fields")
		}
		for j := 0; j < v.NumField(); j++ {
			name := v.Type().Field(j).Tag.Get("json")
			field, ok := fields[name]
			if !ok {
				return fmt.Errorf("missing %s", name)
			}
			if err := fillNormalizedNative(field, v.Field(j)); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
		return nil
	}
	return errors.New("unsupported native type")
}

func runNormalizedVector(v normalizedVector) (out NormalizedSealSummary, canonical []byte, err error) {
	selection := NormalizedSealSelection{Kind: v.Kind, Version: v.Expected.Version, Algorithm: v.Expected.Algorithm}
	expected, err := vectorDeclaration(v)
	if err != nil {
		return out, nil, err
	}
	var buf bytes.Buffer
	if v.Kind == NormalizedEvidenceKind {
		var in NormalizedEvidenceInput
		if err = decodeNormalizedNative(v.NativeInput, &in); err != nil {
			return out, nil, err
		}
		out, err = VerifyNormalizedEvidenceSeal(selection, in, expected)
		if err == nil {
			e := normalizedEncoder{h: &buf}
			encodeNormalizedEvidence(&e, selection, in)
			err = e.err
		}
	} else {
		// Expected source declaration is carried by the native operation's intent;
		// it is operational and deliberately absent from normalized C.
		var root map[string]json.RawMessage
		if err = json.Unmarshal(v.NativeInput, &root); err != nil {
			return out, nil, err
		}
		var intent map[string]json.RawMessage
		if err = json.Unmarshal(root["intent"], &intent); err != nil {
			return out, nil, err
		}
		var declaredBytes int64
		var declaredDigest [32]byte
		if err = fillNormalizedNative(intent["source_expected_bytes"], reflect.ValueOf(&declaredBytes).Elem()); err != nil {
			return out, nil, err
		}
		if err = fillNormalizedNative(intent["source_expected_commitment"], reflect.ValueOf(&declaredDigest).Elem()); err != nil {
			return out, nil, err
		}
		if declaredBytes != expected.CanonicalBytes || declaredDigest != expected.Commitment {
			return out, nil, errors.New("source native declaration mismatch")
		}
		delete(intent, "source_expected_bytes")
		delete(intent, "source_expected_commitment")
		root["intent"], err = json.Marshal(intent)
		if err != nil {
			return out, nil, err
		}
		raw, e := json.Marshal(root)
		if e != nil {
			return out, nil, e
		}
		var in NormalizedSourceInput
		if err = decodeNormalizedNative(raw, &in); err != nil {
			return out, nil, err
		}
		out, err = VerifyNormalizedSourceSeal(selection, in, expected)
		if err == nil {
			e := normalizedEncoder{h: &buf}
			encodeNormalizedSource(&e, selection, in)
			err = e.err
		}
	}
	if err != nil {
		return NormalizedSealSummary{}, nil, err
	}
	return out, buf.Bytes(), nil
}

func TestNormalizedSealIndependentVectors(t *testing.T) {
	d := loadNormalizedVectors(t)
	for _, v := range d.Positive {
		t.Run(v.Name, func(t *testing.T) {
			out, canonical, err := runNormalizedVector(v)
			if err != nil {
				t.Fatal(err)
			}
			expected, _ := vectorDeclaration(v)
			if out != expected {
				t.Fatalf("summary %+v", out)
			}
			want, err := hex.DecodeString(v.CanonicalHex)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(canonical, want) {
				t.Fatalf("canonical mismatch: got %x, want %x", canonical, want)
			}
		})
	}
}

func replaceNormalizedJSON(raw json.RawMessage, path []string, value json.RawMessage) (json.RawMessage, error) {
	if len(path) == 0 {
		return value, nil
	}
	if raw[0] == '[' {
		var a []json.RawMessage
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, err
		}
		j, err := strconv.Atoi(path[0])
		if err != nil || j < 0 || j >= len(a) {
			return nil, errors.New("bad path")
		}
		a[j], err = replaceNormalizedJSON(a[j], path[1:], value)
		if err != nil {
			return nil, err
		}
		return json.Marshal(a)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	var err error
	if len(path) == 1 {
		m[path[0]] = value
	} else {
		m[path[0]], err = replaceNormalizedJSON(m[path[0]], path[1:], value)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

func TestNormalizedSealIndependentRefusals(t *testing.T) {
	d := loadNormalizedVectors(t)
	rawSource, err := os.ReadFile("testdata/normalized-seal-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var rawDocument struct{ Positive []json.RawMessage }
	if err := json.Unmarshal(rawSource, &rawDocument); err != nil {
		t.Fatal(err)
	}
	bases := make(map[string]json.RawMessage)
	for _, p := range rawDocument.Positive {
		var name struct{ Name string }
		if err := json.Unmarshal(p, &name); err != nil {
			t.Fatal(err)
		}
		bases[name.Name] = p
	}

	for _, r := range d.Refusals {
		t.Run(r.Name, func(t *testing.T) {
			raw, ok := bases[r.Base]
			if !ok {
				t.Fatal("missing base")
			}
			var v normalizedVector

			changed, err := replaceNormalizedJSON(raw, strings.Split(r.ReplacePath, "."), r.Replacement)
			if err != nil {
				t.Fatal(err)
			}
			v = normalizedVector{} // never overwrite backing RawMessage bytes of a base fixture
			if err = json.Unmarshal(changed, &v); err != nil {
				t.Fatal(err)
			}
			out, canonical, err := runNormalizedVector(v)
			if err == nil || out != (NormalizedSealSummary{}) || canonical != nil {
				t.Fatalf("refusal %s leaked %+v / %v", r.ExpectedRefusal, out, err)
			}
		})
	}
}

func TestNormalizedSealPrimitiveAnswers(t *testing.T) {
	d := loadNormalizedVectors(t)
	for j, p := range d.Primitives {
		t.Run(fmt.Sprint(j, p.Type), func(t *testing.T) {
			var b bytes.Buffer
			e := normalizedEncoder{h: &b}
			switch p.Type {
			case "U64":
				var n uint64
				if err := json.Unmarshal(p.Value, &n); err != nil {
					t.Fatal(err)
				}
				e.unsigned(n)
			case "I64":
				var n int64
				if err := json.Unmarshal(p.Value, &n); err != nil {
					t.Fatal(err)
				}
				e.integer(n)
			case "NI":
				var n EvidenceInteger
				if err := decodeNormalizedNative(p.Value, &n); err != nil {
					t.Fatal(err)
				}
				e.nullableInteger(n)
			case "NT":
				var s EvidenceText
				if err := decodeNormalizedNative(p.Value, &s); err != nil {
					t.Fatal(err)
				}
				e.nullableText(s)
			case "T":
				var s string
				if err := decodeNormalizedNative(p.Value, &s); err != nil {
					t.Fatal(err)
				}
				e.text(s)
			case "B16":
				var v [16]byte
				if err := decodeNormalizedNative(p.Value, &v); err != nil {
					t.Fatal(err)
				}
				e.blob16(v)
			case "B32":
				var v [32]byte
				if err := decodeNormalizedNative(p.Value, &v); err != nil {
					t.Fatal(err)
				}
				e.blob32(v)
			default:
				t.Fatal("unhandled primitive")
			}
			if e.err != nil || hex.EncodeToString(b.Bytes()) != p.Hex {
				t.Fatalf("got %x / %v", b.Bytes(), e.err)
			}
		})
	}
	var e normalizedEncoder
	e.unsigned(math.MaxInt64 + 1)
	if e.err == nil {
		t.Fatal("unsigned overflow")
	}
	{
		b := d.Framing
		var x, y bytes.Buffer
		a, c := normalizedEncoder{h: &x}, normalizedEncoder{h: &y}
		for _, s := range b[0] {
			a.text(s)
		}
		for _, s := range b[1] {
			c.text(s)
		}
		if bytes.Equal(x.Bytes(), y.Bytes()) {
			t.Fatal("ambiguous framing")
		}
	}
}

func TestNormalizedSealControlOccurrencesAndContentBudget(t *testing.T) {
	d := loadNormalizedVectors(t)
	for _, v := range d.Admission {
		var occurrences []string
		for _, n := range v.Lengths {
			occurrences = append(occurrences, strings.Repeat("x", n))
		}
		n, err := CheckSealControlStrings(occurrences)
		if v.Admitted {
			if err != nil || n != v.Aggregate {
				t.Fatal(n, err)
			}
		} else if !errors.Is(err, ErrCap) || n != 0 {
			t.Fatal(n, err)
		}
	}
	if n, err := CheckSealControlStrings([]string{"\xff"}); n != 0 || !errors.Is(err, ErrInvalid) {
		t.Fatal(n, err)
	}
	if err := CheckEvidenceSealContentBytes(6<<20-16, 16); err != nil {
		t.Fatal(err)
	}
	if err := CheckEvidenceSealContentBytes(6<<20-16, 17); !errors.Is(err, ErrCap) {
		t.Fatal(err)
	}
	if err := CheckEvidenceSealContentBytes(math.MaxUint64, 1); !errors.Is(err, ErrCap) {
		t.Fatal(err)
	}
}

func TestNormalizedSealWholeInputPreflight(t *testing.T) {
	d := loadNormalizedVectors(t)
	var in NormalizedEvidenceInput
	if err := decodeNormalizedNative(d.Positive[0].NativeInput, &in); err != nil {
		t.Fatal(err)
	}
	s := NormalizedSealSelection{NormalizedEvidenceKind, NormalizedEvidenceAlgorithm, 1}
	for _, mutate := range []func(*NormalizedEvidenceInput){
		func(v *NormalizedEvidenceInput) { v.Credits = nil },
		func(v *NormalizedEvidenceInput) { v.Header.Overview = strings.Repeat("x", maxCanonicalRequestBytes) },
		func(v *NormalizedEvidenceInput) { v.Header.Overview = strings.Repeat("x", 6<<20) },
		func(v *NormalizedEvidenceInput) { v.Header.LengthMillis = EvidenceInteger{Value: 1} },
		func(v *NormalizedEvidenceInput) { v.Header.ArtistType = EvidenceText{Value: "ignored"} },
	} {
		changed := in
		mutate(&changed)
		out, err := NormalizedEvidenceSeal(s, changed)
		if err == nil || out != (NormalizedSealSummary{}) {
			t.Fatal(out, err)
		}
	}
	// An unsupported descriptor is refused before even the missing cohort.
	_, err := NormalizedEvidenceSeal(NormalizedSealSelection{Kind: "effective_seal", Version: 1, Algorithm: NormalizedEvidenceAlgorithm}, NormalizedEvidenceInput{})
	var policy *PolicyError
	if !errors.As(err, &policy) || policy.Field != "seal_algorithm" {
		t.Fatal(err)
	}
	if err := decodeNormalizedNative(d.Positive[1].NativeInput, &in); err != nil {
		t.Fatal(err)
	}
	in.Credits[0].Name = strings.Repeat("x", 1201)
	if out, err := NormalizedEvidenceSeal(s, in); !errors.Is(err, ErrCap) || out != (NormalizedSealSummary{}) {
		t.Fatal(out, err)
	}
}
