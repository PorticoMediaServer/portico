package playback

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

// SessionStreamDecision is what happened to one stream of the source. It is the
// per-stream half of the diagnostic: a viewer asking "why does this look like
// that" is really asking which stream was copied and which was re-encoded.
type SessionStreamDecision struct {
	Kind        string `json:"kind"`
	InputCodec  string `json:"inputCodec"`
	OutputCodec string `json:"outputCodec"`
	Action      string `json:"action"`
}

// SessionHardware names the encoder family in force and where each operation
// ran. An empty stage list means conversion has not started yet.
type SessionHardware struct {
	Execution *ConversionExecution    `json:"execution,omitempty"`
	Backend   string                  `json:"backend"`
	Stages    []decoder.HardwareStage `json:"stages"`
}

// SessionDelivery is the published per-session decision detail. It carries no
// paths, no source identifiers and no credentials: it is an explanation, not a
// handle.
type SessionDelivery struct {
	AudioRenditions       []AudioRenditionPlan    `json:"audioRenditions,omitempty"`
	Mode                  string                  `json:"mode"`
	Strategy              string                  `json:"strategy"`
	ReasonCodes           []string                `json:"reasonCodes"`
	Policy                *ResolvedDeliveryPolicy `json:"policy,omitempty"`
	QualityID             string                  `json:"qualityId"`
	Hardware              SessionHardware         `json:"hardware"`
	Streams               []SessionStreamDecision `json:"streams"`
	Throttled             bool                    `json:"throttled"`
	ConvertedThroughMs    int64                   `json:"convertedThroughMs"`
	ToneMap               bool                    `json:"toneMap"`
	ToneMapAlgorithm      string                  `json:"toneMapAlgorithm,omitempty"`
	TargetDisplayHeight   int                     `json:"targetDisplayHeight,omitempty"`
	TargetVideoBitrateBPS int                     `json:"targetVideoBitrateBps,omitempty"`
	TargetAudioBitrateBPS int                     `json:"targetAudioBitrateBps,omitempty"`
	PlayedRetentionMs     int64                   `json:"playedRetentionMs"`
	ThrottleBufferMs      int64                   `json:"throttleBufferMs"`
	// Decision is the trace of the plan: the device it was made for, the source
	// as the planner saw it, and for every route whether it was admissible and,
	// if not, each rule that rejected it.
	Decision *DecisionTrace `json:"decision,omitempty"`
	// AudioChannels and Downmix say what happened to the sound.
	AudioChannels   int    `json:"audioChannels,omitempty"`
	Downmix         string `json:"downmix,omitempty"`
	Deinterlace     bool   `json:"deinterlace,omitempty"`
	OutputContainer string `json:"outputContainer,omitempty"`
	// Failure is the class of a conversion failure, or of a hardware failure the
	// session recovered from in software. The converter's own words stay on the
	// owner's diagnostics page.
	Failure string `json:"failure,omitempty"`
}

func streamAction(action string) string {
	switch action {
	case "copy", "convert", "original":
		return action
	case "none":
		return "drop"
	}
	return "unknown"
}

// SessionDeliveryTx reads one session's decision detail inside the caller's
// transaction. A session without a stored plan returns nil rather than an
// invented one; the reader publishes no delivery block at all in that case.
func SessionDeliveryTx(ctx context.Context, tx *sql.Tx, settings DeliverySettings, session string) (*SessionDelivery, error) {
	return sessionDeliveryTx(ctx, tx, settings, session, false)
}

// AdminSessionDeliveryTx is for routes already authorized for owner/admin diagnostics.
func AdminSessionDeliveryTx(ctx context.Context, tx *sql.Tx, settings DeliverySettings, session string) (*SessionDelivery, error) {
	return sessionDeliveryTx(ctx, tx, settings, session, true)
}

func sessionDeliveryTx(ctx context.Context, tx *sql.Tx, settings DeliverySettings, session string, admin bool) (*SessionDelivery, error) {
	plan, err := loadDeliveryPlan(ctx, tx, session)
	if err != nil {
		if errors.Is(err, ErrStaleOffer) {
			return nil, nil
		}
		return nil, err
	}
	if plan == nil {
		return nil, nil
	}
	cfg := deliveryConfiguration(settings)
	out := &SessionDelivery{
		AudioRenditions: plan.Renditions, Mode: plan.Mode, Strategy: string(plan.Strategy), ReasonCodes: plan.ReasonCodes, Policy: plan.Policy,
		QualityID: plan.QualityID, ToneMap: plan.ToneMap, ToneMapAlgorithm: plan.ToneMapAlgorithm,
		TargetDisplayHeight: plan.TargetHeight, TargetVideoBitrateBPS: plan.VideoBitrateBPS, TargetAudioBitrateBPS: plan.AudioBitrateBPS,
		PlayedRetentionMs: int64(cfg.PlayedRetentionSeconds) * 1000, ThrottleBufferMs: int64(cfg.ThrottleBufferSeconds) * 1000,
		Hardware: SessionHardware{Backend: plan.HardwareBackend, Stages: []decoder.HardwareStage{}},
		Streams:  []SessionStreamDecision{},
		Decision: plan.Trace, AudioChannels: plan.AudioChannels, Downmix: plan.Downmix, Deinterlace: plan.Deinterlace, OutputContainer: plan.OutputContainer,
	}
	if failure, failureErr := readConversionFailure(ctx, tx, session); failureErr != nil {
		return nil, failureErr
	} else if failure != nil {
		out.Failure = failure.Code
	}
	if out.ReasonCodes == nil {
		out.ReasonCodes = []string{}
	}
	if plan.SourceVideoCodec != "" || plan.VideoAction != "original" {
		out.Streams = append(out.Streams, SessionStreamDecision{Kind: "video", InputCodec: plan.SourceVideoCodec, OutputCodec: plan.OutputVideoCodec, Action: streamAction(plan.VideoAction)})
	}
	if plan.SourceAudioCodec != "" {
		out.Streams = append(out.Streams, SessionStreamDecision{Kind: "audio", InputCodec: plan.SourceAudioCodec, OutputCodec: plan.OutputAudioCodec, Action: streamAction(plan.AudioAction)})
	}
	var burned bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_subtitle_presentations WHERE session_id=? AND render_id!='')`, session).Scan(&burned); err != nil {
		return nil, err
	}
	if burned {
		out.Streams = append(out.Streams, SessionStreamDecision{Kind: "subtitle", InputCodec: "", OutputCodec: "", Action: "burn_in"})
	}
	var backend, raw string
	err = tx.QueryRowContext(ctx, `SELECT backend,stages_json FROM playback_hls_stages WHERE session_id=?`, session).Scan(&backend, &raw)
	if err == nil {
		record := readConversionStages(raw)
		if record.Stages != nil {
			out.Hardware = SessionHardware{Backend: backend, Stages: record.Stages}
			if admin {
				out.Hardware.Execution = record.Execution
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var throttled int
	var served int64 = -1
	err = tx.QueryRowContext(ctx, `SELECT highest_served,throttled FROM playback_hls_demand WHERE session_id=?`, session).Scan(&served, &throttled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	out.Throttled = throttled != 0
	var produced int64 = -1
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(highest_produced),-1) FROM playback_hls_windows WHERE session_id=?`, session).Scan(&produced)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if produced >= 0 {
		through := float64(produced+1) * HLSSegmentSeconds
		if through > plan.Duration && plan.Duration > 0 {
			through = plan.Duration
		}
		out.ConvertedThroughMs = int64(math.Round(through * 1000))
	}
	return out, nil
}
