package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// The owner's settings documents (package settings). Every document is read and saved through
// the same two routes; each area declares its own and reads it from memory, so a save takes
// effect at once with no restart.

// SettingsDocument is one settings document.
type SettingsDocument struct {
	Name string `json:"name"`
	// Values are the document's fields.
	Values JSON `json:"values"`
	// Defaults are what each field is until the owner changes it: a page shows a changed
	// value as changed and offers Reset, which saves the default.
	Defaults JSON `json:"defaults"`
	// ETag is the document's ETag, also sent as the ETag header on a read of one document.
	ETag string `json:"etag"`

	revision
}

func (s *Server) settingsRoutes() {
	Handle(s, "GET /v1/admin/settings", Owner, func(r *http.Request, _ None) (Page[SettingsDocument], error) {
		out := Page[SettingsDocument]{Items: []SettingsDocument{}}
		for _, name := range s.settings.Names() {
			doc, err := s.settings.Read(name)
			if err != nil {
				return out, err
			}
			out.Items = append(out.Items, settingsDocument(doc))
		}
		return out, nil
	})
	Handle(s, "GET /v1/admin/settings/{name}", Owner, func(r *http.Request, _ None) (SettingsDocument, error) {
		doc, err := s.settings.Read(r.PathValue("name"))
		if errors.Is(err, settings.ErrNoDocument) {
			return SettingsDocument{}, ErrNotFound()
		}
		return settingsDocument(doc), err
	})
	// A save sends the fields it changes; a field left out keeps its value. With If-Match, a
	// document saved since is refused with 412 and the page reads it again.
	Handle(s, "PUT /v1/admin/settings/{name}", Owner, func(r *http.Request, change JSON) (SettingsDocument, error) {
		match, err := settingsIfMatch(r)
		if err != nil {
			return SettingsDocument{}, err
		}
		if len(change) == 0 {
			return SettingsDocument{}, ErrInvalid("Send the settings to change.")
		}
		doc, err := s.settings.Write(r.Context(), CallerFrom(r.Context()).UserID, r.PathValue("name"), change, match, s.now())
		var invalid *settings.Invalid
		switch {
		case errors.Is(err, settings.ErrNoDocument):
			return SettingsDocument{}, ErrNotFound()
		case errors.Is(err, settings.ErrStale):
			return SettingsDocument{}, errStale()
		case errors.As(err, &invalid):
			return SettingsDocument{}, errSetting(invalid)
		case err != nil:
			return SettingsDocument{}, err
		}
		return settingsDocument(doc), nil
	})
}

func settingsDocument(doc settings.Document) SettingsDocument {
	out := SettingsDocument{Name: doc.Name, Values: JSON(doc.Values), Defaults: JSON(doc.Defaults),
		ETag: `"` + strconv.FormatInt(doc.Revision, 10) + `"`, revision: revision(doc.Revision)}
	return out
}

// errSetting refuses a value a setting does not take, naming the field.
func errSetting(invalid *settings.Invalid) *Error {
	e := refusal(http.StatusUnprocessableEntity, "invalid_setting", "Check the highlighted setting and try again.", false)
	e.Fields = []Field{{Path: invalid.Field, Code: "invalid", Message: invalid.Message}}
	return e
}
