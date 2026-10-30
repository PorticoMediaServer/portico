package catalog

import (
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

var ErrListeningPreferencesConflict = errors.New("listening preferences changed; refresh before saving")

type ListeningPreferences struct {
	Revision       int64   `json:"revision"`
	BookRate       float64 `json:"bookRate"`
	PassoutMinutes int     `json:"passoutMinutes"`
}

func listeningPreferences(snapshot operations.PreferenceSnapshot) ListeningPreferences {
	out := ListeningPreferences{Revision: 1, BookRate: snapshot.Effective.Float("audiobooks.defaultSpeed"), PassoutMinutes: snapshot.Effective.Int("playback.sleepTimerMinutes")}
	for _, d := range snapshot.Documents {
		if d.Scope == operations.ScopeProfileServer {
			out.Revision = d.Revision
		}
	}
	return out
}

// Listening preferences are a projection of the typed registry, using its
// profile document and revision. There is no second preference store.
func (s *Service) ListeningPreferences(viewer identity.Viewer, authorize operations.Authorize) (ListeningPreferences, error) {
	snapshot, e := operations.New(s.db).Preferences(s.Context(), identity.Principal{Viewer: viewer}, "web", authorize)
	return listeningPreferences(snapshot), e
}
func (s *Service) SaveListeningPreferences(viewer identity.Viewer, p ListeningPreferences, authorize operations.Authorize) (ListeningPreferences, error) {
	snapshot, e := operations.New(s.db).ApplyPreferences(s.Context(), identity.Principal{Viewer: viewer}, authorize, operations.PreferenceChange{
		Scope: operations.ScopeProfileServer, DeviceClass: "web", ExpectedRevision: p.Revision, IdempotencyKey: identity.Token(),
		Values: operations.PreferencePatch{"audiobooks.defaultSpeed": p.BookRate, "playback.sleepTimerMinutes": p.PassoutMinutes},
	})
	var conflict *operations.ConflictError
	if errors.As(e, &conflict) {
		return p, ErrListeningPreferencesConflict
	}
	if e != nil {
		return p, e
	}
	return listeningPreferences(snapshot), nil
}
