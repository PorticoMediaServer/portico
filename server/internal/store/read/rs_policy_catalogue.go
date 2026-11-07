package read

// This is exactly the pre-seam catalogue envelope. New Reading or container
// eligibility belongs to a separately reviewed, explicitly selected recipe.
func init() {
	registerReadPolicy(readPolicyRecipe{id: 0, domain: "catalogue-default", kinds: []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, validate: validateCataloguePolicy})
}
func validateCataloguePolicy(_ browseQuery, _ BrowseInput, f browseFence) (string, error) {
	if f.container != 0 && (f.maximumAge >= 0 || f.allowUnrated == 0) {
		return "", ErrUnqualified
	}
	return "", nil
}
