package dbwork

import (
	"encoding/json"
	"os"
	"testing"
)

func TestRegisteredAuthorityRootsInvalidateActualStatementFence(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range AuthorityRegistry() {
		if e.Table == "" || seen[e.Table] || e.Root == "" || e.Lifetime == "" || e.Authorizer == "" || e.Retirement == "" || e.Consumers == "" {
			t.Fatalf("incomplete/duplicate authority entry: %+v", e)
		}
		seen[e.Table] = true
		before := AuthorityGeneration()
		noticeAuthorityWrite("DELETE FROM " + e.Table + " WHERE 0")
		if (AuthorityGeneration() != before) != e.CacheInvalidates {
			t.Fatalf("authority effect differs from registered root: %+v", e)
		}
	}
	if len(AuthorityEnablementBlockers()) == 0 {
		t.Fatal("missing lifetime seams hidden")
	}
	if path := os.Getenv("PORTICO_AUTHORITY_REGISTRY_REPORT"); path != "" {
		data, err := json.MarshalIndent(AuthorityRegistry(), "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}

}
