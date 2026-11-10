package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/preferences"
)

// A profile's own settings. The server publishes the whole list with every default, so an
// app draws its settings pages from this answer and has no defaults of its own. A setting is
// saved for the profile everywhere, for one kind of device, or for one device; the narrowest
// saved value wins, and the last save wins.

// PreferenceRegistry is the list of settings this server has. Revision changes when the list does.
type PreferenceRegistry struct {
	Revision string                `json:"revision"`
	Fields   []preferences.Setting `json:"fields"`
}

// PreferenceDocument is what the profile saved at one scope: profile-server (everywhere),
// profile-device-class (this kind of device) or profile-device (this device).
type PreferenceDocument struct {
	Scope  string         `json:"scope"`
	Values map[string]any `json:"values"`
}

// Preferences is the answer of both preferences routes.
type Preferences struct {
	// DeviceClass is the kind of device asked about: web, mobile, tablet or television.
	DeviceClass string `json:"deviceClass"`
	// DeviceBound is false for a caller with no device of its own, such as a tool with a key.
	// It then has no profile-device document.
	DeviceBound bool                 `json:"deviceBound"`
	Registry    PreferenceRegistry   `json:"registry"`
	Documents   []PreferenceDocument `json:"documents"`
	// Effective holds every setting's value in force on this device, and EffectiveSource
	// where each came from: one of the scopes, or "default".
	Effective       map[string]any    `json:"effective"`
	EffectiveSource map[string]string `json:"effectiveSource"`
}

// PreferencesEdit saves settings at one scope. Values holds only the settings to change; a
// null takes that scope's value away, so the next wider one or the default applies again.
type PreferencesEdit struct {
	Scope       string         `json:"scope"`
	DeviceClass string         `json:"deviceClass"`
	Values      map[string]any `json:"values"`
}

func (s *Server) preferenceRoutes() {
	Handle(s, "GET /v1/preferences", WithProfile, func(r *http.Request, _ None) (Preferences, error) {
		return s.preferences(r, r.URL.Query().Get("deviceClass"))
	})
	Handle(s, "PATCH /v1/preferences", WithProfile, s.editPreferences)
}

// callerDevice is the device the caller's session is on, or 0 for a key.
func (s *Server) callerDevice(r *http.Request) (int64, error) {
	caller := CallerFrom(r.Context())
	if caller.SessionID == 0 {
		return 0, nil
	}
	device, err := s.db.SessionDevice(r.Context(), caller.SessionID)
	if errors.Is(err, db.ErrNotFound) {
		return 0, ErrUnauthorized()
	}
	return device.ID, err
}

func (s *Server) preferences(r *http.Request, deviceClass string) (Preferences, error) {
	if !slices.Contains(preferences.DeviceClasses, deviceClass) {
		return Preferences{}, ErrField("deviceClass", "Say which kind of device this is: web, mobile, tablet or television.")
	}
	deviceID, err := s.callerDevice(r)
	if err != nil {
		return Preferences{}, err
	}
	stored, err := s.db.StoredPreferences(r.Context(), CallerFrom(r.Context()).ProfileID, deviceClass, deviceID)
	if err != nil {
		return Preferences{}, err
	}
	out := Preferences{DeviceClass: deviceClass, DeviceBound: deviceID != 0,
		Registry:  PreferenceRegistry{Revision: preferences.Revision(), Fields: preferences.Settings()},
		Effective: preferences.Defaults(), EffectiveSource: map[string]string{}}
	for key := range out.Effective {
		out.EffectiveSource[key] = "default"
	}
	scopes := preferences.Scopes
	if deviceID == 0 {
		scopes = scopes[:2]
	}
	// Widest first, so a narrower document's value replaces a wider one's.
	for _, scope := range scopes {
		doc := PreferenceDocument{Scope: scope, Values: map[string]any{}}
		for _, p := range stored {
			if storedScope(p) != scope {
				continue
			}
			// A value saved by an earlier version that this one no longer takes is passed over.
			field, known := preferences.Find(p.Key)
			var raw any
			if !known || json.Unmarshal([]byte(p.Value), &raw) != nil {
				continue
			}
			value, err := field.Check(raw)
			if err != nil {
				continue
			}
			doc.Values[p.Key], out.Effective[p.Key], out.EffectiveSource[p.Key] = value, value, scope
		}
		out.Documents = append(out.Documents, doc)
	}
	return out, nil
}

func storedScope(p db.StoredPreference) string {
	switch {
	case p.Device:
		return preferences.ScopeDevice
	case p.DeviceClass != "":
		return preferences.ScopeDeviceClass
	}
	return preferences.ScopeProfile
}

func (s *Server) editPreferences(r *http.Request, req PreferencesEdit) (Preferences, error) {
	if !slices.Contains(preferences.DeviceClasses, req.DeviceClass) {
		return Preferences{}, ErrField("deviceClass", "Say which kind of device this is: web, mobile, tablet or television.")
	}
	deviceID, err := s.callerDevice(r)
	if err != nil {
		return Preferences{}, err
	}
	class, device := "", int64(0)
	switch {
	case req.Scope == preferences.ScopeProfile:
	case req.Scope == preferences.ScopeDeviceClass:
		class = req.DeviceClass
	case req.Scope == preferences.ScopeDevice && deviceID != 0:
		device = deviceID
	default:
		return Preferences{}, ErrField("scope", "Save a setting as profile-server, profile-device-class or, from a signed-in device, profile-device.")
	}
	set, clear := map[string]string{}, []string{}
	for key, raw := range req.Values {
		field, known := preferences.Find(key)
		if !known || !slices.Contains(field.Scopes, req.Scope) {
			return Preferences{}, ErrField("values."+key, "This server has no such setting at this scope.")
		}
		if raw == nil {
			clear = append(clear, key)
			continue
		}
		value, err := field.Check(raw)
		if err != nil {
			return Preferences{}, ErrField("values."+key, "That is not a value this setting takes.")
		}
		stored, _ := json.Marshal(value)
		set[key] = string(stored)
	}
	if len(set)+len(clear) > 0 {
		if err := s.db.SetPreferences(r.Context(), CallerFrom(r.Context()).ProfileID, class, device, set, clear, s.now()); err != nil {
			return Preferences{}, err
		}
	}
	return s.preferences(r, req.DeviceClass)
}
