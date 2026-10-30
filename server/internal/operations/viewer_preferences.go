package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"sort"
)

type PreferencePatch map[string]any
type PreferenceChange struct {
	ExpectedRevision int64           `json:"expectedRevision"`
	IdempotencyKey   string          `json:"idempotencyKey"`
	Scope            string          `json:"scope"`
	DeviceClass      string          `json:"deviceClass"`
	Values           PreferencePatch `json:"values"`
}
type PreferenceDocument struct {
	Scope          string           `json:"scope"`
	Revision       int64            `json:"revision"`
	Values         PreferenceValues `json:"values"`
	Digest         string           `json:"digest"`
	ActiveRevision int64            `json:"activeRevision"`
}
type PreferenceRegistryDocument struct {
	Revision string            `json:"revision"`
	Fields   []PreferenceField `json:"fields"`
}
type PreferenceSnapshot struct {
	AudioEffects     AudioEffectPreferences     `json:"audioEffects"`
	DeviceClass      string                     `json:"deviceClass"`
	DeviceBound      bool                       `json:"deviceBound"`
	Registry         PreferenceRegistryDocument `json:"registry"`
	Documents        []PreferenceDocument       `json:"documents"`
	Effective        PreferenceValues           `json:"effective"`
	EffectiveSource  map[string]string          `json:"effectiveSource"`
	ClampedFields    []string                   `json:"clampedFields"`
	RegistryRevision string                     `json:"registryRevision"`
}

func readPreferences(tx *sql.Tx, p identity.Principal, device string, clamped []string) (PreferenceSnapshot, error) {
	out := PreferenceSnapshot{
		DeviceClass:      device,
		DeviceBound:      p.DeviceID != "",
		Registry:         PreferenceRegistryDocument{Revision: PreferenceRegistryRevision, Fields: PreferenceRegistry()},
		Documents:        []PreferenceDocument{},
		ClampedFields:    []string{},
		RegistryRevision: PreferenceRegistryRevision,
	}
	if !validDeviceClass(device) {
		return out, invalidFields("deviceClass")
	}
	for _, scope := range PreferenceScopes {
		if scope == ScopeDevice && p.DeviceID == "" {
			continue
		}
		address, e := preferenceAddressFor(p.Viewer, scope, device, p.DeviceID)
		if e != nil {
			return out, e
		}
		revision, values, e := readPreferenceScope(tx, address)
		if e != nil {
			return out, e
		}
		d := PreferenceDocument{Scope: scope, Revision: revision, Values: values}
		b, _ := json.Marshal(d.Values)
		d.Digest = Hash(string(b))
		d.ActiveRevision = d.Revision
		out.Documents = append(out.Documents, d)
	}
	effective, source, policy, e := EffectiveDevicePreferences(tx, p.Viewer, device, p.DeviceID)
	if e != nil {
		return out, e
	}
	out.Effective, out.EffectiveSource = effective, source
	out.AudioEffects = effective.AudioEffects()
	seen := map[string]bool{}
	for _, key := range append(append([]string{}, clamped...), policy...) {
		if !seen[key] {
			seen[key] = true
			out.ClampedFields = append(out.ClampedFields, key)
		}
	}
	sort.Strings(out.ClampedFields)
	return out, nil
}
func (s *Store) Preferences(ctx context.Context, p identity.Principal, device string, auth Authorize) (out PreferenceSnapshot, e error) {
	e = s.snapshot(ctx, auth, "", func(tx *sql.Tx) error {
		var err error
		out, err = readPreferences(tx, p, device, nil)
		return err
	})
	return
}

// validatePreferencePatch resolves a submitted patch against the registry. An
// unknown key, a key outside the addressed scope, or a value the registry cannot
// accept names that key in the rejection; only numeric ranges clamp.
func validatePreferencePatch(scope string, patch PreferencePatch) (PreferenceValues, []string, []string, error) {
	values, cleared, clamped := PreferenceValues{}, []string{}, []string{}
	if patch == nil {
		return nil, nil, nil, invalidFields("values")
	}
	keys := make([]string, 0, len(patch))
	for key := range patch {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		f, ok := PreferenceFieldFor(key)
		if !ok || !f.allows(scope) {
			return nil, nil, nil, invalidFields("values." + key)
		}
		if patch[key] == nil {
			cleared = append(cleared, key)
			continue
		}
		canonical, wasClamped, e := canonicalPreference(f, patch[key])
		if e != nil {
			return nil, nil, nil, invalidFields("values." + key)
		}
		values[key] = canonical
		if wasClamped {
			clamped = append(clamped, key)
		}
	}
	return values, cleared, clamped, nil
}

// ApplyPreferences merges the submitted keys into one scope document. A null
// value removes that scope's override, so the next lower scope or the registry
// default becomes effective again.
func (s *Store) ApplyPreferences(ctx context.Context, p identity.Principal, auth Authorize, c PreferenceChange) (out PreferenceSnapshot, e error) {
	if !validDeviceClass(c.DeviceClass) {
		return out, invalidFields("deviceClass")
	}
	address, e := preferenceAddressFor(p.Viewer, c.Scope, c.DeviceClass, p.DeviceID)
	if e != nil {
		return out, invalidFields("scope")
	}
	patch, cleared, clamped, e := validatePreferencePatch(c.Scope, c.Values)
	if e != nil {
		return out, e
	}
	e = s.transaction(ctx, auth, "", func(tx *sql.Tx) error {
		receipt, err := usermutation.CheckContext(ctx, tx, s.now())
		if err != nil {
			return err
		}
		if receipt != nil {
			out, err = readPreferences(tx, p, c.DeviceClass, nil)
			return err
		}

		identity, _ := json.Marshal([]string{p.Authority, p.AccountID, p.ProfileID, c.Scope, address.device, c.DeviceClass, p.DeviceID})
		key := "preferences:" + Hash(string(identity))
		raw, digest, err := settingsLegacyReceipt(ctx, tx, key, c.IdempotencyKey, c, s.now())
		if err != nil {
			return err
		}
		if raw != "" {
			return decodeDocument(raw, &out)
		}
		revision, merged, err := readPreferenceScope(tx, address)
		if err != nil {
			return err
		}
		if revision != c.ExpectedRevision {
			return &ConflictError{revision}
		}
		for field, value := range patch {
			merged[field] = value
		}
		for _, field := range cleared {
			delete(merged, field)
		}
		if err = writePreferenceScope(tx, address, revision+1, s.now(), merged); err != nil {
			return err
		}
		if err = apievents.Append(tx, apievents.ProfileAudience(p.Authority, p.AccountID, p.ProfileID), "settings.changed", "settings", "preferences", fmt.Sprint(revision+1), map[string]string{"scope": c.Scope, "deviceClass": c.DeviceClass, "deviceId": p.DeviceID}); err != nil {
			return err
		}
		out, err = readPreferences(tx, p, c.DeviceClass, clamped)
		if err != nil {
			return err
		}
		return saveSettingsReceipt(ctx, tx, key, c.IdempotencyKey, digest, out, "preferences", revision+1, s.now())
	})
	return
}
