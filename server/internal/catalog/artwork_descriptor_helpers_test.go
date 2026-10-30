package catalog

import (
	"net/url"

	"github.com/PorticoMediaServer/portico/server/internal/artworkref"
)

// fixtureArtQuery is the described query of a fixture source whose slots are
// all its own one blob, as the catalogue fixtures publish them.
func fixtureArtQuery(source string, width, height, bytes int) string {
	slot := artworkref.Slot{Blob: source, Width: width, Height: height, Bytes: bytes}
	return artworkref.Descriptor{Small: slot, Large: slot}.Query(source)
}

// fixtureArtURL is selectedArtworkURL for such a fixture source.
func fixtureArtURL(kind, id, role, subject, source string, width, height, bytes int) string {
	path := "/v1/metadata/" + url.PathEscape(kind) + "/" + url.PathEscape(id) + "/art/" + role + "?" + fixtureArtQuery(source, width, height, bytes)
	if subject != "" {
		path += "&subject=" + url.QueryEscape(subject)
	}
	return path
}
