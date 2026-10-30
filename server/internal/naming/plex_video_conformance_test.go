package naming

import "testing"

// These vectors mirror the Plex naming guides with generated, blank paths. They
// never read, probe, identify or play real titles' media.

// Plex's movie guides place an edition on the folder and a version label on the
// file. The two stay separate evidence, and neither becomes the title or year.
func TestPlex45BladeRunnerEditionsAndVersionsTree(t *testing.T) {
	tree := []struct{ path, edition, version string }{
		{"Blade Runner (1982) {edition-Director's Cut}/Blade Runner (1982) - 1080p.mkv", "Director's Cut", "1080p"},
		{"Blade Runner (1982) {edition-Director's Cut}/Blade Runner (1982) - SD.mkv", "Director's Cut", "SD"},
		{"Blade Runner (1982) {edition-The Final Cut}/Blade Runner (1982) - 1080p.mkv", "The Final Cut", "1080p"},
	}
	for _, row := range tree {
		t.Run(row.path, func(t *testing.T) {
			r := Read(row.path, "movie")
			title, year := r.PreferredTitle()
			if title != "Blade Runner" || year != 1982 {
				t.Fatalf("title/year = %q %d; want Blade Runner 1982; reading %+v", title, year, r)
			}
			if len(r.Diagnostics) != 0 {
				t.Fatalf("skipped evidence: %v", r.Diagnostics)
			}
			if len(r.Editions) != 1 || r.Editions[0].Value != row.edition || r.Editions[0].Source != ParentFolder {
				t.Fatalf("folder edition evidence %+v; want %q from the folder", r.Editions, row.edition)
			}
			if r.Editions[0].Path == "" {
				t.Fatalf("edition lost its folder path: %+v", r.Editions[0])
			}
			if len(r.Versions) != 1 || r.Versions[0].Value != row.version || r.Versions[0].Source != FileName {
				t.Fatalf("file version evidence %+v; want %q from the file name", r.Versions, row.version)
			}
			if len(r.Parts) != 0 {
				t.Fatalf("an edition version became a split part: %+v", r.Parts)
			}
			if len(r.Episodes) != 0 {
				t.Fatalf("a movie edition became an episode: %+v", r.Episodes)
			}
		})
	}
}

// Plex's movie guide example carries a resolution label on one version and a
// plain "SD" label on the other. Both are versions of one title, not editions.
func TestPlex45PulpFictionVersionsStayFileVersions(t *testing.T) {
	for _, row := range []struct{ path, version string }{
		{"Pulp Fiction (1994)/Pulp Fiction (1994) - 1080p.mkv", "1080p"},
		{"Pulp Fiction (1994)/Pulp Fiction (1994) - SD.mkv", "SD"},
	} {
		t.Run(row.path, func(t *testing.T) {
			r := Read(row.path, "movie")
			title, year := r.PreferredTitle()
			if title != "Pulp Fiction" || year != 1994 {
				t.Fatalf("title/year = %q %d; want Pulp Fiction 1994; reading %+v", title, year, r)
			}
			if len(r.Versions) != 1 || r.Versions[0].Value != row.version || r.Versions[0].Source != FileName {
				t.Fatalf("version evidence %+v; want %q from the file name", r.Versions, row.version)
			}
			if len(r.Editions) != 0 || len(r.Parts) != 0 {
				t.Fatalf("a version label became an edition or part: editions=%+v parts=%+v", r.Editions, r.Parts)
			}
		})
	}
}

// Plex's inline and folder extras names for one film. Each keeps Plex's kind and
// its owning media folder; the extras never become versions or title evidence.
func TestPlex45AvatarInlineAndFolderExtrasKeepPlexKinds(t *testing.T) {
	inline := []struct{ name, kind string }{
		{"Avatar (2009)-behindthescenes.mkv", "behind_the_scenes"},
		{"Avatar (2009)-deleted.mkv", "deleted_scene"},
		{"Avatar (2009)-featurette.mkv", "featurette"},
		{"Avatar (2009)-interview.mkv", "interview"},
		{"Avatar (2009)-scene.mkv", "scene"},
		{"Avatar (2009)-short.mkv", "short"},
		{"Avatar (2009)-trailer.mkv", "trailer"},
		{"Avatar (2009)-other.mkv", "other"},
	}
	for _, row := range inline {
		t.Run(row.name, func(t *testing.T) {
			path := "Avatar (2009)/" + row.name
			r := Read(path, "movie")
			if len(r.Extras) != 1 || r.Extras[0].Value.Kind != row.kind || r.Extras[0].Value.ParentPath != "Avatar (2009)" || r.Extras[0].Source != FileName || r.Extras[0].Path != path {
				t.Fatalf("inline extra evidence %+v; want %q owned by Avatar (2009)", r.Extras, row.kind)
			}
			if len(r.Versions) != 0 {
				t.Fatalf("an inline extra became a version: %+v", r.Versions)
			}
		})
	}
	folders := []struct{ folder, kind string }{
		{"Behind The Scenes", "behind_the_scenes"},
		{"Deleted Scenes", "deleted_scene"},
		{"Featurettes", "featurette"},
		{"Interviews", "interview"},
		{"Scenes", "scene"},
		{"Shorts", "short"},
		{"Trailers", "trailer"},
		{"Other", "other"},
	}
	for _, row := range folders {
		t.Run(row.folder, func(t *testing.T) {
			path := "Avatar (2009)/" + row.folder + "/Clip.mkv"
			r := Read(path, "movie")
			if len(r.Extras) != 1 || r.Extras[0].Value.Kind != row.kind || r.Extras[0].Value.ParentPath != "Avatar (2009)" || r.Extras[0].Source != ParentFolder || r.Extras[0].Path != "Avatar (2009)/"+row.folder {
				t.Fatalf("folder extra evidence %+v; want %q owned by Avatar (2009)", r.Extras, row.kind)
			}
		})
	}
}

// Plex's Batman example puts the ID hint in braces beside a stand-alone film
// file, and its Dark Knight example splits one film over part files.
func TestPlex45StandaloneMovieIDAndDarkKnightParts(t *testing.T) {
	r := Read("Batman Begins (2005) {imdb-tt0372784}.mkv", "movie")
	title, year := r.PreferredTitle()
	if title != "Batman Begins" || year != 2005 {
		t.Fatalf("title/year = %q %d; want Batman Begins 2005; reading %+v", title, year, r)
	}
	if len(r.ProviderIDs) != 1 || r.ProviderIDs[0].Value != (ProviderID{"imdb", "tt0372784"}) || r.ProviderIDs[0].Source != FileName {
		t.Fatalf("stand-alone file ID hint %+v", r.ProviderIDs)
	}
	for _, part := range []int{1, 2} {
		path := "The Dark Knight (2008)/The Dark Knight (2008) - pt" + string(rune('0'+part)) + ".mkv"
		r = Read(path, "movie")
		title, year = r.PreferredTitle()
		if title != "The Dark Knight" || year != 2008 {
			t.Fatalf("%s: title/year = %q %d; want The Dark Knight 2008; reading %+v", path, title, year, r)
		}
		if len(r.Parts) != 1 || r.Parts[0].Value != part || r.Parts[0].Source != FileName {
			t.Fatalf("%s: part evidence %+v; want part %d", path, r.Parts, part)
		}
		if len(r.Editions) != 0 || len(r.Versions) != 0 {
			t.Fatalf("%s: a split part became an edition or version: %+v", path, r)
		}
	}
}

// Plex's TV guide names an edition on the show folder. It stays folder evidence
// and out of the show title; deciding whether it is a separate show happens in
// the catalogue, not here.
func TestPlex45ShowFolderEditionStaysFolderEvidence(t *testing.T) {
	path := "Babylon 5 (1994) {edition-Bluray Remaster} {tvdb-1234}/Season 01/Babylon 5 - s01e01 - Midnight on the Firing Line.mkv"
	r := Read(path, "tv")
	title, year := r.PreferredTitle()
	if title != "Babylon 5" || year != 1994 {
		t.Fatalf("title/year = %q %d; want Babylon 5 1994; reading %+v", title, year, r)
	}
	if len(r.Editions) != 1 || r.Editions[0].Value != "Bluray Remaster" || r.Editions[0].Source != GrandparentFolder || r.Editions[0].Path != "Babylon 5 (1994) {edition-Bluray Remaster} {tvdb-1234}" {
		t.Fatalf("show-folder edition evidence %+v", r.Editions)
	}
	if len(r.ProviderIDs) != 1 || r.ProviderIDs[0].Value != (ProviderID{"tvdb", "1234"}) || r.ProviderIDs[0].Source != GrandparentFolder {
		t.Fatalf("show-folder ID hint %+v", r.ProviderIDs)
	}
	seasonOne := false
	for _, season := range r.Seasons {
		seasonOne = seasonOne || season.Value == 1
	}
	if !seasonOne || len(r.Episodes) != 1 || len(r.Episodes[0].Value) != 1 || r.Episodes[0].Value[0] != 1 {
		t.Fatalf("season/episode evidence %+v %+v", r.Seasons, r.Episodes)
	}
}
