package naming

import "testing"

func TestShowEditionComesOnlyFromNearestExplicitFolder(t *testing.T) {
	for _, row := range []struct{ path, want string }{
		{"Babylon 5 (1994) {edition-BlurayRemaster}/Season 01/Babylon 5 S01E01.mkv", "BlurayRemaster"},
		{"Babylon 5 (1994) {edition-BlurayRemaster}/Season 01/Babylon 5 S01E01 {edition-Extended}.mkv", "BlurayRemaster"},
		{"Babylon 5 (1994)/Season 01/Babylon 5 S01E01 {edition-Extended}.mkv", ""},
		{"Archive {edition-BlurayRemaster}/Babylon 5 (1994)/Season 01/Babylon 5 S01E01.mkv", ""},
		{"Babylon 5 (1994) {edition-Original} {edition-Remaster}/Season 01/Babylon 5 S01E01.mkv", ""},
	} {
		r := Read(row.path, "tv")
		edition, known := r.ShowFolderEdition()
		if known != (row.want != "") || edition.Value != row.want {
			t.Errorf("%s: %+v known=%v want=%q", row.path, edition, known, row.want)
		}
		if known && (!folderEvidence(edition.Source) || edition.Path == row.path) {
			t.Fatal("edition fabricated folder provenance", edition)
		}
	}
}
