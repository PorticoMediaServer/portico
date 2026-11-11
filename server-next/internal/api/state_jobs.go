package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
)

// stateJobKind sets the same state on every title in a view: "mark all 48,000 watched".
// Any profile may start it, and it runs as that profile over what that profile may see.
const stateJobKind = "set_state"

// StateJobParams are the parameters of a set_state job: the state to set on every title in
// the view, with the same meanings as on PUT /v1/items/{id}/state. Positions are not taken:
// a resume point belongs to one title.
//
// The view is the job's target.filter: the query string of GET /v1/items that shows it, such
// as "library=lib_…&kind=movie&genre=Drama&watched=false". Its sort, cursor and limit are
// ignored.
type StateJobParams struct {
	Watched       *bool `json:"watched,omitempty"`
	Favorite      *bool `json:"favorite,omitempty"`
	InList        *bool `json:"inList,omitempty"`
	NotInterested *bool `json:"notInterested,omitempty"`
	// Rating is half a star to five stars, sent as 1 to 10, or null to clear it.
	Rating NullableInt `json:"rating"`
}

// statePage is how many titles one write of a set_state job changes. Each page is its own
// short background write, so people's own taps go first.
const statePage = 200

func (s *Server) stateJobs() {
	s.jobs.Register(jobs.Kind{Name: stateJobKind, Access: jobs.AnyProfile,
		Check: func(ctx context.Context, req jobs.Request) error {
			q, _, err := stateJob(req)
			if err != nil {
				return err
			}
			q.Limit = 1
			_, err = s.db.Items(ctx, db.Viewer{UserID: req.UserID, ProfileID: req.ProfileID}, q)
			return refuse(err)
		},
		Run: s.runStateJob})
}

// stateJob reads a set_state request: the view and the edit.
func stateJob(req jobs.Request) (db.ItemQuery, db.StateEdit, error) {
	var edit db.StateEdit
	if req.Target.Filter == "" {
		return db.ItemQuery{}, edit, ErrField("target.filter", "Send the view as the query of the list that shows it.")
	}
	values, err := url.ParseQuery(req.Target.Filter)
	if err != nil {
		return db.ItemQuery{}, edit, ErrField("target.filter", "This is not a list query.")
	}
	q, err := itemQuery(values)
	if err != nil {
		return q, edit, err
	}
	// The sort is fixed to the list's first, so a page never moves under the job as it marks.
	q.Sort, q.Desc, q.Cursor, q.Limit, q.WithTotal = "", false, "", statePage, false
	var p StateJobParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return q, edit, ErrField("params", "These are not the parameters of this job.")
		}
	}
	edit = db.StateEdit{Watched: p.Watched, Favorite: p.Favorite, InList: p.InList, Hidden: p.NotInterested,
		Rating: p.Rating.Value, ClearRating: p.Rating.Set && p.Rating.Value == nil}
	if p.Watched == nil && p.Favorite == nil && p.InList == nil && p.NotInterested == nil && !p.Rating.Set {
		return q, edit, ErrField("params", "Say what to set: watched, favorite, inList, notInterested or rating.")
	}
	return q, edit, nil
}

// runStateJob walks the view a page at a time, as the profile that asked, and sets the state
// on each page in one background write. Every edit is stamped with when the job was asked
// for, so running it again after a restart changes nothing that was set since, by this job
// or by the person: the later edit wins, as everywhere. Titles the profile may not see are
// not in its view and are never touched.
func (s *Server) runStateJob(ctx context.Context, work jobs.Work) error {
	q, edit, err := stateJob(work.Request)
	if err != nil {
		return errors.New("This job's request can no longer be read.")
	}
	v := db.Viewer{UserID: work.UserID, ProfileID: work.ProfileID}
	if v.ProfileID == 0 {
		return errors.New("This job has no profile to act as.")
	}
	asked := time.UnixMilli(work.Job.CreatedAt)
	q.WithTotal = true
	var done, total, refused int64
	var reason string
	for {
		page, err := s.db.Items(ctx, v, q)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err() // a cancel or a stop
			}
			// The view has gone (its library was removed, or the profile may no longer see
			// it) or could not be read. The job must not say it finished.
			return fmt.Errorf("This view can no longer be read. %d titles were done and the rest were left as they were.", done)
		}
		if q.WithTotal {
			total, q.WithTotal = int64(page.Total), false
		}
		edits := make([]db.ItemEdit, len(page.Items))
		for n, it := range page.Items {
			edits[n] = db.ItemEdit{ItemID: it.PublicID, StateEdit: edit}
			edits[n].Ago = max(time.Since(asked), 0)
		}
		_, refusals, err := s.db.SetStates(ctx, db.Background, v, edits)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		for n, refusal := range refusals {
			if refusal != nil && !errors.Is(refusal, db.ErrNotFound) {
				refused++
				if reason == "" {
					reason = page.Items[n].Title + ": " + strings.TrimPrefix(refusal.Error(), db.ErrInvalid.Error()+": ")
				}
			}
		}
		done += int64(len(page.Items))
		work.Progress(done, max(total, done))
		if page.NextCursor == "" || ctx.Err() != nil {
			break
		}
		q.Cursor = page.NextCursor
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if refused > 0 {
		// The rest is done. The person is told how many were left as they were, and why.
		return fmt.Errorf("%d of %d titles were left as they were. %s", refused, done, reason)
	}
	return nil
}
