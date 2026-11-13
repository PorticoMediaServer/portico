package api

import (
	"errors"
	"net/http"
	"slices"
	"strconv"

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

// libraryLanguage uses the same advertised choices for validation and defaults. Empty means default.
func (s *Server) libraryLanguage(kind, name, language string) (string, error) {
	name = (db.LibrarySource{Kind: kind, Provider: name}).ProviderName()
	for _, provider := range s.metadata.LibraryProviders(kind) {
		if provider.ID != name {
			continue
		}
		if language == "" {
			return provider.DefaultLanguage, nil
		}
		if slices.Contains(provider.Languages, language) {
			return language, nil
		}
		break
	}
	return "", ErrField("language", "Choose a language offered by this metadata provider.")
}

func (s *Server) libraryOut(r *http.Request, l db.AdminLibrary) (AdminLibrary, error) {
	out := AdminLibrary{ID: l.PublicID, Name: l.Name, Kind: l.Kind, Language: l.Language, Folders: s.foldersOut(l.Library, l.Folders),
		Options: libraryOptionsOut(l.Options), LastScanStartedAt: timePtr(l.ScanStartedAt), LastScanFinishedAt: timePtr(l.ScanFinishedAt),
		Revision: strconv.FormatInt(l.Revision, 10)}
	out.MetadataProvider = (db.LibrarySource{Kind: l.Kind, Provider: l.MetadataProvider}).ProviderName()
	out.MetadataProviders = s.metadata.LibraryProviders(l.Kind)
	if out.Language == "" {
		out.Language, _ = s.libraryLanguage(l.Kind, out.MetadataProvider, "")
	}
	if l.Kind == db.LibraryMovies || l.Kind == db.LibraryShows || l.Kind == db.LibraryAnime {
		out.Region = &l.Region
	}
	if !l.Options.NoWatch {
		out.WatchNote = watchNote
	}
	job, err := s.db.JobFor(r.Context(), db.JobScan, l.PublicID)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return out, err
	}
	out.Scanning = err == nil && (job.State == db.JobWaiting || job.State == db.JobRunning)
	hashes, err := s.db.LibraryCoverHashes(r.Context(), l.Library)
	out.LibraryCoverURLs = []string{}
	for _, hash := range hashes {
		out.LibraryCoverURLs = append(out.LibraryCoverURLs, "/v1/images/"+hash)
	}
	return out, err
}
