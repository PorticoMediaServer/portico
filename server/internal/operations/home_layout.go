package operations

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Home customisation is two registry preference keys (home.rowOrder and
// home.hiddenRowIds) on the profile-server document, so it shares the settings
// surface, revision fencing, idempotency and conflict handling with every
// other viewer preference. The catalog owns which row ids exist; this store
// only records the viewer's order and hidden set.

const homeLayoutScope = ScopeProfileServer

type HomeLayoutDocument struct {
	Revision     int64    `json:"revision"`
	RowOrder     []string `json:"rowOrder"`
	HiddenRowIDs []string `json:"hiddenRowIds"`
}

type HomeLayoutChange struct {
	ExpectedRevision int64    `json:"expectedRevision"`
	IdempotencyKey   string   `json:"idempotencyKey"`
	RowOrder         []string `json:"rowOrder"`
	HiddenRowIDs     []string `json:"hiddenRowIds"`
	Reset            bool     `json:"-"`
}

func homeLayoutFromSnapshot(snapshot PreferenceSnapshot) HomeLayoutDocument {
	out := HomeLayoutDocument{Revision: 1, RowOrder: []string{}, HiddenRowIDs: []string{}}
	for _, d := range snapshot.Documents {
		if d.Scope == homeLayoutScope {
			out.Revision = d.Revision
		}
	}
	if v := snapshot.Effective.List("home.rowOrder"); v != nil {
		out.RowOrder = v
	}
	if v := snapshot.Effective.List("home.hiddenRowIds"); v != nil {
		out.HiddenRowIDs = v
	}
	return out
}

// HomeLayout reads the viewer's row order and hidden set with the revision of
// the profile-server document that carries them.
func (s *Store) HomeLayout(ctx context.Context, p identity.Principal, auth Authorize) (HomeLayoutDocument, error) {
	snapshot, e := s.Preferences(ctx, p, "web", auth)
	if e != nil {
		return HomeLayoutDocument{}, e
	}
	return homeLayoutFromSnapshot(snapshot), nil
}

// ApplyHomeLayout merges the two home keys into the viewer's existing
// profile-server document through the ordinary preference patch path, so
// unrelated preferences are never dropped and one revision fences both surfaces.
func (s *Store) ApplyHomeLayout(ctx context.Context, p identity.Principal, auth Authorize, c HomeLayoutChange) (HomeLayoutDocument, error) {
	values := PreferencePatch{}
	if c.Reset {
		values["home.rowOrder"], values["home.hiddenRowIds"] = nil, nil
	} else {
		values["home.rowOrder"] = append([]string{}, c.RowOrder...)
		values["home.hiddenRowIds"] = append([]string{}, c.HiddenRowIDs...)
	}
	snapshot, e := s.ApplyPreferences(ctx, p, auth, PreferenceChange{ExpectedRevision: c.ExpectedRevision, IdempotencyKey: c.IdempotencyKey, Scope: homeLayoutScope, DeviceClass: "web", Values: values})
	if e != nil {
		return HomeLayoutDocument{}, e
	}
	return homeLayoutFromSnapshot(snapshot), nil
}
