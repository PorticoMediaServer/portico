package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
	"reflect"
	"time"
)

const readingOfflineMaxInteger int64 = 9007199254740991

type ReadingOfflineLocation struct {
	Format      ReadingFormat `json:"format"`
	CFI         string        `json:"cfi,omitempty"`
	Section     string        `json:"section,omitempty"`
	PageIndex   *int64        `json:"pageIndex,omitempty"`
	Progression *float64      `json:"progression"`
}

func (l *ReadingOfflineLocation) UnmarshalJSON(raw []byte) error {
	parsed, err := reading.ParseOfflineLocation(raw)
	if err != nil {
		return err
	}
	progression := parsed.Progression
	*l = ReadingOfflineLocation{Format: ReadingFormat(parsed.Format), CFI: parsed.CFI, Section: parsed.Section, PageIndex: parsed.PageIndex, Progression: &progression}
	return nil
}
func readingOfflineInteger(minimum int64) map[string]any {
	return map[string]any{"type": "integer", "minimum": minimum, "maximum": readingOfflineMaxInteger}
}
func (ReadingOfflineLocation) JSONSchema() map[string]any {
	variants := []any{}
	for _, format := range []string{"epub", "pdf", "cbz", "cb7", "cbr"} {
		properties := map[string]any{"format": map[string]any{"type": "string", "const": format}, "progression": map[string]any{"type": "number", "minimum": 0, "maximum": 1}}
		required := []string{"format", "progression"}
		if format == "epub" {
			properties["cfi"] = map[string]any{"type": "string", "minLength": 10, "maxLength": 4096, "pattern": `^epubcfi\(/.*\)$`}
			properties["section"] = map[string]any{"type": "string", "maxLength": 1024}
			required = append(required, "cfi")
		} else {
			properties["pageIndex"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 1000000}
			required = append(required, "pageIndex")
		}
		variants = append(variants, map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required})
	}
	return map[string]any{"oneOf": variants}
}

type ReadingOfflineProgressRequest struct {
	ObservationReceipt *downloads.Receipt     `json:"observationReceipt,omitempty"`
	Receipt            downloads.Receipt      `json:"receipt"`
	ExpectedRevision   *int64                 `json:"expectedRevision"`
	ObservedAt         int64                  `json:"observedAt"`
	StartedAt          int64                  `json:"startedAt"`
	Location           ReadingOfflineLocation `json:"location"`
}

func (body *ReadingOfflineProgressRequest) UnmarshalJSON(raw []byte) error {
	if _, err := apikit.CanonicalJSON(raw); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return reading.ErrInvalid
	}
	allowed := map[string]bool{"receipt": true, "expectedRevision": true, "observedAt": true, "startedAt": true, "location": true, "observationReceipt": true}
	for key, value := range fields {
		if !allowed[key] || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return reading.ErrInvalid
		}
	}
	for _, key := range []string{"receipt", "expectedRevision", "observedAt", "startedAt", "location"} {
		if _, ok := fields[key]; !ok {
			return reading.ErrInvalid
		}
	}
	var plain readingOfflineProgressRequestShape
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plain); err != nil {
		return err
	}
	*body = ReadingOfflineProgressRequest(plain)
	return nil
}

type readingOfflineProgressRequestShape ReadingOfflineProgressRequest

func (ReadingOfflineProgressRequest) JSONSchema() map[string]any {
	schema, err := apikit.Schema(reflect.TypeFor[readingOfflineProgressRequestShape]())
	if err != nil {
		panic(err)
	}
	properties := schema["properties"].(map[string]any)
	receiptSchema, err := apikit.Schema(reflect.TypeFor[downloads.Receipt]())
	if err != nil {
		panic(err)
	}
	properties["observationReceipt"] = receiptSchema
	properties["expectedRevision"] = readingOfflineInteger(0)
	properties["observedAt"] = readingOfflineInteger(1)
	properties["startedAt"] = readingOfflineInteger(1)
	schema["required"] = []string{"receipt", "expectedRevision", "observedAt", "startedAt", "location"}
	schema["additionalProperties"] = false
	return schema
}

type ReadingOfflineOutcome string

func (ReadingOfflineOutcome) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{"applied", "history_paused", "stale_observation", "superseded_online"}}
}

type ReadingOfflineProgressResponse struct {
	Progress *ReadingProgress      `json:"progress"`
	Saved    bool                  `json:"saved"`
	Outcome  ReadingOfflineOutcome `json:"outcome"`
}

func (ReadingOfflineProgressResponse) JSONSchema() map[string]any {
	location := ReadingOfflineLocation{}.JSONSchema()
	progress := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"sourceId", "sourceRevision", "location", "revision", "observedAt", "stale"}, "properties": map[string]any{
		"sourceId":       map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[A-Za-z0-9_-]+$"},
		"sourceRevision": map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"}, "location": location, "revision": readingOfflineInteger(0), "observedAt": readingOfflineInteger(1), "stale": map[string]any{"type": "boolean"},
	}}
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"progress", "saved", "outcome"}, "properties": map[string]any{"progress": map[string]any{"anyOf": []any{progress, map[string]any{"type": "null"}}}, "saved": map[string]any{"type": "boolean"}, "outcome": ReadingOfflineOutcome("").JSONSchema()}}
}

func (d Dependencies) readingOfflineProgress(w http.ResponseWriter, r *http.Request) {
	p, ok := d.readingPrincipal(w, r)
	if !ok {
		return
	}
	if d.Downloads == nil || d.Identity == nil {
		readingFailure(w, reading.ErrUnavailable)
		return
	}
	var body ReadingOfflineProgressRequest
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	if err := decode(w, r, &body); err != nil {
		readingFailure(w, reading.ErrInvalid)
		return
	}
	if body.ExpectedRevision == nil || *body.ExpectedRevision < 0 || *body.ExpectedRevision > readingOfflineMaxInteger || body.ObservedAt < 1 || body.ObservedAt > readingOfflineMaxInteger || body.StartedAt < 1 || body.StartedAt > body.ObservedAt || body.Location.Progression == nil || body.Location.PageIndex != nil && *body.Location.PageIndex > readingOfflineMaxInteger {
		readingFailure(w, reading.ErrInvalid)
		return
	}
	location := body.Location
	parsedLocation := reading.Location{Format: string(location.Format), CFI: location.CFI, Section: location.Section, PageIndex: location.PageIndex, Progression: *location.Progression}
	if err := parsedLocation.Validate(); err != nil {
		readingFailure(w, err)
		return
	}
	claims, validate, err := d.Downloads.PublicationReceiptValidator(r.Context(), p, d.Identity.ID(), body.Receipt)
	if err != nil {
		readingFailure(w, downloadError(err))
		return
	}
	original := body.Receipt
	if body.ObservationReceipt != nil {
		original = *body.ObservationReceipt
	}
	if err = d.Downloads.VerifyPublicationObservation(r.Context(), claims, original, body.StartedAt, body.ObservedAt); err != nil {
		readingFailure(w, downloadError(err))
		return
	}
	expires, err := time.Parse(time.RFC3339Nano, claims.ExpiresAt)
	if err != nil || body.ObservedAt > expires.UnixMilli() {
		readingFailure(w, reading.ErrInvalid)
		return
	}
	f, err := d.readingService().Open(r.Context(), p, claims.ItemID, claims.Publication.SourceID, claims.Publication.Revision)
	if err != nil {
		readingFailure(w, err)
		return
	}
	defer f.Close()
	progress, saved, outcome, err := f.SaveOfflineProgress(*body.ExpectedRevision, parsedLocation, body.ObservedAt, body.StartedAt, validate)
	if err != nil {
		readingFailure(w, downloadError(err))
		return
	}
	if err = f.Validate(); err != nil {
		readingFailure(w, err)
		return
	}
	write(w, 200, ReadingOfflineProgressResponse{readingProgressProjection(progress), saved, ReadingOfflineOutcome(outcome)})
}
