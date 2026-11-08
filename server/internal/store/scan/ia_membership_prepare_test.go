package scan

import (
	"errors"
	"slices"
	"testing"
)

func iaMembershipEvidence(t *testing.T, kind, path string) MediaEvidence {
	t.Helper()
	evidence, err := PrepareMediaEvidence(kind, iaEvidenceFile(path), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}

func TestIAMembershipExtrasBeforeGenericAssociation(t *testing.T) {
	evidence := iaMembershipEvidence(t, "tv", "Show/Trailers/Preview (2019) [Director's Cut] Part 2.mkv")
	owner := &IAMembershipOwner{Entity: 900, Library: 42}
	plan, err := PrepareIAMembership(evidence, owner, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Association != IAMembershipExtra || plan.ExtraType != "trailer" || plan.ParentPath != "Show" {
		t.Fatalf("extra association=%+v", plan)
	}
	if !slices.Contains(plan.Editions, "Director's Cut") || !slices.Equal(plan.Parts, []int{2}) {
		t.Fatalf("edition/part evidence=%+v", plan)
	}
	if plan.Owner == nil || plan.Owner.Entity != 900 || plan.Owner.Library != 42 {
		t.Fatalf("owner=%+v", plan.Owner)
	}
	// The same file never becomes a generic association of its owner.
	generic, err := PrepareIAMembership(iaMembershipEvidence(t, "tv", "Show/Film (2019) [Director's Cut].mkv"), nil, 1<<20)
	if err != nil || generic.Association != IAMembershipGeneric || generic.ExtraType != "" || generic.ParentPath != "" || !slices.Contains(generic.Editions, "Director's Cut") {
		t.Fatalf("generic association=%+v err=%v", generic, err)
	}
}

func TestIAMembershipUnresolvedOwnerIsExplicit(t *testing.T) {
	plan, err := PrepareIAMembership(iaMembershipEvidence(t, "tv", "Show/Extras/Unresolved.mkv"), nil, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	// No parent path, title or item is promoted into an owner identity.
	if plan.Association != IAMembershipExtra || plan.Owner != nil || plan.ParentPath != "Show" || plan.ExtraType != "other" {
		t.Fatalf("unresolved owner=%+v", plan)
	}
}

func TestIAMembershipRefusesForeignAndRetiredOwner(t *testing.T) {
	evidence := iaMembershipEvidence(t, "tv", "Show/Trailers/Preview.mkv")
	for _, owner := range []*IAMembershipOwner{
		{Entity: 900, Library: 7},
		{Entity: 900, Library: 42, Retired: true},
		{Entity: 0, Library: 42},
	} {
		if plan, err := PrepareIAMembership(evidence, owner, 1<<20); !errors.Is(err, ErrInvalid) || plan.Association != 0 || plan.Owner != nil {
			t.Fatalf("owner %+v plan=%+v err=%v", owner, plan, err)
		}
	}
	// An owner without extra evidence would invent a relationship.
	if _, err := PrepareIAMembership(iaMembershipEvidence(t, "tv", "Show/Film.mkv"), &IAMembershipOwner{Entity: 900, Library: 42}, 1<<20); !errors.Is(err, ErrInvalid) {
		t.Fatalf("generic owner err=%v", err)
	}
}

func TestIAMembershipNoAudioOrReadingExtras(t *testing.T) {
	for _, v := range []struct{ kind, path string }{
		{"music", "Artist/Album/Trailers/Trailer.mkv"},
		{"audiobook", "Author/Book/Extras/Trailer.mkv"},
		{"book", "Author/Book/Extras/Trailer.mkv"},
	} {
		plan, err := PrepareIAMembership(iaMembershipEvidence(t, v.kind, v.path), nil, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		// A video extras convention never reclassifies audio or reading folders.
		if plan.Association != IAMembershipGeneric || plan.ExtraType != "" {
			t.Fatalf("%s became a video extra: %+v", v.kind, plan)
		}
	}
}

func TestIAMembershipBudgetAndScope(t *testing.T) {
	evidence := iaMembershipEvidence(t, "tv", "Show/Trailers/Preview.mkv")
	if _, err := PrepareIAMembership(evidence, nil, 0); !errors.Is(err, ErrCapacity) {
		t.Fatalf("zero budget err=%v", err)
	}
	if _, err := PrepareIAMembership(MediaEvidence{}, nil, 1<<20); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty evidence err=%v", err)
	}
	// Every retained value is charged before any slice is allocated.
	if _, err := PrepareIAMembership(evidence, nil, 1); !errors.Is(err, ErrCapacity) {
		t.Fatalf("tiny budget err=%v", err)
	}
}
