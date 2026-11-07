package read

import (
	"context"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"time"
)

// ErrSavedCollectionProof identifies the missing composition, preserving the
// shared refusal classification. It must never become a successful partial page.
var ErrSavedCollectionProof = fmt.Errorf("%w: complete cross-library saved member eligibility/source footprint (typed subject42, Reading40/41, complete collection43/44) is unavailable", ErrUnqualified)
var ErrSavedCollectionDirectoryProof = fmt.Errorf("%w: saved collection directory requires complete owner/share/server union, personal pin ordering and exact visible-member reduction", ErrUnqualified)

type SavedCollectionReader struct {
	snapshots browseSnapshots
	pool      *governor.Snapshots
}

func NewSavedCollectionReader(s *governor.Snapshots) (*SavedCollectionReader, error) {
	if s == nil {
		return nil, ErrInput
	}
	return &SavedCollectionReader{snapshots: s, pool: s}, nil
}

type SavedCollectionInput struct {
	View     BrowseInput
	Resource string
}
type SavedCollectionPage struct {
	Items     []TitleItem
	Total     int64
	Exhausted bool
}

// Directory intentionally refuses until the complete union/order/count recipe
// exists. A library-scoped browse fence cannot prove a global saved directory.
// No caller actor key or saved-view execution is accepted by this interface.
func (r *SavedCollectionReader) Directory(context.Context, governor.Request, time.Duration, BrowseOutput, BrowseInput) (SavedCollectionPage, BrowseReadResult, error) {
	if r == nil {
		return SavedCollectionPage{}, BrowseReadResult{}, ErrInput
	}
	return SavedCollectionPage{}, BrowseReadResult{}, ErrSavedCollectionDirectoryProof
}

// Members currently proves only truly empty collections. Populated collections
// refuse explicitly rather than pretending a library-scoped prefix is complete.
// This is a harness draft, not a replacement for SavedResourceContent.
func (r *SavedCollectionReader) Members(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, in SavedCollectionInput) (SavedCollectionPage, BrowseReadResult, error) {
	if r == nil || in.Resource == "" || in.View.After != nil {
		return SavedCollectionPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, req, budget, out, r.pool, in.View.Now, func(ctx context.Context, req governor.Request, budget time.Duration, at time.Time) (SavedCollectionPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.members(ctx, req, budget, out, in)
	})
}
func (r *SavedCollectionReader) members(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, in SavedCollectionInput) (SavedCollectionPage, BrowseReadResult, error) {
	// Common principal is local-only and verifies global generation, native
	// command member incarnation and profile lifetime against the session family.
	// Derive account/profile from that fence, never from caller actor strings.
	var first []any
	root := func(q browseQuery) ([]any, error) {
		var source []any
		permitted, nonempty := false, false
		for _, name := range []string{"source", "server", "share"} {
			args := []any{in.View.TokenHash, in.Resource}
			if name == "server" {
				args = []any{in.Resource}
			}
			rows, err := q("rs.saved_collection."+name, args...)
			if err != nil {
				return nil, err
			}
			source = append(source, int64(len(rows)))
			if len(rows) == 1 {
				permitted = true
				source = append(source, rows[0]...)
				nonempty = nonempty || rows[0][2] != nil
			}
		}
		if !permitted {
			return nil, ErrDenied
		}
		n := int64(0)
		if nonempty {
			n = 1
		}
		source = append(source, n)
		if first != nil && !equalSource(first, source) {
			return nil, ErrStale
		}
		first = source
		return source, nil
	}
	return publishRead(ctx, r.snapshots, req, budget, out, in.View, []readStep[SavedCollectionPage]{func(ctx context.Context, q browseQuery, f browseFence, p *SavedCollectionPage) error {
		if first[len(first)-1] != int64(0) {
			return ErrSavedCollectionProof
		}
		p.Exhausted = true
		return ctx.Err()
	}}, root)
}
