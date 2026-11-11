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

// statePage is how many titles of the view one read of a set_state job takes.
//
// stateWrite is about how many titles one write changes, counting each episode a show, season
// or album marks: a page of 200 shows is some 10,000 episodes, and one write of them held the
// writer for 300 to 500 ms at 100,000 titles. Each write is its own short background write, so
// people's taps and position saves never wait behind more than one of them.
//
// What it costs: at 100,000 titles on mini-1, a view of films took about 16 s and one of
// episodes 23 to 53 s, before the watched mark of the shows and seasons above them. All
// set_state jobs share one queue across profiles, so a second profile's job waits behind it.
// Every title written appends a row to the change feed, which keeps 200,000: a view larger
// than that makes every open app read everything again, once.
const (
	statePage  = 200
	stateWrite = 300
)

func (s *Server) stateJobs() {
	// Distinct: every request is a job of its own, run after the ones asked before it, so
	// "mark all watched" then "mark all unwatched" both happen, in that order.
	s.jobs.Register(jobs.Kind{Name: stateJobKind, Access: jobs.AnyProfile, Distinct: true,
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
	// The whole view, in the list's first sort, so a page never moves under the job as it
	// marks: where the list was scrolled to or opened at is not part of the view.
	q.Sort, q.Desc, q.Cursor, q.At, q.Limit, q.WithTotal = "", false, "", 0, statePage, false
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
	if r := p.Rating.Value; r != nil && (*r < 1 || *r > 10) {
		return q, edit, ErrField("params.rating", "A rating is half a star to five stars, sent as 1 to 10, or null for no rating.")
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
		return &jobs.Failure{Shown: "This job's request can no longer be read."}
	}
	v := db.Viewer{UserID: work.UserID, ProfileID: work.ProfileID}
	if v.ProfileID == 0 {
		return &jobs.Failure{Shown: "This job has no profile to act as."}
	}
	asked := time.UnixMilli(work.Job.CreatedAt)
	q.WithTotal = true
	var done, total, refused int64
	var reason, title string
	for {
		page, err := s.db.Items(ctx, v, q)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err() // a cancel, or the server stopping: it runs again at the next start
			}
			return &jobs.Failure{Shown: fmt.Sprintf("The view could not be read after %d titles, so this stopped there. "+
				"What was done stays done; asking again finishes it.", done), Detail: err.Error()}
		}
		if q.WithTotal {
			total, q.WithTotal = int64(page.Total), false
		}
		edit.Ago = max(time.Since(asked), 0)
		first := 0 // where this write's titles start in the page
		for _, write := range stateWrites(page.Items, edit) {
			_, refusals, err := s.db.SetStates(ctx, db.Background, v, write)
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
						reason, title = strings.TrimPrefix(refusal.Error(), db.ErrInvalid.Error()+": "), page.Items[first+n].Title
					}
				}
			}
			first += len(write)
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
		// The rest is done. The person is told how many were left as they were and why, in
		// words that name no title; the owner also reads the first title.
		return &jobs.Failure{Shown: fmt.Sprintf("%d of %d titles were left as they were: %s", refused, done, reason),
			Detail: "The first was " + title + "."}
	}
	return nil
}

// stateWrites splits a page of titles into the writes that set the edit on them, each of
// about stateWrite titles written: a show, season or album marked watched or unwatched counts
// its episodes or tracks. One holder larger than stateWrite is a write of its own.
func stateWrites(items []db.Item, edit db.StateEdit) [][]db.ItemEdit {
	var writes [][]db.ItemEdit
	var write []db.ItemEdit
	weight := 0
	for _, it := range items {
		n := 1
		if edit.Watched != nil && !it.Kind.Playable() {
			n = max(it.Leaves, 1)
		}
		if len(write) > 0 && weight+n > stateWrite {
			writes, write, weight = append(writes, write), nil, 0
		}
		write = append(write, db.ItemEdit{ItemID: it.PublicID, StateEdit: edit})
		weight += n
	}
	if len(write) > 0 {
		writes = append(writes, write)
	}
	return writes
}
