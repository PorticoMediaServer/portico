package read

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func newUOOperationsFixture(t *testing.T) (*browseFixture, *ForegroundReader) {
	t.Helper()
	defs := append(browseDefinitions(), UOOperationsDefinitions()...)
	f := composeBrowseFixture(t, newForegroundSource(t), defs)
	r, err := NewForegroundReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

func uoOperationsRead(t *testing.T, f *browseFixture, r *ForegroundReader, in UOOperationsInput) (UOOperationsPage, BrowseReadResult, error) {
	t.Helper()
	if in.View.Kind == 0 {
		in.View.Kind = 1
	}
	return r.operationsWindow(t.Context(), foregroundRequest(), time.Second, f.output, in)
}

func uoOperationsInput(f *browseFixture, family UOOperationsFamily) UOOperationsInput {
	in := UOOperationsInput{View: f.input, Family: family}
	in.View.Library, in.View.Kind, in.View.Limit = 0, 1, 40
	return in
}

func TestUOOperationsConflictsAndDevicesAreOwnAndBounded(t *testing.T) {
	f, r := newUOOperationsFixture(t)
	for i := 1; i <= 70; i++ {
		f.exec(t, `INSERT INTO personal_conflicts(id,profile_id,item_id,field,base_revision,base_value,resolved) VALUES(?,?,?,?,?,?,?)`,
			fmt.Sprintf("conflict-%03d", i), "p", i, "watched", int64(1), fmt.Sprintf("value-%d", i), int64(i%2))
	}
	f.exec(t, `INSERT INTO personal_conflicts(id,profile_id,item_id,field,base_revision,base_value,resolved) VALUES('q-conflict','q',1,'watched',1,'other',0)`)
	in := uoOperationsInput(f, UOOperationsConflicts)
	var got []string
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("conflict cursor did not terminate")
		}
		page, result, err := uoOperationsRead(t, f, r, in)
		if err != nil || result.Refusal != nil {
			t.Fatalf("conflicts %v %+v", err, result)
		}
		if page.RawRows > MaxRaw+1 || page.RawPositions > MaxRaw || len(page.Conflicts) > in.View.Limit {
			t.Fatalf("conflicts unbounded %+v", page)
		}
		for _, value := range page.Conflicts {
			if value.ID == "q-conflict" {
				t.Fatal("another profile's conflict was returned")
			}
			got = append(got, value.ID)
		}
		if page.Exhausted {
			break
		}
		if page.Next == nil {
			t.Fatal("conflicts lost continuation")
		}
		in.After = page.Next
	}
	rows, err := f.conn.QueryContext(t.Context(), `SELECT id FROM personal_conflicts WHERE profile_id='p' ORDER BY id ASC`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var want []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		want = append(want, id)
	}
	if !reflect.DeepEqual(got, want) || len(got) != 70 {
		t.Fatalf("conflict traversal %v want %v", got, want)
	}

	for i := 1; i <= 70; i++ {
		f.exec(t, `INSERT INTO personal_device_sequences(profile_id,device_id,sequence) VALUES(?,?,?)`, "p", fmt.Sprintf("device-%03d", i), int64(i))
	}
	f.exec(t, `INSERT INTO personal_device_sequences(profile_id,device_id,sequence) VALUES('q','q-device',99)`)
	in = uoOperationsInput(f, UOOperationsDevices)
	in.View.Limit = 30
	var devices []string
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("device cursor did not terminate")
		}
		page, _, err := uoOperationsRead(t, f, r, in)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range page.Devices {
			if value.ID == "q-device" {
				t.Fatal("another profile's device was returned")
			}
			devices = append(devices, value.ID)
		}
		if page.Exhausted {
			break
		}
		in.After = page.Next
	}
	if len(devices) != 70 || devices[0] != "device-001" || devices[69] != "device-070" {
		t.Fatalf("device traversal %d %v", len(devices), devices[:min(3, len(devices))])
	}
}

func TestUOOperationsJobsPointAndDue(t *testing.T) {
	f, r := newUOOperationsFixture(t)
	f.exec(t, `INSERT INTO personal_jobs(id,profile_id,operation_id,request_hash,principal,selector,args,state,created_ms,updated_ms) VALUES('job-a','p','op-a','hash','p','selector','{}','queued',1,2)`)
	f.exec(t, `INSERT INTO personal_jobs(id,profile_id,operation_id,request_hash,principal,selector,args,state,created_ms,updated_ms) VALUES('job-q','q','op-q','hash','q','selector','{}','queued',1,2)`)
	in := uoOperationsInput(f, UOOperationsJobs)
	in.JobID = "job-a"
	page, _, err := uoOperationsRead(t, f, r, in)
	if err != nil || len(page.Jobs) != 1 || page.Jobs[0].ID != "job-a" || page.Jobs[0].State != "queued" || page.Jobs[0].Due {
		t.Fatalf("job point %+v %v", page, err)
	}
	// Another profile's job id is not readable through this point.
	in.JobID = "job-q"
	if page, result, err := uoOperationsRead(t, f, r, in); !errors.Is(err, ErrDenied) || result.LookupStatements == 0 || !reflect.DeepEqual(page, UOOperationsPage{}) {
		t.Fatalf("foreign job point %+v %+v %v", page, result, err)
	}
	f.exec(t, `UPDATE personal_jobs SET job_next_attempt_ms=1000 WHERE id='job-a'`)
	in = uoOperationsInput(f, UOOperationsJobDue)
	due, _, err := uoOperationsRead(t, f, r, in)
	if err != nil || len(due.Jobs) != 1 || due.Jobs[0].ID != "job-a" || !due.Jobs[0].Due || !due.Jobs[0].Runnable || due.More {
		t.Fatalf("due job %+v %v", due, err)
	}
	// A second, earlier due job is the single returned candidate and is reported
	// as more work without letting the claimant choose a priority.
	f.exec(t, `INSERT INTO personal_jobs(id,profile_id,operation_id,request_hash,principal,selector,args,state,created_ms,updated_ms,job_next_attempt_ms) VALUES('job-b','p','op-b','hash','p','selector','{}','queued',1,2,500)`)
	due, _, err = uoOperationsRead(t, f, r, in)
	if err != nil || len(due.Jobs) != 1 || due.Jobs[0].ID != "job-b" || !due.More {
		t.Fatalf("due priority %+v %v", due, err)
	}
}

func TestUOOperationsSettingsAdminFenceAndPendingKeyset(t *testing.T) {
	f, r := newUOOperationsFixture(t)
	f.exec(t, `INSERT INTO console_settings_revision(singleton,revision) VALUES(1,3)`)
	f.exec(t, `INSERT INTO server_settings(singleton,updated_ms,configuration_pending_revision) VALUES(1,1234,2)`)
	for i := 1; i <= 70; i++ {
		f.exec(t, `INSERT INTO server_configuration_pending(field,revision) VALUES(?,?)`, fmt.Sprintf("field-%03d", i), int64(1))
	}
	in := uoOperationsInput(f, UOOperationsSettings)
	in.View.Limit = 30
	// The fixture principal is a member; ordinary server settings require current
	// owner/admin authority at both fences.
	if page, result, err := uoOperationsRead(t, f, r, in); !errors.Is(err, ErrDenied) || result.LookupStatements == 0 || !reflect.DeepEqual(page, UOOperationsPage{}) {
		t.Fatalf("member read settings %+v %+v %v", page, result, err)
	}
	f.exec(t, `UPDATE direct_memberships SET role='admin' WHERE account_id='a'`)
	var pending []string
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("settings cursor did not terminate")
		}
		page, _, err := uoOperationsRead(t, f, r, in)
		if err != nil {
			t.Fatal(err)
		}
		if page.Settings == nil || page.Settings.Revision != 3 || page.Settings.UpdatedMS != 1234 || page.Settings.PendingRevision != 2 {
			t.Fatalf("settings head %+v", page.Settings)
		}
		for _, value := range page.Settings.Pending {
			pending = append(pending, value.Field)
		}
		if page.Settings.Exhausted {
			break
		}
		in.After = page.Next
	}
	if len(pending) != 70 || pending[0] != "field-001" || pending[69] != "field-070" {
		t.Fatalf("settings pending %d", len(pending))
	}
}

func TestUOOperationsDeliveryLogBoundsAndNoSecrets(t *testing.T) {
	f, r := newUOOperationsFixture(t)
	f.exec(t, `INSERT INTO webhooks(id,name,url,secret,events,created_ms,updated_ms) VALUES('w','Webhook','https://secret.invalid/hook','top-secret','[]',1,1)`)
	for i := 1; i <= 70; i++ {
		state := "delivered"
		if i%3 == 0 {
			state = "pending"
		}
		f.exec(t, `INSERT INTO webhook_deliveries(id,webhook_id,type,subject,data,state,created_ms,finished_ms,status,duration_ms,error,job_revision) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			fmt.Sprintf("delivery-%03d", i), "w", "movie.added", "subject", `{"secret":"payload"}`, state, int64(i), int64(0), int64(200), int64(5), "ok", int64(1))
	}
	in := uoOperationsInput(f, UOOperationsDeliveryLog)
	in.Webhook = "w"
	in.View.Limit = 30
	var ids []string
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("delivery cursor did not terminate")
		}
		page, _, err := uoOperationsRead(t, f, r, in)
		if err != nil {
			t.Fatal(err)
		}
		if page.WebhookRevision != 1 || page.WebhookAuthority != 1 {
			t.Fatalf("delivery gate %+v", page)
		}
		for _, value := range page.Deliveries {
			if value.NextMS != 0 || value.Revision != 1 {
				t.Fatalf("nullable delivery facts %+v", value)
			}
			ids = append(ids, value.ID)
		}
		if page.Exhausted {
			break
		}
		in.After = page.Next
	}
	if len(ids) != 70 || ids[0] != "delivery-070" || ids[69] != "delivery-001" {
		t.Fatalf("delivery order %v", ids)
	}
	raw, err := json.Marshal(struct {
		Page UOOperationsPage
	}{Page: UOOperationsPage{Deliveries: []UODelivery{{ID: "delivery-001"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "https://") || strings.Contains(string(raw), "payload") {
		t.Fatalf("sensitive delivery fields escaped %s", raw)
	}
	// A hostile error text is refused by its bound instead of copied.
	f.exec(t, `UPDATE webhook_deliveries SET error=? WHERE id='delivery-070'`, strings.Repeat("e", 600))
	in = uoOperationsInput(f, UOOperationsDeliveryLog)
	in.Webhook = "w"
	if page, result, err := uoOperationsRead(t, f, r, in); !errors.Is(err, ErrUnqualified) || result.LookupStatements == 0 || !reflect.DeepEqual(page, UOOperationsPage{}) {
		t.Fatalf("hostile delivery error released %+v %+v %v", page, result, err)
	}
	// The delivery log is not readable without its exact current webhook gate.
	in.Webhook = "missing"
	if page, _, err := uoOperationsRead(t, f, r, in); !errors.Is(err, ErrDenied) || !reflect.DeepEqual(page, UOOperationsPage{}) {
		t.Fatalf("missing webhook gate %+v %v", page, err)
	}
}

func TestUOOperationsHostilePayloadAndProducerRefusal(t *testing.T) {
	f, r := newUOOperationsFixture(t)
	f.exec(t, `INSERT INTO personal_conflicts(id,profile_id,item_id,field,base_revision,base_value,resolved) VALUES('big','p',1,'watched',1,?,0)`, strings.Repeat("v", 5000))
	in := uoOperationsInput(f, UOOperationsConflicts)
	if page, result, err := uoOperationsRead(t, f, r, in); !errors.Is(err, ErrUnqualified) || result.LookupStatements == 0 || !reflect.DeepEqual(page, UOOperationsPage{}) {
		t.Fatalf("hostile conflict value released %+v %+v %v", page, result, err)
	}
	// UO-08 outcome rows have no producer yet and are refused by name.
	in = uoOperationsInput(f, UOOperationsJobs)
	in.JobID = "job-a"
	in.IncludeOutcomes = true
	page, result, err := r.UOOperationsWindow(t.Context(), foregroundRequest(), time.Second, f.output, in)
	var pending *UOProducerPending
	if !errors.As(err, &pending) || pending.Task != "UO-08" || !errors.Is(err, ErrUOSourceNotLanded) || result.LookupStatements != 0 || !reflect.DeepEqual(page, UOOperationsPage{}) {
		t.Fatalf("outcomes did not name producer %+v %+v %v", page, result, err)
	}
}

func TestUOOperationsInvalidInput(t *testing.T) {
	f, r := newUOOperationsFixture(t)
	cases := []UOOperationsInput{
		{View: func() BrowseInput { v := f.input; v.Library = 1; return v }(), Family: UOOperationsConflicts},
		{View: f.input, Family: 0},
		{View: f.input, Family: 7},
		{View: f.input, Family: UOOperationsJobs},
		{View: f.input, Family: UOOperationsDeliveryLog},
	}
	for _, in := range cases {
		in.View.Library, in.View.Kind = 0, 1
		if in.Family == UOOperationsConflicts {
			in.View.Library = 1
		}
		page, result, err := uoOperationsRead(t, f, r, in)
		if !errors.Is(err, ErrInput) || result.LookupStatements != 0 || !reflect.DeepEqual(page, UOOperationsPage{}) {
			t.Fatalf("invalid operations ran %+v %+v %v", in, page, err)
		}
	}
}
