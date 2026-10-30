package catalog

// browseReady uses fixed-domain indexed journal probes. It conservatively
// withholds another library's browse while any included source domain catches
// up, but never walks pending source keys or asset memberships on a request.
func (s *Service) browseReady(_ string) error {
	return s.compactProjectionReady(18, 19, 20, 25, 26, 28)
}
