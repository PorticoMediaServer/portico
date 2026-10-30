package httpapi

import (
	"errors"
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// playbackStartResponse keeps a pre-session wait distinct from a real session.
// The minimal branch belongs only to a still-current original preparation;
// ordinary starts construct the existing session-backed branch.
// Embedding preserves the real SessionView's JSON and nullable-field handling.
//
// The two shapes are tagged (O-159 R-09b): variant "session" carries the real
// session; variant "wait" is the retry-only preparation. A client dispatches on
// the tag, never on which fields happen to be present.
type playbackStartResponse struct {
	Variant string `json:"variant"`
	*playbackv1.SessionView
	RetryAfterMs int `json:"retryAfterMs,omitempty"`
}

const (
	startVariantSession = "session"
	startVariantWait    = "wait"
)

func startPreparedResponse(v playbackv1.PreparedStart) (playbackStartResponse, error) {
	if v.Session != nil {
		if v.RetryAfterMs != v.Session.RetryAfterMs {
			return playbackStartResponse{}, errors.New("start retry does not match its real session")
		}
		return startSessionResponse(*v.Session, v.Preparing)
	}
	if !v.Preparing || v.RetryAfterMs < 1 || v.RetryAfterMs > 10000 {
		return playbackStartResponse{}, errors.New("start did not return a current preparation or a real session")
	}
	return playbackStartResponse{Variant: startVariantWait, RetryAfterMs: v.RetryAfterMs}, nil
}

func startSessionResponse(v playbackv1.SessionView, preparing bool) (playbackStartResponse, error) {
	// The real channel session/lease precedes its asynchronous timeshift buffer.
	// Generation zero is that exact pending projection, never a made-up VOD generation.
	coldChannel := !preparing && (v.Kind == "live" || v.Kind == "channel") && v.Presentation.Mode == "stream" &&
		v.Presentation.Generation == 0 && v.Presentation.URL == "" &&
		v.Presentation.Linear != nil && v.Presentation.Linear.State == "preparing"
	endedChannel := !preparing && (v.Kind == "live" || v.Kind == "channel") && v.State == "ended" && v.End != nil && v.Presentation.Generation == 0 && v.Presentation.Mode == "stream" && v.Presentation.URL == "" && v.Presentation.Engine == ""
	if v.ID == "" || v.Revision == "" || v.Presentation.Generation < 1 && !coldChannel && !endedChannel || preparing && (v.RetryAfterMs < 1 || v.RetryAfterMs > 10000) || !preparing && v.RetryAfterMs != 0 {
		return playbackStartResponse{}, errors.New("start did not return its real session response")
	}
	return playbackStartResponse{Variant: startVariantSession, SessionView: &v, RetryAfterMs: v.RetryAfterMs}, nil
}

func startProperties(shape map[string]any) map[string]any {
	return shape["properties"].(map[string]any)
}
func startField(properties map[string]any, name string) map[string]any {
	return properties[name].(map[string]any)
}
func startEnum(shape map[string]any, values ...string) {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	shape["enum"] = out
}

// startSessionSchema is the one session shape every start returns, native or
// channel, playing, preparing or ended. Responses read open (R-09b), so it is
// not closed; the server's own invariants (a cold channel's generation 0, an
// ended channel's empty engine) stay server-side and in its tests.
func startSessionSchema(preparing bool) map[string]any {
	shape, err := apikit.Schema(reflect.TypeFor[playbackv1.SessionView]())
	if err != nil {
		panic(err)
	}
	properties := startProperties(shape)
	properties["variant"] = map[string]any{"type": "string", "const": startVariantSession}
	shape["required"] = append([]string{"variant"}, shape["required"].([]string)...)
	properties["retryAfterMs"] = startRetrySchema()
	if preparing {
		shape["required"] = append(shape["required"].([]string), "retryAfterMs")
	}
	for _, key := range []string{"id", "revision"} {
		startField(properties, key)["minLength"] = 1
	}
	startEnum(startField(properties, "role"), "local")
	if preparing {
		// Only a native vod/audio start is ever prepared.
		startEnum(startField(properties, "kind"), "vod", "audio")
	} else {
		startEnum(startField(properties, "kind"), "vod", "audio", "live", "channel")
	}
	if preparing {
		startEnum(startField(properties, "state"), "playing", "paused")
	} else {
		startEnum(startField(properties, "state"), "playing", "paused", "ended")
	}
	lease := startProperties(startField(properties, "lease"))
	startField(lease, "reportEveryMs")["minimum"] = 1
	// The shared runtime ignores format. Bound the actual UTC RFC3339Nano wire
	// spelling with its supported pattern; calendar validity/future expiry still
	// belong to the server's clock/lease authority, not this structural decoder.
	startField(lease, "expiresAt")["pattern"] = `^[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](\.[0-9]{1,9})?Z$`
	presentation := startProperties(startField(properties, "presentation"))
	startEnum(startField(presentation, "mode"), "direct", "stream")
	// An ended channel returns before an engine is chosen: its engine is empty.
	startEnum(startField(presentation, "engine"), playback.EngineNative, playback.EngineMPV, "")
	startField(presentation, "generation")["minimum"] = 0
	startField(presentation, "startPositionMs")["minimum"] = 0
	for _, value := range startProperties(startField(presentation, "decision")) {
		if stream, ok := value.(map[string]any); ok {
			startEnum(startField(startProperties(stream), "action"), "direct", "copy", "transcode", "burn")
		}
	}
	if quality, ok := presentation["quality"].(map[string]any); ok {
		startEnum(startField(startProperties(quality), "mode"), playbackv1.QualityOriginal, playbackv1.QualityAutomatic, playbackv1.QualityFixed, playbackv1.QualityLimit, playbackv1.QualityCD)
	}
	files := startField(presentation, "subtitles")["items"].(map[string]any)
	startEnum(startField(startProperties(files), "timeDomain"), "source-relative")
	return shape
}

func startRetrySchema() map[string]any {
	return map[string]any{"type": "integer", "minimum": 1, "maximum": 10000}
}

func startWaitSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"variant": map[string]any{"type": "string", "const": startVariantWait}, "retryAfterMs": startRetrySchema()},
		"required":   []string{"variant", "retryAfterMs"},
	}
}

func (playbackStartResponse) JSONSchema() map[string]any {
	return map[string]any{"anyOf": []any{startSessionSchema(false), startWaitSchema()}}
}

func (playbackStartResponse) JSONSchemaForStatus(status int) map[string]any {
	switch status {
	case 201:
		return startSessionSchema(false)
	case 202:
		return map[string]any{"oneOf": []any{startSessionSchema(true), startWaitSchema()}}
	default:
		return nil
	}
}
