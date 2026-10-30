package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"reflect"
	"strings"
	"time"
)

type ConsoleReply[T any] struct {
	Scope ConsoleReplyScope `json:"scope"`
	Data  T                 `json:"data"`
}
type ConsoleReplyScope struct {
	ServerID    string `json:"serverId"`
	ViewerFence string `json:"viewerFence"`
}
type ConsoleExportOptions struct {
	IncludePaths bool `json:"includePaths"`
}
type ConsoleChannelLogoUpload struct {
	Base64Multipart bool `json:"base64Multipart"`
}
type ConsoleRegistryDocument struct {
	ChannelLogoUpload *ConsoleChannelLogoUpload `json:"channelLogoUpload,omitempty"`
	ExportOptions     *ConsoleExportOptions     `json:"exportOptions,omitempty"`
	Revision          string                    `json:"revision"`
	Fields            []operations.Field        `json:"fields"`
	ExternalScopes    map[string]string         `json:"externalScopes"`
}
type ConsoleSettingsChange operations.SettingsChange

// Settings are a complete document: omission must not silently clear a saved
// boolean, a cap or an override. Only pointer fields intentionally accept null.
func (c *ConsoleSettingsChange) UnmarshalJSON(raw []byte) error {
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil {
		return operations.ErrInvalid
	}
	for _, key := range []string{"expectedRevision", "idempotencyKey", "values"} {
		if _, ok := top[key]; !ok {
			return operations.ErrInvalid
		}
	}
	for key := range top {
		if key != "expectedRevision" && key != "idempotencyKey" && key != "values" {
			return operations.ErrInvalid
		}
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(top["values"], &values) != nil || values == nil {
		return operations.ErrInvalid
	}
	fields := reflect.TypeFor[operations.Settings]()
	known := map[string]bool{}
	for i := 0; i < fields.NumField(); i++ {
		field := fields.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		known[name] = true
		value, ok := values[name]
		if !ok && (name == "people.invitation_role" || name == "people.invitation_days" || name == "logDetailMinutes" || name == "segments.filmCreditsWindow" || name == "segments.creditsWindow" || name == "segments.introWindow" || name == "segments.shortestFilm" || name == "segments.shortestEpisode" || name == "segments.tvMode" || name == "segments.animeMode" || name == "segments.movieMode" || name == "segments.useChapterNames" || name == "segments.viewerConfidencePercent" || name == "segments.automaticConfidencePercent") {
			continue
		}
		if name == "segments.shortestEpisode" {
			var minutes int
			if json.Unmarshal(value, &minutes) != nil || minutes < 1 || minutes > 30 || minutes%1 != 0 {
				return operations.ErrInvalid
			}
		}
		if name == "segments.shortestFilm" {
			var minutes int
			if json.Unmarshal(value, &minutes) != nil || minutes < 5 || minutes > 60 || minutes%5 != 0 {
				return operations.ErrInvalid
			}
		}
		if name == "segments.introWindow" {
			var window string
			if json.Unmarshal(value, &window) != nil || (window != "usual" && window != "long") {
				return operations.ErrInvalid
			}
		}
		if name == "segments.creditsWindow" {
			var window string
			if json.Unmarshal(value, &window) != nil || (window != "usual" && window != "long") {
				return operations.ErrInvalid
			}
		}
		if name == "segments.filmCreditsWindow" {
			var window string
			if json.Unmarshal(value, &window) != nil || (window != "usual" && window != "long") {
				return operations.ErrInvalid
			}
		}
		if name == "segments.tvMode" || name == "segments.animeMode" || name == "segments.movieMode" {
			var mode string
			if json.Unmarshal(value, &mode) != nil || (mode != "server" && mode != "standard" && mode != "thorough" && mode != "off") {
				return operations.ErrInvalid
			}
		}
		if name == "segments.useChapterNames" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return operations.ErrInvalid
		}
		if name == "segments.viewerConfidencePercent" || name == "segments.automaticConfidencePercent" {
			var percent int
			if json.Unmarshal(value, &percent) != nil || name == "segments.viewerConfidencePercent" && percent != 30 && percent != 50 && percent != 70 || name == "segments.automaticConfidencePercent" && percent != 70 && percent != 85 && percent != 95 {
				return operations.ErrInvalid
			}
		}
		if !ok || field.Type.Kind() != reflect.Pointer && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return operations.ErrInvalid
		}
	}
	for key := range values {
		if !known[key] {
			return operations.ErrInvalid
		}
	}
	var decoded operations.SettingsChange
	reader := json.NewDecoder(bytes.NewReader(raw))
	reader.DisallowUnknownFields()
	if err := reader.Decode(&decoded); err != nil {
		return err
	}
	*c = ConsoleSettingsChange(decoded)
	return nil
}
func conversionSettingsError(err error) error {
	if mutation := userMutationError(err); mutation != nil {
		return mutation
	}

	if err == nil {
		return nil
	}
	var conflict *operations.ConflictError
	if errors.As(err, &conflict) {
		return &apierror.Error{Code: "revision_mismatch", Current: struct {
			Revision int64 `json:"revision"`
		}{conflict.CurrentRevision}}
	}
	var validation *operations.ValidationError
	if errors.As(err, &validation) {
		fields := []apierror.Field{}
		for _, path := range validation.Fields {
			fields = append(fields, apierror.Field{Path: path, Code: "invalid_value", Message: publicErrorMessage("invalid_request")})
		}
		return &apierror.Error{Code: "invalid_request", Fields: fields}
	}
	switch {
	case errors.Is(err, operations.ErrIdempotencyKeyReused):
		return &apierror.Error{Code: "idempotency_key_reused"}
	case errors.Is(err, operations.ErrInvalid):
		return &apierror.Error{Code: "invalid_request"}
	case errors.Is(err, operations.ErrCapacity):
		return &apierror.Error{Code: "rate_limited", RetryAfterSeconds: 2}
	case errors.Is(err, identity.ErrUnauthorized), errors.Is(err, identity.ErrForbidden), errors.Is(err, identity.ErrNotVisible):
		return foundationAuthError(err)
	}
	return &apierror.Error{Code: "internal", Cause: err}
}
func consoleTypedReply[T any](d Dependencies, p identity.Principal, data T) ConsoleReply[T] {
	return ConsoleReply[T]{ConsoleReplyScope{d.Identity.ID(), fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d", presentationIdentity(p), p.Epoch))))}, data}
}
func registerConversionSettings(registry *apikit.Registry, d Dependencies) error {
	begin := conversionSettingsAdmission(d)
	failures := conversionSettingsFailures()
	if err := apikit.Register(registry, apikit.Route[struct{}, ConsoleReply[ConsoleRegistryDocument]]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "get_console_registry", Method: "GET", Path: "/v1/console/registry", Summary: "Read the owner setting definitions", Access: apikit.Device, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.PageSized, Status: 200, Errors: failures}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (ConsoleReply[ConsoleRegistryDocument], error) {
		ctx, p, auth, done, err := begin(ctx, r, false)
		if err != nil {
			return ConsoleReply[ConsoleRegistryDocument]{}, err
		}
		defer done()
		fields := []operations.Field{}
		for _, field := range operations.Registry() {
			if field.Permission != "owner" || p.Role == "owner" {
				if field.Default == nil {
					field.Default = (*int)(nil)
				}
				fields = append(fields, field)
			}
		}
		if err := d.ConsoleCheck(ctx, auth); err != nil {
			return ConsoleReply[ConsoleRegistryDocument]{}, conversionSettingsError(err)
		}
		return consoleTypedReply(d, p, ConsoleRegistryDocument{Revision: operations.RegistryRevision, Fields: fields, ExternalScopes: map[string]string{"account": "existing account and security surface", "library": "existing library configuration surface", "installation": "local platform preferences; never authorization"}, ExportOptions: &ConsoleExportOptions{IncludePaths: true}, ChannelLogoUpload: &ConsoleChannelLogoUpload{Base64Multipart: true}}), nil
	}}); err != nil {
		return err
	}
	return nil
}

func registerConversionOwnerSettings(registry *apikit.Registry, d Dependencies) error {
	begin := conversionSettingsAdmission(d)
	failures := conversionSettingsFailures()

	if err := apikit.Register(registry, apikit.Route[struct{}, ConsoleReply[operations.SettingsDocument]]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "get_console_settings", Method: "GET", Path: "/v1/admin/console/settings", Summary: "Read requested and effective owner settings", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: failures}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (ConsoleReply[operations.SettingsDocument], error) {
		ctx, p, auth, done, err := begin(ctx, r, true)
		if err != nil {
			return ConsoleReply[operations.SettingsDocument]{}, err
		}
		defer done()
		// An explicit owner revalidation asks for a fresh tool check. Ordinary
		// settings reads serve the cached qualification; no new wire shape.
		for _, directive := range strings.Split(r.Header.Get("Cache-Control"), ",") {
			if strings.EqualFold(strings.TrimSpace(directive), "no-cache") {
				ctx = operations.WithFFmpegRecheck(ctx)
				break
			}
		}
		doc, err := d.Console.Settings(ctx, auth)
		if err != nil {
			return ConsoleReply[operations.SettingsDocument]{}, conversionSettingsError(err)
		}
		if err := d.ConsoleCheck(ctx, auth); err != nil {
			return ConsoleReply[operations.SettingsDocument]{}, conversionSettingsError(err)
		}
		return consoleTypedReply(d, p, doc), nil
	}}); err != nil {
		return err
	}
	return apikit.Register(registry, apikit.Route[ConsoleSettingsChange, ConsoleReply[operations.SettingsDocument]]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "change_console_settings", Method: "PATCH", Path: "/v1/admin/console/settings", Summary: "Save a complete owner settings document", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: failures}, Handler: func(ctx context.Context, r *http.Request, in ConsoleSettingsChange) (ConsoleReply[operations.SettingsDocument], error) {
		ctx, p, auth, done, err := begin(ctx, r, true)
		if err != nil {
			return ConsoleReply[operations.SettingsDocument]{}, err
		}
		defer done()
		doc, err := d.Console.ApplySettings(ctx, p, auth, operations.SettingsChange(in))
		if err != nil {
			return ConsoleReply[operations.SettingsDocument]{}, conversionSettingsError(err)
		}
		if !d.configurationOwnsLiveSettings {
			if err := d.applyLiveSettings(ctx); err != nil {
				return ConsoleReply[operations.SettingsDocument]{}, conversionSettingsError(err)
			}
		}
		if err := d.ConsoleCheck(ctx, auth); err != nil {
			return ConsoleReply[operations.SettingsDocument]{}, conversionSettingsError(err)
		}
		return consoleTypedReply(d, p, doc), nil
	}})
}

type conversionSettingsBegin func(context.Context, *http.Request, bool) (context.Context, identity.Principal, operations.Authorize, func(), error)

func conversionSettingsAdmission(d Dependencies) conversionSettingsBegin {
	slots := make(chan struct{}, 8)
	return func(ctx context.Context, r *http.Request, owner bool) (context.Context, identity.Principal, operations.Authorize, func(), error) {
		if d.Console == nil || d.Identity == nil {
			return nil, identity.Principal{}, nil, nil, &apierror.Error{Code: "internal"}
		}
		p, err := d.foundationCaller(r)
		if err != nil {
			return nil, p, nil, nil, foundationAuthError(err)
		}
		select {
		case slots <- struct{}{}:
		default:
			return nil, p, nil, nil, &apierror.Error{Code: "rate_limited", RetryAfterSeconds: 2}
		}
		bounded, cancel := context.WithTimeout(ctx, 7*time.Second)
		return bounded, p, d.consoleAuthority(p, owner), func() { cancel(); <-slots }, nil
	}
}
func conversionSettingsFailures() []string {
	return []string{"unauthorized", "not_permitted", "invalid_request", "revision_mismatch", "idempotency_key_reused", "rate_limited", "internal"}
}
