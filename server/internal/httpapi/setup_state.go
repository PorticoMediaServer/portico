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

// JSONSchema bounds status to its three values.
func (SetupStateResponse) JSONSchema() map[string]any {
	type response SetupStateResponse
	shape, err := apikit.Schema(reflect.TypeFor[response]())
	if err != nil {
		panic(err)
	}
	props := shape["properties"].(map[string]any)
	props["status"].(map[string]any)["enum"] = []string{string(setupflow.CoarseReady), string(setupflow.CoarseInProgress), string(setupflow.CoarseComplete)}
	props["retryAfterSeconds"].(map[string]any)["minimum"] = 1
	return shape
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
	state, own, err := d.Identity.SetupState(r.Context(), secret, now, nil)
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
