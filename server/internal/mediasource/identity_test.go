package mediasource

import (
	"errors"
	"strings"
	"testing"
)

func remoteEvidence() Evidence {
	return Evidence{Kind: StrongETag, Scope: "provider-a", Object: "movie-1", Revision: `"revision-1"`, Size: 900}
}

func TestVersionRequiresCompleteTrustedEvidence(t *testing.T) {
	cases := []struct {
		name     string
		evidence Evidence
		err      error
	}{
		{"unknown", Evidence{}, ErrIdentityRequired},
		{"weak", Evidence{Kind: StrongETag, Scope: "p", Object: "o", Revision: `W/"r"`, Size: 1}, ErrEvidence},
		{"missing_scope", Evidence{Kind: StrongETag, Object: "o", Revision: `"r"`, Size: 1}, ErrEvidence},
		{"unknown_size", Evidence{Kind: StrongETag, Scope: "p", Object: "o", Revision: `"r"`, Size: -1}, ErrEvidence},
		{"mtime_only_assumption", Evidence{Kind: LocalRevision, Scope: "root", Object: "device:inode", Revision: "mtime:ctime", Size: 1}, ErrIdentityRequired},
		{"injected_validator", Evidence{Kind: StrongETag, Scope: "p", Object: "o", Revision: "\"r\"\r\nInjected: true", Size: 1}, ErrEvidence},
		{"unknown_kind", Evidence{Kind: "guess", Scope: "p", Object: "o", Revision: "r", Size: 1}, ErrEvidence},
		{"bad_digest", Evidence{Kind: ContentSHA256, Scope: "p", Object: "o", Revision: strings.Repeat("z", 64), Size: 1}, ErrEvidence},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewVersion(tc.evidence); !errors.Is(err, tc.err) {
				t.Fatalf("got %v want %v", err, tc.err)
			}
		})
	}
}

func TestVersionPinsAllObjectEvidence(t *testing.T) {
	e := remoteEvidence()
	v, err := NewVersion(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Match(e); err != nil {
		t.Fatal(err)
	} // renewed locator is not evidence
	changes := []Evidence{e, e, e, e}
	changes[0].Revision = `"revision-2"`
	changes[1].Size = 901
	changes[2].Object = "different-object"
	changes[3].Scope = "different-provider"
	for _, changed := range changes {
		if err := v.Match(changed); !errors.Is(err, ErrSourceChanged) {
			t.Fatalf("changed representation accepted: %v", err)
		}
	}
	copy := v.Evidence()
	copy.Revision = `"mutated"`
	if err := v.Match(e); err != nil {
		t.Fatal("returned evidence mutated version")
	}
	if err := (Version{}).Match(e); !errors.Is(err, ErrEvidence) {
		t.Fatal("zero version accepted")
	}
}

func TestLocalAtomicReplacementIsDifferentEvenWithEqualSizeAndRevision(t *testing.T) {
	e := Evidence{Kind: LocalRevision, Scope: "mounted-root", Object: "device:inode-1", Revision: "unchanged-mtime-and-ctime", Size: 12, NoInPlaceMutation: true}
	v, err := NewVersion(e)
	if err != nil {
		t.Fatal(err)
	}
	e.Object = "device:inode-2"
	if err := v.Match(e); !errors.Is(err, ErrSourceChanged) {
		t.Fatal("atomic replacement accepted")
	}
}

func TestRepresentationPinsDependenciesPolicyAndTrackOrder(t *testing.T) {
	source, _ := NewVersion(remoteEvidence())
	e := remoteEvidence()
	e.Object = "subtitle"
	sub, _ := NewVersion(e)
	r := Representation{Source: source, Dependencies: []Dependency{{"subtitle", sub}}, Tracks: []string{"audio-2", "video-0"}, Timeline: "timeline-1", Transform: "copy", Output: "ts", Producer: "tool-and-config-1"}
	id, err := RepresentationID(r)
	if err != nil {
		t.Fatal(err)
	}
	variants := []Representation{r, r, r, r, r, r}
	variants[0].Producer = "tool-and-config-2"
	variants[1].Timeline = "timeline-2"
	variants[2].Transform = "burn-in"
	variants[3].Output = "fmp4"
	variants[4].Tracks = []string{"video-0", "audio-2"}
	e.Revision = `"subtitle-2"`
	next, _ := NewVersion(e)
	variants[5].Dependencies = []Dependency{{"subtitle", next}}
	for _, variant := range variants {
		got, err := RepresentationID(variant)
		if err != nil || got == id {
			t.Fatalf("identity did not change: %v", err)
		}
	}
	r.Dependencies = []Dependency{{"subtitle", Version{}}}
	if _, err := RepresentationID(r); !errors.Is(err, ErrEvidence) {
		t.Fatal("missing dependency accepted")
	}
}

func TestRepresentationDependencyOrderIsCanonical(t *testing.T) {
	source, _ := NewVersion(remoteEvidence())
	r := Representation{Source: source, Dependencies: []Dependency{{"subtitle", source}, {"font", source}}, Tracks: []string{"video-0"}, Timeline: "t", Transform: "x", Output: "o", Producer: "p"}
	a, err := RepresentationID(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Dependencies = []Dependency{{"font", source}, {"subtitle", source}}
	b, err := RepresentationID(r)
	if err != nil || a != b {
		t.Fatal("unordered dependency set changed identity")
	}
	r.Dependencies = append(r.Dependencies, Dependency{"font", source})
	if _, err := RepresentationID(r); !errors.Is(err, ErrEvidence) {
		t.Fatal("duplicate role accepted")
	}
}
