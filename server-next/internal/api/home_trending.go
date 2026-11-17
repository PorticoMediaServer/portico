package api

import "context"

// Kept with the server's other background work, never a Home request.
func (s *Server) KeepTrending(ctx context.Context) { s.metadata.KeepTrending(ctx, s.log) }
