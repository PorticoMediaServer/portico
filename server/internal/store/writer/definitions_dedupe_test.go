package writer

import (
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// dedupeDefinitions keeps ONE catalogue entry per command ID across composition
// layers that legitimately repeat the same compiled set (for example the shared
// browse set inside NativeFullSearchDefinitions). A differing duplicate is a
// fixture error, never a silent override.
func dedupeDefinitions(t *testing.T, defs []stmt.Definition) []stmt.Definition {
	t.Helper()
	unique := make([]stmt.Definition, 0, len(defs))
	seen := map[stmt.ID]stmt.Definition{}
	for _, def := range defs {
		if prior, ok := seen[def.ID]; ok {
			if prior.SQL != def.SQL || fmt.Sprint(prior.Params) != fmt.Sprint(def.Params) {
				t.Fatalf("conflicting duplicate definition %s", def.ID)
			}
			continue
		}
		seen[def.ID] = def
		unique = append(unique, def)
	}
	return unique
}
