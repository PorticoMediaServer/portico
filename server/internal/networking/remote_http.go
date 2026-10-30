package networking

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

func (h *ClaimHandler) registerRemote(mux HTTPRegistrar) {
	if h.remote == nil {
		return
	}
	for _, path := range []string{"GET /v1/networking/remote", "POST /v1/networking/remote/config", "POST /v1/networking/remote/check"} {
		mux.HandleFunc(path, h.remoteHTTP)
	}
}
func (h *ClaimHandler) remoteHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	guard, e := h.authorizeManagement(r)
	if e != nil || guard == nil {
		h.failure(w, r, ErrClaimOwnerRequired)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var out RemoteStatus
	e = h.runner.Do(ctx, func(ctx context.Context) error {
		ctx, e = guardedClaimRequest(ctx, h.store, guard)
		if e != nil {
			return e
		}
		if r.URL.Path == "/v1/networking/remote/config" {
			var in struct {
				AuthorityID string       `json:"authorityId"`
				Config      RemoteConfig `json:"config"`
			}
			if e = readClaimBody(w, r, &in); e != nil {
				return e
			}
			if e = h.remote.configure(ctx, in.AuthorityID, in.Config); e != nil {
				return e
			}
		}
		c, certificate, authority, state, key, e := h.remote.snapshot(ctx)
		clear(key)
		if e != nil {
			return e
		}
		var raw string
		if e = h.store.db.QueryRowContext(ctx, `SELECT status FROM networking_remote_state WHERE singleton=1`).Scan(&raw); e != nil {
			return e
		}
		if json.Unmarshal([]byte(raw), &out) != nil || out.AuthorityID != authority || out.Generation != state.Generation {
			out = RemoteStatus{AuthorityID: authority, Generation: state.Generation, State: "checking", Candidates: []RemoteCandidate{}, Mappings: []MappingSummary{}}
		}
		if out.Topology.LAN == nil {
			out.Topology.LAN = []string{}
		}
		if out.Topology.Public == nil {
			out.Topology.Public = []string{}
		}
		out.Config = c
		out.Warnings = remoteWarnings(c.PublicPort, h.remote.accessURLs(ctx))
		if certificate.State == "claim_required" && !c.Enabled {
			out.State = "unconfigured"
		}
		return checkClaimRequest(ctx)
	})
	if e != nil {
		h.failure(w, r, e)
		return
	}
	if r.Method == http.MethodPost {
		if r.URL.Path == "/v1/networking/remote/config" {
			h.remote.Invalidate()
		} else {
			h.remote.Wake()
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
