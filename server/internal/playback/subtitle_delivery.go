package playback

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// ChangeSubtitleDeliveryTx is called inside the subtitle selection transaction,
// after its viewer, source, resource and policy fences have been checked.
// The new grant, selected subtitle and ordinary delivery plan commit together.
func (s *Service) ChangeSubtitleDeliveryTx(ctx context.Context, tx *sql.Tx, p identity.Principal, session string, resource *subtitles.Resource) error {
	if err := s.checkDiscSessionTx(ctx, tx, session); err != nil {
		return err
	}
	_, _, private, err := privateDiscBindingTx(ctx, tx, session)
	if err != nil {
		return err
	}
	if private {
		transition, ok := ctx.Value(discSubtitleTransitionKey{}).(*DiscSubtitleTransition)
		if !ok || transition.service != s || transition.parent.SessionID != session {
			return subtitles.ErrUnsupported
		}
		transition.mu.Lock()
		exact := transition.tx == tx && transition.claim != nil && !transition.child
		transition.mu.Unlock()
		if !exact {
			return subtitles.ErrUnsupported
		}
		if err = transition.CheckTx(ctx, tx); err != nil {
			return err
		}
	}
	if resource != nil {
		if private {
			if s.Subtitles == nil {
				return subtitles.ErrUnavailable
			}
			if err = s.Subtitles.CheckDiscResourceSessionTx(ctx, tx, session, resource.ID); err != nil {
				return err
			}
		} else if err = subtitles.CheckDiscResourceSessionTx(ctx, tx, session, resource.ID); err != nil {
			return err
		}
	}
	old, err := loadDeliveryPlan(ctx, tx, session)
	if err != nil {
		return err
	}
	if old == nil {
		return subtitles.ErrUnavailable
	}
	source, err := s.loadSessionDeliverySourceTx(ctx, tx, session, old.SourceID)
	if err != nil {
		return err
	}
	if source.Video == nil {
		return subtitles.ErrUnsupported
	}
	preparedDelivery, err := s.checkedDeliveryTx(ctx, tx)
	if err != nil {
		return err
	}
	cfg := preparedDelivery.state.configuration
	edge := DeliveryContextFrom(ctx)
	values := s.viewerPreferences(tx, p, edge)
	policy := ResolveDeliveryPolicy(values, edge.NetworkClass, edge.ServerLocality, edge.TransportClass, cfg)
	var enabled bool
	if err = tx.QueryRowContext(ctx, `SELECT transcoding_enabled FROM playback_owner_policy WHERE singleton=1`).Scan(&enabled); err != nil {
		return err
	}
	var current *ReplanCeilings
	if ceilings, ok := replanCeilingsFrom(ctx); ok {
		current = &ceilings
	}
	if current != nil {
		ceiling, err := memberAwayCeilingsTx(ctx, tx, p.AccountID, current.Remote || old.TargetReach != reachLocal)
		if err != nil {
			return err
		}
		current.MaxVideoBitrateBPS = narrowCeiling(current.MaxVideoBitrateBPS, ceiling.MaxVideoBitrateBPS)
		current.MaxVideoHeight = narrowCeiling(current.MaxVideoHeight, ceiling.MaxVideoHeight)
	}
	target, replanned, err := replanTarget(old, cfg, current, true)
	if err != nil {
		return err
	}
	input := DeliveryInput{SourceID: source.ID, Source: &source, Container: source.Container, VideoCodec: source.Video.Codec, Duration: source.Duration, Height: source.Video.Height, Policy: policy, Target: target, Config: cfg, TranscodingEnabled: enabled, Hardware: preparedDelivery.hardware, BurnIn: resource != nil, Remote: source.Remote, Engine: old.Engine}
	var explicit *int
	if old.AudioStream >= 0 {
		explicit = &old.AudioStream
	}
	discSession, err := hasDiscSessionTx(ctx, tx, session)
	if err != nil {
		return err
	}
	var boundDisc []*DeliverySource
	if discSession || source.Remote {
		boundDisc = []*DeliverySource{&source}
	}
	if err = s.completeDeliveryInputTx(ctx, tx, p, &input, values, true, explicit, boundDisc...); err != nil {
		return err
	}
	if old.AirPlay {
		input.Device = nil
		input.Client = BaselineClientProfile()
		input.Client.Transports = []ClientTransport{{Transport: TransportHLSTS, Containers: []string{"mpegts"}, Video: []string{"h264"}, Audio: []string{"aac"}}}
		input.Client.SubtitleRendering = []string{SubtitleText}
		input.Engine = EngineNative
	}
	plan, err := planDelivery(input)
	if err != nil {
		return err
	}
	if private && plan.Mode != "hls" {
		return subtitles.ErrUnsupported
	}
	if old.Trace != nil && old.Trace.Audio != nil && plan.Trace != nil && plan.Trace.Audio != nil {
		plan.Trace.Audio.ChosenBy = old.Trace.Audio.ChosenBy
	}
	if resource != nil {
		plan.BurnIn = &BurnInSpec{ResourceID: resource.ID, Revision: resource.Revision, Format: resource.Format}
		// Validate the actual graph before committing a new presentation. This
		// constructs arguments only; no probe or process is allowed in this tx.
		_, err = plan.conversionGraph(&decoder.BurnIn{File: filepath.Join(os.TempDir(), "selected.ass"), Format: resource.Format, Width: source.Video.Width, Height: source.Video.Height})
		if err != nil {
			return subtitles.ErrUnsupported
		}
	}
	if plan.Mode == "hls" {
		if err = reserveHLS(tx, session, session, &plan, cfg); err != nil {
			return err
		}
	}

	plan.AirPlay = old.AirPlay
	if old.AirPlay {
		plan.Renditions = nil
	}
	plan.SourceSize, plan.SourceModifiedNS, plan.FactsRevision = old.SourceSize, old.SourceModifiedNS, old.FactsRevision
	plan.Target, plan.TargetReach = replanned.target, replanned.reach
	if _, err = tx.ExecContext(ctx, `DELETE FROM playback_delivery_plans WHERE session_id=?`, session); err != nil {
		return err
	}
	if err = persistDeliveryPlan(tx, session, &plan); err != nil {
		return err
	}
	// The subtitle transaction publishes its new generation after this callback.
	if _, err = tx.ExecContext(ctx, `UPDATE playback_conversion_targets SET generation=generation+1 WHERE session_id=?`, session); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE playback_sessions SET mode=? WHERE id=?`, plan.Mode, session); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM playback_hls_windows WHERE session_id=?`, session); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE playback_hls_stages SET stages_json=json_set(CASE WHEN json_type(stages_json)='object' THEN stages_json ELSE json_object('stages',json(stages_json)) END,'$.stages',json('[]')) WHERE session_id=?`, session)
	return err
}

// ReplanCeilings is what the caller's admission allows right now, on the
// connection its request arrived on, for a change that makes a presentation
// produce new bytes without being admitted again (a subtitle burned in or
// turned off). Remote is admission's own view of the connection, not a hint
// the client sent. MaxVideoBitrateBPS is the tightest video bitrate the member
// and the server's upload budget allow there; zero is none.
// MaxVideoHeight independently preserves the account's selected ladder height.
type ReplanCeilings struct {
	Remote             bool
	MaxVideoBitrateBPS int
	MaxVideoHeight     int
}

type replanCeilingsKey struct{}

// WithReplanCeilings attaches them to the request that may re-plan. A v1
// presentation is not re-planned for a request that carries none.
func WithReplanCeilings(ctx context.Context, c ReplanCeilings) context.Context {
	return context.WithValue(ctx, replanCeilingsKey{}, c)
}

func replanCeilingsFrom(ctx context.Context) (ReplanCeilings, bool) {
	c, ok := ctx.Value(replanCeilingsKey{}).(ReplanCeilings)
	return c, ok
}

const reachLocal, reachRemote = "local", "remote"

// keptTarget is what a re-planned presentation stores for its next re-plan:
// its narrowed target and the reach admission saw.
type keptTarget struct {
	target *QualityTarget
	reach  string
}

// replanTarget is the target a presentation's stored plan is planned under
// again: a subtitle burned in or turned off, a copied stream that has to be
// converted after all.
//
// A request brings what its caller is allowed right now (current): admission's
// view of the connection and the member's ceiling there. One that brings none
// is refused. The conversion fallback has no request; its current is what
// fallbackCeilings read for the presentation.
//
// A presentation is planned under the ceilings it was last planned with (the
// quality the viewer chose, the member's remote cap, the owner's), narrowed by
// what applies now, and keeps the result: they only ever come down. The
// owner's ceilings apply as they are now; the remote ones do when admission
// has called this presentation's connection remote, at its start, at an
// earlier re-plan or for this request, or when that was never recorded. A plan
// stored without its ceilings is refused: what it started under cannot be
// recovered, and no ceiling is not an answer.
func replanTarget(old *DeliveryPlan, cfg DeliveryConfiguration, current *ReplanCeilings, request bool) (QualityTarget, keptTarget, error) {
	if request && current == nil || old.Target == nil {
		return QualityTarget{}, keptTarget{}, ErrQualityUnavailable
	}
	remote, member := old.TargetReach != reachLocal, 0
	if current != nil {
		remote, member = remote || current.Remote, current.MaxVideoBitrateBPS
	}
	target, reach := narrowTarget(*old.Target, remote, member, cfg), reachLocal
	if current != nil {
		target.MaxVideoHeight = narrowCeiling(target.MaxVideoHeight, current.MaxVideoHeight)
	}
	if remote {
		reach = reachRemote
	}
	return target, keptTarget{&target, reach}, nil
}

// narrowTarget brings a stored target down to what applies now: the member's
// current video bitrate ceiling (zero: none) and the owner's ceilings. The
// server-wide video bitrate limit is a remote-connection limit, as it is at
// the start of a presentation. Nothing here raises a ceiling.
func narrowTarget(t QualityTarget, remote bool, member int, cfg DeliveryConfiguration) QualityTarget {
	if remote {
		t.MaxVideoBitrateBPS = narrowCeiling(t.MaxVideoBitrateBPS, cfg.MaxVideoBitrateBPS)
	}
	t.MaxVideoBitrateBPS = narrowCeiling(t.MaxVideoBitrateBPS, member)
	t.MaxAudioBitrateBPS = narrowCeiling(t.MaxAudioBitrateBPS, cfg.MaxAudioBitrateBPS)
	t.MaxVideoHeight = narrowCeiling(t.MaxVideoHeight, cfg.MaxVideoHeight)
	return t
}

func (s *Service) ConfigureSubtitleDelivery(service *subtitles.Service) {
	s.Subtitles = service
	service.PrepareDelivery = s.prepareDelivery
	service.ChangeDeliveryTx = s.ChangeSubtitleDeliveryTx
	service.RestartDelivery = s.RestartConversion
	if s.hls != nil {
		s.hls.subtitleService = service
	}
}
