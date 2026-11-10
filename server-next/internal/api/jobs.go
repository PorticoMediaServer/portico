package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
)

// JobStart asks for slow work to be done in the background. Send an Idempotency-Key so a
// repeat of the request does not start the work a second time.
type JobStart struct {
	// Kind is the work: "scan", and whatever else this server lists.
	Kind string `json:"kind"`
	// Target is what to work on: exactly one of its fields.
	Target JobTarget `json:"target"`
	// Params are the kind's own parameters.
	Params JSON `json:"params,omitempty"`
}

// JobTarget is a library, a saved filter, a view, or a list of items (at most 1,000), each
// by id.
type JobTarget struct {
	Library string   `json:"library,omitempty"`
	Filter  string   `json:"filter,omitempty"`
	View    string   `json:"view,omitempty"`
	Items   []string `json:"items,omitempty"`
}

func (s *Server) jobRoutes() {
	// The kind decides who may start it, so the route itself only needs someone signed in.
	// A key needs the library scope.
	Handle(s, "POST /v1/jobs", SignedIn, s.startJob).with(ScopeLibrary).Status = http.StatusAccepted
	Handle(s, "GET /v1/jobs/{id}", SignedIn, s.readJob)
	Handle(s, "DELETE /v1/jobs/{id}", SignedIn, s.cancelJob).with(ScopeLibrary)
}

func (s *Server) startJob(r *http.Request, req JobStart) (Job, error) {
	c := CallerFrom(r.Context())
	kind, ok := s.jobs.Kind(req.Kind)
	// A kind only the server starts is, to an app, a kind that is not there.
	if !ok || kind.Access == jobs.Internal {
		return Job{}, ErrField("kind", "This server has no job of that kind.")
	}
	switch {
	case !mayStart(kind, c):
		return Job{}, ErrNotPermitted()
	case kind.Access == jobs.AnyProfile && c.ProfileID == 0:
		return Job{}, ErrConflict("profile_selection_required", "Choose a profile to continue.")
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 128 {
		return Job{}, ErrInvalid("The Idempotency-Key is at most 128 characters.")
	}
	job, err := s.jobs.Start(r.Context(), req.Kind, jobs.Request{Target: jobs.Target(req.Target), Params: json.RawMessage(req.Params),
		UserID: c.UserID, ProfileID: c.ProfileID, IdempotencyKey: key})
	if err != nil {
		return Job{}, libraryError(err)
	}
	return *jobOut(job), nil
}

// mayStart reports whether the caller's role lets them start a kind of job.
func mayStart(kind jobs.Kind, c Caller) bool {
	switch kind.Access {
	case jobs.Owner:
		return c.Role == "owner"
	case jobs.Admins:
		return c.Role == "owner" || c.Role == "admin"
	}
	return kind.Access == jobs.AnyProfile
}

// job finds a job for the person who started it, and for the owner and admins. To anyone
// else it is not there.
func (s *Server) job(r *http.Request) (db.Job, error) {
	job, err := s.db.Job(r.Context(), r.PathValue("id"))
	if err != nil {
		return job, libraryError(err)
	}
	if c := CallerFrom(r.Context()); job.OwnerID != c.UserID && c.Role != "owner" && c.Role != "admin" {
		return job, ErrNotFound()
	}
	return job, nil
}

func (s *Server) readJob(r *http.Request, _ None) (Job, error) {
	job, err := s.job(r)
	if err != nil {
		return Job{}, err
	}
	return *jobOut(job), nil
}

// cancelJob stops a job that is waiting or running. What it has done so far stays done.
// Cancelling a job that has ended changes nothing.
func (s *Server) cancelJob(r *http.Request, _ None) (None, error) {
	job, err := s.job(r)
	if err != nil {
		return None{}, err
	}
	// Whoever started a job may stop it, and so may anyone who could have started it for
	// everybody (the owner's scan is not an admin's to stop). A kind with a route of its own
	// (removing a library) is not stopped from here.
	c := CallerFrom(r.Context())
	kind, known := s.jobs.Kind(job.Kind)
	if known && kind.Access == jobs.Internal || job.OwnerID != c.UserID && !(known && kind.Access != jobs.AnyProfile && mayStart(kind, c)) && c.Role != "owner" {
		return None{}, ErrNotPermitted()
	}
	if err := s.jobs.Cancel(r.Context(), job); err != nil && !errors.Is(err, db.ErrNotFound) {
		return None{}, err
	}
	return None{}, nil
}
