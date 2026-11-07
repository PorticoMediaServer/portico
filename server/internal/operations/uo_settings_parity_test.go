package operations_test

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/operations"
	readvalues "github.com/PorticoMediaServer/portico/server/internal/store/read"
	writer "github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

func uoLegacyValue(t *testing.T, kind string, raw any) writer.UOValue {
	t.Helper()
	v := writer.UOValue{Kind: writer.UOValueKind(kind)}
	switch kind {
	case "nullable-integer":
		// The closed cap adapter uses zero only as its NULL transport sentinel;
		// native playback_owner_policy remains nullable and typed.
		if raw != nil {
			t.Fatal("unexpected legacy cap default", raw)
		}
		v.Kind = writer.UOInteger
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
		v.Kind = writer.UOText
		if kind == "locale" {
			v.Kind = writer.UOLocale
		}
		v.Text = raw.(string)
	case "log-retention-list":
		v.Retention = []writer.UOLogRetention{}
		for _, e := range raw.([]operations.LogRetention) {
			v.Retention = append(v.Retention, writer.UOLogRetention{writer.UOLogCategory(e.Category), int64(e.Days)})
		}
	case "string-list", "languageList":
		v.List = raw.([]string)
	default:
		t.Fatal(kind)
	}
	return v
}
func TestUOPreferencesMatchLegacyFiniteRegistry(t *testing.T) {
	legacy := operations.PreferenceRegistry()
	count := 0
	for _, scope := range []writer.UOPreferenceScope{writer.UOProfileServer, writer.UODeviceClass, writer.UODevice} {
		count += len(writer.UOPreferenceKeys(scope))
	}
	if len(legacy) != count {
		t.Fatal("finite key inventory mismatch", len(legacy), count)
	}
	for _, f := range legacy {
		key := writer.UOPreferenceKey(f.Key)
		def, ok := writer.UOPreferenceDefault(key)
		if !ok || len(f.Scopes) != 1 || !writer.UOPreferenceAllowed(key, writer.UOPreferenceScope(f.Scopes[0])) {
			t.Fatal("scope mismatch", f.Key)
		}
		for _, scope := range []writer.UOPreferenceScope{writer.UOProfileServer, writer.UODeviceClass, writer.UODevice} {
			if writer.UOPreferenceAllowed(key, scope) != (string(scope) == f.Scopes[0]) {
				t.Fatal("extra scope accepted", key, scope)
			}
		}
		expected := uoLegacyValue(t, f.Type, f.Default)
		if !reflect.DeepEqual(expected, def) {
			t.Fatal("default mismatch", f.Key, expected, def)
		}
		// Compare the public finite vocabularies without importing legacy SQL
		// callback registration into the writer test binary.
		readDefault, ok := readvalues.UOPreferenceDefault(readvalues.UOPreferenceKey(key))
		if !ok {
			t.Fatal(key)
		}
		a, _ := json.Marshal(def)
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
	for _, key := range writer.UOSettingKeys() {
		def, valid := writer.UOSettingDefault(key)
		if !valid {
			t.Fatal(key)
		}
		f, ok := legacy[string(key)]
		if ok {
			v := uoLegacyValue(t, f.Type, f.Default)
			if !reflect.DeepEqual(def, v) {
				t.Fatal("default mismatch", key, def, v)
			}
		}
		other, ok := readvalues.UOSettingDefault(readvalues.UOSettingKey(key))
		if !ok {
			t.Fatal(key)
		}
		a, _ := json.Marshal(def)
		b, _ := json.Marshal(other)
		if !reflect.DeepEqual(a, b) {
			t.Fatal("read/write setting drift", key)
		}
		if ok && len(f.Options) > 0 && def.Kind != writer.UORetention {
			for _, x := range f.Options {
				v := writer.UOValue{Kind: writer.UOText, Text: x}
				if def.Kind == writer.UOInteger {
					n, e := strconv.ParseFloat(x, 64)
					if e != nil {
						t.Fatal(e)
					}
					v = writer.UOValue{Kind: writer.UOInteger, Number: n}
				}
				if _, e := writer.UOValidateSetting(key, v); e != nil {
					t.Fatal("legacy enum refused", key, x, e)
				}
			}
		}
	}
}
