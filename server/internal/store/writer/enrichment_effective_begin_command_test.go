package writer

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

// Test-only access to private immutable DTO fields. Production never uses
// unsafe, reflection setters on private fields, or an alternate request codec.
func effectiveBeginTestField(v reflect.Value) reflect.Value {
	return reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
}
func effectiveBeginTestLeaves(v reflect.Value, path []int, visit func([]int, reflect.Value)) {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			effectiveBeginTestLeaves(v.Field(i), append(append([]int(nil), path...), i), visit)
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			effectiveBeginTestLeaves(v.Index(i), append(append([]int(nil), path...), i), visit)
		}
	default:
		visit(path, v)
	}
}
func effectiveBeginTestAt(v reflect.Value, path []int) reflect.Value {
	for _, i := range path {
		if v.Kind() == reflect.Struct {
			v = v.Field(i)
		} else {
			v = v.Index(i)
		}
	}
	return effectiveBeginTestField(v)
}

func TestEffectiveBeginRequestCoversEveryNativeField(t *testing.T) {
	_, _, request, identity := effectiveBeginWholeFixture(t)
	input, err := newEffectiveStageBeginCommand(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	want, err := canonicalRequestDigest(EffectiveStage, 1, identity, request)
	if err != nil || input.digest != want {
		t.Fatal("constructor omitted full PRE-allocation root", err)
	}
	count := 0
	effectiveBeginTestLeaves(reflect.ValueOf(&request).Elem(), nil, func(path []int, _ reflect.Value) {
		count++
		changed := request
		leaf := effectiveBeginTestAt(reflect.ValueOf(&changed).Elem(), path)
		switch leaf.Kind() {
		case reflect.Bool:
			leaf.SetBool(!leaf.Bool())
		case reflect.Int64:
			leaf.SetInt(leaf.Int() + 1)
		case reflect.Uint8:
			leaf.SetUint(leaf.Uint() ^ 1)
		case reflect.Float64:
			leaf.SetFloat(leaf.Float() + 0.125)
		case reflect.String:
			leaf.SetString(leaf.String() + "changed")
		default:
			t.Fatalf("uncovered new native type %s at %v", leaf.Type(), path)
		}
		digest, err := canonicalRequestDigest(EffectiveStage, 1, identity, changed)
		if err != nil || digest == input.digest {
			t.Fatalf("native field omitted at %v: %v", path, err)
		}
		prepared, err := newEffectiveStageBeginCommand(identity, changed)
		if err == nil && prepared.digest != digest {
			t.Fatalf("constructor partially hashes field at %v", path)
		}
	})
	if count < 100 {
		t.Fatal("sparse request coverage", count)
	}
	// Original command identity is independently bound, including nonce bytes.
	for field := 0; field < 3; field++ {
		changed := identity
		switch field {
		case 0:
			changed.Incarnation[15] ^= 1
		case 1:
			changed.Sequence++
		case 2:
			changed.OperationID[7] ^= 1
		}
		got, err := canonicalRequestDigest(EffectiveStage, 1, changed, request)
		if err != nil || got == input.digest {
			t.Fatal("command identity omitted", field, err)
		}
	}
	// Generated outputs do not rewrite the PRE-allocation request.
	input.retirementIncarnation[0] ^= 1
	got, err := canonicalRequestDigest(EffectiveStage, 1, input.identity, input.request)
	if err != nil || got != input.digest {
		t.Fatal("generated retirement output rewrote request", err)
	}
	t.Logf("complete typed request: %d independently mutated native leaves", count)
}

func TestEffectiveBeginRequestOwnsAllRetainedStrings(t *testing.T) {
	_, _, request, identity := effectiveBeginWholeFixture(t)
	count := 0
	effectiveBeginTestLeaves(reflect.ValueOf(&request).Elem(), nil, func(_ []int, v reflect.Value) {
		if v.Kind() != reflect.String {
			return
		}
		count++
		s := v.String()
		backing := s + strings.Repeat("retention-sentinel", 65536)
		effectiveBeginTestField(v).SetString(backing[:len(s)])
	})
	input, err := newEffectiveStageBeginCommand(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	effectiveBeginTestLeaves(reflect.ValueOf(&request).Elem(), nil, func(path []int, v reflect.Value) {
		if v.Kind() != reflect.String {
			return
		}
		got := effectiveBeginTestAt(reflect.ValueOf(&input.request).Elem(), path).String()
		if got != v.String() || (unsafe.StringData(got) != nil && unsafe.StringData(got) == unsafe.StringData(v.String())) {
			t.Fatalf("retained backing buffer at %v", path)
		}
	})
	if count < 30 {
		t.Fatal("sparse string ownership coverage", count)
	}
}

func TestEffectiveBeginRequestRefusesUnsupportedAndMalformedShape(t *testing.T) {
	_, _, request, identity := effectiveBeginWholeFixture(t)
	cases := []func(*effectiveStageBeginRequest){
		func(r *effectiveStageBeginRequest) { r.variant = "seal" },
		func(r *effectiveStageBeginRequest) { r.entity.kind = 2 },
		func(r *effectiveStageBeginRequest) { r.entity.tvSourceRevision.Valid = true },
		func(r *effectiveStageBeginRequest) { r.owner.hierarchyRevision = 19 },
		func(r *effectiveStageBeginRequest) { r.owner.selectionRevision = 71 },
		func(r *effectiveStageBeginRequest) { r.owner.descriptiveRevision = 19 },
		func(r *effectiveStageBeginRequest) { r.producer.Selection.Role = "fallback" },
		func(r *effectiveStageBeginRequest) { r.producer.Selection.Status = "matched" },
		func(r *effectiveStageBeginRequest) { r.producer.Selection.Kind = "series" },
		func(r *effectiveStageBeginRequest) { r.owner.imageID.Valid = true },
		func(r *effectiveStageBeginRequest) { r.owner.acknowledgedSequence-- },
		func(r *effectiveStageBeginRequest) { r.owner.leaseUntil = "not-a-time" },
		func(r *effectiveStageBeginRequest) { r.header.expectedManifest[0] ^= 1 },
		func(r *effectiveStageBeginRequest) { r.header.expectedBytes = 1 },
		func(r *effectiveStageBeginRequest) { r.header.language = "en\x00CA" },
		func(r *effectiveStageBeginRequest) { r.owner.provider = string([]byte{255}) },
		func(r *effectiveStageBeginRequest) { r.producer.Object.Root = string([]byte{255}) },
	}
	for i, mutate := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			changed := request
			mutate(&changed)
			if _, err := newEffectiveStageBeginCommand(identity, changed); err == nil {
				t.Fatal("invalid FIRST input accepted")
			}
		})
	}
}
