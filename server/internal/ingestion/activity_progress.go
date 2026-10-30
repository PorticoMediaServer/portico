package ingestion

import (
	"context"
	"path/filepath"
)

type activityFileObservation struct {
	path string
	ctx  context.Context
}

// CurrentActivityFile is a worker's actual in-flight relative file, not its
// next pending queue entry. Only the owner summary exposes this observation.
func (s *Service) CurrentActivityFile(job string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v := s.activityFiles[job]; v != nil && v.ctx.Err() == nil {
		return v.path
	}
	return ""
}
func (s *Service) activityFile(ctx context.Context, job, relative string) func() {
	if ctx.Err() != nil || !filepath.IsLocal(relative) || relative == "." {
		return func() {}
	}
	v := &activityFileObservation{relative, ctx}
	s.mu.Lock()
	if s.activityFiles == nil {
		s.activityFiles = map[string]*activityFileObservation{}
	}
	s.activityFiles[job] = v
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		if s.activityFiles[job] == v {
			delete(s.activityFiles, job)
		}
		s.mu.Unlock()
	}
}
