package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
)

const selectionFailureSummary = "Some titles could not be changed. The rest is done."

// JobListResult is an add_view_to_list job's committed result for currently visible source
// titles. Added counts list entries (including holder expansion); Failed counts titles.
type JobListResult struct {
	Added          int64 `json:"added"`
	AlreadyPresent int64 `json:"alreadyPresent"`
	Failed         int64 `json:"failed"`
}

// jobResult keeps a partial selection job's summary in step with the failure page the reader
// can open now. Removed or newly hidden titles do not leave an unexplained failed result.
// A job-wide fault keeps its own failed state and reason.
func (s *Server) jobResult(r *http.Request, job db.Job) (db.Job, error) {
	if job.Kind == listViewJobKind {
		result, err := s.db.ListJobResult(r.Context(), viewer(r), job)
		if err != nil {
			return job, err
		}
		job.ListResult = &result
		if job.State == db.JobDone {
			job.Error = listViewNote(result.Added, result.AlreadyPresent)
		}
	}
	if job.Kind != stateJobKind && job.Kind != listViewJobKind && job.Kind != jobEditItems && job.Kind != jobRefreshItems ||
		job.State != db.JobFailed || job.Error != selectionFailureSummary {
		return job, nil
	}
	failed := job.ListResult != nil && job.ListResult.Failed > 0
	if job.ListResult == nil {
		failures, err := s.db.JobItemFailures(r.Context(), viewer(r), job.ID, 0, 1)
		if err != nil {
			return job, err
		}
		failed = len(failures) > 0
	}
	if !failed {
		job.State, job.Error = db.JobDone, ""
	}
	return job, nil
}

// selectionResult reports a partial failure without names or an unfiltered failure count.
func (s *Server) selectionResult(ctx context.Context, jobID int64) error {
	failed, err := s.db.JobHasItemFailures(ctx, jobID)
	if err != nil {
		return err
	}
	if failed {
		return &jobs.Failure{Shown: selectionFailureSummary}
	}
	return nil
}

// JobItemFailure is one failed title the selected profile may still see. Reason codes
// form the closed list in API.md; messages contain no underlying error details.
type JobItemFailure struct {
	ItemID string     `json:"itemId"`
	Title  string     `json:"title"`
	Error  BatchError `json:"error"`
}

func (s *Server) readJobFailures(r *http.Request, _ None) (Page[JobItemFailure], error) {
	out := Page[JobItemFailure]{Items: []JobItemFailure{}}
	// Check the job first, even before parsing paging: another profile's job answers the
	// same not-found as GET /jobs/{id}, whatever query the caller sends.
	job, err := s.job(r)
	if err != nil {
		return out, err
	}
	q := r.URL.Query()
	var after int64
	if cursor := q.Get("cursor"); cursor != "" {
		after, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || after <= 0 {
			return out, ErrField("cursor", "This is not a cursor this list gave.")
		}
	}
	limit, err := limitParam(q)
	if err != nil {
		return out, err
	}
	if limit == 0 {
		limit = 50
	}
	limit = min(limit, 200)
	failures, err := s.db.JobItemFailures(r.Context(), viewer(r), job.ID, after, limit+1)
	if err != nil {
		return out, err
	}
	if len(failures) > limit {
		failures = failures[:limit]
		out.NextCursor = strconv.FormatInt(failures[len(failures)-1].ItemID, 10)
	}
	for _, f := range failures {
		out.Items = append(out.Items, JobItemFailure{ItemID: f.PublicID, Title: f.Title, Error: BatchError{Code: f.Code, Message: f.Message}})
	}
	return out, nil
}
