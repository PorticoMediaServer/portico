package api

import (
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
)

// AdminLibraryPage includes choices before any library exists, so Add library can choose
// its source before the first scan. Keys are the server's library kinds.
type AdminLibraryPage struct {
	Items             []AdminLibrary                                `json:"items"`
	NextCursor        string                                        `json:"nextCursor,omitempty"`
	MetadataProviders map[string][]metadata.LibraryMetadataProvider `json:"metadataProviders"`
}

func (s *Server) libraryMetadataChoices() map[string][]metadata.LibraryMetadataProvider {
	out := map[string][]metadata.LibraryMetadataProvider{}
	for _, kind := range []string{db.LibraryMovies, db.LibraryShows, db.LibraryAnime, db.LibraryMusic, db.LibraryAudiobooks, db.LibraryBooks, db.LibraryComics} {
		out[kind] = s.metadata.LibraryProviders(kind)
	}
	return out
}

// An explicit choice (including resetting to the kind's default) must be usable now.
func (s *Server) checkLibraryProvider(kind, name string) error {
	if name == "" {
		name = (db.LibrarySource{Kind: kind}).ProviderName()
	}
	for _, provider := range s.metadata.LibraryProviders(kind) {
		if provider.ID == name {
			if provider.Available {
				return nil
			}
			return ErrField("metadataProvider", provider.Reason)
		}
	}
	return ErrField("metadataProvider", "This metadata provider does not serve this kind of library.")
}
