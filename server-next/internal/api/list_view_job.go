package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
)

// listViewJobKind adds every title of a view to a playlist or a collection: "Add all 48,000
// to a playlist" (area 6 rules 39 to 42). Any profile may start it, for a list it may change,
// and it runs as that profile over what that profile may see.
const listViewJobKind = "add_view_to_list"

// ListViewJobParams are the parameters of an add_view_to_list job: the list. The view is the
// job's target.filter, the query string of GET /v1/items that shows it, its sort included:
// the titles are added in the view's own order. Its cursor and limit are ignored.
type ListViewJobParams struct {
	List string `json:"list"`
}

// listViewMax is the most entries a list is filled to by adding a whole view: area 6 rule 26's
// playlist of 10,000 entries, which opens, plays and reorders. Titles of the view past it are
// left out, and the job's note says how many.
//
// listViewPage is how many titles of the view one read takes, and one write adds.
var listViewMax = 10000 // a var only so a test can make it small

const listViewPage = 200

func (s *Server) listViewJobs() {
	// Distinct: every request is a job of its own, run in the order asked.
	s.jobs.Register(jobs.Kind{Name: listViewJobKind, Access: jobs.AnyProfile, Distinct: true,
		Check: func(ctx context.Context, req jobs.Request) error {
			q, list, err := listViewJob(req)
			if err != nil {
				return err
			}
			v := db.Viewer{UserID: req.UserID, ProfileID: req.ProfileID}
			l, err := s.db.List(ctx, v, list)
			if err != nil {
				return listError(err)
			}
			if !l.CanEdit {
				return listError(db.ErrNotAllowed)
			}
			q.Limit = 1
			_, err = s.db.Items(ctx, v, q)
			return refuse(err)
		},
		Run: s.runListViewJob})
}

// listViewJob reads an add_view_to_list request: the view and the list.
func listViewJob(req jobs.Request) (db.ItemQuery, string, error) {
	if req.Target.Filter == "" {
		return db.ItemQuery{}, "", ErrField("target.filter", "Send the view as the query of the list that shows it.")
	}
	values, err := url.ParseQuery(req.Target.Filter)
	if err != nil {
		return db.ItemQuery{}, "", ErrField("target.filter", "This is not a list query.")
	}
	q, err := itemQuery(values)
	if err != nil {
		return q, "", err
	}
	q.Cursor, q.At, q.Limit, q.WithTotal = "", 0, listViewPage, false
	var p ListViewJobParams
	if len(req.Params) > 0 && json.Unmarshal(req.Params, &p) != nil || p.List == "" {
		return q, "", ErrField("params.list", "Say which playlist or collection to add the view to.")
	}
	return q, p.List, nil
}

// runListViewJob walks the view a page at a time, in its own order, as the profile that
// asked, and adds each page at the end of the list. Each page's addition carries a key made
// of the job and the page, so a run taken up again after a restart adds no page twice: what
// was added stays added and the rest follows. A collection that has a title already does not
// take it again. Titles the profile may not see are not in its view and are never added.
func (s *Server) runListViewJob(ctx context.Context, work jobs.Work) error {
	q, list, err := listViewJob(work.Request)
	if err != nil {
		return &jobs.Failure{Shown: "This job's request can no longer be read."}
	}
	v := db.Viewer{UserID: work.UserID, ProfileID: work.ProfileID}
	if v.ProfileID == 0 {
		return &jobs.Failure{Shown: "This job has no profile to act as."}
	}
	l, err := s.db.List(ctx, v, list)
	if errors.Is(err, db.ErrNotFound) {
		return &jobs.Failure{Shown: "This list is not there any more."}
	}
	if err != nil {
		return err
	}
	room := listViewMax - l.Count
	q.WithTotal = true
	var seen, total, added, already int64
	for page := 0; ; page++ {
		items, err := s.db.Items(ctx, v, q)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err() // a cancel, or the server stopping: it carries on at the next start
			}
			return &jobs.Failure{Shown: fmt.Sprintf("The view could not be read after %d titles, so this stopped there. "+
				"What was added stays added; asking again adds the rest again.", seen), Detail: err.Error()}
		}
		if q.WithTotal {
			total, q.WithTotal = int64(items.Total), false
		}
		ids := make([]string, 0, len(items.Items))
		for _, it := range items.Items[:min(len(items.Items), max(room, 0))] {
			ids = append(ids, it.PublicID)
		}
		seen += int64(len(items.Items))
		if len(ids) > 0 {
			key := "list-view:" + work.Job.PublicID + ":" + strconv.Itoa(page)
			done, err := s.db.AddToList(ctx, v, list, 0, ids, 0, key, key, s.now())
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return &jobs.Failure{Shown: fmt.Sprintf("Adding stopped after %d titles: %s", added, listError(err).Error())}
			}
			added, already, room = added+int64(done.Added), already+int64(done.Already), room-done.Added
		}
		work.Progress(seen, max(total, seen))
		if items.NextCursor == "" || room <= 0 || ctx.Err() != nil {
			break
		}
		q.Cursor = items.NextCursor
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// The job ends done, and its note is for the person who asked, not only the owner as a
	// done job's note is: it names no title, only how many.
	note := listViewNote(added, already, max(total-added-already, 0))
	return errors.Join(jobs.DoneWith{Note: note}, &jobs.Failure{Shown: note})
}

// listViewNote says what adding a view did, for the screen to tell the person.
func listViewNote(added, already, leftOut int64) string {
	note := fmt.Sprintf("Added %d.", added)
	if already > 0 {
		note += fmt.Sprintf(" %d were in it already.", already)
	}
	if leftOut > 0 {
		note += fmt.Sprintf(" %d were left out: a list holds at most %d by adding a whole view.", leftOut, listViewMax)
	}
	return note
}
