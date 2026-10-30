package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestHomeDestinationReceiptDeduplicatesOverlapAndSurvivesCapturedItemPurge(t *testing.T) {
	service, scheduler, p, v, access, fixture, names := bulkFixture(t, 3)
	name := "Destination"
	created, err := service.MutatePlaylist(jobActor(p), "", "create", "", PlaylistMutation{OperationID: "receipt-create", Name: &name}, nil)
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := service.MutatePlaylist(jobActor(p), created.PlaylistID, "add", "", PlaylistMutation{OperationID: "receipt-existing", ExpectedRevision: created.Revision, ItemID: names["ep000000"].Public}, nil)
	if err != nil {
		t.Fatal(err)
	}
	run := func(operation string, selector JobSelector, revision int64) BulkJob {
		t.Helper()
		request := JobRequest{OperationID: operation, Command: "playlist-add", Selector: selector, Args: JobArguments{PlaylistID: created.PlaylistID, ExpectedRevision: &revision, Placement: json.RawMessage(`"end"`)}}
		job, err := service.CreateBulkJob(context.Background(), p, v, request, scheduler, access)
		if err != nil {
			t.Fatal(err)
		}
		job = finishCapture(t, service, job, access)
		for !bulkTerminal(job.State) {
			job, err = service.AdvanceBulkJob(context.Background(), job.JobID, access)
			if err != nil {
				t.Fatal(err)
			}
		}
		if job.State != "complete" {
			t.Fatal(job)
		}
		replay, err := service.CreateBulkJob(context.Background(), p, v, request, scheduler, access)
		if err != nil || replay.JobID != job.JobID {
			t.Fatal(replay, err)
		}
		if _, err = service.purgeBulkJobItems(context.Background(), job.JobID, time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		return job
	}
	show := run("receipt-show", JobSelector{Container: &JobContainer{Kind: "show", ID: names["show"].Public}}, seeded.Revision)
	resource, err := service.Playlist("server", v.Fence, created.PlaylistID, jobActor(p))
	if err != nil {
		t.Fatal(err)
	}
	leaf := run("receipt-leaf", JobSelector{Items: &JobItems{IDs: []string{names["ep000000"].Public, names["ep000001"].Public}}}, resource.Revision)
	snapshot, err := dbwork.BeginSnapshot(context.Background(), fixture.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Rollback()
	request := HomeDestinationReceiptRequest{Kind: "playlist", DestinationID: created.PlaylistID, JobIDs: []string{show.JobID, leaf.JobID}}
	receipt, err := service.HomeDestinationReceiptTx(context.Background(), snapshot.Tx(), "server", p, v, request, access)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Added != 2 || receipt.AlreadyPresent != 1 || receipt.Failed != 0 || receipt.Unknown != 0 || !receipt.Complete {
		t.Fatalf("encounters were substituted for distinct outcomes: %+v", receipt)
	}
	var captured int
	if err = snapshot.Tx().QueryRow(`SELECT count(*) FROM personal_job_items WHERE job_id IN(?,?)`, show.JobID, leaf.JobID).Scan(&captured); err != nil || captured != 0 {
		t.Fatal("terminal membership was retained instead of the receipt", captured, err)
	}
	foreign := p
	foreign.AccountID = "another-account"
	if _, err = service.HomeDestinationReceiptTx(context.Background(), snapshot.Tx(), "server", foreign, v, request, access); err == nil {
		t.Fatal("foreign job union accepted")
	}
	wrong := request
	wrong.DestinationID = "another-destination"
	if _, err = service.HomeDestinationReceiptTx(context.Background(), snapshot.Tx(), "server", p, v, wrong, access); err == nil {
		t.Fatal("destination-swapped job union accepted")
	}
}

func TestHomeDestinationMembershipNeverAppliesToReusedIntegerIdentity(t *testing.T) {
	service, scheduler, p, v, access, fixture, names := bulkFixture(t, 1)
	name := "Stable destination"
	created, err := service.MutatePlaylist(jobActor(p), "", "create", "", PlaylistMutation{OperationID: "stable-create", Name: &name}, nil)
	if err != nil {
		t.Fatal(err)
	}
	revision := created.Revision
	request := JobRequest{OperationID: "stable-job", Command: "playlist-add", Selector: JobSelector{Items: &JobItems{IDs: []string{names["ep000000"].Public}}}, Args: JobArguments{PlaylistID: created.PlaylistID, ExpectedRevision: &revision, Placement: json.RawMessage(`"end"`)}}
	job, err := service.CreateBulkJob(context.Background(), p, v, request, scheduler, access)
	if err != nil {
		t.Fatal(err)
	}
	job = finishCapture(t, service, job, access)
	old := names["ep000000"]
	fixture.Delete(old.ID)
	// Recreate a different entity at the same integer id. Its new public id is
	// deliberately unrelated to the original captured member.
	fixture.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head) VALUES(?,randomblob(16),(SELECT id FROM catalog_libraries WHERE library_id='lib'),1,'Replacement','Replacement','R')`, old.ID)
		return err
	})
	job, err = service.AdvanceBulkJob(context.Background(), job.JobID, access)
	if err != nil {
		t.Fatal(err)
	}
	if job.Done != 0 || job.Failed != 1 {
		t.Fatalf("replacement inherited the captured command: %+v", job)
	}
	var original, outcome, code string
	var entries int
	if err = fixture.DB.QueryRow(`SELECT pid(item_public_id),outcome,code FROM personal_job_membership_outcomes WHERE job_id=?`, job.JobID).Scan(&original, &outcome, &code); err != nil {
		t.Fatal(err)
	}
	if original != old.Public || outcome != "failed" || code != "not_found" {
		t.Fatal("failure lost its original identity", original, outcome, code)
	}
	if err = fixture.DB.QueryRow(`SELECT count(*) FROM catalog_playlist_entries`).Scan(&entries); err != nil || entries != 0 {
		t.Fatal("replacement was added", entries, err)
	}
	failures, err := service.BulkFailures(v.Profile, job.JobID, "", v)
	if err != nil || len(failures.Items) != 0 {
		t.Fatal("replacement leaked as an original failed title", failures, err)
	}
	snapshot, err := dbwork.BeginSnapshot(context.Background(), fixture.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Rollback()
	receipt, err := service.HomeDestinationReceiptTx(context.Background(), snapshot.Tx(), "server", p, v, HomeDestinationReceiptRequest{Kind: "playlist", DestinationID: created.PlaylistID, JobIDs: []string{job.JobID}}, access)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Added != 0 || receipt.Unknown != 1 || receipt.Complete || len(receipt.Failures) != 0 || len(receipt.FailureEntries) != 0 {
		t.Fatal("unavailable original identity was fabricated", receipt)
	}
}

func TestHomeDestinationMembershipOutcomeRollsBackWithProgress(t *testing.T) {
	service, scheduler, p, v, access, fixture, names := bulkFixture(t, 1)
	name := "Atomic destination"
	created, err := service.MutatePlaylist(jobActor(p), "", "create", "", PlaylistMutation{OperationID: "atomic-create", Name: &name}, nil)
	if err != nil {
		t.Fatal(err)
	}
	revision := created.Revision
	job, err := service.CreateBulkJob(context.Background(), p, v, JobRequest{OperationID: "atomic-job", Command: "playlist-add", Selector: JobSelector{Items: &JobItems{IDs: []string{names["ep000000"].Public}}}, Args: JobArguments{PlaylistID: created.PlaylistID, ExpectedRevision: &revision, Placement: json.RawMessage(`"end"`)}}, scheduler, access)
	if err != nil {
		t.Fatal(err)
	}
	job = finishCapture(t, service, job, access)
	if _, err = fixture.DB.Exec(`CREATE TRIGGER reject_membership_progress BEFORE UPDATE OF done ON personal_jobs WHEN NEW.done>0 BEGIN SELECT RAISE(ABORT,'progress commit refused'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = service.AdvanceBulkJob(context.Background(), job.JobID, access); err == nil {
		t.Fatal("forced progress failure accepted")
	}
	var outcome string
	var entries int
	if err = fixture.DB.QueryRow(`SELECT outcome FROM personal_job_membership_outcomes WHERE job_id=?`, job.JobID).Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	if err = fixture.DB.QueryRow(`SELECT count(*) FROM catalog_playlist_entries`).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if outcome != "pending" || entries != 0 {
		t.Fatal("effect/outcome escaped progress rollback", outcome, entries)
	}
}

func TestHomeDestinationReceiptFailureWindowHasRealLeafTitlesAndRetryResolvesOnlyActualFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	service, scheduler, p, v, access, fixture, names := bulkFixture(t, 101)
	name := "Failure window"
	created, err := service.MutatePlaylist(jobActor(p), "", "create", "", PlaylistMutation{OperationID: "fail-window-create", Name: &name}, nil)
	if err != nil {
		t.Fatal(err)
	}
	revision := created.Revision
	job, err := service.CreateBulkJob(context.Background(), p, v, JobRequest{OperationID: "fail-window-job", Command: "playlist-add", Selector: JobSelector{Container: &JobContainer{Kind: "show", ID: names["show"].Public}}, Args: JobArguments{PlaylistID: created.PlaylistID, ExpectedRevision: &revision, Placement: json.RawMessage(`"end"`)}}, scheduler, access)
	if err != nil {
		t.Fatal(err)
	}
	job = finishCapture(t, service, job, access)
	denied := func(context.Context, *sql.Tx, identity.Principal, string) (string, []any, error) {
		return "0", nil, nil
	}
	for !bulkTerminal(job.State) {
		job, err = service.AdvanceBulkJob(context.Background(), job.JobID, denied)
		if err != nil {
			t.Fatal(err)
		}
	}
	read := func(request HomeDestinationReceiptRequest) HomeDestinationReceipt {
		t.Helper()
		snapshot, err := dbwork.BeginSnapshot(context.Background(), fixture.DB)
		if err != nil {
			t.Fatal(err)
		}
		defer snapshot.Rollback()
		receipt, err := service.HomeDestinationReceiptTx(context.Background(), snapshot.Tx(), "server", p, v, request, access)
		if err != nil {
			t.Fatal(err)
		}
		return receipt
	}
	request := HomeDestinationReceiptRequest{Kind: "playlist", DestinationID: created.PlaylistID, JobIDs: []string{job.JobID}}
	first := read(request)
	if first.Failed != 101 || len(first.Failures) != 100 || len(first.FailureEntries) != 100 || first.NextCursor == "" || !first.Complete {
		t.Fatal("failure aggregate/window is not exact", first)
	}
	for index, entry := range first.FailureEntries {
		if entry.ID != first.Failures[index].ItemID || entry.Title == "Show" || entry.Title == entry.ID || entry.Kind != "episode" {
			t.Fatal("failed leaf display was fabricated", entry)
		}
	}
	request.Cursor = first.NextCursor
	second := read(request)
	if len(second.Failures) != 1 || second.NextCursor != "" {
		t.Fatal("failure cursor did not page the actual window", second)
	}
	resource, err := service.Playlist("server", v.Fence, created.PlaylistID, jobActor(p))
	if err != nil {
		t.Fatal(err)
	}
	revision = resource.Revision
	retry, err := service.CreateBulkJob(context.Background(), p, v, JobRequest{OperationID: "fail-window-retry", Command: "playlist-add", Selector: JobSelector{Items: &JobItems{IDs: []string{first.Failures[0].ItemID}}}, Args: JobArguments{PlaylistID: created.PlaylistID, ExpectedRevision: &revision, Placement: json.RawMessage(`"end"`)}}, scheduler, access)
	if err != nil {
		t.Fatal(err)
	}
	retry = finishCapture(t, service, retry, access)
	for !bulkTerminal(retry.State) {
		retry, err = service.AdvanceBulkJob(context.Background(), retry.JobID, access)
		if err != nil {
			t.Fatal(err)
		}
	}
	request.JobIDs = append(request.JobIDs, retry.JobID)
	request.Cursor = ""
	final := read(request)
	if final.Added != 1 || final.AlreadyPresent != 0 || final.Failed != 100 || !final.Complete {
		t.Fatal("retry discarded successes or retained a resolved failure", final)
	}
	for _, failure := range final.Failures {
		if failure.ItemID == first.Failures[0].ItemID {
			t.Fatal("confirmed retry success remained failed")
		}
	}
}
