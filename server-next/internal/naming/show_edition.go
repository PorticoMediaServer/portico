package naming

// ShowFolderEdition is the explicit edition on the nearest title folder. A
// file's own edition labels its version, and an outer category's edition never
// labels the show. Ambiguous folder labels remain unknown.
func (r Reading) ShowFolderEdition() (Evidence[string], bool) {
	for _, title := range r.Titles {
		if !folderEvidence(title.Source) {
			continue
		}
		var found Evidence[string]
		known := false
		for _, edition := range r.Editions {
			if edition.Path != title.Path || !folderEvidence(edition.Source) || edition.Value == "" {
				continue
			}
			if known && found.Value != edition.Value {
				return Evidence[string]{}, false
			}
			found, known = edition, true
		}
		return found, known
	}
	return Evidence[string]{}, false
}

func folderEvidence(source Source) bool {
	return source == DirectoryName || source == ParentFolder || source == GrandparentFolder || source == AncestorFolder
}
