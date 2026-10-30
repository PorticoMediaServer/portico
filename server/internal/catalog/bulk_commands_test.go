package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestBulkResourceCommandsRestartReplayAndDestinationFence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, command := range []string{"playlist-add", "collection-add"} {
		t.Run(command, func(t *testing.T) {
			s, scheduler, p, v, access, _, names := bulkFixture(t, 43)
			actor := jobActor(p)
			name := "Destination"
			target := "collection"
			if command == "playlist-add" {
				r, e := s.MutatePlaylist(actor, "", "create", "", PlaylistMutation{OperationID: "create", Name: &name}, nil)
				if e != nil {
					t.Fatal(e)
				}
				target = r.PlaylistID
			} else {
				if _, e := s.db.Exec(`INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,created_at) VALUES(?,'collection',?,?,?,?,?,'2026-09-23')`, target, actorKey(actor), actor.Authority, actor.AccountID, actor.ProfileID, name); e != nil {
					t.Fatal(e)
				}
			}
			revision := int64(1)
			args := JobArguments{ExpectedRevision: &revision}
			if command == "playlist-add" {
				args.PlaylistID = target
				args.Placement = json.RawMessage(`"next"`)
			} else {
				args.CollectionID = target
			}
			ids := []string{}
			for i := 42; i >= 0; i-- {
				ids = append(ids, names[fmt.Sprintf("ep%06d", i)].Public)
			}
			in := JobRequest{OperationID: "resources", Command: command, Selector: JobSelector{Items: &JobItems{IDs: ids}}, Args: args}
			j, e := s.CreateBulkJob(context.Background(), p, v, in, scheduler, access)
			if e != nil {
				t.Fatal(e)
			}
			replay, e := s.CreateBulkJob(context.Background(), p, v, in, scheduler, access)
			if e != nil || replay.JobID != j.JobID {
				t.Fatal(replay, e)
			}
			j = finishCapture(t, s, j, access)
			j, e = s.AdvanceBulkJob(context.Background(), j.JobID, access)
			if e != nil || j.Done != bulkApplyBatch {
				t.Fatal(j, e)
			}
			s = restartBulkService(t, s)
			for j.State == "running" || j.State == "queued" {
				j, e = s.AdvanceBulkJob(context.Background(), j.JobID, access)
				if e != nil {
					t.Fatal(e)
				}
			}
			if j.State != "complete" || j.Done != 43 || j.Command != command {
				t.Fatal(j)
			}
			replay, e = s.CreateBulkJob(context.Background(), p, v, in, scheduler, access)
			if e != nil || replay.Done != 43 {
				t.Fatal(replay, e)
			}
			var count int
			countQuery := `SELECT count(*) FROM saved_resource_entries WHERE resource_id=?`
			if command == "playlist-add" {
				countQuery = `SELECT count(*) FROM catalog_playlist_entries WHERE playlist_id=(SELECT id FROM catalog_playlists WHERE token=?)`
			}
			if e = s.db.QueryRow(countQuery, target).Scan(&count); e != nil || count != 43 {
				t.Fatal(count, e)
			}
			if command == "playlist-add" {
				rows, e := s.db.Query(`SELECT e.item_id FROM catalog_playlist_entries e JOIN catalog_playlists p ON p.id=e.playlist_id WHERE p.token=? ORDER BY e.order_key,e.id`, target)
				if e != nil {
					t.Fatal(e)
				}
				i := 0
				for rows.Next() {
					var itemID int64
					rows.Scan(&itemID)
					item := bulkFixtureName(names, itemID)
					want := names.Of(ids[i])
					if item != want {
						t.Fatal("source order changed", i, item, want)
					}
					i++
				}
				rows.Close()
			}
			resourceTable := "saved_resources"
			resourceIDField := "id"
			if command == "collection-add" {
				resourceTable = "saved_resources"
			} else {
				resourceTable = "catalog_playlists"
				resourceIDField = "token"
			}
			// Each apply batch moves the destination's revision; the race job
			// names the revision the first job left behind.
			var current int64
			if e = s.db.QueryRow(`SELECT revision FROM `+resourceTable+` WHERE `+resourceIDField+`=?`, target).Scan(&current); e != nil {
				t.Fatal(e)
			}
			in.Args.ExpectedRevision = &current
			in.OperationID = "race"
			// Job admission is allowed, but an external destination edit before apply is not.
			j, e = s.CreateBulkJob(context.Background(), p, v, in, scheduler, access)
			if e != nil {
				t.Fatal(e)
			}
			j = finishCapture(t, s, j, access)
			if _, e = s.db.Exec(`UPDATE `+resourceTable+` SET revision=revision+1 WHERE `+resourceIDField+`=?`, target); e != nil {
				t.Fatal(e)
			}
			j, e = s.AdvanceBulkJob(context.Background(), j.JobID, access)
			if e != nil || j.State != "failed" || j.ErrorCode != "revision_mismatch" || j.Done != 0 {
				t.Fatal(j, e)
			}
			in.OperationID = "resources"
			in.Args.ExpectedRevision = &revision
			in.Selector.Items.IDs = []string{ids[0]}
			if _, e = s.CreateBulkJob(context.Background(), p, v, in, scheduler, access); !errors.Is(e, ErrOperationConflict) {
				t.Fatal("changed body replay", e)
			}
		})
	}
}

func TestBulkPlaylistAfterAndQueryOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, mode := range []string{"after", "next", "end"} {
		t.Run(mode, func(t *testing.T) {
			s, scheduler, p, v, access, _, names := bulkFixture(t, 4)
			actor := jobActor(p)
			name := "Ordered destination"
			r, err := s.MutatePlaylist(actor, "", "create", "", PlaylistMutation{OperationID: "create", Name: &name, ItemIDs: names.Publics("ep000000", "ep000003")}, nil)
			if err != nil {
				t.Fatal(err)
			}
			placement, _ := json.Marshal(mode)
			if mode == "after" {
				var anchor string
				if err = s.db.QueryRow(`SELECT e.token FROM catalog_playlist_entries e JOIN catalog_playlists p ON p.id=e.playlist_id WHERE p.token=? ORDER BY e.order_key LIMIT 1`, r.PlaylistID).Scan(&anchor); err != nil {
					t.Fatal(err)
				}
				placement, _ = json.Marshal(map[string]string{"after": anchor})
			}
			rev := int64(1)
			in := JobRequest{OperationID: "ordered", Command: "playlist-add", Args: JobArguments{PlaylistID: r.PlaylistID, ExpectedRevision: &rev, Placement: placement}, Selector: JobSelector{Query: &JobQuery{LibraryID: "lib", Pivot: "episodes", Sort: []BrowseSortSelection{{Field: "title", Direction: "desc"}}}}}
			j, err := s.CreateBulkJob(context.Background(), p, v, in, scheduler, access)
			if err != nil {
				t.Fatal(err)
			}
			j = finishCapture(t, s, j, access)
			// Placement and query order must survive a worker restart.
			s = restartBulkService(t, s)
			for j.State == "queued" || j.State == "running" {
				j, err = s.AdvanceBulkJob(context.Background(), j.JobID, access)
				if err != nil {
					t.Fatal(err)
				}
			}
			if j.State != "complete" || j.Done != 4 || j.Failed != 0 {
				t.Fatal(j)
			}
			replay, err := s.CreateBulkJob(context.Background(), p, v, in, scheduler, access)
			if err != nil || replay.JobID != j.JobID || replay.Done != j.Done {
				t.Fatal("replay changed", replay, err)
			}
			rows, err := s.db.Query(`SELECT e.item_id FROM catalog_playlist_entries e JOIN catalog_playlists p ON p.id=e.playlist_id WHERE p.token=? ORDER BY e.order_key,e.id`, r.PlaylistID)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			items := []string{}
			for rows.Next() {
				var id int64
				if err = rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				items = append(items, bulkFixtureName(names, id))
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			expected := []string{"ep000000", "ep000003", "ep000002", "ep000001", "ep000000", "ep000003"}
			added, present := 4, 0
			switch mode {
			case "next":
				expected = []string{"ep000003", "ep000002", "ep000001", "ep000000", "ep000000", "ep000003"}
			case "end":
				// Home's picker appends membership and reports existing titles.
				expected = []string{"ep000000", "ep000003", "ep000002", "ep000001"}
				added, present = 2, 2
			}
			if fmt.Sprint(items) != fmt.Sprint(expected) {
				t.Fatal(items, expected)
			}
			var actualAdded, actualPresent int
			if err = s.db.QueryRow(`SELECT count(*) FILTER(WHERE outcome='added'),count(*) FILTER(WHERE outcome='already_present') FROM personal_job_membership_outcomes WHERE job_id=?`, j.JobID).Scan(&actualAdded, &actualPresent); err != nil || actualAdded != added || actualPresent != present {
				t.Fatal("membership receipts", actualAdded, actualPresent, err)
			}
		})
	}
}
