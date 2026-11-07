package writer

import (
	"errors"
	"testing"
)

func iaRetirementRequestFixture() (CommandIdentity, iaRetirementRequest) {
	return CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, iaRetirementRequest{
		scope: iaRetirementScope{libraryID: 7, libraryKey: "library", sourceID: "source", sourceIncarnation: "original", sourceGeneration: 3, libraryRevision: 5, resolvedRoot: "/media/original", rootIdentity: "device:inode"},
		actor: iaRetirementActor{authority: "local", accountID: "owner", profileID: "profile", membershipID: "membership", accountEpoch: 2, membershipRevision: 3, profileRevision: 4},
	}
}

func TestIARetirementOriginalBindsSourceAndActor(t *testing.T) {
	id, input := iaRetirementRequestFixture()
	c, err := newIARetirementCommand(IARetireSource, id, input)
	if err != nil || c.checkOriginal() != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*iaRetirementRequest){
		func(v *iaRetirementRequest) { v.scope.sourceIncarnation = "replacement" },
		func(v *iaRetirementRequest) { v.scope.sourceGeneration++ },
		func(v *iaRetirementRequest) { v.scope.libraryID++ },
		func(v *iaRetirementRequest) { v.scope.resolvedRoot += "/replacement" },
		func(v *iaRetirementRequest) { v.scope.rootIdentity += ":replacement" },
		func(v *iaRetirementRequest) { v.actor.accountEpoch++ },
		func(v *iaRetirementRequest) { v.actor.membershipID = "new-membership" },
	} {
		changed := input
		change(&changed)
		other, err := newIARetirementCommand(IARetireSource, id, changed)
		if err != nil || other.digest == c.digest {
			t.Fatal("original scope drift not bound", err)
		}
	}
	c.request.scope.sourceGeneration++
	if !errors.Is(c.checkOriginal(), ErrConflict) {
		t.Fatal("mutated original was accepted")
	}
}

func TestIARetirementVariantsAndDescriptors(t *testing.T) {
	id, input := iaRetirementRequestFixture()
	id.Sequence = 2
	if _, err := newIARetirementCommand(IARetireSource, id, input); !errors.Is(err, ErrInvalid) {
		t.Fatal("BEGIN accepted a later sequence", err)
	}
	id.Sequence = 1
	if _, err := newIARetirementCommand(IARetireLibrary, id, input); !errors.Is(err, ErrInvalid) {
		t.Fatal("library purpose accepted a source-shaped request", err)
	}
	input.scope.sourceID, input.scope.sourceIncarnation, input.scope.sourceGeneration = "", "", 0
	input.scope.rootIdentity, input.libraryName, input.confirmation = "", "Library", " Library "
	if c, err := newIARetirementCommand(IARetireLibrary, id, input); err != nil || c == nil {
		t.Fatal(err)
	}
	first, err := newIARetirementCommand(IARetireLibrary, id, input)
	if err != nil {
		t.Fatal(err)
	}
	input.confirmation = "Library"
	second, err := newIARetirementCommand(IARetireLibrary, id, input)
	if err != nil || first.digest == second.digest {
		t.Fatal("raw confirmation was normalized out of original request", err)
	}
	if _, err := newIARetirementCommand(IARetireSource, id, input); !errors.Is(err, ErrInvalid) {
		t.Fatal("source purpose lost its incarnation", err)
	}
	for _, purpose := range []CommandID{IARetireLibrary, IARetireSource} {
		d, err := Describe(purpose)
		if err != nil || d.Durability != Full || d.ID < 100 || d.ID > 199 {
			t.Fatal("retirement denial is not FULL", d, err)
		}
	}
	for _, purpose := range []CommandID{IAAcceptEpisodeInventory, IAAcceptAudioInventory, IAAcceptReadingInventory, IAReconcileObject} {
		d, err := Describe(purpose)
		if err != nil || d.Durability != Normal {
			t.Fatal("ingestion changed durability", d, err)
		}
	}
}
