package writer

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCanonicalRequestIndependentKnownAnswer(t *testing.T) {
	// Independently constructed in Python from the binary contract, not the
	// Go encoder:392 bytes, including the anonymous struct's type schema.
	request := struct {
		Owner    string
		Revision int64
		Raw      []byte
	}{"run/001", -1, []byte{0, 255}}
	digest, err := canonicalRequestDigest(InventoryAccept, 1, codecIdentity(), request)
	if err != nil || hex.EncodeToString(digest[:]) != "d964fb9e76088c91f5f315f2052235dc203868870410d5c0894556fcf83ca63a" {
		t.Fatalf("canonical bytes changed without codec version: %x %v", digest, err)
	}
}

type codecSource struct{ ID, Incarnation, Revision string }
type codecInput struct {
	Owner      string
	Source     codecSource
	Cursor     string
	Nullable   *string
	Raw        []byte
	Ordinals   []int64
	Output     [16]byte
	CapturedMS int64
	Locked     bool
	Weight     float64
	Ignored    string `json:"-"`
	private    string
}

func codecFixture() codecInput {
	s := "captured"
	return codecInput{"run-native", codecSource{"source/native", "inc:00", "rev:001"}, "native-cookie:0007", &s, []byte{0, 255}, []int64{1, 2}, [16]byte{4}, 9, true, 1.5, "included", "backing"}
}
func codecIdentity() CommandIdentity {
	return CommandIdentity{Incarnation: [16]byte{1}, Sequence: 7, OperationID: [16]byte{2}}
}
func hashCodec(t *testing.T, v any) RequestDigest {
	t.Helper()
	d, err := canonicalRequestDigest(InventoryAccept, 1, codecIdentity(), v)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func TestCanonicalRequestCompleteFieldCoverage(t *testing.T) {
	base := codecFixture()
	original := hashCodec(t, base)
	mutations := map[string]func(*codecInput){
		"Owner":      func(v *codecInput) { v.Owner = "different" },
		"Source":     func(v *codecInput) { v.Source.ID = "different" },
		"Cursor":     func(v *codecInput) { v.Cursor = "different" },
		"Nullable":   func(v *codecInput) { s := "different"; v.Nullable = &s },
		"Raw":        func(v *codecInput) { v.Raw = []byte{1, 255} },
		"Ordinals":   func(v *codecInput) { v.Ordinals = []int64{2, 1} },
		"Output":     func(v *codecInput) { v.Output[15] = 1 },
		"CapturedMS": func(v *codecInput) { v.CapturedMS++ },
		"Locked":     func(v *codecInput) { v.Locked = false },
		"Weight":     func(v *codecInput) { v.Weight = 2.5 },
		"Ignored":    func(v *codecInput) { v.Ignored = "different" },
		"private":    func(v *codecInput) { v.private = "different" },
	}
	typ := reflect.TypeOf(base)
	if typ.NumField() != len(mutations) {
		t.Fatal("extend coverage mutations for every new request field")
	}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		change, ok := mutations[name]
		if !ok {
			t.Fatal("uncovered field", name)
		}
		v := codecFixture()
		change(&v)
		if hashCodec(t, v) == original {
			t.Fatal("unhashed field", name)
		}
	}
	for _, field := range []string{"ID", "Incarnation", "Revision"} {
		v := codecFixture()
		x := reflect.ValueOf(&v.Source).Elem().FieldByName(field)
		x.SetString("changed")
		if hashCodec(t, v) == original {
			t.Fatal("unhashed nested witness", field)
		}
	}
	// New zero-valued or json-excluded fields are included automatically, not
	// accidentally omitted by a manual allowlist or omitempty policy.
	a := struct{ A int64 }{1}
	b := struct {
		A     int64
		Added int64 `json:"-"`
	}{1, 0}
	if hashCodec(t, a) == hashCodec(t, b) {
		t.Fatal("new zero request field disappeared")
	}
}
func TestCanonicalRequestDeterminismAndTypedDistinctions(t *testing.T) {
	a, b := codecFixture(), codecFixture()
	if a.Nullable == b.Nullable {
		t.Fatal("fixture did not move pointer")
	}
	original := hashCodec(t, a)
	for range 10 {
		if hashCodec(t, b) != original {
			t.Fatal("address or allocation dependent digest")
		}
	}
	for _, pair := range [][2]any{
		{struct{ V []byte }{nil}, struct{ V []byte }{[]byte{}}},
		{struct{ V *string }{nil}, struct{ V *string }{new(string)}},
		{struct{ V []int64 }{nil}, struct{ V []string }{nil}},
		{struct{ V int64 }{1}, struct{ V string }{"1"}},
		{struct{ A, B string }{"ab", "c"}, struct{ A, B string }{"a", "bc"}},
		{struct{ V string }{"é"}, struct{ V string }{"e\u0301"}},
		{struct{ V float64 }{0}, struct{ V float64 }{math.Copysign(0, -1)}},
	} {
		if hashCodec(t, pair[0]) == hashCodec(t, pair[1]) {
			t.Fatalf("typed/nullable/LP distinction lost: %T/%T", pair[0], pair[1])
		}
	}
	for _, change := range []func(*CommandIdentity){func(i *CommandIdentity) { i.Incarnation[15] = 1 }, func(i *CommandIdentity) { i.Sequence++ }, func(i *CommandIdentity) { i.OperationID[15] = 1 }} {
		i := codecIdentity()
		change(&i)
		d, err := canonicalRequestDigest(InventoryAccept, 1, i, a)
		if err != nil || d == original {
			t.Fatal("identity not bound", err)
		}
	}
	for _, kind := range []CommandID{BasicAccept, SearchImageStage} {
		d, err := canonicalRequestDigest(kind, 1, codecIdentity(), a)
		if err != nil || d == original {
			t.Fatal("command not bound", err)
		}
	}
	d, err := canonicalRequestDigest(InventoryAccept, 2, codecIdentity(), a)
	if err != nil || d == original {
		t.Fatal("domain version not bound", err)
	}
}
func TestCanonicalRequestRejectsUnstableOrUnboundedValues(t *testing.T) {
	type cyclic struct{ Next *cyclic }
	cycle := &cyclic{}
	cycle.Next = cycle
	for _, v := range []any{
		struct{ V map[string]string }{map[string]string{"x": "y"}},
		struct{ V any }{int64(1)}, struct{ V func() }{func() {}}, struct{ V chan int }{make(chan int)},
		struct{ V float64 }{math.NaN()}, struct{ V float64 }{math.Inf(1)}, struct{ V uint64 }{uint64(math.MaxInt64) + 1},
		struct{ V string }{string([]byte{255})}, struct{ V []byte }{bytes.Repeat([]byte{1}, maxCanonicalRequestBytes+1)},
		struct{ V string }{strings.Repeat("a", maxCanonicalRequestBytes+1)}, struct{ V *cyclic }{cycle},
		struct{ V time.Time }{time.Now()}, nil, []int64{1},
	} {
		if _, err := canonicalRequestDigest(InventoryAccept, 1, codecIdentity(), v); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %T: %v", v, err)
		}
	}
	if _, err := canonicalRequestDigest(InventoryAccept, 0, codecIdentity(), codecFixture()); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted unversioned request", err)
	}
	if _, err := canonicalRequestDigest(InventoryAccept, 1, CommandIdentity{}, codecFixture()); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted absent identity", err)
	}
}
