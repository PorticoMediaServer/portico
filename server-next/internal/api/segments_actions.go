package api

import "net/http"

func (s *Server) redetectAllSegments(r *http.Request, _ None) (SegmentChanged, error) {
	return s.remeasureSegments(r, false)
}

func (s *Server) retryUnreadableSegments(r *http.Request, _ None) (SegmentChanged, error) {
	return s.remeasureSegments(r, true)
}

func (s *Server) remeasureSegments(r *http.Request, failedOnly bool) (SegmentChanged, error) {
	var out SegmentChanged
	libs, err := s.db.AllLibraries(r.Context())
	if err != nil {
		return out, err
	}
	for _, lib := range libs {
		if s.segments.Mode(lib) == "off" {
			continue
		}
		var n int
		if failedOnly {
			n, err = s.db.RetryFailedSegments(r.Context(), lib.PublicID)
		} else {
			n, err = s.db.RedetectSegments(r.Context(), lib.PublicID, "")
		}
		if err != nil {
			return out, err
		}
		out.Changed += n
		if n > 0 {
			if err := s.queueSegmentLibrary(r.Context(), lib.PublicID); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}
