package catalog

import "github.com/PorticoMediaServer/portico/server/internal/readprep"

// UseReadPreparations installs the one process-owned memory budget before any
// request or background reader starts. Context clones share it. The caller owns
// shutdown; constructing catalogue services never starts preparation workers.
func (s *Service) UseReadPreparations(store *readprep.Store) {
	s.state.readPreparations = store
}

// ReadPreparations returns the shared store. A nil store means the composition
// root has not enabled incremental reads; it never creates another budget.
func (s *Service) ReadPreparations() *readprep.Store {
	if s == nil || s.state == nil {
		return nil
	}
	return s.state.readPreparations
}

// UseGenreRuntime installs the process-owned maintained genre reader before
// serving. Context clones share it; it never starts workers or a new cache.
func (s *Service) UseGenreRuntime(runtime *GenreRuntime) { s.state.genreRuntime = runtime }

func (s *Service) GenreRuntime() *GenreRuntime {
	if s == nil || s.state == nil {
		return nil
	}
	return s.state.genreRuntime
}
