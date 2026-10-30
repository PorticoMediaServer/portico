package compactcatalog

import "context"

// FacetPolicyEpoch is a constant read of restriction/eligibility inputs, distinct
// from content churn. It includes classification edits. Active-policy SIMPLE
// bases bind it in live Authority; inactive SIMPLE bases omit it. Search genres
// also have retirement/member-library eligibility gates and bind it alongside
// genre_epoch even with inactive content restrictions. Library lifetime/kind
// and live caller grants are checked separately.
// A changed active epoch requires a new exact base; old counts must not leak
// while that base prepares. No per-class accepting-write fanout is involved.
func FacetPolicyEpoch(ctx context.Context, q ReadQuery) (int64, error) {
	var epoch int64
	err := q.QueryRowContext(ctx, `SELECT policy_epoch FROM catalog_facet_source_state WHERE singleton=1`).Scan(&epoch)
	return epoch, err
}

// FacetGenreEpoch fences the empty-title -> first raw member transition used
// by Search genres. SIMPLE Authority omits it. Search Authority binds it for
// the registered search-video-genre-v1:<allowed-member-library-lifetimes> source
// prefix, alongside policy_epoch and the live caller/library grant checks.
func FacetGenreEpoch(ctx context.Context, q ReadQuery) (int64, error) {
	var epoch int64
	err := q.QueryRowContext(ctx, `SELECT genre_epoch FROM catalog_facet_source_state WHERE singleton=1`).Scan(&epoch)
	return epoch, err
}
