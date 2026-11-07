package read

import (
	"testing"
)

func TestUOJobsReadyUsesLeaseDeadlineAndHeldStatus(t *testing.T) {
	f := newBrowseSource(t)
	f.exec(t, `INSERT INTO webhooks(id,name,url,secret,events,created_ms,updated_ms) VALUES('hook','Fixture','https://example.invalid','test-only','[]',1,1)`)
	f.exec(t, `INSERT INTO webhook_deliveries(id,webhook_id,type,created_ms,job_next_attempt_ms,job_lease_owner,job_lease_generation,job_lease_expires_ms) VALUES('held','hook','download.completed',1,NULL,0,0,0),('leased','hook','download.completed',1,100,7,2,500),('due','hook','download.completed',1,200,0,0,0)`)
	f.exec(t, `INSERT INTO notice_broadcast_work(scope,operation_id,digest,upper_account,upper_profile,expires_ms,job_next_attempt_ms) VALUES('scope','op','digest','','',1000,200)`)
	query := uoNativeReadQuery(t, f, uoJobReadDefinitions())
	if _, found, err := readUONextJob(t.Context(), query, uoReadyWebhook, 199); err != nil || found {
		t.Fatal(found, err)
	}
	job, found, err := readUONextJob(t.Context(), query, uoReadyWebhook, 200)
	if err != nil || !found || job.key1 != "due" || job.dueMS != 200 || job.nextMS != 200 || !job.runnable {
		t.Fatal(job, found, err)
	}
	f.exec(t, `UPDATE webhook_deliveries SET job_next_attempt_ms=NULL WHERE id='due'`)
	if _, found, err := readUONextJob(t.Context(), query, uoReadyWebhook, 499); err != nil || found {
		t.Fatal(found, err)
	}
	job, found, err = readUONextJob(t.Context(), query, uoReadyWebhook, 500)
	if err != nil || !found || job.key1 != "leased" || job.generation != 2 || job.nextMS != 100 || job.expiresMS != 500 {
		t.Fatal(job, found, err)
	}
	status, err := readUOJobStatus(t.Context(), query, uoReadyWebhook, "held", "")
	if err != nil || status.runnable || status.nextMS != 0 {
		t.Fatal(status, err)
	}
	notice, found, err := readUONextJob(t.Context(), query, uoReadyNotice, 200)
	if err != nil || !found || notice.key1 != "scope" || notice.key2 != "op" {
		t.Fatal(notice, found, err)
	}
}
