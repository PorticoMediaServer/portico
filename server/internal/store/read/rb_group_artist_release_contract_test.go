package read

import (
	"reflect"
	"testing"
)

func TestRBReleaseOwnerClassifier(t *testing.T) {
	// Literal owner-contract examples; no legacy import or accepting-source flag.
	for _, c := range []struct {
		name, primary string
		secondary     []rbReleaseSecondary
		appearance    bool
		want          rbReleaseClass
	}{
		{name: "local fallback", want: rbReleaseAlbum},
		{name: "known album", primary: "Album", want: rbReleaseAlbum},
		{name: "unknown retained primary", primary: "Broadcast", want: rbReleaseAlbum},
		{name: "ep", primary: "eP", want: rbReleaseEP},
		{name: "single", primary: "SINGLE", want: rbReleaseSingle},
		{name: "compilation wins EP", primary: "EP", secondary: []rbReleaseSecondary{{0, "Live"}, {1, "cOmPiLaTiOn"}, {2, "Unknown type"}}, want: rbReleaseCompilation},
		{name: "secondary EP is not primary", primary: "Album", secondary: []rbReleaseSecondary{{0, "EP"}}, want: rbReleaseAlbum},
		{name: "appearance first", primary: "Single", secondary: []rbReleaseSecondary{{0, "Compilation"}}, appearance: true, want: rbReleaseAppearance},
		{name: "no whitespace invention", primary: " EP ", secondary: []rbReleaseSecondary{{0, " Compilation"}}, want: rbReleaseAlbum},
		{name: "ASCII only", primary: "ſingle", secondary: []rbReleaseSecondary{{0, "Compılation"}}, want: rbReleaseAlbum},
	} {
		t.Run(c.name, func(t *testing.T) {
			before := append([]rbReleaseSecondary(nil), c.secondary...)
			if got := rbClassifyRelease(c.appearance, c.primary, c.secondary); got != c.want {
				t.Fatal(got, c.want)
			}
			if !reflect.DeepEqual(before, c.secondary) {
				t.Fatal("original ordered vocabulary changed")
			}
		})
	}
	if rbMBSecondaryVocabulary != "release_group_secondary_type" {
		t.Fatal("owner vocabulary")
	}
}
