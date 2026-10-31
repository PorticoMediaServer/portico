package httpapi

import (
	"errors"
	"log"
	"math"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/setupflow"
)

// SetupStateResponse is GET /v1/setup/state. Anyone may read status. Setup is
// present only for the caller holding the setup token of a Portico Account setup
// in progress, sent as `Authorization: Bearer <token>` like the setup claim routes.
type SetupStateResponse struct {
	// Status is ready, in_progress or complete.
	Status string `json:"status"`
	// RetryAfterSeconds is when an unapproved setup in progress elsewhere can be
	// started over, while one is.
	RetryAfterSeconds int          `json:"retryAfterSeconds,omitempty"`
	Setup             *SetupDetail `json:"setup,omitempty"`
}

// SetupDetail is the caller's own setup. Variant is its phase.
type SetupDetail struct {
	Variant   string `json:"variant"`
	RequestID string `json:"requestId"`
	Revision  int64  `json:"revision,string"`
	// ExpiresAt ends the approval window (started, awaiting approval) or the owner
	// admission window (installed).
	ExpiresAt  string          `json:"expiresAt,omitempty"`
	Operation  *SetupOperation `json:"operation,omitempty"`
	Cancelling bool            `json:"cancelling,omitempty"`
	Renewable  bool            `json:"renewable,omitempty"`
	// Delivery is the server applying the approval it was given (approved,
	// finishing, exhausted): since the approval, failed tries and the next one.
	Delivery *SetupDelivery `json:"delivery,omitempty"`
}

// SetupDelivery: Slow is two minutes or more since the approval.
type SetupDelivery struct {
	Since       string `json:"since,omitempty"`
	Attempts    int    `json:"attempts"`
	NextRetryAt string `json:"nextRetryAt,omitempty"`
	Slow        bool   `json:"slow"`
}

// SetupOperation names the setup's claim operation, as the claim routes expect it.
type SetupOperation struct {
	OperationID     string `json:"operationId"`
	LocalGeneration int64  `json:"localGeneration,string"`
	Revision        int64  `json:"revision,string"`
}

// JSONSchema tags each phase as its own alternative, so an app reading a phase it
// doesn't know gets an unknown variant, never a wrong guess (O-159).
func (SetupDetail) JSONSchema() map[string]any {
	type detail SetupDetail
	alternatives := make([]any, 0, len(setupflow.Phases))
	for _, phase := range setupflow.Phases {
		if phase == setupflow.Open || phase == setupflow.Completed {
			continue // never sent: no setup is in progress
		}
		shape, err := apikit.Schema(reflect.TypeFor[detail]())
		if err != nil {
			panic(err)
		}
		shape["properties"].(map[string]any)["variant"] = map[string]any{"type": "string", "const": string(phase)}
		alternatives = append(alternatives, shape)
	}
	return map[string]any{"oneOf": alternatives}
}

// JSONSchema keeps status an open string (O-159): a newer server's value must not
// stop an older app reading the rest. Today's values are ready, in_progress and complete.
func (SetupStateResponse) JSONSchema() map[string]any {
	type response SetupStateResponse
	shape, err := apikit.Schema(reflect.TypeFor[response]())
	if err != nil {
		panic(err)
	}
	props := shape["properties"].(map[string]any)
	props["status"].(map[string]any)["description"] = "Open: ready, in_progress or complete today. Read any other value as unknown."
	props["retryAfterSeconds"].(map[string]any)["minimum"] = 1
	return shape
}

// SetupAbandonRequest is Back from the caller's own unapproved Portico Account setup.
type SetupAbandonRequest struct {
	RequestID  string `json:"requestId"`
	Revision   int64  `json:"revision,string"`
	SetupToken string `json:"setupToken"`
}

// serveSetupAbandon is POST /v1/setup/abandon (setupflow S8): the server returns to the
// start of setup, retiring the unapproved claim here alone and stopping its wait;
// nothing is sent to Portico Services. After approval it answers setup_state_changed.
// A replay after success answers the open server.
func (d Dependencies) serveSetupAbandon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !d.setupRequestAllowed(r) {
		failure(w, identity.ErrUnauthorized)
		return
	}
	if !d.setupSourceAllowed(r) {
		policyErrors.Write(w, w.Header().Get("X-Request-Id"), errSetupSourceNotLocal)
		return
	}
	var q SetupAbandonRequest
	if e := decode(w, r, &q); e != nil {
		failure(w, e)
		return
	}
	state, err := d.Identity.AbandonSetup(r.Context(), q.RequestID, q.SetupToken, q.Revision, d.Networking.StopSetupWatch)
	if errors.Is(err, identity.ErrUnauthorized) {
		if limit := d.Identity.SetupLimit(r.Context(), d.setupPeerSubject(r), "abandon", 20); limit != nil {
			setupFailure(w, limit)
			return
		}
		failure(w, err)
		return
	}
	if err != nil {
		writeSetupError(w, err)
		return
	}
	write(w, 200, setupStateResponse(state, false, time.Now()))
}

func setupStateResponse(state setupflow.State, own bool, now time.Time) SetupStateResponse {
	out := SetupStateResponse{Status: string(state.Phase.Coarse())}
	live := state.Phase == setupflow.Started || state.Phase == setupflow.AwaitingApproval
	if live && !state.Cancelling && state.ExpiresAt.After(now) {
		out.RetryAfterSeconds = max(1, int(math.Ceil(state.ExpiresAt.Sub(now).Seconds())))
	}
	if !own {
		return out
	}
	detail := &SetupDetail{Variant: string(state.Phase), RequestID: state.RequestID, Revision: state.Revision, Cancelling: state.Cancelling, Renewable: state.Renewable}
	if !state.ExpiresAt.IsZero() {
		detail.ExpiresAt = state.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	if op := state.Operation; op != nil {
		detail.Operation = &SetupOperation{OperationID: op.ID, LocalGeneration: op.LocalGeneration, Revision: op.Revision}
	}
	if d := state.Delivery; d != nil {
		detail.Delivery = &SetupDelivery{Attempts: d.Attempts, Slow: d.Slow(now)}
		if !d.Since.IsZero() {
			detail.Delivery.Since = d.Since.UTC().Format(time.RFC3339Nano)
		}
		if !d.NextRetryAt.IsZero() {
			detail.Delivery.NextRetryAt = d.NextRetryAt.UTC().Format(time.RFC3339Nano)
		}
	}
	out.Setup = detail
	return out
}

// serveSetupState is GET /v1/setup/state. Without a token it answers the coarse
// status to anyone. With one it requires the server's own page (or an app) and a
// local setup source, like every other setup call. A refused token is
// counted against the caller's setup rate limit; reads with a good one write nothing.
func (d Dependencies) serveSetupState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	secret := ""
	if header := r.Header.Get("Authorization"); header != "" {
		secret = strings.TrimPrefix(header, "Bearer ")
		if secret == header || secret == "" || strings.ContainsAny(secret, " ,\r\n") {
			failure(w, identity.ErrUnauthorized)
			return
		}
		// As every setup write: the server's own page or an app, never another site,
		// even one cross-origin API access otherwise permits.
		site := r.Header.Get("Sec-Fetch-Site")
		origin := r.Header.Get("Origin")
		if site == "cross-site" || site == "same-site" || origin != "" && (!d.sameRequestOrigin(r, origin) || !d.browserSetupAuthorityAllowed(r)) {
			failure(w, identity.ErrUnauthorized)
			return
		}
		if !d.setupSourceAllowed(r) {
			policyErrors.Write(w, w.Header().Get("X-Request-Id"), errSetupSourceNotLocal)
			return
		}
	}
	now := time.Now()
	state, own, err := d.Identity.SetupState(r.Context(), secret, now, d.setupDeliveryLookup())
	switch {
	case errors.Is(err, identity.ErrUnauthorized):
		if limit := d.Identity.SetupLimit(r.Context(), d.setupPeerSubject(r), "state", 20); limit != nil {
			setupFailure(w, limit)
			return
		}
		failure(w, err)
		return
	case errors.Is(err, setupflow.ErrInconsistent):
		// Setup can neither continue nor start over: in progress, with nothing to offer.
		log.Printf("Setup state: %v", err)
		write(w, 200, SetupStateResponse{Status: string(setupflow.CoarseInProgress)})
		return
	case err != nil:
		failure(w, err)
		return
	}
	write(w, 200, setupStateResponse(state, own, now))
}

// setupDeliveryLookup is the networking tracker, when this server has one.
func (d Dependencies) setupDeliveryLookup() setupflow.DeliveryLookup {
	if d.Networking == nil {
		return nil
	}
	return d.Networking.SetupDelivery
}

// SetupRetryRequest is Try again for the caller's own exhausted setup delivery.
type SetupRetryRequest struct {
	RequestID  string `json:"requestId"`
	Revision   int64  `json:"revision,string"`
	SetupToken string `json:"setupToken"`
}

// serveSetupRetry is POST /v1/setup/retry (setupflow S15): when nothing is applying an
// approval any more, the server waits on Portico Services for it again. It answers the
// state as it then is; the setup moves to finishing once the wait is re-armed.
func (d Dependencies) serveSetupRetry(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !d.setupRequestAllowed(r) {
		failure(w, identity.ErrUnauthorized)
		return
	}
	if !d.setupSourceAllowed(r) {
		policyErrors.Write(w, w.Header().Get("X-Request-Id"), errSetupSourceNotLocal)
		return
	}
	var q SetupRetryRequest
	if e := decode(w, r, &q); e != nil {
		failure(w, e)
		return
	}
	now := time.Now()
	state, own, err := d.Identity.SetupState(r.Context(), q.SetupToken, now, d.setupDeliveryLookup())
	if err == nil && (!own || state.RequestID != q.RequestID) {
		err = identity.ErrUnauthorized
	}
	if errors.Is(err, identity.ErrUnauthorized) {
		if limit := d.Identity.SetupLimit(r.Context(), d.setupPeerSubject(r), "retry", 20); limit != nil {
			setupFailure(w, limit)
			return
		}
		failure(w, err)
		return
	}
	if err != nil {
		writeSetupError(w, err)
		return
	}
	if _, err = setupflow.Next(state.Phase, setupflow.EventRetry); err != nil || state.Revision != q.Revision || state.Operation == nil {
		writeSetupError(w, setupflow.ErrStateChanged)
		return
	}
	d.Networking.RetrySetupDelivery(state.Operation.ID)
	write(w, 200, setupStateResponse(state, own, now))
}

// serveSetupApproval is POST /v1/setup/approval (setupflow S6, S7), authorized like the
// setup claim routes by the setup's own token as its bearer. Only the caller's own
// setup, started, awaiting approval or installed, may ask; an expired one goes Back and
// starts again. Networking then prepares, signs and registers the claim under its own
// authority, guard and setup-start capture.
func (d Dependencies) serveSetupApproval(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !d.setupRequestAllowed(r) {
		failure(w, identity.ErrUnauthorized)
		return
	}
	if !d.setupSourceAllowed(r) {
		policyErrors.Write(w, w.Header().Get("X-Request-Id"), errSetupSourceNotLocal)
		return
	}
	secret := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	state, own, err := d.Identity.SetupState(r.Context(), secret, time.Now(), d.setupDeliveryLookup())
	if err == nil && !own {
		err = identity.ErrUnauthorized
	}
	if errors.Is(err, identity.ErrUnauthorized) {
		if limit := d.Identity.SetupLimit(r.Context(), d.setupPeerSubject(r), "approval", 20); limit != nil {
			setupFailure(w, limit)
			return
		}
		failure(w, err)
		return
	}
	if err != nil {
		writeSetupError(w, err)
		return
	}
	if _, err = setupflow.Next(state.Phase, setupflow.EventRequestApproval); err != nil {
		writeSetupError(w, setupflow.ErrStateChanged)
		return
	}
	if d.Networking == nil {
		failure(w, errors.New("claim unavailable"))
		return
	}
	d.Networking.ServeSetupApproval(w, r)
}
