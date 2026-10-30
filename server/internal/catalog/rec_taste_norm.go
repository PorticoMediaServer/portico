package catalog

import (
	"math"
	"sort"
)

// A detached preparation reconstructs the same taste in a different Go map.
// Stable addition order prevents map iteration from perturbing score ties and
// the content/quality balance between ordinary Browse and conversion.
func recTasteNorm(values, idf map[string]float64) float64 {
	keys := make([]string, 0, len(values))
	for facet := range values {
		keys = append(keys, facet)
	}
	sort.Strings(keys)
	norm := 0.0
	for _, facet := range keys {
		if w := recFacetWeight(facet) * idf[facet] * values[facet]; w != 0 {
			norm += w * w
		}
	}
	return math.Sqrt(norm)
}
