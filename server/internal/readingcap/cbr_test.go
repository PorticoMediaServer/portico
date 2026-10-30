package readingcap

import (
	"slices"
	"strings"
	"testing"
)

func TestCBRBuildDomainAndOriginalLocalSourcePredicate(t *testing.T) {
	if slices.Contains(ComicFormats(), "cbr") != CBR() || slices.Contains(ComicArchiveFormats(), "cbr") != CBR() {
		t.Fatal("CBR list and archive domains differ from the compiled decoder")
	}
	if slices.Contains(ComicArchiveFormats(), "pdf") {
		t.Fatal("PDF became an archive")
	}
	guard := LocalComicArchiveSQL("publication")
	for _, part := range []string{"publication.format NOT IN('cb7','cbr')", "cb7_source.kind='local'", "cb7_source.library_id=cb7_library.library_id", "cb7_object.root_incarnation=cb7_source.incarnation", "cb7_object.size=cb7_asset.size", "cb7_object.modified_ns=cb7_asset.modified_ns"} {
		if !strings.Contains(guard, part) {
			t.Fatal("archive source predicate lost original physical binding", part)
		}
	}
	if guard != LocalCB7SQL("publication") {
		t.Fatal("existing reader seam differs from the source predicate")
	}
}
