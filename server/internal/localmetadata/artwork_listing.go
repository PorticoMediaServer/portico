package localmetadata

import "context"

// ArtworkListingEntry is the name and observational inventory stamp of one
// listed image. Revision is not an immutable provider/media version.
type ArtworkListingEntry struct{ Name, Revision string }
type ArtworkDirectoryListing struct {
	SourceID, RootIdentity string
	FollowSymlinks         bool
	Entries                []ArtworkListingEntry
}
type artworkListingKey struct{}

// WithArtworkListing supplies a complete, current inventory observation, not
// directory access. Unknown/resumed/changed listings remain unavailable.
func WithArtworkListing(ctx context.Context, listed func(string) (ArtworkDirectoryListing, bool)) context.Context {
	return context.WithValue(ctx, artworkListingKey{}, listed)
}

// observedArtworkImage binds cached bytes to the exact observed source stamp.
// A failed concurrent reader cannot replace bytes under another stamp.
type observedArtworkImage struct{ stamp, key string }
