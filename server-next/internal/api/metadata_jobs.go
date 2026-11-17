package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// Metadata work on a whole library or a selection runs as jobs, started with POST /v1/jobs:
//
//   - match_library (owner and admins, a library): look up every title no provider has been
//     asked about.
//   - refresh_library (owner and admins, a library): ask the provider again about every
//     matched title.
//   - refresh_items (owner and admins, up to 1,000 titles or a view): Refresh metadata on a selection.
//   - edit_items (owner and admins, up to 1,000 titles or a view of one kind): bulk edit, with BulkEdit
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
	// fetch_lyrics (owner and admins, a music library): Fetch missing lyrics, asking LRCLIB
	// about every song without any, in any library, a local-only one included.
	jobFetchLyrics = "fetch_lyrics"
)

// afterScanParams are what a scan tells the lookup after it: when it started, so only the
// songs it added are asked about.
type afterScanParams struct {
	AddedSince int64 `json:"addedSince,omitempty"`
}

// afterScanRefreshes bounds how many due titles one scan's follow-up refreshes; the rest are
// still due at the next.
const afterScanRefreshes = 10_000

// BulkEdit is what edit_items changes on each title. A field left out changes nothing. A list
// (genres, studios, networks, countries, labels) gains the names in Add and loses those in
// Remove; it is never replaced. Lock, true when left out, locks what is edited so a refresh
// keeps it.
type BulkEdit struct {
	Year             *int                `json:"year,omitempty"`
	MetadataLanguage *string             `json:"metadataLanguage,omitempty"`
	Edition          *string             `json:"edition,omitempty"`
	ContentRating    *string             `json:"contentRating,omitempty"`
	Add              map[string][]string `json:"add,omitempty"`
	Remove           map[string][]string `json:"remove,omitempty"`
	Lock             *bool               `json:"lock,omitempty"`
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
			err = do(ctx, work, lib)
			s.fetchPicturesLater(ctx, lib.PublicID)
			return providerSentence(err)
		}
	}
	s.jobs.Register(jobs.Kind{Name: jobMatchLibrary, Access: jobs.Admins, Check: libraryCheck,
		Run: onLibrary(func(ctx context.Context, work jobs.Work, lib db.Library) error {
			return matched(s.metadata.MatchLibrary(ctx, lib.ID))
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
		looked := s.lookUpAndRefresh(ctx, all[at].ID, time.Now())
		s.fetchPicturesLater(ctx, all[at].PublicID)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Lyrics for the songs this scan added, where the owner chose "Files, then LRCLIB" (never
		// in a library set to local metadata only). The rest of the library is asked about only
		// when a person starts Fetch missing lyrics. Asked even when the lookup above failed:
		// these songs are never asked about again by themselves.
		var params afterScanParams
		if len(work.Params) > 0 {
			json.Unmarshal(work.Params, &params)
		}
		if params.AddedSince > 0 {
			if _, err := s.metadata.FetchNewLyrics(ctx, all[at].ID, time.UnixMilli(params.AddedSince)); err != nil && looked == nil {
				looked = providerSentence(err)
			}
		}
		return looked
	}})
	s.jobs.Register(jobs.Kind{Name: jobFetchLyrics, Access: jobs.Admins, Check: libraryCheck,
		Run: onLibrary(func(ctx context.Context, work jobs.Work, lib db.Library) error {
			_, err := s.metadata.FetchMissingLyrics(ctx, lib.ID, true)
			return err
		})})
	s.jobs.Register(jobs.Kind{Name: jobRefreshItems, Access: jobs.Admins,
		Check: func(ctx context.Context, req jobs.Request) error {
			_, err := s.metadataSelection(ctx, req)
			return err
		}, Run: s.refreshItems})
	s.jobs.Register(jobs.Kind{Name: jobEditItems, Access: jobs.Admins, Distinct: true,
		Check: func(ctx context.Context, req jobs.Request) error {
			kinds, err := s.metadataSelection(ctx, req)
			if err != nil {
				return err
			}
			if len(kinds) != 1 {
				return fmt.Errorf("%w: titles edited together must all be one kind", db.ErrInvalid)
			}
			_, err = bulkEdit(req.Params)
			return err
		}, Run: s.editItems})
}

// lookUpAndRefresh is the after-scan run for a library: its new titles are looked up, then the
// titles due at now are refreshed. A lookup that stops, because the provider is down or
// refusing, does not keep the library's titles from being refreshed.
func (s *Server) lookUpAndRefresh(ctx context.Context, libraryID int64, now time.Time) error {
	stats, lookup := s.metadata.MatchLibrary(ctx, libraryID)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if _, err := s.metadata.RefreshLibrary(ctx, libraryID, now, afterScanRefreshes); err != nil {
		return providerSentence(err)
	}
	return matched(stats, lookup)
}

// matched is how a run of MatchLibrary ends a job: done, saying how many titles the provider
// failed on (each is asked again later by itself), or failed when the provider was down.
func matched(stats metadata.MatchStats, err error) error {
	if err != nil {
		return providerSentence(err)
	}
	if stats.Failed > 0 {
		return jobs.DoneWith{Note: fmt.Sprintf("The metadata provider could not be asked about %d titles this time. They are asked again later by themselves.", stats.Failed)}
	}
	return nil
}

// providerSentence turns a provider that did not answer into the sentence the job shows.
func providerSentence(err error) error {
	switch {
	case errors.Is(err, scan.ErrMetadataUnreadable):
		return errors.New("A title's metadata could not be read from its files. Titles not reached are as they were; try again later.")
	case err == nil, errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, db.ErrNotFound):
		return err
	}
	return errors.New("The metadata provider did not answer. Titles not reached are as they were; try again later.")
}

// Metadata job summaries are safe for every reader; provider payloads and storage errors
// belong in the server log, never in a title's refusal or the job's public reason.
func (s *Server) metadataJobError(ctx context.Context, jobID string, err error) error {
	if err == nil || ctx.Err() != nil || errors.As(err, new(*jobs.Failure)) {
		return err
	}
	if errors.Is(err, metadata.ErrProviderDown) {
		return &jobs.Failure{Shown: "The metadata provider did not answer. Titles not reached are as they were; try again later."}
	}
	if errors.Is(err, db.ErrNotFound) {
		return &jobs.Failure{Shown: "This selection is no longer available. What was saved stays saved."}
	}
	s.log.Error("metadata job stopped", "job", jobID, "error", err)
	return &jobs.Failure{Shown: "This metadata job stopped because of a server problem. What was saved stays saved; try again."}
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

// editItems saves every title it can and keeps each refusal for the failure page.
func (s *Server) editItems(ctx context.Context, work jobs.Work) (result error) {
	defer func() { result = s.metadataJobError(ctx, work.Job.PublicID, result) }()
	b, err := bulkEdit(work.Params)
	if err != nil {
		return err
	}
	lock := b.Lock == nil || *b.Lock
	var done, total int64
	cursor := ""
	for {
		ids, next, count, err := s.metadataPage(ctx, work.Request, cursor)
		if err != nil {
			return err
		}
		if cursor == "" {
			total = count
		}
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return err
			}
			err := s.editOne(ctx, id, b, lock, work.Job.ID)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				code := ""
				switch {
				case errors.Is(err, db.ErrNotFound): // gone or no longer visible: skip
				case errors.Is(err, db.ErrInvalid):
					code = "invalid_metadata"
					if strings.HasSuffix(err.Error(), " entries") {
						code = "metadata_list_full"
					}
				case errors.Is(err, db.ErrStale):
					code = "metadata_changed"
				default:
					return err
				}
				if err := s.db.RecordMetadataJobOutcome(ctx, work.Job.ID, id, code); err != nil {
					return err
				}
			}
			done++
			work.Progress(done, max(total, done))
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return s.selectionResult(ctx, work.Job.ID)
}

// editOne applies a bulk edit to one title. A title changed in the meantime is read again
// and edited on its new copy. The save clears an earlier failure in that same transaction.
func (s *Server) editOne(ctx context.Context, id string, b BulkEdit, lock bool, jobID int64) error {
	if err := s.itemMetadataLanguage(ctx, id, b.MetadataLanguage); err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		doc, err := s.db.MetadataDocument(ctx, id)
		if err != nil {
			return err
		}
		locked := map[string]bool{}
		for _, f := range doc.Locked {
			locked[f] = true
		}
		edit := db.MetadataEdit{JobID: jobID, Year: b.Year, Edition: b.Edition, ContentRating: b.ContentRating, Language: b.MetadataLanguage}
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
		return err
	}
}
