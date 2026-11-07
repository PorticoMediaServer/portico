package writer

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/operations"
	readvalues "github.com/PorticoMediaServer/portico/server/internal/store/read"
)

func uoLegacyValue(t *testing.T, kind string, raw any) UOValue {
	t.Helper()
	v := UOValue{Kind: UOValueKind(kind)}
	switch kind {
	case "boolean":
		v.Bool = raw.(bool)
	case "integer", "number":
		switch x := raw.(type) {
		case int:
			v.Number = float64(x)
		case float64:
			v.Number = x
		default:
			t.Fatal(kind, raw)
		}
	case "string", "locale", "enumeration", "path":
		v.Kind = UOText
		if kind == "locale" {
			v.Kind = UOLocale
		}
		v.Text = raw.(string)
	case "string-list", "languageList":
		v.List = raw.([]string)
	default:
		t.Fatal(kind)
	}
	return v
}
func TestUOPreferencesMatchLegacyFiniteRegistry(t *testing.T) {
	legacy := operations.PreferenceRegistry()
	if len(legacy) != len(uoPreferenceSpecs) {
		t.Fatal("finite key inventory mismatch", len(legacy), len(uoPreferenceSpecs))
	}
	for _, f := range legacy {
		key := UOPreferenceKey(f.Key)
		spec, ok := uoPreferenceSpecFor(key)
		if !ok || len(f.Scopes) != 1 || string(spec.scope) != f.Scopes[0] {
			t.Fatal("scope mismatch", f.Key)
		}
		expected := uoLegacyValue(t, f.Type, f.Default)
		if !uoValueEqual(expected, spec.def) {
			t.Fatal("default mismatch", f.Key, expected, spec.def)
		}
		// The duplicate read vocabulary avoids a writer<->read test import cycle.
		// Keep its values mechanically equivalent until parent extracts common types.
		readDefault, ok := readvalues.UOPreferenceDefault(readvalues.UOPreferenceKey(key))
		if !ok {
			t.Fatal(key)
		}
		a, _ := json.Marshal(spec.def)
		b, _ := json.Marshal(readDefault)
		if !reflect.DeepEqual(a, b) {
			t.Fatal("read/write default drift", key)
		}
	}
}
func TestUOSettingsMatchLegacySupportedDomains(t *testing.T) {
	legacy := map[string]operations.Field{}
	for _, f := range operations.Registry() {
		legacy[f.ID] = f
	}
	for _, spec := range uoSettingSpecs {
		f, ok := legacy[string(spec.key)]
		if ok {
			v := uoLegacyValue(t, f.Type, f.Default)
			if !uoValueEqual(spec.def, v) {
				t.Fatal("default mismatch", spec.key, spec.def, v)
			}
		}
		other, ok := readvalues.UOSettingDefault(readvalues.UOSettingKey(spec.key))
		if !ok {
			t.Fatal(spec.key)
		}
		a, _ := json.Marshal(spec.def)
		b, _ := json.Marshal(other)
		if !reflect.DeepEqual(a, b) {
			t.Fatal("read/write setting drift", spec.key)
		}
		if ok && len(f.Options) > 0 {
			for _, x := range f.Options {
				v := UOValue{Kind: UOText, Text: x}
				if spec.def.Kind == UOInteger {
					n, e := strconv.ParseFloat(x, 64)
					if e != nil {
						t.Fatal(e)
					}
					v = UOValue{Kind: UOInteger, Number: n}
				}
				if _, e := UOValidateSetting(spec.key, v); e != nil {
					t.Fatal("legacy enum refused", spec.key, x, e)
				}
			}
		}
	}
}
