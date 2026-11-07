package writer

import (
	"bytes"
	"testing"
)

func TestN1FreshSourceKeepsBothRootMeanings(t *testing.T) {
	in, id := inventoryPublicInput(t)
	command, err := NewInventoryAcceptCommand(id, in)
	if err != nil {
		t.Fatal(err)
	}
	c := command.(*inventoryAcceptCommand)
	want := scanSourceFacts{libraryID: in.Scope.LibraryID, libraryKey: in.Scope.LibraryKey,
		policyRevision: in.Run.PolicyRevision, source: c.request.source, identityRoot: []byte("/legacy-name-root")}
	if !inventoryDriverSourceMatches(c, want, want) {
		t.Fatal("exact retained/current native source refused")
	}
	for _, change := range []struct {
		name string
		edit func(*scanSourceFacts)
	}{
		{"logical_root", func(f *scanSourceFacts) { f.identityRoot = []byte("/replacement-name-root") }},
		{"physical_root", func(f *scanSourceFacts) { f.source.resolvedRoot = []byte("/replacement-physical-root") }},
		{"policy", func(f *scanSourceFacts) { f.policyRevision++ }},
		{"generation", func(f *scanSourceFacts) { f.source.generation++ }},
	} {
		t.Run(change.name, func(t *testing.T) {
			actual := want
			actual.source.resolvedRoot = bytes.Clone(want.source.resolvedRoot)
			change.edit(&actual)
			if inventoryDriverSourceMatches(c, want, actual) {
				t.Fatal("fresh source changed after observation/freeze")
			}
		})
	}
	wrongPrepared := want
	wrongPrepared.policyRevision++
	if inventoryDriverSourceMatches(c, wrongPrepared, want) {
		t.Fatal("current source concealed a mismatched retained preparation")
	}
}
