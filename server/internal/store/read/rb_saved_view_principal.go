package read

// Saved-view principal reads use RS's sealed scope registry. The private work
// marker selects only this compiled directory/resource purpose; it supplies no
// resource eligibility, which the same-snapshot owner/share points and final
// complete resource frame must still prove before publication.
const rbSavedViewPrincipalPolicy readPolicyID = 32500

type savedViewPrincipalWork struct{ purpose uint8 }

func (*savedViewPrincipalWork) readPolicyState() {}

var savedViewPrincipalPurpose = &savedViewPrincipalWork{purpose: 1}

func init() {
	registerReadPolicy(readPolicyRecipe{
		id: rbSavedViewPrincipalPolicy, domain: "rb-saved-view-principal", kinds: []int64{1},
		stateful: true, scope: readPrincipalScope,
		validate: func(_ browseQuery, in BrowseInput, f browseFence) (string, error) {
			if in.policy.state != savedViewPrincipalPurpose || in.policy.subject != 0 || f.library != "" {
				return "", ErrInput
			}
			return "rb-saved-view-principal-v1", nil
		},
	})
}

func readSavedViewPrincipal(q browseQuery, in BrowseInput) (browseFence, error) {
	// Caller catalogue/sort hints do not choose principal authority. Execution
	// separately resolves its actual stored library and takes the library fence.
	in.Library, in.Kind = 0, 1
	in.Descending, in.KeepArticles, in.After = false, false, nil
	in.policy = readPolicyBinding{recipe: rbSavedViewPrincipalPolicy, state: savedViewPrincipalPurpose}
	return readBrowseFence(q, in)
}
