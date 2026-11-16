package api

import (
	"net/http"
	"strconv"
)

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
