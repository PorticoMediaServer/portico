package ingestion

import "testing"

// A film's own folder may hold its special features under any of the names a
// disc rip uses. They are extras owned by the film, never separate titles.
func TestSpecialFeaturesFoldersAreExtras(t *testing.T) {
	for _, row := range []struct{ folder, kind string }{
		{"Special Features", "featurette"},
		{"Extra Features", "featurette"},
		{"Bonus Material", "featurette"},
		{"Extras", "other"},
		{"Featurettes", "featurette"},
	} {
		t.Run(row.folder, func(t *testing.T) {
			f := newLoopFixture(t, "movie",
				"Film (2009)/Film (2009).mkv",
				"Film (2009)/"+row.folder+"/Making of Pandora.mkv",
			)
			f.finish(f.queue())
			if movies := f.count(`SELECT count(*) FROM catalog_entities WHERE kind=1 AND retired=0`); movies != 1 {
				t.Fatalf("films: %d; want 1 (the clip in %q is an extra, not a film)", movies, row.folder)
			}
			if extras := f.count(`SELECT count(*) FROM item_extras x JOIN catalog_entities o ON o.id=x.owner_id WHERE o.kind=1 AND o.title='Film' AND x.kind=?`, row.kind); extras != 1 {
				t.Fatalf("film %s extras: %d; want the clip in %q owned by Film (2009)", row.kind, extras, row.folder)
			}
		})
	}
}
