package catalog

// Preparing has no invented values, total or HasGenres. A ready preview always
// sets HasGenres, including false when its COMPLETE basis has no genres.
type SearchGenrePreparing struct {
	Kind         string `json:"kind"`
	State        string `json:"state"`
	QueryID      string `json:"queryId"`
	RetryAfterMs int    `json:"retryAfterMs"`
}

type SearchGenreResult struct {
	Page      *SearchGenrePage
	Fallback  *SearchGenreFallbackPage
	Preparing *SearchGenrePreparing
	release   func()
}

// Release must be deferred by HTTP through serialization/transport. Copies
// share the underlying store's idempotent delivery lease.
func (r SearchGenreResult) Release() {
	if r.release != nil {
		r.release()
	}
}

type SearchGenrePreparingError struct{ Preparation SearchGenrePreparing }

func (e *SearchGenrePreparingError) Error() string { return "search genres are preparing" }

// Cold fallback is explicitly unknown, never a successful empty preview. It
// retains the original preparation handle; no mutable-staging row anchors.
type SearchGenreFallbackPage struct {
	Kind              string `json:"kind"`
	TotalIsLowerBound bool   `json:"totalIsLowerBound"`
	SearchGenrePage
	HasGenresKnown bool                 `json:"hasGenresKnown"`
	Preparation    SearchGenrePreparing `json:"preparation"`
}
