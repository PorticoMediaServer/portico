package compactcatalog

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog/identityvalue"
)

func TestIdentityValueDelegates(t *testing.T) {
	if got, want := IdentityText("Schindler’s List"), identityvalue.IdentityText("Schindler’s List"); got != want {
		t.Fatal(got, want)
	}
	if got, want := MovieKey("Amélie", 2001), identityvalue.MovieKey("Amélie", 2001); got != want {
		t.Fatal(got, want)
	}
	if got, want := ItemKey("/media", "/media/Film.mkv", 2), identityvalue.ItemKey("/media", "/media/Film.mkv", 2); got != want {
		t.Fatal(got, want)
	}
}
