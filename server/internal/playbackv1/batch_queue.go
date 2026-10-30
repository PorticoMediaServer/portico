package playbackv1

import "context"

var _ BatchQueueAppender = (*Service)(nil)

// BatchQueue implements the detail/selection contract through the existing
// fenced queue command. Capture, visibility, source order, revision comparison
// and insertion stay in that command's transaction; media kinds are not split.
func (s *Service) BatchQueue(ctx context.Context, caller Caller, queueID, ifMatch string, request BatchQueueRequest) (BatchQueueResult, error) {
	if err := request.Validate(); err != nil {
		return BatchQueueResult{}, err
	}
	return s.AddSegment(ctx, caller, queueID, ifMatch, AddSegmentRequest{Placement: Placement{Where: request.Placement}, Source: request.Source})
}
