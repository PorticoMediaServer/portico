package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
)

// The owner's list of tasks: those waiting or running, and those that ended in the last 30
// days with their reason; a failed one can be run again. Cancel is DELETE /v1/jobs/{id}. A
// failed conversion is not a task: its only history is the log.

// AdminJob is one task as the owner's list shows it.
type AdminJob struct {
	ID          string       `json:"id"`
	Kind        string       `json:"kind"`
	State       string       `json:"state"`
	LibraryID   string       `json:"libraryId,omitempty"`
	LibraryName string       `json:"libraryName,omitempty"`
	Progress    *JobProgress `json:"progress,omitempty"`
	// Error is why it failed.
	Error      string     `json:"error,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	// CanRetry says Run again is offered: a failed task of a kind the owner starts from here.
	CanRetry bool `json:"canRetry"`
}

const (
	// jobsKept is how long a task that ended is listed before the tidy-up removes it.
	jobsKept = 30 * 24 * time.Hour
	// endedJobsPage is how many ended tasks one page lists.
	endedJobsPage = 100
)

func (s *Server) adminJobRoutes() {
	Handle(s, "GET /v1/admin/jobs", Owner, s.adminJobs)
	Handle(s, "POST /v1/admin/jobs/{id}/retry", Owner, s.retryJob).Status = http.StatusAccepted
	s.jobs.Tidy("ended tasks", 24*time.Hour, func(ctx context.Context) error {
		_, err := s.db.RemoveEndedJobs(ctx, s.now().Add(-jobsKept))
		return err
	})
}

// adminJobs lists the tasks waiting or running on the first page, then the ended ones, the
// latest end first, a page at a time.
func (s *Server) adminJobs(r *http.Request, _ None) (Page[AdminJob], error) {
	ctx := r.Context()
	out := Page[AdminJob]{Items: []AdminJob{}}
	var afterAt, afterID int64
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		at, id, ok := strings.Cut(cursor, ".")
		var err1, err2 error
		afterAt, err1 = strconv.ParseInt(at, 10, 64)
		afterID, err2 = strconv.ParseInt(id, 10, 64)
		if !ok || err1 != nil || err2 != nil || afterID <= 0 {
			return out, ErrField("cursor", "This is not a cursor this list gave.")
		}
	}
	libraries, err := s.db.AdminLibraries(ctx, "")
	if err != nil {
		return out, err
	}
	names := map[string]string{}
	for _, l := range libraries {
		names[l.PublicID] = l.Name
	}
	var list []db.Job
	if afterID == 0 {
		if list, err = s.db.UnfinishedJobs(ctx); err != nil {
			return out, err
		}
	}
	ended, err := s.db.EndedJobs(ctx, s.now().Add(-jobsKept), afterAt, afterID, endedJobsPage+1)
	if err != nil {
		return out, err
	}
	if len(ended) > endedJobsPage {
		ended = ended[:endedJobsPage]
		last := ended[len(ended)-1]
		out.NextCursor = fmt.Sprintf("%d.%d", last.FinishedAt, last.ID)
	}
	for _, j := range append(list, ended...) {
		out.Items = append(out.Items, s.adminJob(r, j, names))
	}
	return out, nil
}

func (s *Server) adminJob(r *http.Request, j db.Job, libraries map[string]string) AdminJob {
	plain := jobOut(r, j)
	out := AdminJob{ID: j.PublicID, Kind: j.Kind, State: j.State, Progress: plain.Progress, Error: plain.Error,
		CreatedAt: time.UnixMilli(j.CreatedAt).UTC(), StartedAt: timePtr(j.StartedAt), FinishedAt: timePtr(j.FinishedAt), CanRetry: s.mayRetry(j)}
	if name, ok := libraries[j.Target]; ok {
		out.LibraryID, out.LibraryName = j.Target, name
	}
	return out
}

// mayRetry says a task can be run again from the list: one that failed, of a kind the owner
// or an administrator starts, where asking again joins the one there is (not Distinct). A person's own task (marking a view watched) is asked for again
// by that person, and a kind with a route of its own (removing a library) from that route.
func (s *Server) mayRetry(j db.Job) bool {
	kind, ok := s.jobs.Kind(j.Kind)
	return j.State == db.JobFailed && ok && !kind.Distinct && (kind.Access == jobs.Owner || kind.Access == jobs.Admins)
}

// retryJob runs a failed task again with what it was first asked to do.
func (s *Server) retryJob(r *http.Request, _ None) (Job, error) {
	job, err := s.db.Job(r.Context(), r.PathValue("id"))
	if err != nil {
		return Job{}, libraryError(err)
	}
	if !s.mayRetry(job) {
		if job.State != db.JobFailed {
			return Job{}, ErrConflict("job_not_failed", "Only a task that failed can be run again.")
		}
		return Job{}, ErrConflict("job_not_retryable", "This task can't be run again from here. Ask for it again where it was started.")
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 128 {
		return Job{}, ErrInvalid("The Idempotency-Key is at most 128 characters.")
	}
	var req jobs.Request
	if err := json.Unmarshal([]byte(job.Payload), &req); err != nil {
		return Job{}, ErrConflict("job_not_retryable", "This task can't be run again from here. Ask for it again where it was started.")
	}
	req.UserID, req.IdempotencyKey = CallerFrom(r.Context()).UserID, key
	started, err := s.jobs.Start(r.Context(), job.Kind, req)
	if err != nil {
		return Job{}, libraryError(err)
	}
	return *jobOut(r, started), nil
}
