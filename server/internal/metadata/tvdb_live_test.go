package metadata

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// This opt-in probe never opens a catalog database and never prints credentials,
// tokens, raw provider payloads, or account data. Normal tests stay offline.
func TestTVDBLiveBoundedProjectIdentifier(t *testing.T) {
	if os.Getenv("PORTICO_TEST_TVDB_LIVE") != "1" {
		t.Skip("explicit live provider probe disabled")
	}
	provider, e := metadataprovider.NewTVDB(TVDBProjectKey)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	rows, e := provider.SearchSeries(ctx, "Planet Earth", 2006)
	if e != nil {
		t.Fatal(e)
	}
	var id int64
	matches := 0
	for _, row := range rows {
		if strings.EqualFold(row.Name, "Planet Earth") && row.Year == "2006" {
			id, e = strconv.ParseInt(row.ID, 10, 64)
			if e != nil {
				t.Fatal("invalid series identity")
			}
			matches++
		}
	}
	t.Logf("authenticated search returned %d bounded candidates, %d exact title/year matches", len(rows), matches)
	if matches != 1 {
		t.Fatal("probe series identity is not unambiguous")
	}
	page, e := provider.Episodes(ctx, id, metadataprovider.Official, 0)
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("validated official episode page: %d episodes; continuation present: %t", len(page.Episodes), page.NextPage != nil)
}
