package writer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Group D is embedded, so the page statements are qualified against the
// canonical raw acquisition key and person source owner.
func ipRefreshPageFixture(t *testing.T) (*executor, *ipPolicyRegistry, *usOwnerAuthorityProof) {
	t.Helper()
	w, keeper, p := ipPolicySecurityFixture(t)
	for _, query := range []string{
		`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(777,'ip-library','/media')`,
		`INSERT INTO metadata_providers(code,adapter,enabled) VALUES('tmdb','tmdb',1),('tvdb','tvdb',1),('musicbrainz','musicbrainz',1)`,
		`INSERT INTO metadata_provider_policy(library_id,provider,incarnation,revision,enabled,priority,region,algorithm,refresh_mode,credit_enabled,credit_after_item,credit_after_provider,command_incarnation) VALUES(777,'tmdb',X'09000000000000000000000000000000',1,1,5,'US','v1','fill_missing',1,NULL,NULL,X'01000000000000000000000000000000'),(777,'musicbrainz',X'0a000000000000000000000000000000',1,1,5,'US','v1','fill_missing',1,NULL,NULL,X'01000000000000000000000000000000')`,
		`INSERT INTO screen_metadata_consent(singleton,revision,confirmed) VALUES(1,1,1) ON CONFLICT(singleton) DO UPDATE SET revision=1,confirmed=1`,
		`INSERT INTO metadata_runtime_settings(singleton,online_lookups,lookup_epoch) VALUES(1,1,3) ON CONFLICT(singleton) DO UPDATE SET online_lookups=1,lookup_epoch=3`,
	} {
		if _, err := w.db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(query, err)
		}
	}
	defs := append(ipPolicyDefinitions(), ipRefreshPageDefinitions()...)
	r, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	return w, &ipPolicyRegistry{r, keeper}, p
}

func ipRefreshPageBegin(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, IPRefreshBegin)
	if out, err := applyIPRefreshBegin(t.Context(), turn, p, t.Context(), b, ipRefreshTestCommand(t, 1, 2, nil)); err != nil || out.Outcome.State != Applied {
		t.Fatal("begin", out, err)
	}
	ipPolicyCommitTurn(t, turn, tx)
	ipRefreshPageAck(t, w, 1)
}

func ipRefreshPageAck(t *testing.T, w *executor, sequence int64) {
	t.Helper()
	ipPolicyExec(t, w, `UPDATE metadata_refresh_sweeps SET acknowledged_command_sequence=? WHERE provider='tmdb' AND purpose='metadata'`, sequence)
}

func ipRefreshPageTestCommand(t *testing.T, sequence int64, operation byte, mutate func(*ipRefreshPageRequest)) *ipRefreshPageCommand {
	t.Helper()
	q := ipRefreshPageRequest{provider: "tmdb", purpose: "metadata", expectedGeneration: 1, expectedRevision: 1, consentRevision: 1}
	if mutate != nil {
		mutate(&q)
	}
	c, err := newIPRefreshPageCommand(ipPolicyIdentity(sequence, operation), q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ipRefreshPageApply(t *testing.T, w *executor, r *ipPolicyRegistry, p *usOwnerAuthorityProof, c *ipRefreshPageCommand) (ipRefreshPageCommandResult, *commandTurn, *transaction, ipPolicyBudget) {
	t.Helper()
	turn, tx, b := ipPolicyTurn(t, w, r, IPRefreshPage)
	out, err := applyIPRefreshPage(t.Context(), turn, p, t.Context(), b, c)
	if err != nil {
		t.Fatal("page", out, err)
	}
	return out, turn, tx, b
}

func ipRefreshPageEntity(t *testing.T, w *executor, id int64) {
	t.Helper()
	public := make([]byte, 16)
	public[8], public[9] = byte(id>>8), byte(id)
	inc := []byte{0x0b, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	ipPolicyExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,777,1)`, id, public)
	ipPolicyExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,source_revision,identity_revision,metadata_revision) VALUES(?,?,777,1,?,'s','s','s','s',1,?,?,1,1,1)`, id, public, "Entity", inc, inc)
}

type ipRefreshPageJobOptions struct {
	status, selected, lease string
	nextAttempt, leaseUntil int64
}

func ipRefreshPageJob(t *testing.T, w *executor, id int64, provider, purpose string, o ipRefreshPageJobOptions) {
	t.Helper()
	ipRefreshPageEntity(t, w, id)
	if o.status == "" {
		o.status = "matched"
	}
	if o.selected == "" {
		o.selected = "provider-" + strings.Repeat("x", 4)
	}
	ipPolicyExec(t, w, `INSERT INTO metadata_acquisition_jobs(entity_id,provider,purpose,generation,revision,status,selected_id,manual,review_search,query_override,year_override,force_refresh,episode_order,parent_publication,attempts,next_attempt_ms,lease,lease_until_ms,error,page,after_item,staged_bytes,observed_at) VALUES(?,?,?,1,1,?,?,0,0,'',0,0,'','',0,?,?,?,'',0,'',0,'2026-01-01T00:00:00.000000000Z')`, id, provider, purpose, o.status, o.selected, o.nextAttempt, o.lease, o.leaseUntil)
}

func ipRefreshPageAddJobs(t *testing.T, w *executor, first, last int64, provider, purpose string, o ipRefreshPageJobOptions) {
	t.Helper()
	for id := first; id <= last; id++ {
		ipRefreshPageJob(t, w, id, provider, purpose, o)
	}
}

func ipRefreshPageCommit(t *testing.T, turn *commandTurn, tx *transaction) {
	t.Helper()
	ipPolicyCommitTurn(t, turn, tx)
}

func TestIPRefreshPageEntitiesRawKeysetSchedulesBoundedPage(t *testing.T) {
	w, r, p := ipRefreshPageFixture(t)
	ipRefreshPageAddJobs(t, w, 1, 18, "tmdb", "metadata", ipRefreshPageJobOptions{})
	ipRefreshPageBegin(t, w, r, p)

	out, turn, tx, _ := ipRefreshPageApply(t, w, r, p, ipRefreshPageTestCommand(t, 2, 3, nil))
	if out.Outcome.State != Applied || out.Outcome.Replayed || out.Value.Phase != "entities" ||
		out.Value.AfterID != 16 || out.Value.ScheduledCount != 16 || !out.Value.PageWaiting ||
		out.Value.AcceptedRevision != 2 || out.Digest == (ResultDigest{}) {
		t.Fatal(out)
	}
	ipRefreshPageCommit(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_acquisition_jobs WHERE status='pending' AND force_refresh=1`:         16,
		`SELECT count(*) FROM metadata_acquisition_jobs WHERE entity_id>16 AND status='matched'`:            2,
		`SELECT after_id FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`:         16,
		`SELECT page_from_id FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`:     0,
		`SELECT page_until_id FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`:    16,
		`SELECT page_waiting FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`:     1,
		`SELECT count(*) FROM metadata_refresh_receipts WHERE command_kind='page' AND scheduled_count=16`:   1,
		`SELECT count(*) FROM metadata_acquisition_jobs WHERE observed_at='2026-01-01T00:00:00.000000000Z'`: 18,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
	// Replay after unrelated job changes returns the same saved page and writes
	// nothing new.
	ipPolicyExec(t, w, `UPDATE metadata_acquisition_jobs SET status='matched' WHERE entity_id=1`)
	turn, tx, b := ipPolicyTurn(t, w, r, IPRefreshPage)
	replay, err := applyIPRefreshPage(t.Context(), turn, p, t.Context(), b, ipRefreshPageTestCommand(t, 2, 3, nil))
	if err != nil || !replay.Outcome.Replayed || replay.Value != out.Value || replay.Digest != out.Digest {
		t.Fatal("page replay", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_refresh_receipts`); got != 2 {
		t.Fatal("replay wrote receipts", got)
	}
	// At or below ACK is Retired even while the receipt row remains physical.
	ipPolicyExec(t, w, `UPDATE metadata_refresh_sweeps SET acknowledged_command_sequence=2 WHERE provider='tmdb' AND purpose='metadata'`)
	turn, tx, b = ipPolicyTurn(t, w, r, IPRefreshPage)
	retired, err := applyIPRefreshPage(t.Context(), turn, p, t.Context(), b, ipRefreshPageTestCommand(t, 2, 3, nil))
	if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (ipRefreshPageResult{}) || retired.Digest != (ResultDigest{}) {
		t.Fatal("retired page result served", retired, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
}

// A completed sweep leaves the due index: IP-03's begin arms
// job_next_attempt_ms, and only the completing people page disarms it. Pages
// that still have work must keep the sweep selectable.
func TestIPRefreshPageCompletionLeavesDueIndex(t *testing.T) {
	w, r, p := ipRefreshPageFixture(t)
	ipRefreshPageAddJobs(t, w, 1, 2, "tmdb", "metadata", ipRefreshPageJobOptions{})
	ipRefreshPageBegin(t, w, r, p)
	due := `SELECT count(*) FROM metadata_refresh_sweeps INDEXED BY metadata_refresh_sweep_due WHERE job_due_ms IS NOT NULL AND job_due_ms<=?`
	if got := ipPolicyQueryInt(t, w, due, int64(1<<50)); got != 1 {
		t.Fatal("begin did not arm the due index", got)
	}
	// The first page schedules the whole raw window and must stay due.
	first, turn, tx, _ := ipRefreshPageApply(t, w, r, p, ipRefreshPageTestCommand(t, 2, 3, nil))
	if first.Value.Phase != "entities" || first.Value.AfterID != 2 || first.Value.ScheduledCount != 2 || !first.Value.PageWaiting {
		t.Fatal(first)
	}
	ipRefreshPageCommit(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, due, int64(1<<50)); got != 1 {
		t.Fatal("non-final entity page left the due index", got)
	}
	// Once its window clears, the next page only transitions to people and is
	// still not final.
	ipPolicyExec(t, w, `UPDATE metadata_acquisition_jobs SET status='done'`)
	ipRefreshPageAck(t, w, 2)
	transition, turn, tx, _ := ipRefreshPageApply(t, w, r, p, ipRefreshPageTestCommand(t, 3, 4, func(q *ipRefreshPageRequest) { q.expectedRevision = 2 }))
	if transition.Value.Phase != "people" || transition.Value.AfterID != 0 {
		t.Fatal(transition)
	}
	ipRefreshPageCommit(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, due, int64(1<<50)); got != 1 {
		t.Fatal("entity->people transition left the due index", got)
	}
	// No accepted person sources exist, so this page finishes the sweep.
	ipRefreshPageAck(t, w, 3)
	complete, turn, tx, _ := ipRefreshPageApply(t, w, r, p, ipRefreshPageTestCommand(t, 4, 5, func(q *ipRefreshPageRequest) { q.expectedRevision = 3 }))
	if complete.Value.Phase != "complete" {
		t.Fatal(complete)
	}
	ipRefreshPageCommit(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata' AND job_next_attempt_ms IS NULL AND job_due_ms IS NULL`:                      1,
		`SELECT count(*) FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata' AND job_lease_owner=0 AND job_lease_generation=0 AND job_lease_expires_ms=0`: 1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
	if got := ipPolicyQueryInt(t, w, due, int64(1<<50)); got != 0 {
		t.Fatal("finished sweep stayed selectable through the due index", got)
	}
}

// A provider-wide backoff holds the page whole: no raw key is consumed, no
// cursor or receipt is written, and the same keys are scheduled by the retried
// page once metadata_providers.next_attempt_ms has passed.
func TestIPRefreshPageProviderBackoffHoldsRawKeys(t *testing.T) {
	w, r, p := ipRefreshPageFixture(t)
	ipRefreshPageAddJobs(t, w, 1, 3, "tmdb", "metadata", ipRefreshPageJobOptions{})
	ipRefreshPageBegin(t, w, r, p)
	ipPolicyExec(t, w, `UPDATE metadata_providers SET next_attempt_ms=? WHERE code='tmdb'`, int64(1<<50))
	turn, tx, b := ipPolicyTurn(t, w, r, IPRefreshPage)
	out, err := applyIPRefreshPage(t.Context(), turn, p, t.Context(), b, ipRefreshPageTestCommand(t, 2, 3, nil))
	if out != (ipRefreshPageCommandResult{}) || !errors.Is(err, ErrConflict) || !errors.Is(err, errIPRefreshPageBackoff) {
		t.Fatal("backoff page was not held", out, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT after_id FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`: 0,
		`SELECT revision FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`: 1,
		`SELECT count(*) FROM metadata_acquisition_jobs WHERE status='pending'`:                     0,
		`SELECT count(*) FROM metadata_acquisition_jobs WHERE status='matched'`:                     3,
		`SELECT count(*) FROM metadata_refresh_receipts WHERE command_kind='page'`:                  0,
		`SELECT count(*) FROM metadata_refresh_receipts`:                                            1,
		`SELECT count(*) FROM metadata_refresh_sweeps WHERE job_due_ms IS NOT NULL`:                 1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
	// The same command retries after the backoff and schedules the same keys.
	ipPolicyExec(t, w, `UPDATE metadata_providers SET next_attempt_ms=0 WHERE code='tmdb'`)
	retried, turn, tx, _ := ipRefreshPageApply(t, w, r, p, ipRefreshPageTestCommand(t, 2, 3, nil))
	if retried.Value.Phase != "entities" || retried.Value.AfterID != 3 || retried.Value.ScheduledCount != 3 || !retried.Value.PageWaiting {
		t.Fatal("retried page", retried)
	}
	ipRefreshPageCommit(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_acquisition_jobs WHERE status='pending' AND force_refresh=1`); got != 3 {
		t.Fatal("retried page did not schedule the same keys", got)
	}
}

func TestIPRefreshPageAllExcludedStillAdvancesRawCursor(t *testing.T) {
	w, r, p := ipRefreshPageFixture(t)
	// A live lease excludes each raw key without changing the keyset order.
	ipRefreshPageAddJobs(t, w, 1, 18, "tmdb", "metadata", ipRefreshPageJobOptions{lease: "worker-7", leaseUntil: 1 << 50})
	ipRefreshPageBegin(t, w, r, p)
	out, turn, tx, _ := ipRefreshPageApply(t, w, r, p, ipRefreshPageTestCommand(t, 2, 3, nil))
	if out.Value.Phase != "entities" || out.Value.AfterID != 16 || out.Value.ScheduledCount != 0 || out.Value.PageWaiting {
		t.Fatal(out)
	}
	ipRefreshPageCommit(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_acquisition_jobs WHERE status='pending'`:                          0,
		`SELECT after_id FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`:      16,
		`SELECT page_waiting FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`:  0,
		`SELECT count(*) FROM metadata_refresh_receipts WHERE command_kind='page' AND scheduled_count=0`: 1,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
}

func TestIPRefreshPageStaleGenerationRevisionAndIdentity(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ipRefreshPageRequest)
		seq    int64
		op     byte
	}{
		{"stale_generation", func(q *ipRefreshPageRequest) { q.expectedGeneration = 2 }, 2, 3},
		{"stale_revision", func(q *ipRefreshPageRequest) { q.expectedRevision = 2 }, 2, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, r, p := ipRefreshPageFixture(t)
			ipRefreshPageAddJobs(t, w, 1, 2, "tmdb", "metadata", ipRefreshPageJobOptions{})
			ipRefreshPageBegin(t, w, r, p)
			turn, tx, b := ipPolicyTurn(t, w, r, IPRefreshPage)
			out, err := applyIPRefreshPage(t.Context(), turn, p, t.Context(), b, ipRefreshPageTestCommand(t, tc.seq, tc.op, tc.mutate))
			if out != (ipRefreshPageCommandResult{}) || !errors.Is(err, ErrConflict) {
				t.Fatal("stale page admitted", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
			if got := ipPolicyQueryInt(t, w, `SELECT after_id FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`); got != 0 {
				t.Fatal("stale page moved cursor", got)
			}
			if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_refresh_receipts`); got != 1 {
				t.Fatal("stale page wrote receipt", got)
			}
		})
	}
	t.Run("wrong_incarnation", func(t *testing.T) {
		w, r, p := ipRefreshPageFixture(t)
		ipRefreshPageBegin(t, w, r, p)
		cmd, err := newIPRefreshPageCommand(CommandIdentity{Incarnation: [16]byte{9}, Sequence: 2, OperationID: [16]byte{3}}, ipRefreshPageRequest{provider: "tmdb", purpose: "metadata", expectedGeneration: 1, expectedRevision: 1, consentRevision: 1})
		if err != nil {
			t.Fatal(err)
		}
		turn, tx, b := ipPolicyTurn(t, w, r, IPRefreshPage)
		out, err := applyIPRefreshPage(t.Context(), turn, p, t.Context(), b, cmd)
		if out != (ipRefreshPageCommandResult{}) || !errors.Is(err, ErrResolveRequired) {
			t.Fatal("foreign incarnation admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
}

func TestIPRefreshPageAllowanceRace(t *testing.T) {
	w, r, p := ipRefreshPageFixture(t)
	ipRefreshPageAddJobs(t, w, 1, 4, "tmdb", "metadata", ipRefreshPageJobOptions{})
	ipRefreshPageBegin(t, w, r, p)
	first := ipRefreshPageTestCommand(t, 2, 3, nil)
	out, turn, tx, _ := ipRefreshPageApply(t, w, r, p, first)
	if out.Value.ScheduledCount != 4 {
		t.Fatal(out)
	}
	ipRefreshPageCommit(t, turn, tx)
	// A second caller built against the same allowance but a different operation
	// can neither replay nor consume a second page.
	second := ipRefreshPageTestCommand(t, 2, 4, nil)
	turn, tx, b := ipPolicyTurn(t, w, r, IPRefreshPage)
	raced, err := applyIPRefreshPage(t.Context(), turn, p, t.Context(), b, second)
	if raced != (ipRefreshPageCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("spent page allowance admitted a second page", raced, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	// A later sequence against the stale expected revision is refused too.
	ipRefreshPageAck(t, w, 2)
	third := ipRefreshPageTestCommand(t, 3, 5, nil)
	turn, tx, b = ipPolicyTurn(t, w, r, IPRefreshPage)
	late, err := applyIPRefreshPage(t.Context(), turn, p, t.Context(), b, third)
	if late != (ipRefreshPageCommandResult{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("revision race admitted", late, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_refresh_receipts WHERE command_kind='page'`); got != 1 {
		t.Fatal("race wrote extra page receipt", got)
	}
}

func TestIPRefreshPageWaitsForPreviousPage(t *testing.T) {
	w, r, p := ipRefreshPageFixture(t)
	ipRefreshPageAddJobs(t, w, 1, 3, "tmdb", "metadata", ipRefreshPageJobOptions{})
	ipRefreshPageBegin(t, w, r, p)
	first, turn, tx, _ := ipRefreshPageApply(t, w, r, p, ipRefreshPageTestCommand(t, 2, 3, nil))
	if first.Value.ScheduledCount != 3 || !first.Value.PageWaiting {
		t.Fatal(first)
	}
	ipRefreshPageCommit(t, turn, tx)
	ipRefreshPageAck(t, w, 2)
	// Jobs are still pending, so the next allowance must not advance the raw
	// cursor into the people phase.
	waiting, turn, tx, _ := ipRefreshPageApply(t, w, r, p, ipRefreshPageTestCommand(t, 3, 4, func(q *ipRefreshPageRequest) { q.expectedRevision = 2 }))
	if waiting.Value.Phase != "entities" || waiting.Value.AfterID != 3 || waiting.Value.ScheduledCount != 0 || !waiting.Value.PageWaiting {
		t.Fatal("page advanced while its predecessor was still in flight", waiting)
	}
	ipRefreshPageCommit(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_refresh_receipts WHERE command_kind='page' AND phase='entities' AND scheduled_count=0 AND page_waiting=1`); got != 1 {
		t.Fatal("waiting receipt", got)
	}
}

func TestIPRefreshPageEntityToPeopleTransitionAndProviderKeyset(t *testing.T) {
	w, r, p := ipRefreshPageFixture(t)
	ipRefreshPageAddJobs(t, w, 1, 3, "tmdb", "metadata", ipRefreshPageJobOptions{})
	ipRefreshPageBegin(t, w, r, p)
	first, turn, tx, _ := ipRefreshPageApply(t, w, r, p, ipRefreshPageTestCommand(t, 2, 3, nil))
	if first.Value.AfterID != 3 || first.Value.ScheduledCount != 3 || !first.Value.PageWaiting {
		t.Fatal(first)
	}
	ipRefreshPageCommit(t, turn, tx)
	ipRefreshPageAck(t, w, 2)
	// The workers finish the page, so the waiting window clears.
	ipPolicyExec(t, w, `UPDATE metadata_acquisition_jobs SET status='done'`)
	transition, turn, tx, _ := ipRefreshPageApply(t, w, r, p, ipRefreshPageTestCommand(t, 3, 4, func(q *ipRefreshPageRequest) { q.expectedRevision = 2 }))
	if transition.Value.Phase != "people" || transition.Value.AfterID != 0 || transition.Value.ScheduledCount != 0 ||
		transition.Value.PageWaiting {
		t.Fatal(transition)
	}
	ipRefreshPageCommit(t, turn, tx)
	for query, want := range map[string]int64{
		`SELECT after_id FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`:      0,
		`SELECT page_from_id FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`:  0,
		`SELECT page_until_id FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`: 0,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}
	if got := ipPolicyString(t, w, `SELECT phase FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`); got != "people" {
		t.Fatal("phase", got)
	}
	for id := int64(5); id <= 22; id++ {
		inc := []byte{byte(id), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
		ipPolicyExec(t, w, `INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation) VALUES(?,?,?,?,?,?)`, id, "p"+string(rune('a'+id)), "k"+string(rune('a'+id)), "Person", "Person", inc)
		ipPolicyExec(t, w, `INSERT INTO metadata_person_sources(person_id,provider,role,provider_id,incarnation,revision) VALUES(?,'tmdb','actor',?,?,1)`, id, "tmdb-"+string(rune('a'+id)), inc)
	}
	ipPolicyExec(t, w, `INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation) VALUES(30,'p30','k30','Other','Other',X'1e000000000000000000000000000000')`)
	ipPolicyExec(t, w, `INSERT INTO metadata_person_sources(person_id,provider,role,provider_id,incarnation,revision) VALUES(30,'tvdb','actor','tvdb-30',X'1e000000000000000000000000000000',1)`)
	ipRefreshPageAck(t, w, 3)
	people, turn, tx, _ := ipRefreshPageApply(t, w, r, p, ipRefreshPageTestCommand(t, 4, 5, func(q *ipRefreshPageRequest) { q.expectedRevision = 3 }))
	if people.Value.Phase != "people" || people.Value.AfterID != 20 || people.Value.ScheduledCount != 0 || people.Value.PageWaiting {
		t.Fatal("people page", people)
	}
	ipRefreshPageCommit(t, turn, tx)
	// One transition receipt (after_id reset) plus this people page.
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_refresh_receipts WHERE command_kind='page' AND phase='people'`); got != 2 {
		t.Fatal("people receipt", got)
	}
	ipRefreshPageAck(t, w, 4)
	complete, turn, tx, _ := ipRefreshPageApply(t, w, r, p, ipRefreshPageTestCommand(t, 5, 6, func(q *ipRefreshPageRequest) { q.expectedRevision = 4 }))
	if complete.Value.Phase != "complete" || complete.Value.AfterID != 22 || complete.Value.ScheduledCount != 0 {
		t.Fatal("complete page", complete)
	}
	ipRefreshPageCommit(t, turn, tx)
	if got := ipPolicyString(t, w, `SELECT phase FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`); got != "complete" {
		t.Fatal("complete phase", got)
	}
}

func ipPolicyString(t *testing.T, w *executor, query string, args ...any) string {
	t.Helper()
	var s string
	if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(&s); err != nil {
		t.Fatal(query, err)
	}
	return s
}

// A page command that reaches a sweep which is ALREADY complete is a truthful
// terminal page. The completion CAS requires phase='people', so reusing it here
// would match no row (and a second completion would bump the terminal revision
// again). The terminal path consumes only its own command sequence, leaves the
// sweep revision, phase, cursor and job lease exactly as saved, and records the
// one page receipt that typed replay reads.
func TestIPRefreshPageAlreadyCompleteIsTruthfulAndInert(t *testing.T) {
	w, r, p := ipRefreshPageFixture(t)
	ipRefreshPageBegin(t, w, r, p)

	// No entity jobs, so the entity page transitions straight to people.
	people, turn, tx, _ := ipRefreshPageApply(t, w, r, p, ipRefreshPageTestCommand(t, 2, 3, nil))
	if people.Value.Phase != "people" || people.Value.AcceptedRevision != 2 {
		t.Fatal("entity page", people)
	}
	ipRefreshPageCommit(t, turn, tx)
	ipRefreshPageAck(t, w, 2)

	// No person sources, so the people page completes the sweep.
	complete, turn, tx, _ := ipRefreshPageApply(t, w, r, p,
		ipRefreshPageTestCommand(t, 3, 4, func(q *ipRefreshPageRequest) { q.expectedRevision = 2 }))
	if complete.Value.Phase != "complete" || complete.Value.AcceptedRevision != 3 {
		t.Fatal("completing page", complete)
	}
	ipRefreshPageCommit(t, turn, tx)
	ipRefreshPageAck(t, w, 3)

	cmd := ipRefreshPageTestCommand(t, 4, 5, func(q *ipRefreshPageRequest) { q.expectedRevision = 3 })
	terminal, turn, tx, _ := ipRefreshPageApply(t, w, r, p, cmd)
	if terminal.Outcome.State != Applied || terminal.Outcome.Replayed ||
		terminal.Value.Phase != "complete" || terminal.Value.AcceptedRevision != 3 ||
		terminal.Value.AfterID != 0 || terminal.Value.ScheduledCount != 0 || terminal.Value.PageWaiting {
		t.Fatal("already-complete page", terminal)
	}
	ipRefreshPageCommit(t, turn, tx)
	if got := ipPolicyString(t, w, `SELECT phase FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`); got != "complete" {
		t.Fatal("terminal phase", got)
	}
	for query, want := range map[string]int64{
		`SELECT revision FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`:              3,
		`SELECT after_id FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`:              0,
		`SELECT last_command_sequence FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`: 4,
		`SELECT count(*) FROM metadata_refresh_receipts WHERE command_kind='page' AND phase='complete'`:          2,
		`SELECT count(*) FROM metadata_acquisition_jobs WHERE status='pending'`:                                  0,
	} {
		if got := ipPolicyQueryInt(t, w, query); got != want {
			t.Fatal(query, got)
		}
	}

	// The saved terminal result replays exactly with no new receipt.
	turn, tx, b := ipPolicyTurn(t, w, r, IPRefreshPage)
	replay, err := applyIPRefreshPage(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || !replay.Outcome.Replayed || replay.Value != terminal.Value ||
		replay.Digest != terminal.Digest {
		t.Fatal("terminal page replay", replay, err)
	}
	ipPolicyRollbackTurn(t, turn, tx)
	if got := ipPolicyQueryInt(t, w, `SELECT count(*) FROM metadata_refresh_receipts WHERE command_kind='page'`); got != 3 {
		t.Fatal("replay wrote a receipt", got)
	}
}

func TestIPRefreshPageCurrentAuthorityAndLifetime(t *testing.T) {
	for _, mode := range []string{"revoked_family", "forged_proof", "revoked_membership"} {
		t.Run(mode, func(t *testing.T) {
			w, r, p := ipRefreshPageFixture(t)
			ipRefreshPageAddJobs(t, w, 1, 2, "tmdb", "metadata", ipRefreshPageJobOptions{})
			ipRefreshPageBegin(t, w, r, p)
			proof := ipPolicyRevoke(t, w, p, mode)
			turn, tx, b := ipPolicyTurn(t, w, r, IPRefreshPage)
			out, err := applyIPRefreshPage(t.Context(), turn, proof, t.Context(), b, ipRefreshPageTestCommand(t, 2, 3, nil))
			if out != (ipRefreshPageCommandResult{}) || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("stale current authority served the page", out, err)
			}
			ipPolicyRollbackTurn(t, turn, tx)
		})
	}
	t.Run("cancelled_lifetime", func(t *testing.T) {
		w, r, p := ipRefreshPageFixture(t)
		ipRefreshPageBegin(t, w, r, p)
		lifetime, cancel := context.WithCancel(t.Context())
		cancel()
		turn, tx, b := ipPolicyTurn(t, w, r, IPRefreshPage)
		out, err := applyIPRefreshPage(t.Context(), turn, p, lifetime, b, ipRefreshPageTestCommand(t, 2, 3, nil))
		if out != (ipRefreshPageCommandResult{}) || err == nil {
			t.Fatal("cancelled lifetime admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
}

func TestIPRefreshPageUnknownCommitFreshRead(t *testing.T) {
	w, r, p := ipRefreshPageFixture(t)
	ipRefreshPageAddJobs(t, w, 1, 2, "tmdb", "metadata", ipRefreshPageJobOptions{})
	ipRefreshPageBegin(t, w, r, p)
	path := ipPolicyDatabasePath(t, w)
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	installFaultPool(w, &faultConnector{Connector: connector, afterCommit: true, replyError: true})
	turn, tx, b := ipPolicyTurn(t, w, r, IPRefreshPage)
	cmd := ipRefreshPageTestCommand(t, 2, 3, nil)
	out, err := applyIPRefreshPage(t.Context(), turn, p, t.Context(), b, cmd)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	result, err := tx.commit()
	if result.state != commitUnknown || !errors.Is(err, ErrResolveRequired) {
		t.Fatal(result, err)
	}
	if again, e := applyIPRefreshPage(t.Context(), turn, p, t.Context(), b, cmd); again != (ipRefreshPageCommandResult{}) || e == nil {
		t.Fatal("uncertain frame reserved the saved page", again, e)
	}
	if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM metadata_refresh_receipts WHERE command_kind='page'`); got != 1 {
		t.Fatal("durable page receipt", got)
	}
	if got := ipPolicyFreshInt(t, path, `SELECT after_id FROM metadata_refresh_sweeps WHERE provider='tmdb' AND purpose='metadata'`); got != 2 {
		t.Fatal("durable page cursor", got)
	}
	if got := ipPolicyFreshInt(t, path, `SELECT count(*) FROM metadata_acquisition_jobs WHERE status='pending'`); got != 2 {
		t.Fatal("durable page schedule", got)
	}
}

func TestIPRefreshPageBoundsAndMissingOwner(t *testing.T) {
	for name, mutate := range map[string]func(*ipRefreshPageRequest){
		"unknown_provider":    func(q *ipRefreshPageRequest) { q.provider = "plex" },
		"wrong_purpose":       func(q *ipRefreshPageRequest) { q.purpose = "everything" },
		"artwork_music":       func(q *ipRefreshPageRequest) { q.provider, q.purpose = "musicbrainz", "artwork" },
		"zero_generation":     func(q *ipRefreshPageRequest) { q.expectedGeneration = 0 },
		"zero_revision":       func(q *ipRefreshPageRequest) { q.expectedRevision = 0 },
		"zero_consent":        func(q *ipRefreshPageRequest) { q.consentRevision = 0 },
		"negative_generation": func(q *ipRefreshPageRequest) { q.expectedGeneration = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			q := ipRefreshPageRequest{provider: "tmdb", purpose: "metadata", expectedGeneration: 1, expectedRevision: 1, consentRevision: 1}
			mutate(&q)
			if c, err := newIPRefreshPageCommand(ipPolicyIdentity(2, 3), q); c != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(c, err)
			}
		})
	}
	t.Run("missing_owner", func(t *testing.T) {
		w, r, p := ipRefreshPageFixture(t)
		turn, tx, b := ipPolicyTurn(t, w, r, IPRefreshPage)
		out, err := applyIPRefreshPage(t.Context(), turn, p, t.Context(), b, ipRefreshPageTestCommand(t, 1, 3, nil))
		if out != (ipRefreshPageCommandResult{}) || !errors.Is(err, ErrResolveRequired) {
			t.Fatal("missing sweep admitted", out, err)
		}
		ipPolicyRollbackTurn(t, turn, tx)
	})
}

func TestIPRefreshPageDescriptors(t *testing.T) {
	for _, expected := range []Descriptor{
		{ID: IPRefreshBegin, Name: "ip_refresh_begin", Durability: Normal},
		{ID: IPRefreshPage, Name: "ip_refresh_page", Durability: Normal},
	} {
		actual, err := Describe(expected.ID)
		if err != nil || actual != expected || actual.ID < 200 || actual.ID > 299 {
			t.Fatal(actual, err)
		}
	}
}
