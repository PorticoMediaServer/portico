package catalog

import (
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/segmentmarkers"
)

// ReadingDetail uses the current Reading publication projection for an exact
// visible leaf. It offers no timed-media action, source file or derived duration.
func (s *Service) ReadingDetail(viewer Viewer, server, item string, admit SavedGroupAdmission) (Detail, error) {
	out := Detail{Markers: []segmentmarkers.Marker{}, Actions: []DetailAction{}, Collections: []DetailCollection{}, Metadata: DetailMetadata{Status: "unavailable", Sources: []MetadataSource{}, Ratings: []ProviderRating{}, Genres: []Genre{}, Credits: []Credit{}}}
	if err := s.VisibleItem(s.Context(), viewer, item); err != nil {
		return out, err
	}
	entries := map[string]ContentEntry{}
	if err := s.resourceReadingEntries(viewer, []string{item}, entries, admit); err != nil {
		return out, err
	}
	entry, ok := entries[item]
	if !ok {
		return out, sql.ErrNoRows
	}
	out.Scope = DetailScope{server, entry.LibraryID, item, viewer.Fence}
	var err error
	out.Revision, err = s.ContentRevision(entry.LibraryID, viewer.Profile)
	if err != nil {
		return out, err
	}
	// Existing Detail's numeric slots remain their zero value. No action or
	// file advertises them as a playable interval or a Reading location.
	out.Item = Item{ID: item, LibraryID: entry.LibraryID, Kind: entry.Kind, Title: entry.Title, PosterURL: entry.PosterURL, AddedAt: entry.AddedAt}
	if entry.Available != nil {
		out.Item.Available = *entry.Available
	}
	if entry.Year != nil {
		out.Item.Year = *entry.Year
	}
	out.Personal, err = readPersonal(s.read(), viewer.Profile, item)
	return out, err
}
