package api

import "github.com/PorticoMediaServer/portico/server-next/internal/db"

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
