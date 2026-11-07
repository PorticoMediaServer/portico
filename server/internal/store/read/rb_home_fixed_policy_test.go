package read

import (
	"errors"
	"testing"
)

// Values-only capability guard control; actual authority/birth/library proof is
// exercised by the native fixed-row controls, never supplied by this test fence.
func TestRBHomeFixedPolicyValues(t *testing.T) {
	in := BrowseInput{Library: 1, Kind: 1}
	f := browseFence{libraryID: 1, library: "movies"}
	in.policy = readPolicyBinding{recipe: rbHomeFixedLibraryPolicy, state: homeFixedLibraryPurpose}
	if e := validateReadPolicy(nil, in, &f); e != nil || f.policy != rbHomeFixedLibraryPolicy {
		t.Fatal(f, e)
	}
	in.policy.state = &homeFixedLibraryWork{purpose: 1}
	if e := validateReadPolicy(nil, in, &f); !errors.Is(e, ErrInput) {
		t.Fatal("foreign work minted fixed-family purpose", e)
	}
	in.policy.state = homeFixedLibraryPurpose
	in.policy.subject = 1
	if e := validateReadPolicy(nil, in, &f); !errors.Is(e, ErrInput) {
		t.Fatal("subject widened envelope purpose", e)
	}
}
