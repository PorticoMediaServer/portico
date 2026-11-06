package writer

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unsafe"
)

// The test oracle uses native field names, independently of the production
// struct literal. Unusual public/native names are explicit; all other names
// differ only in their first letter. Unknown or duplicate leaves fail closed.
func effectivePrepareNativePath(path []string) []string {
	root := map[string]string{"ProviderOperationID": "ownerID", "EvidenceID": "evidenceID", "PublicationID": "publicationID", "ObjectID": "objectID"}
	if name, ok := root[path[0]]; ok {
		return []string{name}
	}
	out := append([]string(nil), path...)
	switch path[0] {
	case "Operation":
		out[0] = "owner"
		receipt := map[string]string{"Exists": "exists", "CommandIncarnation": "incarnation", "LastCommandSequence": "lastSequence", "AcknowledgedCommandSequence": "acknowledgedSequence"}
		if name, ok := receipt[path[1]]; ok {
			return append([]string{"owner", "receiptOwnerFacts", name}, path[2:]...)
		}
	case "Entity":
		out[0] = "entity"
		if path[1] == "MetadataIncarnation" {
			out[1] = "incarnation"
			return out
		}
	case "Image":
		out[0] = "header"
	case "Producer":
		out[0] = "producer"
		return out // Exact shared native producer shapes; no renamed fields.
	default:
		panic("unclassified public projection root: " + path[0])
	}
	switch {
	case path[1] == "ID":
		out[1] = "id"
	case path[1] == "CreditImageID":
		out[1] = "imageID"
	case strings.HasPrefix(path[1], "TV"):
		out[1] = "tv" + path[1][2:]
		if path[1] == "TVIncarnation" {
			out[2] = strings.ToLower(path[2][:1]) + path[2][1:]
		}
	default:
		out[1] = strings.ToLower(path[1][:1]) + path[1][1:]
	}
	return out
}

func effectivePrepareLeaves(v reflect.Value, path []string, visit func([]string, reflect.Value)) {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			effectivePrepareLeaves(v.Field(i), append(append([]string(nil), path...), v.Type().Field(i).Name), visit)
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			effectivePrepareLeaves(v.Index(i), append(append([]string(nil), path...), strconv.Itoa(i)), visit)
		}
	default:
		visit(path, v)
	}
}

func effectivePrepareAt(t *testing.T, v reflect.Value, path []string) reflect.Value {
	t.Helper()
	for _, part := range path {
		if v.Kind() == reflect.Struct {
			v = v.FieldByName(part)
		} else if v.Kind() == reflect.Array {
			i, err := strconv.Atoi(part)
			if err != nil {
				t.Fatal(path, err)
			}
			v = v.Index(i)
		} else {
			t.Fatal("unmapped projection leaf", path)
		}
		if !v.IsValid() {
			t.Fatal("missing native projection leaf", path)
		}
	}
	return effectiveBeginTestField(v)
}

func effectivePrepareFixture(t *testing.T) (EffectiveStageBeginInput, effectiveStageBeginRequest, CommandIdentity) {
	t.Helper()
	_, _, native, identity := effectiveBeginWholeFixture(t)
	var input EffectiveStageBeginInput
	effectivePrepareLeaves(reflect.ValueOf(&input).Elem(), nil, func(path []string, value reflect.Value) {
		value.Set(effectivePrepareAt(t, reflect.ValueOf(&native).Elem(), effectivePrepareNativePath(path)))
	})
	return input, native, identity
}

func TestEffectivePrepareCompleteNativeProjection(t *testing.T) {
	var input EffectiveStageBeginInput
	want := effectiveStageBeginRequest{variant: "begin"}
	seen := map[string]bool{"variant": true}
	count := 0
	// Fully populated, distinct fields, including normally unsupported FIRST TV
	// and set fields. Projection must preserve them so validation can refuse.
	effectivePrepareLeaves(reflect.ValueOf(&input).Elem(), nil, func(path []string, value reflect.Value) {
		count++
		switch value.Kind() {
		case reflect.String:
			value.SetString(strings.Join(path, ".") + fmt.Sprint(":", count))
		case reflect.Int64:
			value.SetInt(int64(1000 + count))
		case reflect.Uint8:
			value.SetUint(uint64(count % 251))
		case reflect.Float64:
			value.SetFloat(float64(count) + 0.125)
		case reflect.Bool:
			value.SetBool(true)
		default:
			t.Fatal("uncovered new native type", path, value.Type())
		}
		nativePath := effectivePrepareNativePath(path)
		key := strings.Join(nativePath, ".")
		if seen[key] {
			t.Fatal("duplicate projected native leaf", key)
		}
		seen[key] = true
		effectivePrepareAt(t, reflect.ValueOf(&want).Elem(), nativePath).Set(value)
	})
	if got := effectivePrepareRequest(input); !reflect.DeepEqual(got, want) {
		t.Fatal("complete public/native projection differs")
	}
	effectivePrepareLeaves(reflect.ValueOf(&want).Elem(), nil, func(path []string, _ reflect.Value) {
		if !seen[strings.Join(path, ".")] {
			t.Fatal("uncaptured native leaf", path)
		}
	})
	// Single-leaf mutations independently catch swaps even for equal booleans,
	// zero/NULL tags and bytes that happen to share a value in the full vector.
	effectivePrepareLeaves(reflect.ValueOf(&input).Elem(), nil, func(path []string, _ reflect.Value) {
		changed, expected := input, want
		leaf := effectivePrepareAt(t, reflect.ValueOf(&changed).Elem(), path)
		switch leaf.Kind() {
		case reflect.String:
			leaf.SetString(leaf.String() + "changed")
		case reflect.Int64:
			leaf.SetInt(leaf.Int() + 1)
		case reflect.Uint8:
			leaf.SetUint(leaf.Uint() ^ 1)
		case reflect.Float64:
			leaf.SetFloat(leaf.Float() + 0.5)
		case reflect.Bool:
			leaf.SetBool(!leaf.Bool())
		}
		effectivePrepareAt(t, reflect.ValueOf(&expected).Elem(), effectivePrepareNativePath(path)).Set(leaf)
		if got := effectivePrepareRequest(changed); !reflect.DeepEqual(got, expected) {
			t.Fatal("wrong single-leaf native binding", path)
		}
	})
	t.Logf("complete projection: %d public leaves plus fixed BEGIN discriminator", count)
}

func TestEffectivePrepareOriginalRootAndReference(t *testing.T) {
	input, native, identity := effectivePrepareFixture(t)
	original := input
	command, err := PrepareEffectiveStageBegin(identity, input)
	if err != nil {
		t.Fatal(err)
	}
	prepared, ok := command.(*effectiveStageBeginCommand)
	if !ok || !reflect.DeepEqual(prepared.request, native) {
		t.Fatal("adapter changed original native root")
	}
	want, err := canonicalRequestDigest(EffectiveStage, 1, identity, native)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := EffectiveStageBeginReference(command)
	if err != nil || ref != (ReceiptReference{Identity: identity, RequestDigest: want}) {
		t.Fatal("original reference mismatch", ref, err)
	}
	for _, wrapped := range []EffectiveStageBeginCommand{effectivePrepareWrapped{command}, &effectivePrepareWrapped{command}} {
		got, err := EffectiveStageBeginReference(wrapped)
		if !errors.Is(err, ErrInvalid) || got != (ReceiptReference{}) {
			t.Fatal("wrapped real command exposed a reference", err)
		}
	}
	// Caller mutations and generated retirement candidates never rewrite input
	// commitment. Reference outputs are copied, not borrowed identity/hash views.
	input.Operation.QueryTitle = "later query"
	input.Image.DeclaredInput[0] ^= 1
	prepared.retirementIncarnation[0] ^= 1
	ref.Identity.Incarnation[0] ^= 1
	ref.RequestDigest[0] ^= 1
	again, err := EffectiveStageBeginReference(command)
	if err != nil || again != (ReceiptReference{Identity: identity, RequestDigest: want}) {
		t.Fatal("mutable original reference", err)
	}
	if !reflect.DeepEqual(prepared.request, native) {
		t.Fatal("caller mutation changed frozen native request")
	}
	for _, mutate := range []func(*EffectiveStageBeginInput){
		func(v *EffectiveStageBeginInput) { v.Operation.QueryTitle += "changed" },
		func(v *EffectiveStageBeginInput) { v.Producer.Publication.Record += ":opaque" },
		func(v *EffectiveStageBeginInput) { v.Image.ExpectedCount, v.Image.ExpectedBytes = 1, 17 },
	} {
		changed := original
		mutate(&changed)
		next, err := PrepareEffectiveStageBegin(identity, changed)
		if err != nil {
			t.Fatal(err)
		}
		nextRef, err := EffectiveStageBeginReference(next)
		if err != nil || nextRef.RequestDigest == want {
			t.Fatal("changed original input omitted", err)
		}
	}
}

func TestEffectivePrepareOwnsEveryRetainedString(t *testing.T) {
	input, _, identity := effectivePrepareFixture(t)
	count := 0
	effectivePrepareLeaves(reflect.ValueOf(&input).Elem(), nil, func(_ []string, value reflect.Value) {
		if value.Kind() == reflect.String {
			count++
			original := value.String()
			backing := original + strings.Repeat("retention-sentinel", 2048)
			value.SetString(backing[:len(original)])
		}
	})
	command, err := PrepareEffectiveStageBegin(identity, input)
	if err != nil {
		t.Fatal(err)
	}
	prepared := command.(*effectiveStageBeginCommand)
	effectivePrepareLeaves(reflect.ValueOf(&input).Elem(), nil, func(path []string, value reflect.Value) {
		if value.Kind() != reflect.String {
			return
		}
		got := effectivePrepareAt(t, reflect.ValueOf(&prepared.request).Elem(), effectivePrepareNativePath(path)).String()
		if got != value.String() || (unsafe.StringData(got) != nil && unsafe.StringData(got) == unsafe.StringData(value.String())) {
			t.Fatal("retained caller backing buffer", path)
		}
	})
	t.Logf("owned %d detached native strings", count)
}

func TestEffectivePrepareRefusesMalformedNullAndFirstShape(t *testing.T) {
	input, _, identity := effectivePrepareFixture(t)
	cases := map[string]func(*EffectiveStageBeginInput){
		"owner_image_null":           func(v *EffectiveStageBeginInput) { v.Operation.CreditImageID.Int64 = 17 },
		"entity_image_null":          func(v *EffectiveStageBeginInput) { v.Entity.CreditImageID.Int64 = 19 },
		"tv_source_null":             func(v *EffectiveStageBeginInput) { v.Entity.TVSourceRevision.Int64 = 23 },
		"tv_hierarchy_null":          func(v *EffectiveStageBeginInput) { v.Entity.TVHierarchyRevision.Int64 = 29 },
		"tv_selection_null":          func(v *EffectiveStageBeginInput) { v.Entity.TVSelectionRevision.Int64 = 31 },
		"tv_description_null":        func(v *EffectiveStageBeginInput) { v.Entity.TVDescriptiveRevision.Int64 = 37 },
		"tv_incarnation_null":        func(v *EffectiveStageBeginInput) { v.Entity.TVIncarnation.Value[0] = 1 },
		"cue_null":                   func(v *EffectiveStageBeginInput) { v.Producer.Association.CUE.Int64 = 41 },
		"set_id_null":                func(v *EffectiveStageBeginInput) { v.Operation.SetID.String = "hidden" },
		"set_kind_null":              func(v *EffectiveStageBeginInput) { v.Operation.SetSubjectKind.String = "movie" },
		"set_provider_null":          func(v *EffectiveStageBeginInput) { v.Operation.SetProviderID.String = "hidden" },
		"selection_evidence_null":    func(v *EffectiveStageBeginInput) { v.Producer.Selection.Evidence.String = "hidden" },
		"selection_set_null":         func(v *EffectiveStageBeginInput) { v.Producer.Selection.Set.String = "hidden" },
		"selection_publication_null": func(v *EffectiveStageBeginInput) { v.Producer.Selection.Publication.String = "hidden" },
		"selection_track_null":       func(v *EffectiveStageBeginInput) { v.Producer.Selection.Track.String = "hidden" },
		"info_owner_null":            func(v *EffectiveStageBeginInput) { v.Producer.Asset.InfoOwner = sql.NullString{String: "hidden"} },
		"info_revision_null":         func(v *EffectiveStageBeginInput) { v.Producer.Asset.InfoRevision = sql.NullString{String: "hidden"} },
		"capture_end_null":           func(v *EffectiveStageBeginInput) { v.Producer.Capture.End = sql.NullFloat64{Float64: 9.5} },
		"association_end_null":       func(v *EffectiveStageBeginInput) { v.Producer.Association.End = sql.NullFloat64{Float64: 9.5} },
		"negative_zero_null": func(v *EffectiveStageBeginInput) {
			v.Producer.Capture.End = sql.NullFloat64{Float64: math.Copysign(0, -1)}
		},
		"head_missing_id":       func(v *EffectiveStageBeginInput) { v.Producer.CreditHead.PublicationID = "hidden" },
		"head_missing_revision": func(v *EffectiveStageBeginInput) { v.Producer.CreditHead.Revision = 43 },
		"set_intent":            func(v *EffectiveStageBeginInput) { v.Operation.SetID = sql.NullString{String: "set", Valid: true} },
		"episode_intent":        func(v *EffectiveStageBeginInput) { v.Operation.EpisodeOrder = "aired" },
		"not_movie":             func(v *EffectiveStageBeginInput) { v.Entity.Kind = 2 },
		"tv_zero_is_not_null":   func(v *EffectiveStageBeginInput) { v.Entity.TVSourceRevision.Valid = true },
		"invalid_utf8":          func(v *EffectiveStageBeginInput) { v.Producer.Object.Root = string([]byte{255}) },
		"whole_text_budget": func(v *EffectiveStageBeginInput) {
			v.Operation.QueryTitle = strings.Repeat("x", maxCanonicalRequestBytes)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			changed := input
			mutate(&changed)
			command, err := PrepareEffectiveStageBegin(identity, changed)
			if !errors.Is(err, ErrInvalid) || command != nil {
				t.Fatal("invalid input escaped preparation", err)
			}
		})
	}
	// Native present-zero/empty values remain distinct from NULL in the frozen
	// root. These nullable serving witnesses are not the closed set/TV intent.
	for _, mutate := range []func(*EffectiveStageBeginInput){
		func(v *EffectiveStageBeginInput) { v.Producer.Selection.Evidence = sql.NullString{Valid: true} },
		func(v *EffectiveStageBeginInput) {
			v.Producer.Capture.End = sql.NullFloat64{Valid: true}
			v.Producer.Association.End = sql.NullFloat64{Valid: true}
		},
	} {
		changed := input
		mutate(&changed)
		command, err := PrepareEffectiveStageBegin(identity, changed)
		if err != nil {
			t.Fatal("lost native present-zero/empty", err)
		}
		if !reflect.DeepEqual(command.(*effectiveStageBeginCommand).request, effectivePrepareRequest(changed)) {
			t.Fatal("nullable value normalized")
		}
	}
}

type effectivePrepareForeign struct{}

func (effectivePrepareForeign) isEffectiveStageBeginCommand() { panic("foreign marker invoked") }

type effectivePrepareWrapped struct{ EffectiveStageBeginCommand }

func TestEffectivePrepareReferenceRejectsForeignAndWrapped(t *testing.T) {
	for _, command := range []EffectiveStageBeginCommand{nil, (*effectiveStageBeginCommand)(nil), &effectiveStageBeginCommand{}, effectivePrepareForeign{}, effectivePrepareWrapped{effectivePrepareForeign{}}, &effectivePrepareWrapped{}} {
		got, err := EffectiveStageBeginReference(command)
		if !errors.Is(err, ErrInvalid) || got != (ReceiptReference{}) {
			t.Fatal("foreign/invalid command reference accepted", err)
		}
	}
}
