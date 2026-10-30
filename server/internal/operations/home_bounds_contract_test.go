package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"strings"
	"testing"
)

func TestHomePreferenceBoundsMatchLayoutContract(t *testing.T) {
	s, p, a := consoleFixture(t)
	ids := []string{}
	for i := 0; i < 64; i++ {
		ids = append(ids, "recent_"+fmt.Sprintf("%064x", i))
	}
	out, err := s.ApplyPreferences(context.Background(), p, a, PreferenceChange{Scope: ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 1, IdempotencyKey: "home-64", Values: PreferencePatch{"home.rowOrder": ids, "home.hiddenRowIds": ids}})
	if err != nil || len(out.Effective.List("home.rowOrder")) != 64 {
		t.Fatalf("%+v %v", out, err)
	}
	layout, err := s.HomeLayout(context.Background(), p, a)
	if err != nil {
		t.Fatal(err)
	}
	doc, schema, err := apispec.Schema("HomeLayout")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(layout)
	if issues := doc.ValidateJSON(schema, raw); len(issues) > 0 {
		t.Fatal(issues)
	}
	for _, key := range []string{"home.rowOrder", "home.hiddenRowIds"} {
		field, _ := PreferenceFieldFor(key)
		for _, bad := range [][]string{append(ids, "extra"), {strings.Repeat("x", 129)}} {
			if _, _, err := canonicalPreference(field, bad); err == nil {
				t.Fatal("accepted out-of-bounds", key)
			}
		}
		if _, _, err := canonicalPreference(field, []string{strings.Repeat("x", 128)}); err != nil {
			t.Fatal(err)
		}
	}
}
