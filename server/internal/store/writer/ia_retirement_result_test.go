package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func iaRetirementResultFixture() (CommandIdentity, RequestDigest, iaRetirementResult) {
	id := CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}
	request := RequestDigest{9}
	return id, request, iaRetirementResult{
		original: iaRetirementOriginal{kind: "folder", target: "source", libraryID: 7,
			libraryKey: "library", resolvedRoot: "/media/original", sourceIncarnation: "original",
			sourceGeneration: 3, rootIdentity: "device:inode", operationID: id.OperationID, requestDigest: request},
		acceptedRevision: 1, appliedMS: 100, state: "retiring",
	}
}

// Independent wire oracle: bytes.Buffer + binary.Write, no requestEncoder,
// production writer, preflight, retained-byte constant, or reflection codec.
func iaRetirementResultWire(id CommandIdentity, request RequestDigest, v iaRetirementResult, command string) []byte {
	var b bytes.Buffer
	number := func(n int64) { _ = binary.Write(&b, binary.BigEndian, n) }
	text := func(s string) { number(int64(len(s))); b.WriteString(s) }
	flag := func(v bool) {
		if v {
			b.WriteByte(1)
		} else {
			b.WriteByte(0)
		}
	}
	b.WriteString("portico-writer-result-sha256-v1\x00")
	b.Write([]byte{0, 1})
	text(command)
	text("retirement")
	text("applied")
	b.Write(id.Incarnation[:])
	number(id.Sequence)
	b.Write(id.OperationID[:])
	b.Write(request[:])
	o := v.original
	text(o.kind)
	text(o.target)
	number(o.libraryID)
	text(o.libraryKey)
	text(o.resolvedRoot)
	flag(o.kind == "folder")
	text(o.sourceIncarnation)
	number(o.sourceGeneration)
	text(o.rootIdentity)
	b.Write(o.operationID[:])
	b.Write(o.requestDigest[:])
	for _, n := range []int64{v.acceptedRevision, v.appliedMS, v.phase, v.entityKind, v.entityCursor} {
		number(n)
	}
	text(v.objectCursor)
	flag(v.walkRemoved)
	flag(v.walkKept)
	text(v.state)
	number(0)
	number(0)
	return b.Bytes()
}

func TestIARetirementResultCanonicalWire(t *testing.T) {
	id, request, source := iaRetirementResultFixture()
	library := source
	library.original.kind, library.original.target = "library", source.original.libraryKey
	library.original.sourceIncarnation, library.original.rootIdentity, library.original.sourceGeneration = "", "", 0
	step := source
	step.acceptedRevision, step.appliedMS, step.phase, step.entityKind, step.entityCursor = 4, 120, 3, 7, 42
	step.objectCursor, step.walkRemoved, step.walkKept = "raw/é/007", true, true
	terminal := step
	terminal.phase, terminal.state = 5, "complete"
	for _, tc := range []struct {
		name   string
		kind   CommandID
		result iaRetirementResult
		value  func(iaRetirementResult) any
	}{
		{"retire_library", IARetireLibrary, library, func(v iaRetirementResult) any { return iaRetireLibraryResult{v} }},
		{"retire_source", IARetireSource, source, func(v iaRetirementResult) any { return iaRetireSourceResult{v} }},
		{"retire_step", IARetireStep, step, func(v iaRetirementResult) any { return iaRetireStepResult{v} }},
		{"retire_step", IARetireStep, terminal, func(v iaRetirementResult) any { return iaRetireStepResult{v} }},
	} {
		t.Run(tc.name+tc.result.state, func(t *testing.T) {
			identity := id
			if tc.kind == IARetireStep {
				identity.Sequence, identity.OperationID = 2, [16]byte{3}
			}
			wire := iaRetirementResultWire(identity, request, tc.result, tc.name)
			var actual iaResultRecorder
			encoder := &requestEncoder{h: &actual}
			if err := encodeCanonicalResult(encoder, identity, request, tc.value(tc.result)); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(wire, actual.Bytes()) {
				t.Fatal("canonical wire differs from independent encoder")
			}
			got, err := canonicalResultDigest(identity, request, tc.value(tc.result))
			if err != nil || got != ResultDigest(sha256.Sum256(wire)) {
				t.Fatal("digest mismatch", err)
			}
			texts := []string{tc.name, tc.result.original.kind, tc.result.original.target, tc.result.original.libraryKey, tc.result.original.resolvedRoot,
				tc.result.original.sourceIncarnation, tc.result.original.rootIdentity, tc.result.objectCursor, tc.result.state}
			textBytes := 0
			for _, s := range texts {
				textBytes += len(s)
			}
			if len(wire)-textBytes != iaRetirementResultFixedBytes {
				t.Fatal("wire overhead", len(wire)-textBytes, iaRetirementResultFixedBytes)
			}
		})
	}
}

// bytes.Buffer has the wrong Sum signature for hash.Hash; use a forwarding
// recorder without hashing for the exact wire check.
type iaResultRecorder struct{ bytes.Buffer }

func (b *iaResultRecorder) Sum(in []byte) []byte { return append(in, b.Bytes()...) }
func (*iaResultRecorder) Size() int              { return 32 }
func (*iaResultRecorder) BlockSize() int         { return 64 }

func TestIARetirementResultBindsEveryFact(t *testing.T) {
	id, request, value := iaRetirementResultFixture()
	id.Sequence, id.OperationID = 2, [16]byte{3}
	before, err := canonicalResultDigest(id, request, iaRetireStepResult{value})
	if err != nil {
		t.Fatal(err)
	}
	// All original/result scalars and texts must affect the digest. Reflection
	// enumerates the schema, while the mutations remain semantically valid.
	mutations := []func(*iaRetirementResult){
		func(v *iaRetirementResult) { v.original.target += "x" }, func(v *iaRetirementResult) { v.original.libraryID++ },
		func(v *iaRetirementResult) { v.original.libraryKey += "x" }, func(v *iaRetirementResult) { v.original.resolvedRoot += "x" },
		func(v *iaRetirementResult) { v.original.sourceIncarnation += "x" }, func(v *iaRetirementResult) { v.original.sourceGeneration++ },
		func(v *iaRetirementResult) { v.original.rootIdentity += "x" }, func(v *iaRetirementResult) { v.original.operationID[1]++ },
		func(v *iaRetirementResult) { v.original.requestDigest[0]++ }, func(v *iaRetirementResult) { v.acceptedRevision++ },
		func(v *iaRetirementResult) { v.appliedMS++ }, func(v *iaRetirementResult) { v.phase++ }, func(v *iaRetirementResult) { v.entityKind++ },
		func(v *iaRetirementResult) { v.entityCursor++ }, func(v *iaRetirementResult) { v.objectCursor += "x" },
		func(v *iaRetirementResult) { v.walkRemoved = true }, func(v *iaRetirementResult) { v.walkKept = true },
	}
	if len(mutations) != reflect.TypeFor[iaRetirementOriginal]().NumField()-1+reflect.TypeFor[iaRetirementResult]().NumField()-2 {
		t.Fatal("new facts need a mutation")
	}
	for i, mutate := range mutations {
		changed := value
		mutate(&changed)
		got, err := canonicalResultDigest(id, request, iaRetireStepResult{changed})
		if err != nil || got == before {
			t.Fatalf("fact %d omitted: %v", i, err)
		}
	}
	for _, change := range []func(*CommandIdentity, *RequestDigest){
		func(i *CommandIdentity, _ *RequestDigest) { i.Incarnation[0]++ },
		func(i *CommandIdentity, _ *RequestDigest) { i.Sequence++ },
		func(i *CommandIdentity, _ *RequestDigest) { i.OperationID[0]++ },
		func(_ *CommandIdentity, r *RequestDigest) { r[0]++ },
	} {
		i, r := id, request
		change(&i, &r)
		got, err := canonicalResultDigest(i, r, iaRetireStepResult{value})
		if err != nil || got == before {
			t.Fatal("original command binding omitted", err)
		}
	}
}

func TestIARetirementResultRefusesVariantsAndBytes(t *testing.T) {
	id, request, value := iaRetirementResultFixture()
	for _, mutate := range []func(*iaRetirementResult){
		func(v *iaRetirementResult) { v.original.kind = "library" }, func(v *iaRetirementResult) { v.original.rootIdentity = "" },
		func(v *iaRetirementResult) { v.original.resolvedRoot = "" }, func(v *iaRetirementResult) { v.original.requestDigest[0]++ },
		func(v *iaRetirementResult) { v.phase = 6 }, func(v *iaRetirementResult) { v.phase = 1 },
		func(v *iaRetirementResult) { v.entityCursor = 1 }, func(v *iaRetirementResult) { v.state = "complete" },
		func(v *iaRetirementResult) { v.objectCursor = "bad\x00cursor" }, func(v *iaRetirementResult) { v.original.target = "\xff" },
		func(v *iaRetirementResult) { v.original.libraryID = 0 }, func(v *iaRetirementResult) { v.appliedMS = -1 },
	} {
		bad := value
		mutate(&bad)
		if got, err := canonicalResultDigest(id, request, iaRetireSourceResult{bad}); !errors.Is(err, ErrInvalid) || got != (ResultDigest{}) {
			t.Fatal("invalid result produced a digest", err)
		}
	}
	if _, err := canonicalResultDigest(id, request, iaRetireLibraryResult{value}); !errors.Is(err, ErrInvalid) {
		t.Fatal("source relabeled as library", err)
	}
	if _, err := canonicalResultDigest(id, request, iaRetireStepResult{value}); !errors.Is(err, ErrInvalid) {
		t.Fatal("begin relabeled as step", err)
	}
	id.Sequence, id.OperationID = 2, [16]byte{3}
	value.objectCursor = strings.Repeat("a", maxCanonicalResultBytes)
	if got, err := canonicalResultDigest(id, request, iaRetireStepResult{value}); !errors.Is(err, ErrInvalid) || got != (ResultDigest{}) {
		t.Fatal("oversized tail produced digest", err)
	}
}

func TestIARetirementRetainedBytesIndependent(t *testing.T) {
	_, _, v := iaRetirementResultFixture()
	v.original.target = "ia-source-1"
	got, err := iaRetirementRetainedBytes(IARetireSource, v)
	if err != nil || got != 238 {
		t.Fatal("independent native payload fixture", got, err)
	}
	v.objectCursor = "é/064"
	got, err = iaRetirementRetainedBytes(IARetireStep, v)
	if err != nil || got != 242 {
		t.Fatal("UTF-8 octets, not runes", got, err)
	}
}
