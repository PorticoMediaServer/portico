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
	"slices"
	"strings"
	"testing"
)

type inventoryResultVector struct {
	Name      string
	Input     map[string]json.RawMessage
	WireBytes int    `json:"wire_bytes"`
	WireHex   string `json:"wire_hex"`
	DigestHex string `json:"digest_hex"`
}

func inventoryResultAnswers(t *testing.T) ([]inventoryResultVector, map[string]json.RawMessage) {
	t.Helper()
	b, err := os.ReadFile("testdata/inventory-accept-result-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256.Sum256(b); hex.EncodeToString(got[:]) != "5c66e1e926ea5a6cbacc70932ca857ca2c81e29610476aab8f397a0c18d77aa1" {
		t.Fatal("independent SC vector bytes changed")
	}
	var f struct {
		DocSHA      string `json:"wire_doc_sha256"`
		HeaderFixed int    `json:"header_fixed"`
		MemberFixed int    `json:"member_fixed"`
		Vectors     []inventoryResultVector
		Resources   map[string]json.RawMessage
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if f.DocSHA != "a1facc7cc60710459bafff3626d07b207159ca90d0a7e43aaa2d2dcd2eefab3b" || f.HeaderFixed != 239 || f.MemberFixed != 64 || len(f.Vectors) != 8 {
		t.Fatal("independent closed wire changed")
	}
	return f.Vectors, f.Resources
}

func inventoryResultValues(t *testing.T, f inventoryResultVector) (CommandIdentity, RequestDigest, inventoryAcceptResult) {
	t.Helper()
	read := func(raw json.RawMessage, dest any) {
		t.Helper()
		if err := json.Unmarshal(raw, dest); err != nil {
			t.Fatal(err)
		}
	}
	var identityFields, resultFields map[string]json.RawMessage
	read(f.Input["Identity"], &identityFields)
	read(f.Input["Result"], &resultFields)
	var i CommandIdentity
	var q RequestDigest
	var v inventoryAcceptResult
	for _, field := range []struct {
		raw  json.RawMessage
		dest []byte
	}{{identityFields["Incarnation"], i.Incarnation[:]}, {identityFields["OperationID"], i.OperationID[:]}, {f.Input["Request"], q[:]}} {
		var s struct{ Hex string }
		read(field.raw, &s)
		b, err := hex.DecodeString(s.Hex)
		if err != nil || len(b) != len(field.dest) {
			t.Fatal("native raw width", err)
		}
		copy(field.dest, b)
	}
	read(identityFields["Sequence"], &i.Sequence)
	for name, dest := range map[string]*string{"RunID": &v.RunID, "SourceID": &v.SourceID, "SourceIncarnation": &v.SourceIncarnation, "SourceRevision": &v.SourceRevision, "NextCursor": &v.NextCursor} {
		var s struct{ UTF8 string }
		read(resultFields[name], &s)
		*dest = s.UTF8
	}
	read(resultFields["AcceptedRunRevision"], &v.AcceptedRunRevision)
	read(resultFields["AppliedMS"], &v.AppliedMS)
	read(resultFields["ResultCount"], &v.ResultCount)
	var members []map[string]json.RawMessage
	read(resultFields["Members"], &members)
	for _, fields := range members {
		var m inventoryResultMember
		var s struct{ UTF8 string }
		read(fields["ObjectID"], &s)
		m.ObjectID = s.UTF8
		for name, dest := range map[string]*int64{"InputOrdinal": &m.InputOrdinal, "ChildKind": &m.ChildKind, "ChildOrdinal": &m.ChildOrdinal, "LibraryID": &m.LibraryID, "SubjectID": &m.SubjectID, "AssetID": &m.AssetID, "AcceptedAssetRevision": &m.AcceptedAssetRevision} {
			read(fields[name], dest)
		}
		v.Members = append(v.Members, m)
	}
	return i, q, v
}

func TestInventoryResultIndependentKnownAnswers(t *testing.T) {
	vectors, _ := inventoryResultAnswers(t)
	for _, f := range vectors {
		t.Run(f.Name, func(t *testing.T) {
			i, q, v := inventoryResultValues(t, f)
			h := &recordedResultHash{Hash: sha256.New()}
			e := requestEncoder{h: h}
			if err := encodeCanonicalResult(&e, i, q, v); err != nil {
				t.Fatal(err)
			}
			if h.bytes.Len() != f.WireBytes || hex.EncodeToString(h.bytes.Bytes()) != f.WireHex {
				t.Fatal("exact bytes differ from independent SC reference")
			}
			d, err := inventoryAcceptResultDigest(i, q, v)
			if err != nil || hex.EncodeToString(d[:]) != f.DigestHex || !bytes.Equal(d[:], h.Sum(nil)) {
				t.Fatalf("independent digest mismatch: %x %v", d, err)
			}
			common, err := canonicalResultDigest(i, q, v)
			if err != nil || common != d {
				t.Fatal("closed helper diverges", err)
			}
		})
	}
}

func TestInventoryResultCompleteFieldCoverage(t *testing.T) {
	vectors, _ := inventoryResultAnswers(t)
	i, q, base := inventoryResultValues(t, vectors[0])
	original, err := inventoryAcceptResultDigest(i, q, base)
	if err != nil {
		t.Fatal(err)
	}
	check := func(i CommandIdentity, q RequestDigest, v inventoryAcceptResult) {
		t.Helper()
		d, err := inventoryAcceptResultDigest(i, q, v)
		if err == nil && d == original || err != nil && (!errors.Is(err, ErrInvalid) || d != (ResultDigest{})) {
			t.Fatal("field is unbound or refusal returned a digest", err)
		}
	}
	for _, target := range []struct {
		value any
		names []string
	}{{&base, []string{"RunID", "AcceptedRunRevision", "AppliedMS", "SourceID", "SourceIncarnation", "SourceRevision", "NextCursor", "ResultCount", "Members"}},
		{&base.Members[0], []string{"InputOrdinal", "ChildKind", "ChildOrdinal", "ObjectID", "LibraryID", "SubjectID", "AssetID", "AcceptedAssetRevision"}}} {
		typ := reflect.TypeOf(target.value).Elem()
		if typ.NumField() != len(target.names) {
			t.Fatal("new result field requires wire and mutation coverage")
		}
		for n := range typ.NumField() {
			name := typ.Field(n).Name
			if !slices.Contains(target.names, name) {
				t.Fatal("uncovered result field", name)
			}
			t.Run(typ.Name()+"/"+name, func(t *testing.T) {
				v := base
				v.Members = slices.Clone(base.Members)
				dest := reflect.ValueOf(&v).Elem()
				if typ == reflect.TypeOf(inventoryResultMember{}) {
					dest = reflect.ValueOf(&v.Members[0]).Elem()
				}
				field := dest.FieldByName(name)
				switch field.Kind() {
				case reflect.String:
					field.SetString(field.String() + "x")
				case reflect.Int64:
					field.SetInt(field.Int() + 1)
				case reflect.Slice:
					field.SetZero()
				default:
					t.Fatal("extend field mutation coverage", name)
				}
				check(i, q, v)
			})
		}
	}
	if reflect.TypeOf(i).NumField() != 3 {
		t.Fatal("extend common identity coverage")
	}
	i.Sequence++
	check(i, q, base)
	i.Sequence--
	for n := range 64 {
		identity, request := i, q
		switch {
		case n < 16:
			identity.Incarnation[n] ^= 1
		case n < 32:
			identity.OperationID[n-16] ^= 1
		default:
			request[n-32] ^= 1
		}
		check(identity, request, base)
	}
	// All seven positive native integers are deliberately distinct in the vector.
	// Every pair swap must bind its position, not a sorted/aliased value set.
	for a := 0; a < 7; a++ {
		for b := a + 1; b < 7; b++ {
			identity, v := i, base
			v.Members = slices.Clone(base.Members)
			m := &v.Members[0]
			fields := []*int64{&identity.Sequence, &v.AcceptedRunRevision, &v.AppliedMS, &m.LibraryID, &m.SubjectID, &m.AssetID, &m.AcceptedAssetRevision}
			*fields[a], *fields[b] = *fields[b], *fields[a]
			check(identity, q, v)
		}
	}
}

func TestInventoryResultRefusesBeforeHashing(t *testing.T) {
	vectors, _ := inventoryResultAnswers(t)
	i, q, base := inventoryResultValues(t, vectors[0])
	refuse := func(i CommandIdentity, value any) {
		t.Helper()
		h := &countedResultHash{Hash: sha256.New()}
		e := requestEncoder{h: h}
		if err := encodeCanonicalResult(&e, i, q, value); !errors.Is(err, ErrInvalid) || h.writes != 0 || e.written != 0 {
			t.Fatal("invalid result reached hash", err, h.writes, e.written)
		}
		if d, err := canonicalResultDigest(i, q, value); !errors.Is(err, ErrInvalid) || d != (ResultDigest{}) {
			t.Fatal("invalid result returned a digest", err)
		}
	}
	for _, name := range []string{"RunID", "SourceID", "SourceIncarnation", "SourceRevision", "NextCursor"} {
		for _, text := range []string{"", string([]byte{0xff})} {
			if name == "NextCursor" && text == "" {
				continue // explicitly valid; independently covered by saved vector
			}
			v := base
			reflect.ValueOf(&v).Elem().FieldByName(name).SetString(text)
			refuse(i, v)
		}
	}
	for _, name := range []string{"LibraryID", "SubjectID", "AssetID", "AcceptedAssetRevision", "InputOrdinal", "ChildKind", "ChildOrdinal"} {
		for _, number := range []int64{-1, 0, 1, math.MaxInt64} {
			if (name == "LibraryID" || name == "SubjectID" || name == "AssetID" || name == "AcceptedAssetRevision") && number > 0 ||
				(name == "InputOrdinal" || name == "ChildKind" || name == "ChildOrdinal") && number == 0 {
				continue
			}
			v := base
			v.Members = slices.Clone(base.Members)
			reflect.ValueOf(&v.Members[0]).Elem().FieldByName(name).SetInt(number)
			refuse(i, v)
		}
	}
	for _, change := range []func(*inventoryAcceptResult){
		func(v *inventoryAcceptResult) { v.AcceptedRunRevision = 0 },
		func(v *inventoryAcceptResult) { v.AcceptedRunRevision = -1 },
		func(v *inventoryAcceptResult) { v.AppliedMS = -1 },
		func(v *inventoryAcceptResult) { v.ResultCount = 0; v.Members = nil },
		func(v *inventoryAcceptResult) { v.ResultCount = 0; v.Members = []inventoryResultMember{} },
		func(v *inventoryAcceptResult) { v.ResultCount = -1 },
		func(v *inventoryAcceptResult) { v.ResultCount = math.MaxInt64 },
		func(v *inventoryAcceptResult) { v.Members = nil },
		func(v *inventoryAcceptResult) { v.Members = append(v.Members, v.Members[0]); v.ResultCount++ }, // duplicate ordinal
		func(v *inventoryAcceptResult) { v.Members[0].ObjectID = "" },
		func(v *inventoryAcceptResult) { v.Members[0].ObjectID = string([]byte{0xff}) },
	} {
		v := base
		v.Members = slices.Clone(base.Members)
		change(&v)
		refuse(i, v)
	}
	for _, sequence := range []int64{-1, 0} {
		identity := i
		identity.Sequence = sequence
		refuse(identity, base)
	}
	type unregistered inventoryAcceptResult
	for _, v := range []any{nil, &base, unregistered(base), struct{}{}, RequestDigest{}} {
		refuse(i, v)
	}
}

func TestInventoryResultLiteralResourceBoundaries(t *testing.T) {
	vectors, resources := inventoryResultAnswers(t)
	i, q, base := inventoryResultValues(t, vectors[0])
	for _, name := range []string{"ceiling_minus_one", "exact_ceiling", "ceiling_plus_one"} {
		t.Run(name, func(t *testing.T) {
			var f struct {
				ObjectBytes int    `json:"object_bytes"`
				WireBytes   int    `json:"wire_bytes"`
				DigestHex   string `json:"digest_hex"`
				Refused     bool
			}
			if err := json.Unmarshal(resources[name], &f); err != nil {
				t.Fatal(err)
			}
			v := base
			v.Members = slices.Clone(base.Members)
			v.Members[0].ObjectID = strings.Repeat("x", f.ObjectBytes)
			h := &countedResultHash{Hash: sha256.New()}
			e := requestEncoder{h: h}
			err := encodeCanonicalResult(&e, i, q, v)
			if f.Refused {
				if !errors.Is(err, ErrInvalid) || h.writes != 0 || e.written != 0 {
					t.Fatal("ceiling+1 hashed", err, h.writes)
				}
				return
			}
			if err != nil || h.bytes != f.WireBytes || hex.EncodeToString(h.Sum(nil)) != f.DigestHex || f.WireBytes > 32<<20 {
				t.Fatal("literal independent boundary", name, err, h.bytes)
			}
		})
	}
	// A too-large final member must refuse before scanning even the FIRST UTF-8
	// field. Distinct failure fields make that order observable without a hook.
	v := base
	v.RunID = string([]byte{0xff}) + strings.Repeat("h", 16<<20)
	v.Members = slices.Clone(base.Members)
	v.Members[0].ObjectID = strings.Repeat("z", 16<<20)
	h := &countedResultHash{Hash: sha256.New()}
	e := requestEncoder{h: h}
	err := encodeCanonicalResult(&e, i, q, v)
	var policy *PolicyError
	if !errors.As(err, &policy) || policy.Field != "inventory_result_bytes" || h.writes != 0 || e.written != 0 {
		t.Fatal("late size failure followed UTF-8/hash work", err, h.writes)
	}
}
