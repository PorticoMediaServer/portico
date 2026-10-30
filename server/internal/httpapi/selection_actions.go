package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
	"github.com/PorticoMediaServer/portico/server/internal/selectionactions"
)

// NewSelectionActions composes the durable user-command worker. The process root
// owns Run/Close; constructing a router must never leak an unowned goroutine.
func NewSelectionActions(d Dependencies) *selectionactions.Service {
	authorize := func(ctx context.Context, tx *sql.Tx, p identity.Principal, action, library string, target *selectionactions.Target) error {
		return d.selectionActionAuthorize(ctx, tx, p, action, library, target, "")
	}
	apply := func(ctx context.Context, p identity.Principal, action string, options selectionactions.Options, target selectionactions.Target, file *selectionactions.File, checkpoint func(*sql.Tx, selectionactions.Effect) error) error {
		if action == "delete" {
			if d.Administration == nil {
				return administration.ErrUnavailable
			}
			err := d.Administration.ApplySelectionDelete(ctx, p, action, options, target, file, authorize, checkpoint)
			var refusal *administration.SelectionDeleteError
			if errors.As(err, &refusal) {
				return &selectionactions.TargetFailure{Code: refusal.Code, Cause: err}
			}
			if errors.Is(err, administration.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
				return &selectionactions.TargetFailure{Code: "target_not_found", Cause: err}
			}
			if errors.Is(err, administration.ErrDenied) {
				return &selectionactions.TargetFailure{Code: "not_permitted", Cause: err}
			}
			return err
		}
		if action == "download" {
			if d.Downloads == nil {
				return downloads.ErrPolicy
			}
			err := d.Downloads.ApplySelectionDownload(ctx, p, action, options, target, file, authorize, checkpoint)
			if errors.Is(err, downloads.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
				return &selectionactions.TargetFailure{Code: "target_not_found", Cause: err}
			}
			if errors.Is(err, downloads.ErrPolicy) {
				return &selectionactions.TargetFailure{Code: "not_permitted", Cause: err}
			}
			return err
		}
		return selectionactions.ErrInput
	}
	service := selectionactions.New(d.DB, nil, authorize, apply)
	if d.Downloads != nil {
		service.OnAccepted = d.Downloads.OpenSelectionActionTransferTx
		service.OnTerminal = d.Downloads.CloseSelectionActionTransferTx
	}
	return service
}

type selectionActionPreparationResult struct {
	Ready   *selectionactions.Preview
	Pending *selectionactions.Pending
}

func (r selectionActionPreparationResult) MarshalJSON() ([]byte, error) {
	if (r.Ready == nil) == (r.Pending == nil) {
		return nil, selectionactions.ErrInput
	}
	if r.Ready != nil {
		return json.Marshal(r.Ready)
	}
	return json.Marshal(r.Pending)
}
func (selectionActionPreparationResult) JSONSchema() map[string]any {
	return map[string]any{"oneOf": []any{selectionActionSchema(reflect.TypeFor[selectionactions.Preview]()), selectionActionSchema(reflect.TypeFor[selectionactions.Pending]())}}
}
func (selectionActionPreparationResult) JSONSchemaForStatus(status int) map[string]any {
	if status == 200 {
		return selectionActionSchema(reflect.TypeFor[selectionactions.Preview]())
	}
	if status == 202 {
		return selectionActionSchema(reflect.TypeFor[selectionactions.Pending]())
	}
	return nil
}
func selectionActionSchema(t reflect.Type) map[string]any {
	shape, err := apikit.Schema(t)
	if err != nil {
		panic(err)
	}
	shape["additionalProperties"] = false
	return shape
}
func (selectionActionInput) JSONSchema() map[string]any {
	query := selectionActionSchema(reflect.TypeFor[catalog.JobQuery]())
	required, _ := query["required"].([]string)
	query["required"] = append(required, "selectionBasis")
	id := map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]{1,160}$"}
	variants := []any{}
	for _, action := range []string{"delete", "download"} {
		options := map[string]any{"type": "object", "additionalProperties": false}
		if action == "delete" {
			options["required"] = []string{"deleteFiles"}
			options["properties"] = map[string]any{"deleteFiles": map[string]any{"type": "boolean"}}
		} else {
			options["required"] = []string{"deviceId", "quality"}
			options["properties"] = map[string]any{"deviceId": id, "quality": downloads.RequestQuality("").JSONSchema()}
		}
		variants = append(variants, map[string]any{"type": "object", "additionalProperties": false, "required": []string{"action", "selector", "expected", "options"}, "properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{action}}, "selector": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"query"}, "properties": map[string]any{"query": query}},
			"expected": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"catalogRevision"}, "properties": map[string]any{"catalogRevision": map[string]any{"type": "string", "pattern": "^browse_[a-f0-9]{64}$"}}}, "options": options,
		}})
	}
	return map[string]any{"oneOf": variants}
}

func registerSelectionActions(registry *apikit.Registry, d Dependencies) error {
	failures := []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "stale_continuation", "rate_limited", "idempotency_key_reused", "internal"}
	prep := "/v1/libraries/{id}/selection-actions/preparations"
	poll := prep + "/{preparationId}"
	headers := []apikit.ResponseHeader{{Status: 202, Name: "Cache-Control", Description: "Private volatile preparation.", Required: true}, {Status: 202, Name: "Retry-After", Description: "Minimum poll delay.", Required: true}, {Status: 202, Name: "Location", Description: "Private preparation resource.", Required: true}}
	if err := apikit.RegisterRaw(registry, apikit.RawRoute[selectionActionInput, selectionactions.Pending]{Metadata: apikit.Metadata{Audience: apikit.Clients, ID: "create_selection_action_preparation", Method: "POST", Path: prep, Summary: "Prepare an exact whole-view delete or download confirmation", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.SelectionAsync, Status: 202, BodyLimit: 131072, Errors: failures, ResponseHeaders: headers}, Serve: d.createSelectionActionPreparation}); err != nil {
		return err
	}
	if err := apikit.RegisterRaw(registry, apikit.RawRoute[noBody, selectionActionPreparationResult]{Metadata: apikit.Metadata{Audience: apikit.Clients, ID: "get_selection_action_preparation", Method: "GET", Path: poll, Summary: "Read an exact volatile action preview", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, Status: 200, SuccessStatuses: []int{202}, Errors: failures, ResponseHeaders: append(headers, apikit.ResponseHeader{Status: 200, Name: "Cache-Control", Description: "Private action preview.", Required: true})}, Serve: d.getSelectionActionPreparation}); err != nil {
		return err
	}
	if err := apikit.RegisterRaw(registry, apikit.RawRoute[noBody, noBody]{Metadata: apikit.Metadata{Audience: apikit.Clients, ID: "delete_selection_action_preparation", Method: "DELETE", Path: poll, Summary: "Release this caller's unconfirmed action preparation", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, Status: 204, NoResponseBody: true, Errors: failures}, Serve: d.deleteSelectionActionPreparation}); err != nil {
		return err
	}
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, ID: "create_selection_action", Method: "POST", Path: "/v1/libraries/{id}/selection-actions", Summary: "Confirm the exact preview and admit durable immutable target commands", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.SelectionAsync, Status: 202, BodyLimit: 131072, Errors: failures}, d.confirmSelectionAction)
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_selection_action", Method: "GET", Path: "/v1/selection-actions/{operationId}", Summary: "Read confirmed command progress", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, Status: 200, Errors: failures}, d.getSelectionAction)
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, ID: "cancel_selection_action", Method: "DELETE", Path: "/v1/selection-actions/{operationId}", Summary: "Stop further command effects while preserving completed outcomes", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, Status: 200, Errors: failures}, d.cancelSelectionAction)
	return nil
}

func actionError(err error) error {
	var validation *catalog.BrowseValidationError
	if errors.As(err, &validation) {
		return &apierror.Error{Code: "invalid_request", Cause: err}
	}
	switch {
	case errors.Is(err, identity.ErrNotVisible), errors.Is(err, downloads.ErrNotFound):
		return &apierror.Error{Code: "not_found", Cause: err}
	case errors.Is(err, selectionactions.ErrInput), errors.Is(err, catalog.ErrSelectionBatchUnsupported):
		return &apierror.Error{Code: "invalid_request", Cause: err}
	case errors.Is(err, selectionactions.ErrStale), errors.Is(err, readprep.ErrStale), errors.Is(err, catalog.ErrSelectionBatchStale), errors.Is(err, catalog.ErrStaleContinuation):
		return &apierror.Error{Code: "stale_continuation", Cause: err}
	case errors.Is(err, readprep.ErrCapacity), errors.Is(err, selectionactions.ErrCapacity), errors.Is(err, readprep.ErrClosed):
		return &apierror.Error{Code: "rate_limited", RetryAfterSeconds: 1, Cause: err}
	case errors.Is(err, selectionactions.ErrConflict), errors.Is(err, downloads.ErrRequestConflict):
		return &apierror.Error{Code: "idempotency_key_reused", Cause: err}
	case errors.Is(err, selectionactions.ErrNotFound):
		return &apierror.Error{Code: "not_found", Cause: err}
	case errors.Is(err, administration.ErrDenied), errors.Is(err, downloads.ErrPolicy), errors.Is(err, identity.ErrForbidden):
		return &apierror.Error{Code: "not_permitted", Cause: err}
	// Identity refusal values deliberately also match ErrUnauthorized. Resolve
	// concealed and policy refusals first, as the ordinary typed routes do.
	case errors.Is(err, identity.ErrUnauthorized):
		return &apierror.Error{Code: "unauthorized", Cause: err}
	}
	return err
}

// A missing library is concealed at the route scope boundary. Only the later
// bounded selection proof uses sql.ErrNoRows as a stale-continuation witness.
func (d Dependencies) selectionActionScope(r *http.Request) (identity.Principal, catalog.Viewer, error) {
	p, viewer, err := d.browseSelectionQueryScope(r)
	if errors.Is(err, sql.ErrNoRows) {
		err = identity.ErrNotVisible
	}
	return p, viewer, err
}

func actionFailure(w http.ResponseWriter, err error) {
	policyErrors.Write(w, w.Header().Get("X-Request-Id"), actionError(actionPreparationError(err)))
}
func actionPreparationPath(library, id string) string {
	return "/v1/libraries/" + url.PathEscape(library) + "/selection-actions/preparations/" + url.PathEscape(id)
}
func actionDecode(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 131072))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return selectionactions.ErrInput
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return selectionactions.ErrInput
	}
	return nil
}
func (d Dependencies) createSelectionActionPreparation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" {
		actionFailure(w, selectionactions.ErrInput)
		return
	}
	var input selectionActionInput
	if err := actionDecode(w, r, &input); err != nil {
		actionFailure(w, err)
		return
	}
	if err := input.validate(r.PathValue("id")); err != nil {
		actionFailure(w, err)
		return
	}
	if err := actionRequestScope(r, input.Action); err != nil {
		actionFailure(w, err)
		return
	}
	p, viewer, err := d.selectionActionScope(r)
	if err != nil {
		actionFailure(w, err)
		return
	}
	if err = d.actionExposure(r.Context(), p, viewer, input); err != nil {
		actionFailure(w, err)
		return
	}
	out, err := d.submitSelectionActionPreparation(r.Context(), p, viewer, input)
	if err != nil {
		actionFailure(w, err)
		return
	}
	if err = d.actionExposure(r.Context(), p, viewer, input); err != nil {
		actionFailure(w, err)
		return
	}
	w.Header().Set("Retry-After", "1")
	w.Header().Set("Location", actionPreparationPath(r.PathValue("id"), out.PreparationID))
	write(w, 202, out)
}

func (d Dependencies) pollSelectionAction(r *http.Request) (identity.Principal, catalog.Viewer, readprep.Result, error) {
	p, viewer, err := d.selectionActionScope(r)
	if err != nil {
		return p, viewer, readprep.Result{}, err
	}
	access, err := d.browseSelectionAccess(p, r.PathValue("id"), viewer)
	if err != nil {
		return p, viewer, readprep.Result{}, err
	}
	store := d.Catalog.ReadPreparations()
	if store == nil {
		return p, viewer, readprep.Result{}, readprep.ErrStale
	}
	result, err := store.Poll(r.Context(), access.Caller, readprep.Handle(r.PathValue("preparationId")))
	if err != nil {
		result.Release()
		return p, viewer, readprep.Result{}, err
	}
	if result.Key.Namespace != "selection-action-v1" {
		result.Release()
		return p, viewer, readprep.Result{}, readprep.ErrStale
	}
	// Recheck the action and full basis after Poll even for pending responses.
	// The canonical private key is bounded intent, never a persisted selector.
	if err = result.ResizeDelivery(int64(8*len(result.Key.Canonical) + 65536)); err != nil {
		result.Release()
		return p, viewer, readprep.Result{}, err
	}
	var spec []json.RawMessage
	var input selectionActionInput
	if err = json.Unmarshal([]byte(result.Key.Canonical), &spec); err != nil || len(spec) != 3 {
		result.Release()
		return p, viewer, readprep.Result{}, readprep.ErrStale
	}
	if err = json.Unmarshal(spec[0], &input); err != nil {
		result.Release()
		return p, viewer, readprep.Result{}, readprep.ErrStale
	}
	if err = input.validate(r.PathValue("id")); err != nil {
		result.Release()
		return p, viewer, readprep.Result{}, err
	}
	if err = actionRequestScope(r, input.Action); err != nil {
		result.Release()
		return p, viewer, readprep.Result{}, err
	}
	if err = d.actionExposure(r.Context(), p, viewer, input); err != nil {
		result.Release()
		return p, viewer, readprep.Result{}, err
	}
	return p, viewer, result, nil
}
func (d Dependencies) getSelectionActionPreparation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" {
		actionFailure(w, selectionactions.ErrInput)
		return
	}
	p, viewer, result, err := d.pollSelectionAction(r)
	if err != nil {
		actionFailure(w, err)
		return
	}
	defer result.Release()
	if result.State == readprep.Preparing {
		if err = result.ResizeDelivery(4096); err != nil {
			actionFailure(w, err)
			return
		}
		pending := selectionactions.Pending{Kind: "selection-action-preparing", PreparationID: r.PathValue("preparationId"), RetryAfterMs: 1000}
		w.Header().Set("Retry-After", "1")
		w.Header().Set("Location", actionPreparationPath(r.PathValue("id"), pending.PreparationID))
		write(w, 202, pending)
		return
	}
	if err = result.ResizeDelivery(int64(8*len(result.Payload) + 65536)); err != nil {
		actionFailure(w, err)
		return
	}
	var intent selectionActionIntent
	if err = json.Unmarshal(result.Payload, &intent); err != nil {
		actionFailure(w, err)
		return
	}
	if err = intent.Input.validate(r.PathValue("id")); err != nil {
		actionFailure(w, err)
		return
	}
	if err = d.actionExposure(r.Context(), p, viewer, intent.Input); err != nil {
		actionFailure(w, err)
		return
	}
	expires, err := time.Parse(time.RFC3339Nano, intent.Preview.ExpiresAt)
	if err != nil || !time.Now().Before(expires) {
		actionFailure(w, readprep.ErrStale)
		return
	}
	intent.Preview.PreparationID = r.PathValue("preparationId")
	write(w, 200, intent.Preview)
}
func (d Dependencies) deleteSelectionActionPreparation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" {
		actionFailure(w, selectionactions.ErrInput)
		return
	}
	p, viewer, err := d.selectionActionScope(r)
	if err != nil {
		actionFailure(w, err)
		return
	}
	access, err := d.browseSelectionAccess(p, r.PathValue("id"), viewer)
	if err != nil {
		actionFailure(w, err)
		return
	}
	if store := d.Catalog.ReadPreparations(); store != nil {
		err = store.CancelNamespace(r.Context(), access.Caller, readprep.Handle(r.PathValue("preparationId")), "selection-action-v1")
	}
	if err != nil {
		actionFailure(w, err)
		return
	}
	w.WriteHeader(204)
}

func (d Dependencies) confirmSelectionAction(ctx context.Context, r *http.Request, in selectionactions.Confirm) (selectionactions.Job, error) {
	if r.URL.RawQuery != "" || !actionWireID(in.OperationID) || in.PreparationID == "" {
		return selectionactions.Job{}, actionError(selectionactions.ErrInput)
	}
	p, viewer, err := d.selectionActionScope(r)
	if err != nil {
		return selectionactions.Job{}, err
	}
	if d.SelectionActions == nil {
		return selectionactions.Job{}, actionError(selectionactions.ErrCapacity)
	}
	raw, _ := json.Marshal([]any{r.PathValue("id"), in})
	hash := identity.Digest(string(raw))
	binding, err := d.selectionActionBinding(ctx, p)
	if err != nil {
		return selectionactions.Job{}, err
	}
	if replay, found, err := d.SelectionActions.Replay(ctx, binding, in.OperationID, hash); err != nil {
		return selectionactions.Job{}, actionError(err)
	} else if found {
		if err := actionRequestScope(r, replay.Action); err != nil {
			return selectionactions.Job{}, err
		}
		return replay, nil
	}
	access, err := d.browseSelectionAccess(p, r.PathValue("id"), viewer)
	if err != nil {
		return selectionactions.Job{}, err
	}
	store := d.Catalog.ReadPreparations()
	if store == nil {
		return selectionactions.Job{}, actionError(readprep.ErrStale)
	}
	result, err := store.Poll(ctx, access.Caller, readprep.Handle(in.PreparationID))
	if err != nil {
		result.Release()
		return selectionactions.Job{}, actionError(err)
	}
	transferred := false
	defer func() {
		if !transferred {
			result.Release()
		}
	}()
	if result.State != readprep.Ready || result.Key.Namespace != "selection-action-v1" {
		return selectionactions.Job{}, actionError(readprep.ErrStale)
	}
	if err = result.ResizeDelivery(int64(8*len(result.Payload) + 65536)); err != nil {
		return selectionactions.Job{}, actionError(err)
	}
	var intent selectionActionIntent
	if err = json.Unmarshal(result.Payload, &intent); err != nil {
		return selectionactions.Job{}, err
	}
	if err = intent.Input.validate(r.PathValue("id")); err != nil {
		return selectionactions.Job{}, actionError(err)
	}
	expires, err := time.Parse(time.RFC3339Nano, intent.Preview.ExpiresAt)
	if err != nil || !time.Now().Before(expires) || intent.Preview.Total <= 0 || in.Confirmation != intent.Preview.Confirmation {
		return selectionactions.Job{}, actionError(selectionactions.ErrStale)
	}
	if err = actionRequestScope(r, intent.Input.Action); err != nil {
		return selectionactions.Job{}, err
	}
	if err = d.actionExposure(ctx, p, viewer, intent.Input); err != nil {
		return selectionactions.Job{}, actionError(err)
	}
	accepted := selectionactions.Acceptance{Principal: p, Binding: binding, Account: p.AccountID, Library: r.PathValue("id"), Action: intent.Input.Action, RequestHash: hash, OperationID: in.OperationID, Options: intent.Input.Options, Total: intent.Preview.Total, Digest: intent.Digest,
		Factory: func(inner context.Context) (selectionactions.Source, error) {
			source, err := d.newSelectionActionSource(inner, p, intent.Input, viewer)
			if err != nil {
				return nil, err
			}
			source.(*selectionActionSource).captureLease = &result
			return source, nil
		},
		Check: func(inner context.Context, tx *sql.Tx) error {
			if err := d.selectionActionAuthorize(inner, tx, p, intent.Input.Action, intent.Input.Selector.Query.LibraryID, nil, intent.Input.Options.DeviceID); err != nil {
				return err
			}
			return actionPreparationError(d.Catalog.WithContext(inner).CheckBrowseSelectionBasisTx(inner, tx, viewer, intent.Input.Selector.Query.LibraryID, intent.Input.Selector.Query.SelectionBasis))
		}, Reserve: func(bytes int64) error { return result.ResizeDelivery(bytes + int64(8*len(result.Payload)+65536)) }, Release: result.Release,
	}
	out, err := d.SelectionActions.Accept(ctx, accepted)
	if err != nil {
		return selectionactions.Job{}, actionError(err)
	}
	transferred = true
	return out, nil
}
func (d Dependencies) getSelectionAction(ctx context.Context, r *http.Request, _ noBody) (selectionactions.Job, error) {
	if r.URL.RawQuery != "" {
		return selectionactions.Job{}, actionError(selectionactions.ErrInput)
	}
	p, err := d.strictPrincipalFor(r)
	if err != nil {
		return selectionactions.Job{}, err
	}
	if d.SelectionActions == nil {
		return selectionactions.Job{}, actionError(selectionactions.ErrNotFound)
	}
	binding, err := d.selectionActionBinding(ctx, p)
	if err != nil {
		return selectionactions.Job{}, err
	}
	out, err := d.SelectionActions.Lookup(ctx, binding, r.PathValue("operationId"))
	return out, actionError(err)
}
func (d Dependencies) cancelSelectionAction(ctx context.Context, r *http.Request, _ noBody) (selectionactions.Job, error) {
	if r.URL.RawQuery != "" {
		return selectionactions.Job{}, actionError(selectionactions.ErrInput)
	}
	p, err := d.strictPrincipalFor(r)
	if err != nil {
		return selectionactions.Job{}, err
	}
	if d.SelectionActions == nil {
		return selectionactions.Job{}, actionError(selectionactions.ErrNotFound)
	}
	binding, err := d.selectionActionBinding(ctx, p)
	if err != nil {
		return selectionactions.Job{}, err
	}
	out, err := d.SelectionActions.Cancel(ctx, binding, r.PathValue("operationId"))
	return out, actionError(err)
}

func actionRequestScope(r *http.Request, action string) error {
	if action == "delete" {
		return registryScopeGate(r, apikit.ScopeLibrary)
	}
	return registryScopeGate(r, apikit.ScopePlayback)
}
