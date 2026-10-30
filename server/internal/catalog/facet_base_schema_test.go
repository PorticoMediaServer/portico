package catalog

import (
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
)

// Algorithm fixtures use the exact production DDL. Integration fixtures must
// install it through persistence.Open; no private schema installer can mask a
// missing table or stale baseline during route activation.
func facetBaselineBlock(name string) string {
	begin := "-- P2-28 " + name + ": begin\n"
	end := "-- P2-28 " + name + ": end"
	_, rest, ok := strings.Cut(persistence.BaselineSQL(), begin)
	if !ok {
		panic("missing production facet baseline block: " + name)
	}
	block, _, ok := strings.Cut(rest, end)
	if !ok {
		panic("unfinished production facet baseline block: " + name)
	}
	return block
}

var facetBaseTestDDL = facetBaselineBlock("normalized maintained facet base facts")
var facetBaseSourceIntegrationDDL = facetBaselineBlock("facet source membership stamps")
