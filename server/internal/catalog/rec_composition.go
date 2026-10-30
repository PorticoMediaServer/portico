package catalog

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// A composition owns these stores: they are never attached to the shared
// process Service. Separate ranking sessions may share immutable catalogue
// inputs only under the same snapshot and exact viewer scope. Session taste,
// IDF maps, memo keys, eligibility and row scoring remain independent.
type recCompositionSources struct {
	snapshot *sql.Tx
	stores   map[recCompositionKey]*recHydration
}

type recCompositionKey struct {
	profile      string
	fence        string
	viewerFence  string
	libraries    string
	restrictions string
}

func newRecCompositionSources(ctx context.Context) *recCompositionSources {
	if snapshot := dbwork.Snapshot(ctx); snapshot != nil {
		return &recCompositionSources{snapshot: snapshot, stores: map[recCompositionKey]*recHydration{}}
	}
	return nil
}

func (s *Service) recHydrationFor(r HomeRequest) *recHydration {
	snapshot := dbwork.Snapshot(s.Context())
	composition := s.recComposition
	if composition == nil || snapshot == nil || composition.snapshot != snapshot {
		return &recHydration{snapshot: snapshot}
	}
	key := recCompositionKey{r.Profile, r.ViewerFence, r.Viewer.Fence, idsJSON(homeUnique(r.Libraries)), RestrictionFence(r.Restrictions)}
	if store := composition.stores[key]; store != nil {
		return store
	}
	store := &recHydration{snapshot: snapshot}
	composition.stores[key] = store
	return store
}
