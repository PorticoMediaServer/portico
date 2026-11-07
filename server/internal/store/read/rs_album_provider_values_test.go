package read

import (
	"reflect"
	"testing"
)

func TestRSAlbumProviderValuesPreserveObservation(t *testing.T) {
	secondary := []string{"Live", "cOMpILATION", "ProviderFutureType", "界\x00raw"}
	got := rsAlbumProviderValues("canonical-release", "original-redirect", "different-release-group", "2020-01-02T03:04:05.000000000Z", "Unknown Primary", secondary)
	want := RSAlbumProviderFact{ReleaseID: "canonical-release", RequestedReleaseID: "original-redirect", ReleaseGroupID: "different-release-group", ObservedAt: "2020-01-02T03:04:05.000000000Z", RawPrimaryType: "Unknown Primary", DisplayPrimaryType: "Compilation", SecondaryTypes: secondary}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	secondary[0] = "changed"
	if got.SecondaryTypes[0] != "Live" {
		t.Fatal("borrowed secondary array")
	}
}

func TestRSAlbumProviderRolePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, primary string
		secondary     []string
		primaryArtist int64
		want          RSArtistAlbumRole
	}{
		{"appearance-first", "EP", []string{"Compilation"}, 2, RSArtistAlbumAppearance},
		{"compilation-over-ep", "EP", []string{"Live", "Compilation"}, 1, RSArtistAlbumCompilation},
		{"compilation-over-single", "Single", []string{"compilation"}, 1, RSArtistAlbumCompilation},
		{"ep", "eP", []string{"Live"}, 1, RSArtistAlbumEP},
		{"single", "sINGLE", nil, 1, RSArtistAlbumSingle},
		{"unknown-primary", "Future Kind", []string{"EP", "Single"}, 1, RSArtistAlbumAlbum},
		{"secondary-ep-is-not-primary", "Album", []string{"EP"}, 1, RSArtistAlbumAlbum},
		{"unicode-fold-is-not-ascii", "ſingle", nil, 1, RSArtistAlbumAlbum},
		{"nul-is-not-terminator", "EP\x00other", nil, 1, RSArtistAlbumAlbum},
		{"unknown-secondary", "EP", []string{"Compilation\x00other"}, 1, RSArtistAlbumEP},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := rsAlbumProviderValues("release", "requested", "group", "original observation", tc.primary, tc.secondary)
			if got := rsAlbumRole(tc.primaryArtist, 1, &source); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
	if rsAlbumRole(1, 1, nil) != RSArtistAlbumAlbum || rsAlbumRole(2, 1, nil) != RSArtistAlbumAppearance {
		t.Fatal("local fallback or relationship changed")
	}
}
