package writer

import (
	"crypto/sha256"
	"math"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

func searchFragmentCommandFixture(t *testing.T) (searchFragmentStageRequest, CommandIdentity) {
	t.Helper()
	s, rows := searchFragmentStageSnapshotFixture(t)
	return searchFragmentStageRequest{variant: "fragment_stage", snapshot: s, row: rows[0].row},
		CommandIdentity{Incarnation: s.job.incarnation, Sequence: s.job.lastSequence + 1, OperationID: [16]byte{31}}
}

func TestSearchFragmentCommandCanonicalEnvelope(t *testing.T) {
	e := requestEncoder{h: sha256.New()}
	e.value(reflect.ValueOf(searchFragmentStageRequest{}), 0)
	fixed := e.written + uint64(len(canonicalRequestCodec)+4+16+8+16)
	if e.err != nil || fixed != searchFragmentStageFixedBytes {
		t.Fatalf("shared request codec fixed bytes=%d; update reviewed preflight, err=%v", fixed, e.err)
	}
}

func TestSearchFragmentCommandFreezesCompleteNativeRequest(t *testing.T) {
	r, id := searchFragmentCommandFixture(t)
	c, err := newSearchFragmentStageCommand(id, r)
	if err != nil || c == nil || !c.valid || c.request != r || c.payload.row != r.row {
		t.Fatal(c, err)
	}
	want, err := canonicalRequestDigest(SearchFragmentStage, 1, id, r)
	if err != nil || want != c.digest {
		t.Fatal("incomplete original request", err)
	}
	fields := 0
	searchImageBeginTestLeaves(reflect.ValueOf(&r).Elem(), func(v reflect.Value) {
		fields++
		before := reflect.New(v.Type()).Elem()
		before.Set(v)
		switch v.Kind() {
		case reflect.Int64:
			v.SetInt(v.Int() + 1)
		case reflect.Uint8, reflect.Uint16:
			v.SetUint(v.Uint() + 1)
		case reflect.Bool:
			v.SetBool(!v.Bool())
		case reflect.String:
			v.SetString(v.String() + "x")
		default:
			t.Fatalf("uncovered native request field kind %v", v.Kind())
		}
		got, err := canonicalRequestDigest(SearchFragmentStage, 1, id, r)
		if err != nil || got == want {
			t.Fatal("native field absent from W request digest", v.Kind(), err)
		}
		v.Set(before)
	})
	if fields < 100 {
		t.Fatal("native request field traversal incomplete", fields)
	}
}

func TestSearchFragmentCommandClonesEveryText(t *testing.T) {
	r, id := searchFragmentCommandFixture(t)
	searchImageBeginTestLeaves(reflect.ValueOf(&r).Elem(), func(v reflect.Value) {
		if v.Kind() == reflect.String && v.Len() > 0 {
			old := v.String()
			backing := strings.Repeat("x", 1<<20) + old
			v.SetString(backing[1<<20:])
		}
	})
	c, err := newSearchFragmentStageCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	var before, after []*byte
	searchImageBeginTestLeaves(reflect.ValueOf(&r).Elem(), func(v reflect.Value) {
		if v.Kind() == reflect.String {
			before = append(before, unsafe.StringData(v.String()))
		}
	})
	searchImageBeginTestLeaves(reflect.ValueOf(&c.request).Elem(), func(v reflect.Value) {
		if v.Kind() == reflect.String {
			after = append(after, unsafe.StringData(v.String()))
		}
	})
	if len(before) != 18 || len(after) != len(before) {
		t.Fatal("TEXT ownership inventory changed", len(before), len(after))
	}
	for i := range before {
		if after[i] != nil && after[i] == before[i] {
			t.Fatal("retained caller backing TEXT", i)
		}
	}
}

func TestSearchFragmentCommandRefusesOversizeBeforeClone(t *testing.T) {
	r, id := searchFragmentCommandFixture(t)
	r.snapshot.job.contentDigest = strings.Repeat("x", maxCanonicalRequestBytes)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	c, err := newSearchFragmentStageCommand(id, r)
	runtime.ReadMemStats(&after)
	if err == nil || c != nil || after.TotalAlloc-before.TotalAlloc >= 64<<10 {
		t.Fatal("complete wire overhead checked after clone", after.TotalAlloc-before.TotalAlloc, err)
	}
}

func TestSearchFragmentCommandStructuralRefusals(t *testing.T) {
	r, id := searchFragmentCommandFixture(t)
	for _, edit := range []func(*searchFragmentStageRequest){
		func(r *searchFragmentStageRequest) { r.variant = "head_publish" },
		func(r *searchFragmentStageRequest) { r.snapshot.job.leaseOwner = 0 },
		func(r *searchFragmentStageRequest) { r.snapshot.job.leaseGeneration = 0 },
		func(r *searchFragmentStageRequest) { r.snapshot.job.leaseExpiresMS = 0 },
		func(r *searchFragmentStageRequest) { r.snapshot.job.acknowledgedSequence-- },
		func(r *searchFragmentStageRequest) { r.snapshot.job.revision = math.MaxInt64 },
		func(r *searchFragmentStageRequest) { r.snapshot.job.incarnation[0]++ },
		func(r *searchFragmentStageRequest) { r.row.Revision = "011" },
		func(r *searchFragmentStageRequest) { r.row.Prefix = strings.Repeat("a", searchFragmentPackedBytes+1) },
		func(r *searchFragmentStageRequest) { r.row.Lexical = strings.Repeat("a", searchFragmentPackedBytes+1) },
		func(r *searchFragmentStageRequest) {
			r.row.Normalized = strings.Repeat("a", searchFragmentWindowRunes+1)
		},
		func(r *searchFragmentStageRequest) { r.snapshot.job.contentDigest = string([]byte{255}) },
	} {
		bad := r
		edit(&bad)
		if c, err := newSearchFragmentStageCommand(id, bad); err == nil || c != nil {
			t.Fatal("unsupported native shape accepted", c, err)
		}
	}
}
