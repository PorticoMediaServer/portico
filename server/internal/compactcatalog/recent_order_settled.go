package compactcatalog

import (
	"context"
	"fmt"
	"strings"
)

// recentOrderDomains maintain the two facts a newest-first walk compares: a
// work's member added times (catalog_browse_member_metrics) and its
// recent_text, their maximum. A container's recent_text is refreshed in a later
// queued pass than its members' metrics, so the two agree only once all four
// are drained.
var recentOrderDomains = []int{DomainBrowseRows, DomainItemMetrics, DomainAssetMetrics, DomainContainerRefresh}

// RecentOrderSettled reports whether every work's recent_text is the newest
// added time among its member metrics, with no derivation of either pending,
// rebuilding, failed or at an older version. A reader that walks works by
// recent_text as an upper bound of their newest member may rely on it only
// when this holds, and must read it in the same snapshot as the walk.
func RecentOrderSettled(ctx context.Context, q ReadQuery) (bool, error) {
	var settled bool
	err := q.QueryRowContext(ctx, recentOrderSettledSQL()).Scan(&settled)
	return settled, err
}

func recentOrderSettledSQL() string {
	in := make([]string, len(recentOrderDomains))
	current := make([]string, len(recentOrderDomains))
	for i, domain := range recentOrderDomains {
		d, ok := derivation(domain)
		if !ok {
			panic(fmt.Sprintf("recent order domain %d is not registered", domain))
		}
		in[i] = fmt.Sprint(domain)
		current[i] = fmt.Sprintf("EXISTS(SELECT 1 FROM catalog_derivations WHERE domain=%d AND version=%d AND rebuilding=0)", domain, d.Version)
	}
	domains := strings.Join(in, ",")
	return `SELECT ` + strings.Join(current, " AND ") + `
 AND NOT EXISTS(SELECT 1 FROM catalog_dirty WHERE domain IN(` + domains + `))
 AND NOT EXISTS(SELECT 1 FROM catalog_derivation_failures WHERE domain IN(` + domains + `))
 AND NOT EXISTS(SELECT 1 FROM catalog_browse_row_work WHERE domain IN(18,35))
 AND NOT EXISTS(SELECT 1 FROM catalog_browse_row_keys WHERE domain IN(18,35))
 AND NOT EXISTS(SELECT 1 FROM catalog_item_metric_jobs WHERE item_id>=-9223372036854775808)`
}
