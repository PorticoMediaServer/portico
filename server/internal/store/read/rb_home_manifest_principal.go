package read

// Home manifest principal reads use RS's sealed scope registry. The private work
// marker selects only this compiled layout/library-projection purpose; it supplies no
// descriptor or library eligibility, which the same-snapshot complete native projection and final
// complete source frame must still prove before publication.
const rbHomeManifestPrincipalPolicy readPolicyID = 32501

type homeManifestPrincipalWork struct{ purpose uint8 }

func (*homeManifestPrincipalWork) readPolicyState() {}

var homeManifestPrincipalPurpose = &homeManifestPrincipalWork{purpose: 1}

func init() {
	registerReadPolicy(readPolicyRecipe{
		id: rbHomeManifestPrincipalPolicy, domain: "rb-home-manifest-principal", kinds: []int64{1},
		stateful: true, scope: readPrincipalScope,
		validate: func(_ browseQuery, in BrowseInput, f browseFence) (string, error) {
			if in.policy.state != homeManifestPrincipalPurpose || in.policy.subject != 0 || f.library != "" {
				return "", ErrInput
			}
			return "rb-home-manifest-principal-v1", nil
		},
	})
}

func readHomeManifestPrincipal(q browseQuery, in BrowseInput) (browseFence, error) {
	// Caller catalogue/sort hints do not choose principal authority. Execution
	// separately resolves its actual stored library and takes the library fence.
	in.Library, in.Kind = 0, 1
	in.Descending, in.KeepArticles, in.After = false, false, nil
	in.policy = readPolicyBinding{recipe: rbHomeManifestPrincipalPolicy, state: homeManifestPrincipalPurpose}
	return readBrowseFence(q, in)
}
