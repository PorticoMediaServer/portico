package subtitles

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"strconv"
	"sync"
)

// DiscInput is an already admitted descriptor-backed reader. Commands supply
// only trusted codec options; the input, sandbox and lifetime stay with playback.
type DiscInput interface {
	Run(context.Context, string, []string, []string, int64) ([]byte, error)
	Validate(context.Context) error
	Close() error
}
type DiscSubtitleExtractor interface {
	ExtractDisc(context.Context, string, string, int, string) ([]byte, error)
	DiscSubtitleAvailable() bool
}
type DiscExtractionFacts struct {
	SessionID, ItemID, AssetID, ResourceID, Object, TitleKey, FactsRevision string
	SnapshotID                                                              string
	ResourceRevision                                                        int64
	StreamIndex                                                             int
}
type discExtractionKey struct{}
type DiscExtractionInvocation struct {
	facts DiscExtractionFacts
	check func(context.Context, *sql.Tx) error
	input *discExtractionInput
}

type discExtractionInput struct {
	mu       sync.Mutex
	validate func(context.Context) error
	close    func() error
	closed   bool
}

// RetainInput keeps the exact descriptor until resource publication completes.
// The reader's Close may retire its extraction without retiring this lease.
func (i DiscExtractionInvocation) RetainInput(validate func(context.Context) error, close func() error) error {
	if i.input == nil || validate == nil || close == nil {
		return ErrUnavailable
	}
	i.input.mu.Lock()
	defer i.input.mu.Unlock()
	if i.input.closed || i.input.close != nil {
		return ErrConflict
	}
	i.input.validate = validate
	i.input.close = close
	return nil
}
func (i DiscExtractionInvocation) validateInput(ctx context.Context) error {
	if i.input == nil {
		return ErrUnavailable
	}
	i.input.mu.Lock()
	defer i.input.mu.Unlock()
	if i.input.closed || i.input.validate == nil {
		return ErrUnavailable
	}
	return i.input.validate(ctx)
}
func (i DiscExtractionInvocation) closeInput() {
	if i.input == nil {
		return
	}
	i.input.mu.Lock()
	defer i.input.mu.Unlock()
	i.input.closed = true
	if i.input.close != nil {
		_ = i.input.close()
		i.input.close = nil
	}
}

func (i DiscExtractionInvocation) Facts() DiscExtractionFacts { return i.facts }
func (i DiscExtractionInvocation) CheckTx(ctx context.Context, tx *sql.Tx) error {
	if i.check == nil || ctx == nil || tx == nil {
		return ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return i.check(ctx, tx)
}
func DiscExtractionInvocationFromContext(ctx context.Context) (DiscExtractionInvocation, bool) {
	if ctx == nil {
		return DiscExtractionInvocation{}, false
	}
	i, ok := ctx.Value(discExtractionKey{}).(DiscExtractionInvocation)
	return i, ok && i.check != nil
}

func (s *Service) fillDiscCache(ctx context.Context, p identity.Principal, item string, r Resource) error {
	extractor, ok := s.Extractor.(DiscSubtitleExtractor)
	if !ok || !extractor.DiscSubtitleAvailable() {
		return ErrUnavailable
	}
	sourceInvocation, ok := PlaybackSourceInvocationFromContext(ctx)
	if !ok {
		return ErrUnavailable
	}
	admitted := sourceInvocation.Facts()
	if admitted.ItemID != item || admitted.AssetID != r.SourceID {
		return ErrConflict
	}
	snapshot, p, err := s.readTx(ctx, p, item)
	if err != nil {
		return err
	}
	tx := snapshot.Tx()
	if err = s.checkDiscResourceSessionTx(ctx, tx, admitted.SessionID, r.ID); err != nil {
		snapshot.Rollback()
		return err
	}
	facts := DiscExtractionFacts{SessionID: admitted.SessionID, ItemID: item, AssetID: r.SourceID, ResourceID: r.ID, ResourceRevision: r.Revision}
	var origin, duration int64
	var timing bool
	private, scoped, err := s.privateDiscPresentationTx(ctx, tx, admitted.SessionID)
	if err == nil && scoped {
		facts.SnapshotID, facts.Object, facts.TitleKey, facts.FactsRevision = private.SnapshotID, private.ObjectID, private.TitleKey, private.FactsRevision
		origin, duration, timing = private.OriginUS, private.DurationMS, private.TimingKnown
		err = tx.QueryRowContext(ctx, `SELECT stream_index FROM playback_disc_subtitle_resources WHERE snapshot_id=? AND resource_id=?`, private.SnapshotID, r.ID).Scan(&facts.StreamIndex)
	} else if err == nil {
		err = tx.QueryRowContext(ctx, `SELECT dr.object_id,dr.title_key,dr.title_facts_revision,dr.stream_index,dr.origin_us,dr.timing_known,m.duration_ms FROM subtitle_disc_resources dr JOIN inventory_disc_title_media m ON m.object_id=dr.object_id AND m.title_key=dr.title_key AND m.revision=dr.title_facts_revision WHERE dr.resource_id=?`, r.ID).Scan(&facts.Object, &facts.TitleKey, &facts.FactsRevision, &facts.StreamIndex, &origin, &timing, &duration)
	}
	if err != nil {
		snapshot.Rollback()
		return err
	}
	src, err := s.sourceQuery(ctx, tx, item, r.SourceID)
	snapshot.Rollback()
	if err != nil {
		return err
	}
	src.duration = float64(duration) / 1000
	src.originUS = origin
	src.TimingKnown = timing
	invocation := DiscExtractionInvocation{facts: facts, input: &discExtractionInput{}}
	defer invocation.closeInput()
	invocation.check = func(check context.Context, current *sql.Tx) error {
		if err := sourceInvocation.CheckTx(check, current); err != nil {
			return err
		}
		if err := s.checkDiscResourceSessionTx(check, current, facts.SessionID, facts.ResourceID); err != nil {
			return err
		}
		var valid bool
		var err error
		if facts.SnapshotID != "" {
			actual, known, e := s.privateDiscPresentationTx(check, current, facts.SessionID)
			if e != nil {
				return e
			}
			if !known || actual.SnapshotID != facts.SnapshotID || actual.ObjectID != facts.Object || actual.TitleKey != facts.TitleKey || actual.FactsRevision != facts.FactsRevision {
				return ErrConflict
			}
			err = current.QueryRowContext(check, `SELECT EXISTS(SELECT 1 FROM playback_disc_subtitle_resources map JOIN subtitle_resources r ON r.id=map.resource_id WHERE map.snapshot_id=? AND r.id=? AND r.current_revision=? AND r.deleted=0 AND r.source_id=? AND map.stream_index=?)`, facts.SnapshotID, facts.ResourceID, facts.ResourceRevision, facts.AssetID, facts.StreamIndex).Scan(&valid)
		} else {
			err = current.QueryRowContext(check, `SELECT EXISTS(SELECT 1 FROM subtitle_resources r JOIN subtitle_disc_resources dr ON dr.resource_id=r.id WHERE r.id=? AND r.current_revision=? AND r.deleted=0 AND r.source_id=? AND dr.object_id=? AND dr.title_key=? AND dr.title_facts_revision=? AND dr.stream_index=? AND `+discResourceCurrent+`)`, facts.ResourceID, facts.ResourceRevision, facts.AssetID, facts.Object, facts.TitleKey, facts.FactsRevision, facts.StreamIndex).Scan(&valid)
		}

		if err != nil {
			return err
		}
		if !valid {
			return ErrConflict
		}
		return nil
	}
	work := context.WithValue(ctx, discExtractionKey{}, invocation)
	gate, _, err := s.readTx(work, p, item)
	if err != nil {
		return err
	}
	err = invocation.CheckTx(work, gate.Tx())
	gate.Rollback()
	if err != nil {
		return err
	}
	canonical, err := extractor.ExtractDisc(work, item, r.SourceID, facts.StreamIndex, r.Format)
	if err != nil {
		return err
	}
	render := "text"
	if r.Format == "ass" || r.Format == "ssa" {
		render = "styled"
	}
	operation := identity.Digest("disc-cache:" + r.ID + ":" + strconv.FormatInt(r.Revision, 10))
	mutation := Mutation{OperationID: operation, SourceID: r.SourceID, Scope: r.Scope, Format: r.Format, Language: r.Language, Title: r.Title, Render: render}
	pub := publication{source: src, origin: "embedded", renderer: r.Renderer, cacheResource: r.ID, cacheRevision: r.Revision, originalAsset: canonical, originalDigest: digestBytes(canonical), defaultTrack: r.Default, forcedTrack: r.Forced, hearingImpaired: r.HearingImpaired}
	raw := renderChoice(canonical, render, duration*1000)
	_, err = s.publish(work, p, item, mutation, requestDigest([]any{"disc-cache", item, r.ID, r.Revision, facts.FactsRevision}), pub, raw)
	return err
}

// ensureSessionCached retains the live original/manifest grant for a disc fill.
// Ordinary resources retain their existing detached-cache behavior.
func (s *Service) ensureSessionCached(ctx context.Context, p identity.Principal, item, session, id string, revision int64) error {
	snapshot, p, err := s.readTx(ctx, p, item)
	if err != nil {
		return err
	}
	tx := snapshot.Tx()
	var scoped bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM subtitle_disc_resources WHERE resource_id=?1) OR EXISTS(SELECT 1 FROM playback_disc_subtitle_resources WHERE resource_id=?1)`, id).Scan(&scoped); err != nil {
		snapshot.Rollback()
		return err
	}
	if !scoped {
		snapshot.Rollback()
		return s.ensureCached(ctx, p, item, id, revision)
	}
	scope, err := s.sessionTx(ctx, tx, p, item, session)
	if err != nil {
		snapshot.Rollback()
		return err
	}
	if err = s.checkDiscResourceSessionTx(ctx, tx, session, id); err != nil {
		snapshot.Rollback()
		return err
	}
	selected := s.playbackSourceContext(ctx, p, item, session, scope, func(check context.Context, current *sql.Tx) error {
		if err := s.checkDiscResourceSessionTx(check, current, session, id); err != nil {
			return err
		}
		var admitted bool
		err := current.QueryRowContext(check, `SELECT EXISTS(SELECT 1 FROM playback_subtitle_pins pin JOIN subtitle_resources r ON r.id=pin.resource_id WHERE pin.session_id=? AND pin.resource_id=? AND pin.resource_revision=? AND r.source_id=? AND r.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)))`, session, id, revision, scope.source, item).Scan(&admitted)
		if err != nil {
			return err
		}
		if !admitted {
			return ErrConflict
		}
		return nil
	})
	snapshot.Rollback()
	defer closePlaybackSourceContext(selected)
	return s.ensureCached(selected, p, item, id, revision)
}
