package api

import (
	"net/http"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
)

// Management pages keep their whole-job projection. Profile routes count only original
// outcomes the selected reader may still see, including actual children added to a list.
func (s *Server) profileJobOut(r *http.Request, job db.Job) (Job, error) {
	out := *jobOut(r, job)
	if job.Kind != stateJobKind && job.Kind != listViewJobKind {
		return out, nil
	}
	counts, err := s.db.VisibleJobCounts(r.Context(), viewer(r), job.ID)
	if err != nil {
		return Job{}, err
	}
	out.Error = jobs.ErrorText(job, false)
	if strings.HasPrefix(out.Error, "The view could not be read after ") {
		if job.Kind == stateJobKind {
			out.Error = "The view could not be read. What was done stays done; asking again finishes it."
		} else {
			out.Error = "The view could not be read. What was added stays added."
		}
	}

	if job.Kind == listViewJobKind && job.State == db.JobDone && strings.HasPrefix(out.Error, "Added ") {
		out.Error = listViewNote(counts.Added, counts.Already)
	}
	out.Progress = nil
	if job.State == db.JobRunning || counts.Done > 0 {
		out.Progress = &JobProgress{Done: counts.Done}
		// While work is in flight the unprocessed source titles are not known. Use the
		// existing indeterminate progress rather than expose a stale selection total.
		if job.State == db.JobDone || job.Error == selectionFailureSummary {
			out.Progress.Total = &counts.Done
		}
	}
	return out, nil
}
