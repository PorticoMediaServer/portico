// Package facettext shares catalogue facet normalization without persistence dependencies.
package facettext

// FoldFacetSQL returns the canonical SQL expression for matching facet names.
func FoldFacetSQL(v string) string {
	v = "lower(trim(replace(replace(replace(replace(" + v + ",'-',' '),'_',' '),'.',' '),',',' ')))"
	for i := 0; i < 4; i++ {
		v = "replace(" + v + ",'  ',' ')"
	}
	return "CASE " + v + " WHEN 'sci fi' THEN 'science fiction' WHEN 'scifi' THEN 'science fiction' ELSE " + v + " END"
}
