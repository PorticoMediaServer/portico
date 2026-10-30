package mediaanalysis

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/segmentdetect"
)

// SegmentSource is an online database of intro and credits timestamps an
// owner could opt into, for episodes the show's own audio cannot settle.
//
// None ships. TheIntroDB (api.theintrodb.org) and AniSkip (api.aniskip.com)
// are the candidates; neither publishes terms or a data licence for its
// crowd-sourced timestamps that permit a server to fetch and keep them
// (checked 2026-10-18; AniSkip's code is MIT, which covers the code only), so
// no network call is made. An adapter may be added once a source's terms are
// confirmed, off by default and behind an owner setting, and its results are
// suggestions only: the provenance family below is never one segmentmarkers
// lets skip without asking.
type SegmentSource interface {
	// Name is the source's stable identifier ("theintrodb", "aniskip").
	Name() string
	// Lookup returns what the source knows for one episode. It must not
	// send anything but the query (no file names, paths or viewer data).
	Lookup(ctx context.Context, q SegmentQuery) ([]segmentdetect.Detection, error)
}

// SegmentQuery identifies one episode to an online source by public ids and
// the length of the owner's file (sources match cuts by duration).
type SegmentQuery struct {
	TVDBID, TMDBID, IMDbID string
	MALID                  int
	Season, Episode        int
	DurationUS             int64
}

// OnlineProvenance prefixes the provenance of a segment an online source
// supplied ("online:theintrodb:v1").
const OnlineProvenance = "online"
