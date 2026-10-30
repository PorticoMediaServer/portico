package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/webhooks"
)

// The owner's outbound webhooks. Managing them is administration: an owner's
// session may, and so may an owner's API key that was given the admin scope.

func webhookErrorDefinitions() []apierror.Definition {
	return []apierror.Definition{
		{Code: "webhook_limit", Status: 409, Message: "There are too many of these already. Remove one you no longer use.", Retry: apierror.Never},
	}
}

func webhookRouteError(err error) error {
	var fields *webhooks.ValidationError
	var stale *webhooks.RevisionError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &stale):
		return &apierror.Error{Code: "revision_mismatch", Current: stale.Current, Cause: err}
	case errors.As(err, &fields):
		out := &apierror.Error{Code: "invalid_request"}
		for _, name := range fields.Fields {
			out.Fields = append(out.Fields, apierror.Field{Path: name, Code: "invalid", Message: "Check this value."})
		}
		return out
	case errors.Is(err, webhooks.ErrCapacity):
		return &apierror.Error{Code: "webhook_limit"}
	case errors.Is(err, webhooks.ErrIdempotencyKeyReused):
		return &apierror.Error{Code: "idempotency_key_reused"}
	case errors.Is(err, webhooks.ErrCreateGone):
		return &apierror.Error{Code: "not_found"}
	case errors.Is(err, webhooks.ErrInvalid):
		return &apierror.Error{Code: "invalid_request"}
	case errors.Is(err, sql.ErrNoRows):
		return &apierror.Error{Code: "not_found"}
	case errors.Is(err, identity.ErrUnauthorized), errors.Is(err, identity.ErrForbidden), errors.Is(err, identity.ErrNotVisible):
		return foundationAuthError(err)
	default:
		return &apierror.Error{Code: "internal", Cause: err}
	}
}

func registerWebhookRoutes(registry *apikit.Registry, d Dependencies) error {
	// Every route proves the owner again inside the store's own transaction.
	// The actor key a create binds its optional operation identifier to: the owner
	// who asked, from the authenticated principal and never from the body.
	prepare := func(r *http.Request) (*webhooks.Store, webhooks.Authorize, string, error) {
		if d.Webhooks == nil {
			return nil, nil, "", &apierror.Error{Code: "administration_unavailable"}
		}
		p, err := d.foundationCaller(r)
		if err != nil {
			return nil, nil, "", foundationAuthError(err)
		}
		return d.Webhooks, func(ctx context.Context, tx *sql.Tx) error { return d.ownerAuthorityTx(ctx, tx, p) }, p.Authority + ":" + p.AccountID, nil
	}
	base := []string{"unauthorized", "not_permitted", "insufficient_scope", "invalid_request", "administration_unavailable", "internal"}
	found := append([]string{"not_found"}, base...)
	meta := func(id, method, path, summary string, status int, bodyLimit int64, query []string, errs []string) apikit.Metadata {
		var integers []apikit.QueryInteger
		if id == "list_webhook_deliveries" {
			integers = emptyLimitQuery(50, 100)
		}

		return apikit.Metadata{Audience: apikit.Integrations, Admission: laneAdmin, ID: id, Method: method, Path: path, Summary: summary, Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.PageSized, Status: status, BodyLimit: bodyLimit, Query: query, QueryIntegers: integers, Errors: errs}
	}
	err := apikit.Register(registry, apikit.Route[struct{}, webhooks.List]{Metadata: meta("list_webhooks", "GET", "/v1/admin/webhooks", "List webhooks and the events they can be sent", 200, 0, nil, base),
		Handler: func(ctx context.Context, r *http.Request, _ struct{}) (webhooks.List, error) {
			empty := webhooks.List{Items: []webhooks.Webhook{}, Events: []string{}}
			store, auth, _, err := prepare(r)
			if err != nil {
				return empty, err
			}
			out, err := store.List(ctx, auth)
			if err != nil {
				return empty, webhookRouteError(err)
			}
			return out, nil
		}})
	if err != nil {
		return err
	}
	err = apikit.Register(registry, apikit.Route[webhooks.Create, webhooks.Issued]{Metadata: meta("create_webhook", "POST", "/v1/admin/webhooks", "Add a webhook; its signing secret is shown once", 201, 8192, nil, append([]string{"webhook_limit", "body_too_large", "idempotency_key_reused", "not_found"}, base...)),
		Handler: func(ctx context.Context, r *http.Request, body webhooks.Create) (webhooks.Issued, error) {
			empty := webhooks.Issued{Webhook: webhooks.Webhook{Events: []string{}}}
			store, auth, actor, err := prepare(r)
			if err != nil {
				return empty, err
			}
			// Bound to this owner, so a retried create answers with its own webhook
			// and never another caller's.
			body.Actor = actor
			out, err := store.Create(ctx, auth, body)
			if err != nil {
				return empty, webhookRouteError(err)
			}
			return out, nil
		}})
	if err != nil {
		return err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, webhooks.Webhook]{Metadata: meta("get_webhook", "GET", "/v1/admin/webhooks/{id}", "Read one webhook", 200, 0, nil, found),
		Handler: func(ctx context.Context, r *http.Request, _ struct{}) (webhooks.Webhook, error) {
			empty := webhooks.Webhook{Events: []string{}}
			store, auth, _, err := prepare(r)
			if err != nil {
				return empty, err
			}
			out, err := store.Get(ctx, auth, r.PathValue("id"))
			if err != nil {
				return empty, webhookRouteError(err)
			}
			return out, nil
		}})
	if err != nil {
		return err
	}
	err = apikit.Register(registry, apikit.Route[webhooks.Change, webhooks.Issued]{Metadata: meta("update_webhook", "PATCH", "/v1/admin/webhooks/{id}", "Change a webhook, turn it on or off, or replace its secret; fenced by its revision", 200, 8192, nil, append([]string{"body_too_large", "revision_mismatch"}, found...)),
		Handler: func(ctx context.Context, r *http.Request, body webhooks.Change) (webhooks.Issued, error) {
			empty := webhooks.Issued{Webhook: webhooks.Webhook{Events: []string{}}}
			store, auth, _, err := prepare(r)
			if err != nil {
				return empty, err
			}
			out, err := store.Update(ctx, auth, r.PathValue("id"), body)
			if err != nil {
				return empty, webhookRouteError(err)
			}
			return out, nil
		}})
	if err != nil {
		return err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, struct{}]{Metadata: meta("delete_webhook", "DELETE", "/v1/admin/webhooks/{id}", "Remove a webhook with its queue and its log", 204, 0, nil, found),
		Handler: func(ctx context.Context, r *http.Request, _ struct{}) (struct{}, error) {
			store, auth, _, err := prepare(r)
			if err != nil {
				return struct{}{}, err
			}
			return struct{}{}, webhookRouteError(store.Delete(ctx, auth, r.PathValue("id")))
		}})
	if err != nil {
		return err
	}
	err = apikit.Register(registry, apikit.Route[struct{}, webhooks.DeliveryPage]{Metadata: meta("list_webhook_deliveries", "GET", "/v1/admin/webhooks/{id}/deliveries", "Read a webhook's delivery log, newest first", 200, 0, []string{"cursor", "limit"}, found),
		Handler: func(ctx context.Context, r *http.Request, _ struct{}) (webhooks.DeliveryPage, error) {
			empty := webhooks.DeliveryPage{Items: []webhooks.Delivery{}}
			store, auth, _, err := prepare(r)
			if err != nil {
				return empty, err
			}
			limit := 0
			if raw := r.URL.Query().Get("limit"); raw != "" {
				if limit, err = strconv.Atoi(raw); err != nil || limit < 1 {
					return empty, webhookRouteError(&webhooks.ValidationError{Fields: []string{"limit"}})
				}
			}
			out, err := store.Deliveries(ctx, auth, r.PathValue("id"), r.URL.Query().Get("cursor"), limit)
			if err != nil {
				return empty, webhookRouteError(err)
			}
			return out, nil
		}})
	if err != nil {
		return err
	}
	return apikit.Register(registry, apikit.Route[struct{}, webhooks.Delivery]{Metadata: meta("test_webhook", "POST", "/v1/admin/webhooks/{id}/test", "Send a test event to a webhook; the result appears in its delivery log", 202, 0, nil, append([]string{"webhook_limit"}, found...)),
		Handler: func(ctx context.Context, r *http.Request, _ struct{}) (webhooks.Delivery, error) {
			store, auth, _, err := prepare(r)
			if err != nil {
				return webhooks.Delivery{}, err
			}
			out, err := store.SendTest(ctx, auth, r.PathValue("id"))
			if err != nil {
				return webhooks.Delivery{}, webhookRouteError(err)
			}
			return out, nil
		}})
}
