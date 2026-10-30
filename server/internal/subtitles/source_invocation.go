package subtitles

import (
	"context"
	"database/sql"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// PlaybackSourceFacts are captured by Select after current viewer/session
// authorization. These are server-private facts, not authority by context.
// SessionID/SessionGeneration name playback_sessions and its exact generation,
// not a V1 session, physical source, queue occurrence or mapping generation.
// Size/ModifiedNS are the actual playback source pin, not an inventory revision.
type PlaybackSourceFacts struct {
	Principal         identity.Principal `json:"-"`
	ItemID            string             `json:"-"`
	AssetID           string             `json:"-"`
	SessionID         string             `json:"-"`
	SessionGeneration int                `json:"-"`
	SourceSize        int64              `json:"-"`
	SourceModifiedNS  int64              `json:"-"`
}

// PlaybackSourceInvocation is immutable outside this package. A retained source
// resolver must call CheckTx in its own capture/publication snapshots and bind
// the real source/root/observation separately. No transport is activated here.
type PlaybackSourceInvocation struct {
	facts    PlaybackSourceFacts
	checkTx  func(context.Context, *sql.Tx) error
	retained *playbackSourceRetained
	lifetime context.Context
}

type playbackSourceContinuation struct {
	checkTx func(context.Context, *sql.Tx) error
	close   func() error
}

// Retained input leases belong to the Select invocation, rather than to one
// decoder input. Closing an extraction input must not discard the authority
// recheck needed by the later resource/selection publication.
type playbackSourceRetained struct {
	mu     sync.Mutex
	closed bool
	inputs []playbackSourceContinuation
}

func (r *playbackSourceRetained) close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	inputs := r.inputs
	r.inputs = nil
	r.mu.Unlock()
	for _, input := range inputs {
		input.close()
	}
}

// Retain adds a restrictive DB-only continuation and its real lease cleanup.
// It never replaces CheckTx's actor/session/selection admission. Callers must
// close a lease themselves if registration fails, before any provider I/O.
func (i PlaybackSourceInvocation) Retain(checkTx func(context.Context, *sql.Tx) error, close func() error) error {
	if i.retained == nil || checkTx == nil || close == nil {
		return ErrUnavailable
	}
	i.retained.mu.Lock()
	defer i.retained.mu.Unlock()
	if i.retained.closed {
		return ErrUnavailable
	}
	if len(i.retained.inputs) >= 8 {
		return ErrCapacity
	}
	i.retained.inputs = append(i.retained.inputs, playbackSourceContinuation{checkTx: checkTx, close: close})
	return nil
}

func checkPlaybackSourcePublicationTx(ctx context.Context, tx *sql.Tx) error {
	i, ok := PlaybackSourceInvocationFromContext(ctx)
	if !ok {
		return nil
	}
	if err := i.CheckTx(ctx, tx); err != nil {
		return err
	}
	i.retained.mu.Lock()
	inputs := append([]playbackSourceContinuation(nil), i.retained.inputs...)
	i.retained.mu.Unlock()
	for _, input := range inputs {
		if err := input.checkTx(ctx, tx); err != nil {
			return err
		}
	}
	return nil
}

func closePlaybackSourceContext(ctx context.Context) {
	if i, ok := PlaybackSourceInvocationFromContext(ctx); ok {
		i.retained.close()
	}
}

func (i PlaybackSourceInvocation) Facts() PlaybackSourceFacts { return i.facts }

// Lifetime is the actual Select request, not an extraction child's deadline.
// It conveys no authority; capture and every continuation still call CheckTx.
func (i PlaybackSourceInvocation) Lifetime() context.Context { return i.lifetime }

func (i PlaybackSourceInvocation) CheckTx(ctx context.Context, tx *sql.Tx) error {
	if ctx == nil || tx == nil || i.checkTx == nil || i.retained == nil {
		return ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	i.retained.mu.Lock()
	closed := i.retained.closed
	i.retained.mu.Unlock()
	if closed {
		return ErrUnavailable
	}
	return i.checkTx(ctx, tx)
}

type playbackSourceInvocationKey struct{}

func PlaybackSourceInvocationFromContext(ctx context.Context) (PlaybackSourceInvocation, bool) {
	if ctx == nil {
		return PlaybackSourceInvocation{}, false
	}
	i, ok := ctx.Value(playbackSourceInvocationKey{}).(PlaybackSourceInvocation)
	return i, ok && i.checkTx != nil && i.retained != nil
}

func (s *Service) playbackSourceContext(ctx context.Context, p identity.Principal, item, session string, scope sessionScope, checks ...func(context.Context, *sql.Tx) error) context.Context {
	facts := PlaybackSourceFacts{Principal: p, ItemID: item, AssetID: scope.source, SessionID: session, SessionGeneration: scope.generation, SourceSize: scope.size, SourceModifiedNS: scope.modified}
	i := PlaybackSourceInvocation{facts: facts, retained: &playbackSourceRetained{}, lifetime: ctx}
	context.AfterFunc(ctx, i.retained.close)
	i.checkTx = func(check context.Context, tx *sql.Tx) error {
		current, err := s.authorize(check, tx, facts.Principal, facts.ItemID)
		if err != nil {
			return err
		}
		now, err := s.sessionTx(check, tx, current, facts.ItemID, facts.SessionID)
		if err != nil {
			return err
		}
		if !now.available || now.source != facts.AssetID || now.generation != facts.SessionGeneration || now.size != facts.SourceSize || now.modified != facts.SourceModifiedNS {
			return ErrConflict
		}
		for _, checkTx := range checks {
			if err := checkTx(check, tx); err != nil {
				return err
			}
		}
		return nil
	}
	return context.WithValue(ctx, playbackSourceInvocationKey{}, i)
}
