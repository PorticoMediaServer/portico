package writer

import (
	"errors"
	"testing"
)

// The closed dispatcher accepts exactly the ten compiled IA result families.
// A valid scalar result cannot be relabeled as another command's family, and
// no caller-supplied type can become a canonical result.
func TestIAResultCodecFamiliesAreClosed(t *testing.T) {
	media := []func(iaMediaResult) any{
		func(v iaMediaResult) any { return iaEpisodeInventoryResult{v} },
		func(v iaMediaResult) any { return iaAudioInventoryResult{v} },
		func(v iaMediaResult) any { return iaReadingInventoryResult{v} },
		func(v iaMediaResult) any { return iaExtraInventoryResult{v} },
		func(v iaMediaResult) any { return iaMembershipResult{v} },
	}
	wrongMedia := []CommandID{IAAcceptEpisodeInventory, IAAcceptAudioInventory, IAAcceptReadingInventory, IAAcceptExtraInventory, IAAcceptMembership}
	for i, wrap := range media {
		id, r, v := iaMediaFixture(CommandID(110 + i))
		if _, err := canonicalResultDigest(id, r, wrap(v)); err != nil {
			t.Fatalf("media family %d: %v", 110+i, err)
		}
	}
	// Same media facts, a different family's wrapper, must not be relabeled.
	id, r, v := iaMediaFixture(IAAcceptReadingInventory)
	for i, wrap := range media {
		_, err := canonicalResultDigest(id, r, wrap(v))
		if i == 2 {
			if err != nil {
				t.Fatalf("reading family: %v", err)
			}
		} else if !errors.Is(err, ErrInvalid) {
			t.Fatalf("reading facts relabeled as %d", wrongMedia[i])
		}
	}

	libraryID, libraryRequest, library := iaConfigResultFixture()
	if _, err := canonicalResultDigest(libraryID, libraryRequest, iaConfigureLibraryResult{library}); err != nil {
		t.Fatal(err)
	}
	source := library
	source.original.purpose = IAConfigureSource
	source.original.sourceID = "s"
	source.libraryName, source.libraryKind = "", ""
	if _, err := canonicalResultDigest(libraryID, libraryRequest, iaConfigureSourceResult{source}); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalResultDigest(libraryID, libraryRequest, iaConfigureSourceResult{library}); !errors.Is(err, ErrInvalid) {
		t.Fatal("library result relabeled as configuration source")
	}
	if _, err := canonicalResultDigest(libraryID, libraryRequest, iaConfigureLibraryResult{source}); !errors.Is(err, ErrInvalid) {
		t.Fatal("source result relabeled as configuration library")
	}

	identity, request, retirement := iaRetirementResultFixture()
	libraryRetire := retirement
	libraryRetire.original.kind, libraryRetire.original.target = "library", retirement.original.libraryKey
	libraryRetire.original.sourceIncarnation, libraryRetire.original.rootIdentity, libraryRetire.original.sourceGeneration = "", "", 0
	if _, err := canonicalResultDigest(identity, request, iaRetireLibraryResult{libraryRetire}); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalResultDigest(identity, request, iaRetireSourceResult{retirement}); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalResultDigest(identity, request, iaRetireLibraryResult{retirement}); !errors.Is(err, ErrInvalid) {
		t.Fatal("folder result relabeled as retirement library")
	}
	if _, err := canonicalResultDigest(identity, request, iaRetireStepResult{retirement}); !errors.Is(err, ErrInvalid) {
		t.Fatal("begin result relabeled as retirement step")
	}
	stepIdentity := identity
	stepIdentity.Sequence, stepIdentity.OperationID = 2, [16]byte{3}
	if _, err := canonicalResultDigest(stepIdentity, request, iaRetireStepResult{retirement}); err != nil {
		t.Fatal(err)
	}

	if _, err := canonicalResultDigest(identity, request, struct{ Value int }{1}); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown result family produced a digest")
	}
}
