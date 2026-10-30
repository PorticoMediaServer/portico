package catalog

// Keep the shared row entry points, but score the indexed candidates within
// this request. Reloads must observe current facets and personal state; serving
// an old list while a background refresh runs violates that contract.
func (s *Service) modelledCandidates(r HomeRequest, row string) ([]recCandidate, error) {
	if row == "recommended" {
		return s.recRank(r)
	}
	return s.recommendationCandidates(r, row, "")
}
