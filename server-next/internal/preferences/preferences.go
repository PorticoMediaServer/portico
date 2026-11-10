// Package preferences is the list of settings a person can change for themselves: what each
// one is called, what it may be set to and what it is until they change it. The server is the
// only authority on these: an app draws its settings from the list it is handed and never
// invents a setting, a default or a limit of its own. This package runs no SQL.
package preferences

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/language"
)

// Where a setting is saved. Each profile has one set of values that applies everywhere, one
// for each kind of device, and one for each device. A narrower one wins.
const (
	ScopeProfile     = "profile-server"
	ScopeDeviceClass = "profile-device-class"
	ScopeDevice      = "profile-device"
)

// Scopes lists the scopes from widest to narrowest.
var Scopes = []string{ScopeProfile, ScopeDeviceClass, ScopeDevice}

// DeviceClasses are the kinds of device a setting can be saved for.
var DeviceClasses = []string{"web", "mobile", "tablet", "television"}

// Setting describes one setting.
type Setting struct {
	Key string `json:"key"`
	// Type is boolean, integer, number, string, locale (a language tag or "auto"),
	// string-list, or languageList (language tags, most wanted first).
	Type    string `json:"type"`
	Default any    `json:"default"`
	// AllowedValues, when present, are the only values the setting takes.
	AllowedValues []any `json:"allowedValues,omitempty"`
	// Min and Max bound a number that has no list of allowed values.
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
	// Scopes says where the setting can be saved.
	Scopes []string `json:"scopes"`
	// Group is the part of the key before the first dot; LabelKey names the app's text for it.
	Group    string `json:"group"`
	LabelKey string `json:"labelKey"`

	maxItems, maxLength int
}

// Values holds settings by key: bool, int64, float64, string or []string.
type Values map[string]any

// ErrInvalid: the value is not one this setting takes.
var ErrInvalid = errors.New("not a value this setting takes")

var (
	index = func() map[string]Setting {
		out := make(map[string]Setting, len(fields))
		for _, f := range fields {
			out[f.Key] = f
		}
		return out
	}()
	// revision changes whenever the list does, so an app can tell it has an old copy.
	revision = func() string {
		raw, _ := json.Marshal(fields)
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:8])
	}()
)

// Settings returns the whole list, in the order settings pages show it.
func Settings() []Setting { return slices.Clone(fields) }

// Revision names this version of the list.
func Revision() string { return revision }

// Find returns the setting with this key.
func Find(key string) (Setting, bool) {
	f, ok := index[key]
	return f, ok
}

// Defaults returns every setting at its default.
func Defaults() Values {
	out := make(Values, len(fields))
	for _, f := range fields {
		out[f.Key], _ = f.Check(f.Default)
	}
	return out
}

func number(raw any) (float64, bool) {
	switch n := raw.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func plainText(v string, max int) bool {
	return utf8.ValidString(v) && strings.TrimSpace(v) != "" && utf8.RuneCountInString(v) <= max && !strings.ContainsAny(v, "\n\r\t")
}

func languageTag(v string) (string, bool) {
	if strings.Contains(v, "_") {
		return "", false
	}
	tag, err := language.Parse(v)
	if err != nil || tag == language.Und {
		return "", false
	}
	return tag.String(), true
}

// Check turns a value as it arrived in JSON into the form it is kept in, or refuses it. A
// value outside the setting's list or range is refused, never quietly changed: a quiet change
// would hide an app's mistake behind something that seems to work.
func (f Setting) Check(raw any) (any, error) {
	switch f.Type {
	case "boolean":
		if v, ok := raw.(bool); ok {
			return v, nil
		}
	case "integer", "number":
		n, ok := number(raw)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) || f.Type == "integer" && n != math.Trunc(n) {
			break
		}
		if len(f.AllowedValues) > 0 {
			if !slices.ContainsFunc(f.AllowedValues, func(a any) bool { v, _ := number(a); return v == n }) {
				break
			}
		} else if f.Min != nil && n < *f.Min || f.Max != nil && n > *f.Max {
			break
		}
		if f.Type == "integer" {
			return int64(n), nil
		}
		return n, nil
	case "string", "locale":
		v, ok := raw.(string)
		switch {
		case !ok:
		case len(f.AllowedValues) > 0:
			if slices.Contains(f.AllowedValues, any(v)) {
				return v, nil
			}
		case !plainText(v, f.maxLength):
		case f.Type == "locale" && v != "auto":
			if tag, ok := languageTag(v); ok {
				return tag, nil
			}
		default:
			return v, nil
		}
	case "string-list", "languageList":
		var items []string
		switch list := raw.(type) {
		case []string:
			items = list
		case []any:
			for _, item := range list {
				text, ok := item.(string)
				if !ok {
					return nil, ErrInvalid
				}
				items = append(items, text)
			}
		default:
			return nil, ErrInvalid
		}
		if len(items) > f.maxItems {
			break
		}
		out := make([]string, 0, len(items))
		for _, text := range items {
			// "@appLanguage" and "@original" stand for the app's language and the title's own.
			if f.Type == "languageList" && text != "@appLanguage" && text != "@original" {
				tag, ok := languageTag(text)
				if !ok {
					return nil, ErrInvalid
				}
				text = tag
			}
			// Subtitles have no "the title's own language" to follow.
			if !plainText(text, f.maxLength) || slices.Contains(out, text) || text == "@original" && f.Key == "playback.preferredSubtitleLanguages" {
				return nil, ErrInvalid
			}
			out = append(out, text)
		}
		return out, nil
	}
	return nil, ErrInvalid
}

// The readers below return the zero value for a key that is absent or of another type.

func (v Values) Bool(key string) bool {
	out, _ := v[key].(bool)
	return out
}

func (v Values) Int(key string) int {
	n, _ := number(v[key])
	return int(n)
}

func (v Values) Float(key string) float64 {
	n, _ := number(v[key])
	return n
}

func (v Values) Text(key string) string {
	out, _ := v[key].(string)
	return out
}

func (v Values) List(key string) []string {
	out, _ := v[key].([]string)
	return out
}
