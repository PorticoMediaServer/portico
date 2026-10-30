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
type playbackStartResponse struct {
	*playbackv1.SessionView
	RetryAfterMs int `json:"retryAfterMs,omitempty"`
}

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
	return playbackStartResponse{RetryAfterMs: v.RetryAfterMs}, nil
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
	return playbackStartResponse{SessionView: &v, RetryAfterMs: v.RetryAfterMs}, nil
}

// closeStartObjects closes reflected struct shapes while retaining actual map
// schemas, optional/nullable branches and arbitrary JSON leaves. This is local
// to the start resource; the generator and decoder remain canonical/unmodified.
func closeStartObjects(shape map[string]any) {
	if shape["type"] == "object" {
		if _, structShape := shape["properties"]; structShape {
			if _, declared := shape["additionalProperties"]; !declared {
				shape["additionalProperties"] = false
			}
		}
	}
	for key, value := range shape {
		switch key {
		case "properties":
			if properties, ok := value.(map[string]any); ok {
				for _, property := range properties {
					if child, ok := property.(map[string]any); ok {
						closeStartObjects(child)
					}
				}
			}
		case "items", "additionalProperties":
			if child, ok := value.(map[string]any); ok {
				closeStartObjects(child)
			}
		case "anyOf", "oneOf", "allOf":
			if alternatives, ok := value.([]any); ok {
				for _, alternative := range alternatives {
					if child, ok := alternative.(map[string]any); ok {
						closeStartObjects(child)
					}
				}
			}
		}
	}
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

func startSessionSchema(variant string) map[string]any {
	shape, err := apikit.Schema(reflect.TypeFor[playbackv1.SessionView]())
	if err != nil {
		panic(err)
	}
	closeStartObjects(shape)
	properties := startProperties(shape)
	properties["retryAfterMs"] = startRetrySchema()
	for _, key := range []string{"id", "revision"} {
		startField(properties, key)["minLength"] = 1
	}
	// These are the resource layer's actual projections, not planner-internal
	// preparing state or a receiver-specific subset of the response.
	startEnum(startField(properties, "role"), "local")
	startEnum(startField(properties, "state"), "playing", "paused", "ended")
	lease := startProperties(startField(properties, "lease"))
	startField(lease, "reportEveryMs")["minimum"] = 1
	// The shared runtime ignores format. Bound the actual UTC RFC3339Nano wire
	// spelling with its supported pattern; calendar validity/future expiry still
	// belong to the server's clock/lease authority, not this structural decoder.
	startField(lease, "expiresAt")["pattern"] = `^[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](\.[0-9]{1,9})?Z$`
	view := startField(properties, "presentation")
	presentation := startProperties(view)
	startEnum(startField(presentation, "mode"), "direct", "stream")
	startEnum(startField(presentation, "engine"), playback.EngineNative, playback.EngineMPV)
	startField(presentation, "generation")["minimum"] = 1
	startField(presentation, "startPositionMs")["minimum"] = 0
	decision := startField(presentation, "decision")
	for _, value := range startProperties(decision) {
		if stream, ok := value.(map[string]any); ok {
			fields := startProperties(stream)
			startEnum(startField(fields, "action"), "direct", "copy", "transcode", "burn")
		}
	}
	if quality, ok := presentation["quality"].(map[string]any); ok {
		startEnum(startField(startProperties(quality), "mode"), playbackv1.QualityOriginal, playbackv1.QualityAutomatic, playbackv1.QualityFixed, playbackv1.QualityLimit, playbackv1.QualityCD)
	}
	files := startField(presentation, "subtitles")["items"].(map[string]any)
	startEnum(startField(startProperties(files), "timeDomain"), "source-relative")
	switch variant {
	case "native", "native-ended":
		startEnum(startField(properties, "kind"), "vod", "audio")
		if variant == "native-ended" {
			startField(properties, "state")["const"] = "ended"
			shape["required"] = append(shape["required"].([]string), "end")
		} else {
			startEnum(startField(properties, "state"), "playing", "paused")
			delete(properties, "end")
		}

		// The native presentation's real delivery plan describes at least one
		// source stream. Channel early-return projections legitimately have none.
		presentation["decision"] = map[string]any{"anyOf": []any{
			map[string]any{"type": "object", "properties": decision["properties"], "required": []string{"video"}, "additionalProperties": false},
			map[string]any{"type": "object", "properties": decision["properties"], "required": []string{"audio"}, "additionalProperties": false},
		}}
	case "channel":
		startEnum(startField(properties, "state"), "playing", "paused")
		delete(properties, "end")
		startEnum(startField(properties, "kind"), "live", "channel")
		startField(presentation, "mode")["const"] = "stream"
		view["required"] = append(view["required"].([]string), "linear")
	case "cold-channel":
		delete(properties, "end")
		startEnum(startField(properties, "kind"), "live", "channel")
		startEnum(startField(properties, "state"), "playing", "paused")
		delete(startField(presentation, "generation"), "minimum")
		startField(presentation, "generation")["const"] = 0
		startField(presentation, "mode")["const"] = "stream"
		startField(presentation, "url")["const"] = ""
		view["required"] = append(view["required"].([]string), "linear")
		startField(startProperties(startField(presentation, "linear")), "state")["const"] = "preparing"
	case "ended-channel":
		// channelPresentation returns before reading an engine/linear buffer when
		// the exact session ended. Do not fabricate native or widen native engines.
		startEnum(startField(properties, "kind"), "live", "channel")
		startField(properties, "state")["const"] = "ended"
		shape["required"] = append(shape["required"].([]string), "end")
		delete(startField(presentation, "generation"), "minimum")
		startField(presentation, "generation")["const"] = 0
		startField(presentation, "mode")["const"] = "stream"
		startField(presentation, "url")["const"] = ""
		startEnum(startField(presentation, "engine"), "")
	default:
		panic("unknown start resource variant")
	}
	return shape
}

func startRetrySchema() map[string]any {
	return map[string]any{"type": "integer", "minimum": 1, "maximum": 10000}
}

func startWaitSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{"retryAfterMs": startRetrySchema()},
		"required":   []string{"retryAfterMs"},
	}
}

// Both alternatives retain the actual SessionView. Only their real media
// generation/linear state differs; neither is the private retry-only branch.
func startSessionSchemas(status int) []any {
	variants := []string{"native", "native-ended", "channel", "cold-channel", "ended-channel"}
	if status == 202 {
		variants = []string{"native"}
	}
	var out []any
	for _, variant := range variants {
		shape := startSessionSchema(variant)
		properties := startProperties(shape)
		if status == 201 {
			delete(properties, "retryAfterMs")
		} else if status == 202 {
			shape["required"] = append(shape["required"].([]string), "retryAfterMs")
			startEnum(startField(properties, "state"), "playing", "paused")
		}
		out = append(out, shape)
	}
	return out
}

func (playbackStartResponse) JSONSchema() map[string]any {
	return map[string]any{"anyOf": append(append(startSessionSchemas(201), startSessionSchemas(202)...), startWaitSchema())}
}

func (playbackStartResponse) JSONSchemaForStatus(status int) map[string]any {
	switch status {
	case 201:
		return map[string]any{"oneOf": startSessionSchemas(201)}
	case 202:
		return map[string]any{"oneOf": append(startSessionSchemas(202), startWaitSchema())}
	default:
		return nil
	}
}
