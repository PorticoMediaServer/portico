package writer

import (
	"errors"
	"strings"
	"testing"
)

func TestMPTranscodeNativeLeaseCancelRetry(t *testing.T) {
	n := mpNativeFixture(t)
	r, err := applyMPTranscodeCreate(t.Context(), n.purpose(MPTranscodeCreate), mpNativeJob())
	if err != nil {
		t.Fatal(err)
	}
	r, err = applyMPTranscodeClaim(t.Context(), n.purpose(MPTranscodeClaim), mpNativeJobFence(r), 8, 5000, "encode-1")
	if err != nil {
		t.Fatal(err)
	}
	stale := mpNativeJobFence(r)
	stale.leaseOwner = 9
	if out, e := applyMPTranscodeRenew(t.Context(), n.purpose(MPTranscodeRenew), stale, 6000); e == nil || out != (mpTranscodeEffect{}) {
		t.Fatal("wrong lease owner", out, e)
	}
	r, err = applyMPTranscodeRenew(t.Context(), n.purpose(MPTranscodeRenew), mpNativeJobFence(r), 6000)
	if err != nil {
		t.Fatal(err)
	}
	// An expired lease is not physical exit or a retry grant.
	expired := mpNativeJobFence(r)
	expired.nowMS = 7000
	if out, e := applyMPTranscodeClaim(t.Context(), n.purpose(MPTranscodeClaim), expired, 9, 8000, "encode-2"); e == nil || out != (mpTranscodeEffect{}) {
		t.Fatal("expiry reclaimed physical custody", out, e)
	}
	r, err = applyMPTranscodeCancel(t.Context(), n.purpose(MPTranscodeCancel), mpNativeJobFence(r))
	if err != nil || r.state != "cancelling" || r.stopExecution != "encode-1" || !r.cleanupPending {
		t.Fatal(r, err)
	}
	var exited, cleaned int64
	if err = n.conn.QueryRowContext(t.Context(), "SELECT physical_exited,cleaned FROM mp_job_custody WHERE job_id='job'").Scan(&exited, &cleaned); err != nil || exited != 0 || cleaned != 0 {
		t.Fatal("cancel returned permits", exited, cleaned, err)
	}
	f := mpNativeJobFence(r)
	wrong := mpTranscodeCleanup{execution: "encode-1", generation: r.generation, leaseOwner: r.leaseOwner, leaseGeneration: r.leaseGeneration + 1, exitDigest: strings.Repeat("f", 64), finished: true, retryAtMS: -1}
	if out, e := applyMPTranscodeReconcile(t.Context(), n.purpose(MPTranscodeReconcile), f, wrong); !errors.Is(e, ErrInvalid) || out != (mpTranscodeEffect{}) {
		t.Fatal("wrong exit generation", out, e)
	}
	wrong.leaseGeneration = r.leaseGeneration
	wrong.nextCursor = 65
	if out, e := applyMPTranscodeReconcile(t.Context(), n.purpose(MPTranscodeReconcile), f, wrong); !errors.Is(e, ErrInvalid) || out != (mpTranscodeEffect{}) {
		t.Fatal("unbounded cleanup", out, e)
	}
	wrong.nextCursor = 1
	r, err = applyMPTranscodeReconcile(t.Context(), n.purpose(MPTranscodeReconcile), f, wrong)
	if err != nil || r.state != "cancelled" || r.cleanupPending || r.cleanedGeneration != r.generation || r.leaseOwner != 0 {
		t.Fatal(r, err)
	}
}
func TestMPTranscodeNativeRetryRequiresCleanedExit(t *testing.T) {
	n := mpNativeFixture(t)
	r, err := applyMPTranscodeCreate(t.Context(), n.purpose(MPTranscodeCreate), mpNativeJob())
	if err != nil {
		t.Fatal(err)
	}
	r, err = applyMPTranscodeClaim(t.Context(), n.purpose(MPTranscodeClaim), mpNativeJobFence(r), 8, 5000, "encode-1")
	if err != nil {
		t.Fatal(err)
	}
	c := mpTranscodeCleanup{execution: r.execution, generation: r.generation, leaseOwner: r.leaseOwner, leaseGeneration: r.leaseGeneration, exitDigest: strings.Repeat("f", 64), nextCursor: 1, retryAtMS: -1}
	r, err = applyMPTranscodeReconcile(t.Context(), n.purpose(MPTranscodeReconcile), mpNativeJobFence(r), c)
	if err != nil || !r.cleanupPending {
		t.Fatal(r, err)
	}
	if out, e := applyMPTranscodeClaim(t.Context(), n.purpose(MPTranscodeClaim), mpNativeJobFence(r), 9, 6000, "encode-2"); e == nil || out != (mpTranscodeEffect{}) {
		t.Fatal("partial cleanup retried", out, e)
	}
	c.cursor = 1
	c.nextCursor = 2
	c.finished = true
	c.retryAtMS = 300
	r, err = applyMPTranscodeReconcile(t.Context(), n.purpose(MPTranscodeReconcile), mpNativeJobFence(r), c)
	if err != nil || r.state != "queued" || r.nextAttemptMS != 300 {
		t.Fatal(r, err)
	}
	f := mpNativeJobFence(r)
	f.nowMS = 300
	r, err = applyMPTranscodeClaim(t.Context(), n.purpose(MPTranscodeClaim), f, 9, 6000, "encode-2")
	if err != nil || r.generation != 2 || r.leaseGeneration != 2 || r.attempts != 2 {
		t.Fatal(r, err)
	}
}
func TestMPTranscodeNativePublishPinsRetire(t *testing.T) {
	n := mpNativeFixture(t)
	r, err := applyMPTranscodeCreate(t.Context(), n.purpose(MPTranscodeCreate), mpNativeJob())
	if err != nil {
		t.Fatal(err)
	}
	r, err = applyMPTranscodeClaim(t.Context(), n.purpose(MPTranscodeClaim), mpNativeJobFence(r), 8, 5000, "encode-1")
	if err != nil {
		t.Fatal(err)
	}
	f := mpNativeJobFence(r)
	artifact := mpTranscodeVerifiedArtifact{versionIncarnation: [16]byte{7}, job: r.job, execution: r.execution, version: "version", digest: strings.Repeat("d", 64), inputEvidence: "validated-source", transformation: mpDraftRecipe().transformation, facts: "{\"validated\":true}", generation: r.generation, leaseOwner: r.leaseOwner, leaseGeneration: r.leaseGeneration, size: 123, source: mpDraftSource(), recipe: mpDraftRecipe()}
	bad := artifact
	bad.source.revision = "different"
	if out, e := applyMPTranscodePublish(t.Context(), n.purpose(MPTranscodePublish), f, bad); e == nil || out != (mpTranscodeEffect{}) {
		t.Fatal("source mismatch", out, e)
	}
	bad = artifact
	bad.versionIncarnation = r.rootIncarnation
	if out, e := applyMPTranscodePublish(t.Context(), n.purpose(MPTranscodePublish), f, bad); !errors.Is(e, ErrInvalid) || out != (mpTranscodeEffect{}) {
		t.Fatal("borrowed job incarnation", out, e)
	}
	r, err = applyMPTranscodePublish(t.Context(), n.purpose(MPTranscodePublish), f, artifact)
	if err != nil || r.state != "succeeded" || r.version != "version" || !r.cleanupPending || r.leaseOwner != 8 {
		t.Fatal(r, err)
	}
	c := mpTranscodeCleanup{execution: r.execution, generation: r.generation, leaseOwner: r.leaseOwner, leaseGeneration: r.leaseGeneration, exitDigest: strings.Repeat("f", 64), nextCursor: 1, finished: true, retryAtMS: -1}
	r, err = applyMPTranscodeReconcile(t.Context(), n.purpose(MPTranscodeReconcile), mpNativeJobFence(r), c)
	if err != nil {
		t.Fatal(err)
	}
	n.exec("INSERT INTO playback_sessions VALUES('reader'); INSERT INTO prepared_media_session_pins VALUES('reader','version','" + artifact.digest + "',123)")
	v := mpTranscodeVersionFence{version: "version", cleanupRequestID: "delete-version-request", digest: artifact.digest, size: 123, revision: 1}
	if out, e := applyMPTranscodeRetire(t.Context(), n.purpose(MPTranscodeRetire), mpNativeJobFence(r), v); e == nil || out != (mpTranscodeRetirementEffect{}) {
		t.Fatal("retired pinned version", out, e)
	}
	n.exec("DELETE FROM prepared_media_session_pins WHERE session_id='reader'")
	retired, err := applyMPTranscodeRetire(t.Context(), n.purpose(MPTranscodeRetire), mpNativeJobFence(r), v)
	if err != nil || retired.job.cleanupPending || retired.job.versionState != "deleting" || retired.cleanupRequestID != v.cleanupRequestID || retired.custodyVariableBytes <= 0 {
		t.Fatal(retired, err)
	}
	r = retired.job
	var exited, cleaned, finished, generation int64
	var request, execution string
	if err = n.conn.QueryRowContext(t.Context(), "SELECT physical_exited,cleaned FROM mp_job_custody WHERE job_id='job'").Scan(&exited, &cleaned); err != nil || exited != 1 || cleaned != 1 {
		t.Fatal("745 reopened encode custody", exited, cleaned, err)
	}
	if err = n.conn.QueryRowContext(t.Context(), "SELECT request_id,generation,execution_id,finished FROM mp_version_custody WHERE version_id='version'").Scan(&request, &generation, &execution, &finished); err != nil || request != v.cleanupRequestID || generation != 0 || execution != "" || finished != 0 {
		t.Fatal(request, generation, execution, finished, err)
	}
	c.version = v
	c.version.revision = 2
	c.leaseOwner = 0
	if out, e := applyMPTranscodeReconcile(t.Context(), n.purpose(MPTranscodeReconcile), mpNativeJobFence(r), c); !errors.Is(e, ErrInvalid) || out != (mpTranscodeEffect{}) {
		t.Fatal("746 accepted version work", out, e)
	}
	var state string
	if err = n.conn.QueryRowContext(t.Context(), "SELECT state FROM prepared_media_versions WHERE id='version'").Scan(&state); err != nil || state != "deleting" {
		t.Fatal(state, err)
	}
}
