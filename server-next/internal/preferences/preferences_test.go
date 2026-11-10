package preferences

import (
	"slices"
	"testing"
)

// An app cannot start if the list it is handed contradicts itself, so every setting's
// default must be a value that setting takes, and no key may appear twice.
func TestEveryDefaultIsAValueItsSettingTakes(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range Settings() {
		if seen[f.Key] {
			t.Errorf("%s is listed twice", f.Key)
		}
		seen[f.Key] = true
		if _, err := f.Check(f.Default); err != nil {
			t.Errorf("%s: its default %v is refused", f.Key, f.Default)
		}
		if len(f.Scopes) != 1 || !slices.Contains(Scopes, f.Scopes[0]) {
			t.Errorf("%s: scopes %v", f.Key, f.Scopes)
		}
	}
}

// What an app may save: a value from the list or inside the range, in the form it is kept in.
func TestCheck(t *testing.T) {
	for _, c := range []struct {
		key  string
		in   any
		want any
	}{
		{"playback.autoplayNext", false, false},
		{"playback.autoplayNext", "no", nil},
		{"playback.skipBackMs", 15000.0, int64(15000)},
		{"playback.skipBackMs", 12000.0, nil},
		{"playback.defaultSpeed", 1.5, 1.5},
		{"playback.playedThresholdPercent", 50.0, int64(50)},
		{"playback.playedThresholdPercent", 49.0, nil},
		{"appearance.cardSizePercent", 150.0, int64(150)},
		{"appearance.cardSizePercent", 151.0, nil},
		{"appearance.cardSizePercent", 99.5, nil},
		{"playback.subtitleMode", "always", "always"},
		{"playback.subtitleMode", "sometimes", nil},
		{"region.locale", "en-gb", "en-GB"},
		{"region.locale", "auto", "auto"},
		{"region.locale", "not a language", nil},
		{"playback.preferredAudioLanguages", []any{"@original", "FR"}, []string{"@original", "fr"}},
		{"playback.preferredAudioLanguages", []any{"fr", "fr"}, nil},
		{"playback.preferredSubtitleLanguages", []any{"@original"}, nil},
		{"home.hiddenRowIds", []any{"continue", 3.0}, nil},
	} {
		f, ok := Find(c.key)
		if !ok {
			t.Fatalf("no setting %s", c.key)
		}
		got, err := f.Check(c.in)
		if list, isList := c.want.([]string); isList {
			if have, _ := got.([]string); err != nil || !slices.Equal(have, list) {
				t.Errorf("%s = %v: got %v, %v", c.key, c.in, got, err)
			}
		} else if (c.want == nil) != (err != nil) || got != c.want {
			t.Errorf("%s = %v: got %v (%T), %v; want %v", c.key, c.in, got, got, err, c.want)
		}
	}
}
