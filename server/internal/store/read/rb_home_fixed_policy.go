package read

// This purpose resolves only a current actual library envelope. Kind1 is the
// shared leaf-envelope selector, never an assertion about a returned work's kind.
// The complete fixed-family verifier must separately prove every emitted native
// leaf, parent kind, binding and personal fact before a container can publish.
const rbHomeFixedLibraryPolicy readPolicyID = 32502

type homeFixedLibraryWork struct{ purpose uint8 }

func (*homeFixedLibraryWork) readPolicyState() {}

var homeFixedLibraryPurpose = &homeFixedLibraryWork{purpose: 1}

func init() {
	registerReadPolicy(readPolicyRecipe{id: rbHomeFixedLibraryPolicy, domain: "rb-home-fixed-envelope", kinds: []int64{1}, stateful: true, validate: func(_ browseQuery, in BrowseInput, f browseFence) (string, error) {
		if in.policy.state != homeFixedLibraryPurpose || in.policy.subject != 0 || in.Library <= 0 || f.libraryID != in.Library || f.library == "" || f.container != 0 {
			return "", ErrInput
		}
		return "rb-home-fixed-envelope-v1", nil
	}})
}
func readHomeFixedLibraryFence(q browseQuery, in BrowseInput, library int64) (browseFence, error) {
	in.Library, in.Kind = library, 1
	in.Descending, in.KeepArticles, in.After = false, false, nil
	in.policy = readPolicyBinding{recipe: rbHomeFixedLibraryPolicy, state: homeFixedLibraryPurpose}
	return readBrowseFence(q, in)
}
