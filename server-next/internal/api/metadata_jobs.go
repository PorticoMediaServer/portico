package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
)

// Metadata work on a whole library or a selection runs as jobs, started with POST /v1/jobs:
//
//   - match_library (owner and admins, a library): look up every title no provider has been
//     asked about.
//   - refresh_library (owner and admins, a library): ask the provider again about every
//     matched title.
//   - refresh_items (owner and admins, up to 1,000 titles): Refresh metadata on a selection.
//   - edit_items (owner and admins, up to 1,000 titles of one kind): bulk edit, with BulkEdit
//     as the parameters.
//
// After every scan the server itself starts metadata_after_scan for the library: new titles
// are looked up, then what is due is refreshed.
const (
	jobMatchLibrary   = "match_library"
	jobRefreshLibrary = "refresh_library"
	jobRefreshItems   = "refresh_items"
	jobEditItems      = "edit_items"
	jobAfterScan      = "metadata_after_scan"
)

// afterScanRefreshes bounds how many due titles one scan's follow-up refreshes; the rest are
// still due at the next.
const afterScanRefreshes = 10_000

// BulkEdit is what edit_items changes on each title. A field left out changes nothing. A list
// (genres, studios, networks, countries, labels) gains the names in Add and loses those in
// Remove; it is never replaced. Lock, true when left out, locks what is edited so a refresh
// keeps it.
type BulkEdit struct {
	Year          *int                `json:"year,omitempty"`
	Edition       *string             `json:"edition,omitempty"`
	ContentRating *string             `json:"contentRating,omitempty"`
	Add           map[string][]string `json:"add,omitempty"`
	Remove        map[string][]string `json:"remove,omitempty"`
	Lock          *bool               `json:"lock,omitempty"`
}

// bulkLists are the lists bulk edit adds to and removes from, with the field each is locked by.
var bulkLists = map[string]string{db.FieldGenres: db.FieldGenres, db.FieldStudios: db.FieldStudios, db.FieldNetworks: db.FieldNetworks,
	db.FieldCountries: db.FieldCountries, "labels": ""}

func (s *Server) metadataJobs() {
	library := func(ctx context.Context, req jobs.Request) (db.Library, error) {
		if req.Target.Library == "" {
			return db.Library{}, fmt.Errorf("%w: this job works on a library", db.ErrInvalid)
		}
		v, err := s.db.MainViewer(ctx, req.UserID)
		if err != nil {
			return db.Library{}, err
		}
		return s.db.Library(ctx, v, req.Target.Library)
	}
	libraryCheck := func(ctx context.Context, req jobs.Request) error {
		_, err := library(ctx, req)
		return err
	}
	// A job that comes round again after its library was removed has nothing to do.
	onLibrary := func(do func(ctx context.Context, work jobs.Work, lib db.Library) error) func(context.Context, jobs.Work) error {
		return func(ctx context.Context, work jobs.Work) error {
			lib, err := library(ctx, work.Request)
			if errors.Is(err, db.ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			return providerSentence(do(ctx, work, lib))
		}
	}
	s.jobs.Register(jobs.Kind{Name: jobMatchLibrary, Access: jobs.Admins, Check: libraryCheck,
		Run: onLibrary(func(ctx context.Context, work jobs.Work, lib db.Library) error {
			_, err := s.metadata.MatchLibrary(ctx, lib.ID)
			return err
		})})
	s.jobs.Register(jobs.Kind{Name: jobRefreshLibrary, Access: jobs.Admins, Check: libraryCheck,
		Run: onLibrary(func(ctx context.Context, work jobs.Work, lib db.Library) error {
			// Titles refreshed since the job was asked for are done: a run again after a restart carries on.
			return s.metadata.RefreshAll(ctx, lib.ID, time.UnixMilli(work.Job.CreatedAt), func(done int64) { work.Progress(done, 0) })
		})})
	s.jobs.Register(jobs.Kind{Name: jobAfterScan, Access: jobs.Internal, Run: func(ctx context.Context, work jobs.Work) error {
		all, err := s.db.AllLibraries(ctx)
		if err != nil {
			return err
		}
		at := slices.IndexFunc(all, func(l db.Library) bool { return l.PublicID == work.Target.Library })
		if at < 0 {
			return nil // removed since
		}
		lib := all[at]
		if _, err := s.metadata.MatchLibrary(ctx, lib.ID); err != nil {
			return providerSentence(err)
		}
		_, err = s.metadata.RefreshLibrary(ctx, lib.ID, time.Now(), afterScanRefreshes)
		return providerSentence(err)
	}})
	// A selection: the asker's main profile must see every title, or the job is refused as
	// if one were not there.
	selection := func(ctx context.Context, req jobs.Request) ([]db.ItemKind, error) {
		if len(req.Target.Items) == 0 {
			return nil, fmt.Errorf("%w: this job works on a list of titles", db.ErrInvalid)
		}
		v, err := s.db.MainViewer(ctx, req.UserID)
		if err != nil {
			return nil, err
		}
		return s.db.SeesItems(ctx, v, req.Target.Items)
	}
	s.jobs.Register(jobs.Kind{Name: jobRefreshItems, Access: jobs.Admins,
		Check: func(ctx context.Context, req jobs.Request) error {
			_, err := selection(ctx, req)
			return err
		},
		Run: func(ctx context.Context, work jobs.Work) error {
			total := int64(len(work.Target.Items))
			err := s.metadata.RefreshTitles(ctx, work.Target.Items, time.UnixMilli(work.Job.CreatedAt), func(done int64) { work.Progress(done, total) })
			return providerSentence(err)
		}})
	s.jobs.Register(jobs.Kind{Name: jobEditItems, Access: jobs.Admins,
		Check: func(ctx context.Context, req jobs.Request) error {
			kinds, err := selection(ctx, req)
			if err != nil {
				return err
			}
			if slices.ContainsFunc(kinds, func(k db.ItemKind) bool { return k != kinds[0] }) {
				return fmt.Errorf("%w: titles edited together must all be one kind", db.ErrInvalid)
			}
			_, err = bulkEdit(req.Params)
			return err
		},
		Run: s.editItems})
}

// providerSentence turns a provider that did not answer into the sentence the job shows.
func providerSentence(err error) error {
	switch {
	case err == nil, errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, db.ErrNotFound):
		return err
	}
	return errors.New("The metadata provider did not answer. Titles not reached are as they were; try again later.")
}

func bulkEdit(raw json.RawMessage) (BulkEdit, error) {
	var b BulkEdit
	if len(raw) > 0 && json.Unmarshal(raw, &b) != nil {
		return b, fmt.Errorf("%w: params are not a bulk edit", db.ErrInvalid)
	}
	for _, lists := range []map[string][]string{b.Add, b.Remove} {
		for name := range lists {
			if _, known := bulkLists[name]; !known {
				return b, fmt.Errorf("%w: %s: not a list that can be edited together", db.ErrInvalid, name)
			}
		}
	}
	if b.Year != nil && (*b.Year < 0 || *b.Year > 9999) {
		return b, fmt.Errorf("%w: year: 0 to 9999", db.ErrInvalid)
	}
	return b, nil
}

// editItems is bulk edit: each title is its own change and its own History entry. Every title
// that can be saved is; the job ends by naming those that could not be, and why. Asking for the
// same edit again saves them, and changes nothing on the rest.
func (s *Server) editItems(ctx context.Context, work jobs.Work) error {
	b, err := bulkEdit(work.Params)
	if err != nil {
		return err
	}
	lock := b.Lock == nil || *b.Lock
	var failed []string
	total := int64(len(work.Target.Items))
	for n, id := range work.Target.Items {
		if err := ctx.Err(); err != nil {
			return err
		}
		title, err := s.editOne(ctx, id, b, lock)
		if err != nil {
			reason := "it is no longer there"
			if errors.Is(err, db.ErrInvalid) {
				reason = strings.TrimPrefix(err.Error(), db.ErrInvalid.Error()+": ")
			} else if !errors.Is(err, db.ErrNotFound) {
				return err
			}
			failed = append(failed, cmp.Or(title, id)+" ("+reason+")")
		}
		work.Progress(int64(n+1), total)
	}
	if len(failed) > 0 {
		shown := failed[:min(len(failed), 20)]
		more := ""
		if len(failed) > len(shown) {
			more = fmt.Sprintf(" and %d more", len(failed)-len(shown))
		}
		return fmt.Errorf("Saved %d of %d. Not saved: %s%s.", len(work.Target.Items)-len(failed), len(work.Target.Items), strings.Join(shown, "; "), more)
	}
	return nil
}

// editOne applies a bulk edit to one title and answers its title for the report. A title
// someone changed in the meantime is read again and edited on its new copy.
func (s *Server) editOne(ctx context.Context, id string, b BulkEdit, lock bool) (string, error) {
	for attempt := 0; ; attempt++ {
		doc, err := s.db.MetadataDocument(ctx, id)
		if err != nil {
			return "", err
		}
		title, _ := doc.Fields[db.FieldTitle].(string)
		locked := map[string]bool{}
		for _, f := range doc.Locked {
			locked[f] = true
		}
		edit := db.MetadataEdit{Year: b.Year, Edition: b.Edition, ContentRating: b.ContentRating}
		var edited []string
		for field, given := range map[string]bool{db.FieldYear: b.Year != nil, db.FieldEdition: b.Edition != nil,
			db.FieldContentRating: b.ContentRating != nil} {
			if given {
				edited = append(edited, field)
			}
		}
		for name, field := range bulkLists {
			if len(b.Add[name]) == 0 && len(b.Remove[name]) == 0 {
				continue
			}
			values := slices.DeleteFunc(slices.Clone(doc.Lists[name]), func(v string) bool {
				return slices.ContainsFunc(b.Remove[name], func(r string) bool { return strings.EqualFold(strings.TrimSpace(r), v) })
			})
			values = append(values, b.Add[name]...)
			switch name {
			case db.FieldGenres:
				edit.Genres = &values
			case db.FieldStudios:
				edit.Studios = &values
			case db.FieldNetworks:
				edit.Networks = &values
			case db.FieldCountries:
				edit.Countries = &values
			case "labels":
				edit.Labels = &values
			}
			if field != "" {
				edited = append(edited, field)
			}
		}
		if !lock {
			// "Lock edited fields" off: what this edit sets stays open to a refresh, and a
			// field a person had locked before stays locked.
			for _, field := range edited {
				if !locked[field] {
					edit.Unlock = append(edit.Unlock, field)
				}
			}
		}
		_, err = s.db.EditMetadata(ctx, id, doc.Revision, edit)
		if errors.Is(err, db.ErrStale) && attempt < 3 {
			continue
		}
		return title, err
	}
}
