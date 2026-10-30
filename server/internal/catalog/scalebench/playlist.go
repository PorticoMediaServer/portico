package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// G03 large playlist. A playlist is a resource with its own stored order and
// display orders over the same occurrences; a display page sorts the whole
// resource on every request. This fixture therefore has to be a real playlist:
// real playable members, the real entry rows and their triggers, read through
// PlaylistContent, never a synthetic list of ids.
//
// The harness seeds the base playlist once and then reads it. Construction goes
// through the real create mutation for the playlist row; occurrences are
// inserted in bounded writer batches with the same (token, position, order_key)
// shape the create/append path writes, because the public append mutation is
// capped at MaxPlaylistEntries (200) per synchronous write and a per-item
// mutation would spend the run in receipt bookkeeping.

type playlistOptions struct {
	entries    int // playlist members; >= 100000 is the acceptance fixture
	limit      int // page size
	pageBudget int // 0 walks to the last page; otherwise stop after this many pages
	reorders   int // concurrent-reorder writes
	readers    int // concurrent readers during reorder
}

var playlistExercise playlistOptions

type playlistSortResult struct {
	Route         string  `json:"route"`
	Sort          string  `json:"sort"`
	Direction     string  `json:"direction"`
	Pages         int     `json:"pagesWalked"`
	Total         int     `json:"total"`
	ReachedLast   bool    `json:"reachedLast"`
	FirstMS       float64 `json:"firstPageMs"`
	DeepMS        float64 `json:"deepPageMs"`
	LastMS        float64 `json:"lastPageMs,omitempty"`
	WalkMS        float64 `json:"walkMs"`
	MaxVMSteps    int64   `json:"maxVmSteps"`
	MaxStatements uint64  `json:"maxStatements"`
	StaleRestarts int     `json:"staleRestarts"`
	HiddenSeen    int     `json:"hiddenOccurrences,omitempty"`
}

type playlistFilterResult struct {
	Viewer      string         `json:"viewer"`
	Counts      map[string]int `json:"counts"`
	Total       int            `json:"totalCount"`
	SumOK       bool           `json:"watchedAndUnwatchedWithinAll"`
	ProgressSub bool           `json:"inProgressWithinUnwatched"`
	Entries     int            `json:"pageEntries"`
}

type playlistTies struct {
	Sort             string `json:"sort"`
	Walked           int    `json:"occurrencesWalked"`
	Distinct         int    `json:"distinctOccurrences"`
	DuplicateTokens  int    `json:"duplicateTokens"`
	MissingTokens    int    `json:"missingTokens"`
	OrderStable      bool   `json:"everyOccurrenceExactlyOnce"`
	FirstPageOrderOK bool   `json:"fixtureOrderIntact"`
}

type playlistReorderResult struct {
	Order          string  `json:"order"`
	Writes         int     `json:"writes"`
	Reads          int     `json:"reads"`
	StaleRestarts  int     `json:"staleRestarts"`
	MaxWriteMS     float64 `json:"maxWriteMs"`
	FinalEntries   int     `json:"finalEntries"`
	OrderPreserved bool    `json:"expectedOrderPreserved"`
	Conflicts      int     `json:"revisionConflicts"`
	Error          string  `json:"error,omitempty"`
}

type playlistReport struct {
	Entries      int                    `json:"entries"`
	PageLimit    int                    `json:"pageLimit"`
	PageBudget   int                    `json:"pageBudget"`
	Reused       bool                   `json:"reusedFixture"`
	SeedSeconds  float64                `json:"seedSeconds"`
	SeedBytes    int64                  `json:"fixtureBytes"`
	ScratchBytes int64                  `json:"scratchBytes"`
	Libraries    []string               `json:"libraries"`
	Sorts        []playlistSortResult   `json:"sorts"`
	Filters      []playlistFilterResult `json:"filters"`
	Ties         []playlistTies         `json:"ties"`
	Reorder      playlistReorderResult  `json:"concurrentReorder"`
	Notes        []string               `json:"notes"`
}

var playlistSorts = []struct {
	id, direction string
}{
	{"position", "asc"},
	{"title", "asc"},
	{"title", "desc"},
	{"year", "desc"},
	{"year", "asc"},
	{"duration", "asc"},
	{"duration", "desc"},
}

const playlistName = "Scalebench playlist"
const playlistActorAccount = "fixture"

// exercisePlaylist seeds or reuses the large playlist and measures its pages.
func exercisePlaylist(ctx context.Context, db *sql.DB, s *catalog.Service, dir string) (playlistReport, error) {
	report := playlistReport{Entries: playlistExercise.entries, PageLimit: playlistExercise.limit, PageBudget: playlistExercise.pageBudget}
	if playlistExercise.entries < 100 || playlistExercise.limit < 1 || playlistExercise.limit > 100 {
		return report, errors.New("playlist entries must be >= 100 and the page limit 1..100")
	}
	viewer := catalog.Viewer{Profile: "viewer", Fence: "scale-open", Libraries: []string{"movies", "tv", "music"}}
	actor := catalog.ResourceActor{Authority: "local", AccountID: playlistActorAccount, ProfileID: "viewer"}
	report.Libraries = viewer.Libraries
	start := time.Now()
	id, tokens, reused, err := seedPlaylist(ctx, db, s, actor, playlistExercise.entries)
	report.Reused = reused
	report.SeedSeconds = time.Since(start).Seconds()
	if err != nil {
		return report, err
	}
	if tokens == nil {
		if tokens, err = playlistTokens(ctx, db, id); err != nil {
			return report, err
		}
	}
	if len(tokens) != playlistExercise.entries {
		return report, fmt.Errorf("playlist holds %d occurrences, want %d", len(tokens), playlistExercise.entries)
	}
	if report.ScratchBytes, err = scratchUsage(); err != nil {
		return report, err
	}
	report.SeedBytes = report.ScratchBytes
	for _, sortSpec := range playlistSorts {
		result, err := walkPlaylist(ctx, db, s, viewer, id, sortSpec.id, sortSpec.direction, playlistExercise.limit, playlistExercise.pageBudget, false)
		report.Sorts = append(report.Sorts, result.result)
		if err != nil {
			return report, fmt.Errorf("%s/%s: %w", sortSpec.id, sortSpec.direction, err)
		}
	}
	filters, err := playlistFilterCounts(ctx, db, s, id)
	if err != nil {
		return report, err
	}
	report.Filters = filters
	// Every duration in the reference fixture has the same value, so a full walk
	// of that order is the largest possible tie group: it must not skip, repeat
	// or reorder an occurrence. Years cycle over 76 values, a second tie group.
	for _, tie := range []struct{ sort, direction string }{{"duration", "asc"}, {"year", "desc"}} {
		observed, err := walkPlaylist(ctx, db, s, viewer, id, tie.sort, tie.direction, playlistExercise.limit, playlistExercise.pageBudget, true)
		if err != nil {
			return report, err
		}
		ties, err := comparePlaylistWalk(ctx, db, id, tokens, observed.tokens, observed.result.ReachedLast)
		if err != nil {
			return report, err
		}
		ties.Sort = tie.sort
		report.Ties = append(report.Ties, ties)
	}
	if playlistExercise.reorders > 0 {
		reorder, err := exercisePlaylistReorder(ctx, db, s, viewer, actor, id, tokens)
		report.Reorder = reorder
		if err != nil {
			return report, err
		}
	}
	report.Notes = append(report.Notes,
		"Occurrences are real playable catalog items; entry rows are written in bounded writer batches with the create/append shape because one append mutation is capped at 200 entries.",
	)
	return report, writeJSONFile(filepath.Join(dir, "playlist-report.json"), report)
}

// seedPlaylist creates the playlist when it is absent and returns its tokens
// when it is already complete. Reuse never duplicates rows.
func seedPlaylist(ctx context.Context, db *sql.DB, s *catalog.Service, actor catalog.ResourceActor, want int) (string, []string, bool, error) {
	var id string
	var count int
	err := db.QueryRow(`SELECT token,entry_count FROM catalog_playlists WHERE owner_authority=? AND owner_account=? AND owner_profile=? AND name=? AND deleted=0`, actor.Authority, actor.AccountID, actor.ProfileID, playlistName).Scan(&id, &count)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", nil, false, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		name := playlistName
		receipt, createErr := s.WithContext(ctx).MutatePlaylist(actor, "", "create", "", catalog.PlaylistMutation{
			OperationID: "scalebench-playlist-" + identity.Token(), ExpectedRevision: 0, Name: &name,
		}, nil)
		if createErr != nil {
			return "", nil, false, fmt.Errorf("create playlist: %w", createErr)
		}
		id = receipt.PlaylistID
	}
	have, err := playlistTokens(ctx, db, id)
	if err != nil {
		return "", nil, false, err
	}
	if len(have) >= want {
		return id, have, true, nil
	}
	// Deterministic playable members: the reference seed numbers movies first,
	// then episodes, then songs, so a movies-only viewer hides the later tail.
	rows, err := db.Query(`SELECT e.id FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1 AND e.kind<>11 ORDER BY e.id LIMIT ?`, want)
	if err != nil {
		return "", nil, false, err
	}
	items := []int64{}
	for rows.Next() {
		var item int64
		if err = rows.Scan(&item); err != nil {
			rows.Close()
			return "", nil, false, err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", nil, false, err
	}
	if len(items) < want {
		return "", nil, false, fmt.Errorf("fixture has %d playable items, need %d playlist members", len(items), want)
	}
	var playlistRow int64
	if err = db.QueryRow(`SELECT id FROM catalog_playlists WHERE token=?`, id).Scan(&playlistRow); err != nil {
		return "", nil, false, err
	}
	position := len(have)
	const batch = 24
	for offset := len(have); offset < want; offset += batch {
		if err = checkScratchBudget(); err != nil {
			return "", nil, false, err
		}
		end := min(want, offset+batch)
		err = dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			insert, err := tx.PrepareContext(ctx, `INSERT INTO catalog_playlist_entries(token,playlist_id,item_id,position,order_key) VALUES(?,?,?,?,?)`)
			if err != nil {
				return err
			}
			defer insert.Close()
			for index := offset; index < end; index++ {
				position++
				if _, err = insert.ExecContext(ctx, identity.Token(), playlistRow, items[index], position, fmt.Sprintf("%020dV", position)); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return "", nil, false, err
		}
		if end%10000 < batch || end == want {
			fmt.Fprintf(os.Stderr, "playlist seed %d/%d\n", end, want)
		}
	}
	var stored int
	if err = db.QueryRow(`SELECT entry_count FROM catalog_playlists WHERE token=?`, id).Scan(&stored); err != nil {
		return "", nil, false, err
	}
	if stored != want {
		return "", nil, false, fmt.Errorf("playlist trigger counted %d occurrences, want %d", stored, want)
	}
	return id, nil, false, nil
}

func playlistTokens(ctx context.Context, db *sql.DB, id string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT e.token FROM catalog_playlist_entries e JOIN catalog_playlists p ON p.id=e.playlist_id WHERE p.token=? ORDER BY e.order_key,e.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tokens := []string{}
	for rows.Next() {
		var token string
		if err = rows.Scan(&token); err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

type playlistWalk struct {
	result playlistSortResult
	tokens []string
	hidden int
}

// walkPlaylist measures one page at a time along the continuation chain and
// records the first, middle and last page latencies. The walk is the honest
// cost of reaching a last page because a display order has no reverse cursor.
func walkPlaylist(ctx context.Context, db *sql.DB, s *catalog.Service, viewer catalog.Viewer, id, sortID, direction string, limit, budget int, collect bool) (playlistWalk, error) {
	walk := playlistWalk{result: playlistSortResult{Sort: sortID, Direction: direction}}
	walk.result.Route = "playlist/" + sortID + "/" + direction
	restarts, cursor, page := 0, "", 0
	lastPage := -1
	seen := map[string]int{}
	started := time.Now()
	for {
		pageStart := time.Now()
		var env catalog.ContentEnvelope
		var statements uint64
		var vm int64
		err := dbwork.WithReadSnapshot(ctx, db, func(inner context.Context) error {
			measured, cost := dbwork.Measure(inner)
			before := dbwork.Reads().Statements
			var readErr error
			env, readErr = s.WithContext(measured).PlaylistContent(catalog.ContentRequest{
				ServerID: "scalebench", Viewer: viewer, View: "playlist", EntityID: id,
				Sort: sortID, Direction: direction, Cursor: cursor, Limit: limit,
			}, catalog.ResourceActor{Authority: "local", AccountID: playlistActorAccount, ProfileID: "viewer"}, viewer.Libraries)
			statements = dbwork.Reads().Statements - before
			vm = cost().VMSteps
			return readErr
		})
		elapsed := float64(time.Since(pageStart)) / float64(time.Millisecond)
		if errors.Is(err, catalog.ErrStaleContinuation) && restarts < 3 {
			// A concurrent reorder moved the revision; restarting from the top is
			// the client's only correct recovery, and it is counted, not hidden.
			restarts++
			cursor, page, seen = "", 0, map[string]int{}
			continue
		}
		if err != nil {
			return walk, err
		}
		if len(env.Sections) == 0 {
			return walk, errors.New("playlist page has no section")
		}
		section := env.Sections[0]
		walk.result.Total = section.TotalCount
		walk.result.MaxStatements = max(walk.result.MaxStatements, statements)
		walk.result.MaxVMSteps = max(walk.result.MaxVMSteps, vm)
		if lastPage < 0 {
			lastPage = max(0, (section.TotalCount-1)/limit)
		}
		if page == 0 {
			walk.result.FirstMS = elapsed
		}
		if lastPage > 0 && page == lastPage/2 {
			walk.result.DeepMS = elapsed
		}
		if page == lastPage {
			walk.result.LastMS = elapsed
		}
		walk.result.Pages++
		for _, entry := range section.Entries {
			if collect {
				seen[entry.ID]++
				walk.tokens = append(walk.tokens, entry.ID)
			}
			if entry.Hidden != nil && *entry.Hidden {
				walk.hidden++
			}
		}
		cursor = section.NextCursor
		if page == lastPage || cursor == "" || (budget > 0 && page+1 >= budget) {
			break
		}
		page++
	}
	walk.result.WalkMS = float64(time.Since(started)) / float64(time.Millisecond)
	walk.result.StaleRestarts = restarts
	walk.result.HiddenSeen = walk.hidden
	walk.result.ReachedLast = lastPage >= 0 && walk.result.Pages-1 == lastPage
	return walk, nil
}

func readPlaylistPage(ctx context.Context, db *sql.DB, s *catalog.Service, viewer catalog.Viewer, id, sortID, direction string, limit int, cursor string) (catalog.ContentEnvelope, error) {
	var env catalog.ContentEnvelope
	err := dbwork.WithReadSnapshot(ctx, db, func(inner context.Context) error {
		var readErr error
		env, readErr = s.WithContext(inner).PlaylistContent(catalog.ContentRequest{
			ServerID: "scalebench", Viewer: viewer, View: "playlist", EntityID: id,
			Sort: sortID, Direction: direction, Cursor: cursor, Limit: limit,
		}, catalog.ResourceActor{Authority: "local", AccountID: playlistActorAccount, ProfileID: "viewer"}, viewer.Libraries)
		return readErr
	})
	return env, err
}

// playlistFilterCounts reads every filter option the resource publishes, for an
// open viewer, a restricted one and a movies-only viewer whose music and TV
// occurrences are hidden.
func playlistFilterCounts(ctx context.Context, db *sql.DB, s *catalog.Service, id string) ([]playlistFilterResult, error) {
	// A restricted viewer needs a published visibility class (the ordinary
	// matrix builds one); this resource check uses the open viewer and a viewer
	// whose other libraries are not granted, which is the hidden-occurrence case.
	viewers := []struct {
		name   string
		viewer catalog.Viewer
	}{
		{"open", catalog.Viewer{Profile: "viewer", Fence: "scale-open", Libraries: []string{"movies", "tv", "music"}}},
		{"movies-only", catalog.Viewer{Profile: "viewer", Fence: "scale-movies", Libraries: []string{"movies"}}},
	}
	out := []playlistFilterResult{}
	for _, entry := range viewers {
		env, err := readPlaylistPage(ctx, db, s, entry.viewer, id, "position", "asc", 100, "")
		if err != nil {
			return nil, err
		}
		result := playlistFilterResult{Viewer: entry.name, Counts: map[string]int{}}
		if len(env.Sections) > 0 {
			result.Entries = len(env.Sections[0].Entries)
			result.Total = env.Sections[0].TotalCount
		}
		if len(env.Filters) > 0 {
			for _, option := range env.Filters[0].Options {
				result.Counts[option.ID] = option.Count
			}
		}
		// A hidden occurrence is in All but in neither watched nor unwatched, so
		// those two are only a complement when nothing is hidden. In progress is
		// always a subset of unwatched.
		result.SumOK = result.Counts["watched"]+result.Counts["unwatched"] <= result.Counts["all"] &&
			result.Counts["unwatched"] <= result.Counts["all"] && result.Counts["watched"] <= result.Counts["all"]
		result.ProgressSub = result.Counts["inProgress"] <= result.Counts["unwatched"]
		out = append(out, result)
	}
	return out, nil
}

// comparePlaylistWalk checks that a complete page walk returned every
// occurrence exactly once (the occurrence id is the final tie, so no two rows
// compare equal) and that the stored fixture order is intact.
func comparePlaylistWalk(ctx context.Context, db *sql.DB, id string, expected, observed []string, complete bool) (playlistTies, error) {
	out := playlistTies{Walked: len(observed)}
	seen := map[string]bool{}
	for _, token := range observed {
		if seen[token] {
			out.DuplicateTokens++
		}
		seen[token] = true
	}
	for _, token := range expected {
		if !seen[token] {
			out.MissingTokens++
		}
	}
	out.Distinct = len(seen)
	if complete && out.Distinct == len(expected) && len(observed) == len(expected) && out.DuplicateTokens == 0 {
		out.OrderStable = true
	}
	var stored int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM catalog_playlist_entries e JOIN catalog_playlists p ON p.id=e.playlist_id WHERE p.token=?`, id).Scan(&stored); err != nil {
		return out, err
	}
	out.FirstPageOrderOK = stored == len(expected)
	return out, nil
}

// exercisePlaylistReorder writes reorders while readers page. A reader that
// meets a bumped revision must restart from the top: the continuation is fenced
// by the resource revision, so a swap can never be silently skipped or applied
// to a page from an older order.
func exercisePlaylistReorder(ctx context.Context, db *sql.DB, s *catalog.Service, viewer catalog.Viewer, actor catalog.ResourceActor, id string, tokens []string) (playlistReorderResult, error) {
	out := playlistReorderResult{Order: "position/asc"}
	if len(tokens) < 4 {
		return out, errors.New("reorder fixture needs at least four occurrences")
	}
	expected := append([]string{}, tokens...)
	var revision int64
	if err := db.QueryRowContext(ctx, `SELECT revision FROM catalog_playlists WHERE token=? AND deleted=0`, id).Scan(&revision); err != nil {
		return out, err
	}
	stopReaders := startPlaylistReorderReaders(ctx, playlistExercise.readers, func(readCtx context.Context) error {
		_, err := readPlaylistPage(readCtx, db, s, viewer, id, "position", "asc", playlistExercise.limit, "")
		return err
	})
	// Writers can return early on cancellation or mutation failure. Readers
	// must retire before the caller closes the fixture database in every case.
	defer func() { _, _ = stopReaders() }()
	for write := 0; write < playlistExercise.reorders; write++ {
		index := (write * 2) % min(200, len(expected)-1)
		// Ask for the two occurrences in reverse order: the reorder assigns their
		// two existing slots in the order given, which exchanges them.
		ids := []string{expected[index+1], expected[index]}
		start := time.Now()
		receipt, err := s.WithContext(ctx).MutatePlaylist(actor, id, "reorder", "", catalog.PlaylistMutation{
			OperationID: "scalebench-reorder-" + identity.Token(), ExpectedRevision: revision, EntryIDs: ids,
		}, nil)
		out.MaxWriteMS = max(out.MaxWriteMS, float64(time.Since(start))/float64(time.Millisecond))
		if errors.Is(err, catalog.ErrPlaylistConflict) {
			out.Conflicts++
			if err = db.QueryRowContext(ctx, `SELECT revision FROM catalog_playlists WHERE token=? AND deleted=0`, id).Scan(&revision); err != nil {
				return out, err
			}
			continue
		}
		if err != nil {
			return out, err
		}
		revision = receipt.Revision
		expected[index], expected[index+1] = expected[index+1], expected[index]
		out.Writes++
	}
	reads, err := stopReaders()
	out.Reads, out.StaleRestarts = reads.n, reads.stale
	if err != nil {
		return out, err
	}
	stored, err := playlistTokens(ctx, db, id)
	if err != nil {
		return out, err
	}
	out.FinalEntries = len(stored)
	out.OrderPreserved = len(stored) == len(expected)
	for index := range stored {
		if index < len(expected) && stored[index] != expected[index] {
			out.OrderPreserved = false
			break
		}
	}
	if !out.OrderPreserved {
		return out, errors.New("concurrent reorder did not leave the applied order")
	}
	return out, nil
}

// Each reader owns only transient page work. Recover and end a failed reader;
// never restart it and accidentally qualify a partial concurrent exercise.
type playlistReadCount struct{ n, stale int }

func startPlaylistReorderReaders(ctx context.Context, readers int, read func(context.Context) error) func() (playlistReadCount, error) {
	readCtx, cancel := context.WithCancel(ctx)
	type result struct {
		count playlistReadCount
		clean bool
	}
	count := max(1, readers)
	results := make(chan result, count)
	for range count {
		supervise.Go("scalebench playlist reorder reader", func() {
			done := result{}
			// Report even on panic, before supervise contains it, so joining
			// cannot hang and a failed reader never becomes a successful run.
			defer func() { results <- done }()
			for {
				if readCtx.Err() != nil {
					done.clean = true
					return
				}
				err := read(readCtx)
				done.count.n++ // Page attempts, as in the existing report.
				if err != nil {
					if readCtx.Err() != nil {
						done.clean = true
						return
					}
					done.count.stale++
				}
			}
		})
	}
	var once sync.Once
	var total playlistReadCount
	var failure error
	return func() (playlistReadCount, error) {
		once.Do(func() {
			cancel()
			for range count {
				done := <-results
				total.n += done.count.n
				total.stale += done.count.stale
				if !done.clean {
					failure = errors.New("playlist reorder reader ended unexpectedly")
				}
			}
		})
		return total, failure
	}
}

func writeJSONFile(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0600)
}
