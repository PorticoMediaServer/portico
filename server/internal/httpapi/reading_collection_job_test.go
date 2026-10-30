package httpapi

import (
	"context"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"testing"
)

func TestReadingCollectionJobUsesRealReadingAdmissionAndExistingReceipt(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	readingSeriesDrain(t, r)
	d := r.d
	d.Scheduler = operations.NewScheduler(operations.New(d.DB))
	h := New(d)
	token := r.f.owner.AccessToken
	create := readingHTTP(t, h, "POST", "/v1/saved-resources", token, "", map[string]any{"operationId": "00000000-0000-4000-8000-000000000901", "expectedRevision": 0, "kind": "collection", "name": "Reading picks", "summary": ""})
	if create.Code != 200 {
		t.Fatal(create.Code, create.Body.String())
	}
	var created struct {
		Receipt struct {
			ResourceID string `json:"resourceId"`
			Revision   int64  `json:"revision"`
		} `json:"receipt"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil || created.Receipt.ResourceID == "" {
		t.Fatal(err, create.Body.String())
	}
	picker := readingHTTP(t, h, "POST", "/v1/home/destination-picker", token, "", catalog.HomeDestinationPickerRequest{Kind: "collection", Limit: 40, ItemIDs: []string{r.item}})
	if picker.Code != 200 {
		t.Fatal(picker.Code, picker.Body.String())
	}
	var page catalog.HomeDestinationPickerPage
	if err := json.Unmarshal(picker.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	var revision int64
	for _, row := range page.Rows {
		if row.ID == created.Receipt.ResourceID && row.CanAdd {
			revision = row.Revision
		}
	}
	if revision == 0 {
		t.Fatal("Reading destination absent", picker.Body.String())
	}
	request := catalog.JobRequest{OperationID: "reading-collection-http", Command: "collection-add", Selector: catalog.JobSelector{Items: &catalog.JobItems{IDs: []string{r.item}}}, Args: catalog.JobArguments{CollectionID: created.Receipt.ResourceID, ExpectedRevision: &revision}}
	response := readingHTTP(t, h, "POST", "/v1/jobs", token, "", request)
	if response.Code != 202 {
		t.Fatal(response.Code, response.Body.String())
	}
	var job catalog.BulkJob
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 10 && (job.State == "queued" || job.State == "running"); n++ {
		var err error
		job, err = d.Catalog.AdvanceBulkJob(context.Background(), job.JobID, d.bulkAccess)
		if err != nil {
			t.Fatal(err)
		}
	}
	if job.State != "complete" || job.Done != 1 {
		t.Fatalf("real Reading job: %+v", job)
	}
	receipt := readingHTTP(t, h, "POST", "/v1/home/destination-receipt", token, "", catalog.HomeDestinationReceiptRequest{Kind: "collection", DestinationID: created.Receipt.ResourceID, JobIDs: []string{job.JobID}})
	if receipt.Code != 200 {
		t.Fatal(receipt.Code, receipt.Body.String())
	}
	var outcome catalog.HomeDestinationReceipt
	if err := json.Unmarshal(receipt.Body.Bytes(), &outcome); err != nil {
		t.Fatal(err)
	}
	if !outcome.Complete || outcome.Added != 1 || outcome.Unknown != 0 {
		t.Fatal(receipt.Body.String())
	}
	replay := readingHTTP(t, h, "POST", "/v1/jobs", token, "", request)
	if replay.Code != 202 {
		t.Fatal(replay.Code, replay.Body.String())
	}
	// Revoke only Reading eligibility: the generic session and collection remain.
	if _, err := r.f.db.Exec(`DELETE FROM reading_publications WHERE item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, r.item); err != nil {
		t.Fatal(err)
	}
	next := request
	next.OperationID = "reading-collection-no-publication"
	denied := readingHTTP(t, h, "POST", "/v1/jobs", token, "", next)
	if denied.Code == 202 {
		t.Fatal("Reading-less title admitted", denied.Body.String())
	}
	receipt = readingHTTP(t, h, "POST", "/v1/home/destination-receipt", token, "", catalog.HomeDestinationReceiptRequest{Kind: "collection", DestinationID: created.Receipt.ResourceID, JobIDs: []string{job.JobID}})
	if receipt.Code != 200 {
		t.Fatal(receipt.Code, receipt.Body.String())
	}
	if err := json.Unmarshal(receipt.Body.Bytes(), &outcome); err != nil {
		t.Fatal(err)
	}
	if outcome.Complete || outcome.Added != 0 || outcome.Unknown != 1 {
		t.Fatal("revoked Reading receipt was not concealed", receipt.Body.String())
	}
}
