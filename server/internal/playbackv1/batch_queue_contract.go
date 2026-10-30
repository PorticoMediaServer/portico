package playbackv1

import "context"

// BatchQueueRequest appends a selection in its existing order. An explicit list
// may mix playable kinds; a query/container/saved selector captures its members
// on the server rather than asking a client to enumerate a whole library.
// Placement is next (immediately after the current entry) or end (Play later).
type BatchQueueRequest struct {
	Placement string   `json:"placement"`
	Source    Selector `json:"source"`
}

// BatchQueueResult uses the same revision and bounded window as queue commands.
// The queue revision is checked atomically with insertion. Authorization and
// availability of every source member are checked by the implementation; this
// structural contract does not grant access to any item or queue.
type BatchQueueResult = QueueReply

// BatchQueueAppender is implemented by the playback service. ifMatch is the
// queue revision from If-Match, using the existing queue command convention.
type BatchQueueAppender interface {
	BatchQueue(ctx context.Context, caller Caller, queueID, ifMatch string, request BatchQueueRequest) (BatchQueueResult, error)
}

// Validate checks the wire shape without resolving or restricting media kinds.
// The explicit-list bound does not limit queries, containers or saved views.
func (r BatchQueueRequest) Validate() error {
	if r.Placement != "next" && r.Placement != "end" {
		return &FieldError{Path: "placement"}
	}
	return r.Source.validate("source")
}
