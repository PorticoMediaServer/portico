package writer

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
)

func runReclaimer(t *testing.T, f *publisherGovernedFixture, c *searchFragmentReclaimCommand, ack bool) (searchPublisherCommandResult, AckResult, error) {
	t.Helper()
	return f.w.effective.runSearchReclaimer(t.Context(), f.w, f.cap, f.rq, c, ack)
}

func TestSearchReclaimerGovernedOriginalHistoryAndAck(t *testing.T) {
	f := newPublisherGovernedFixture(t, SearchFragmentReclaim, false)
	// Original token retirement does not require a live entity or current fold.
	if _, err := f.e.db.ExecContext(t.Context(), `DELETE FROM catalog_entities WHERE id=41`); err != nil {
		t.Fatal(err)
	}
	p := searchpolicy.Compiled()
	last := "0"
	if p.UnicodeFingerprint[len(p.UnicodeFingerprint)-1] == '0' {
		last = "1"
	}
	p.UnicodeFingerprint = p.UnicodeFingerprint[:len(p.UnicodeFingerprint)-1] + last
	if !searchpolicy.StoredWellFormed(p) {
		t.Fatal("foreign profile malformed")
	}
	if _, err := f.e.db.ExecContext(t.Context(), `UPDATE search_generations SET unicode_fingerprint=? WHERE id=7`, p.UnicodeFingerprint); err != nil {
		t.Fatal(err)
	}
	var publisherAck int64
	if err := f.e.db.QueryRowContext(t.Context(), `SELECT acknowledged_command_sequence FROM search_publish_jobs WHERE id=29`).Scan(&publisherAck); err != nil {
		t.Fatal(err)
	}
	out, ack, err := runReclaimer(t, f, f.reclaim, false)
	if err != nil || out.Outcome.State != Applied || out.Outcome.Replayed || out.Outcome.Reference != f.rq.Receipt || ack != (AckResult{}) || out.Reclaim.JobID != 2 || out.Reclaim.FragmentID != 601 || out.Reclaim.AfterFragmentID != 601 || out.Reclaim.Complete || out.Reclaim.Outcome != (ReceiptOutcome{}) {
		t.Fatal(out, ack, err)
	}
	ev := out.Outcome.Evidence
	if ev == nil || ev.Purpose != SearchFragmentReclaim || ev.Class != governor.Background || ev.Durability != Normal || ev.Commit != CommitSucceeded || ev.PhysicalReturnedAt == nil || ev.WALBefore == nil || ev.WALAfter == nil {
		t.Fatal("not same-purpose physical execution", ev)
	}
	// Durable owner/receipt values still resolve without any live image. This
	// deliberately adversarial cleanup must not cause a result reconstruction.
	for _, q := range []string{
		`UPDATE search_reclaim_jobs SET phase='complete',live_publication_id=NULL,job_revision=9223372036854775807,job_lease_expires_ms=0 WHERE id=2`,
		`INSERT INTO search_fragment_prefix(search_fragment_prefix,rowid,prefix_tokens) VALUES('delete',602,'')`,
		`INSERT INTO search_fragment_lexical(search_fragment_lexical,rowid,lexical_tokens) VALUES('delete',602,'')`,
		`INSERT INTO search_fragment_grams(search_fragment_grams,rowid,normalized) VALUES('delete',602,'')`,
		`DELETE FROM search_fragments WHERE publication_id=13`,
		`DELETE FROM search_image_seals WHERE publication_id=13`,
		`DELETE FROM search_publications WHERE id=13`,
	} {
		if _, err := f.e.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(q, err)
		}
	}
	replay, _, err := runReclaimer(t, f, nil, false)
	if err != nil || replay.Outcome.State != Applied || !replay.Outcome.Replayed || replay.Reclaim != out.Reclaim {
		t.Fatal("saved proof depended on live custody/fresh eligibility", replay, err)
	}
	_, ack, err = runReclaimer(t, f, nil, true)
	if err != nil || ack.AcknowledgedThrough != 1 || ack.Evidence == nil {
		t.Fatal(ack, err)
	}
	retired, _, err := runReclaimer(t, f, nil, false)
	if err != nil || retired.Outcome.State != ResultRetired || retired.Reclaim != (searchFragmentReclaimResult{}) {
		t.Fatal("retired result resurrected", retired, err)
	}
	_, ack, err = runReclaimer(t, f, nil, true)
	if err != nil || ack.AcknowledgedThrough != 1 {
		t.Fatal("idempotent floor", ack, err)
	}
	var current int64
	if err := f.e.db.QueryRowContext(t.Context(), `SELECT acknowledged_command_sequence FROM search_publish_jobs WHERE id=29`).Scan(&current); err != nil || current != publisherAck {
		t.Fatal("reclaimer ACK crossed into publisher owner", current, publisherAck, err)
	}
	if f.a.Usage() != ([4]governor.Amounts{}) || f.e.db.Stats().InUse != 0 {
		t.Fatal("physical custody leaked")
	}
}

func TestSearchReclaimerClosedScopeNonceAndMemory(t *testing.T) {
	t.Run("scope_kind", func(t *testing.T) {
		f := newPublisherGovernedFixture(t, SearchFragmentReclaim, false)
		// Matching numeric fields/nonces never compensate for a wrong realm.
		f.cap.scope.kind = publisherJobScope
		out, _, err := runReclaimer(t, f, f.reclaim, false)
		if !errors.Is(err, ErrDisclosureDenied) || out != (searchPublisherCommandResult{}) || f.a.Usage() != ([4]governor.Amounts{}) {
			t.Fatal("wrong scope kind admitted", out, err)
		}
		f.cap.scope.kind = publisherReclaimerScope
		pub := PublisherReceiptRequest{JobID: f.rq.ReclaimerID, LibraryID: f.rq.LibraryID, EntityID: f.rq.EntityID, Receipt: f.rq.Receipt}
		_, _, err = f.w.effective.runSearchPublisher(t.Context(), f.w, f.cap, pub, SearchFragmentStage, nil, nil, false)
		if !errors.Is(err, ErrDisclosureDenied) {
			t.Fatal("reclaimer capability interpreted as publisher job", err)
		}
		ordinary := publisherFixtureCapability(t, f.w, pub)
		_, _, err = f.w.effective.runSearchReclaimer(t.Context(), f.w, ordinary, f.rq, f.reclaim, false)
		if !errors.Is(err, ErrDisclosureDenied) {
			t.Fatal("publisher job capability interpreted as reclaimer", err)
		}
	})
	t.Run("native_nonce", func(t *testing.T) {
		f := newPublisherGovernedFixture(t, SearchFragmentReclaim, false)
		replacement := [16]byte{62}
		if _, err := f.e.db.ExecContext(t.Context(), `UPDATE search_reclaim_jobs SET command_incarnation=? WHERE id=2`, replacement[:]); err != nil {
			t.Fatal(err)
		}
		out, _, err := runReclaimer(t, f, f.reclaim, false)
		if !errors.Is(err, ErrDisclosureDenied) || out != (searchPublisherCommandResult{}) {
			t.Fatal("native nonce was not current scope", out, err)
		}
		if f.a.Usage() != ([4]governor.Amounts{}) || f.e.db.Stats().InUse != 0 {
			t.Fatal("nonce refusal leaked custody")
		}
	})
	t.Run("partition", func(t *testing.T) {
		f := newPublisherGovernedFixture(t, SearchFragmentReclaim, false)
		f.w.effective.publisherPlans[2].publisherMemory = commandMemoryBudget{}
		out, _, err := runReclaimer(t, f, f.reclaim, false)
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || out != (searchPublisherCommandResult{}) {
			t.Fatal("borrowed publisher purpose partition", out, err)
		}
		if f.a.Usage() != ([4]governor.Amounts{}) {
			t.Fatal("memory refusal leaked")
		}
	})
}

func TestSearchReclaimerNativeOwnerPublicationAndLibrary(t *testing.T) {
	f := newPublisherGovernedFixture(t, SearchFragmentReclaim, false)
	f.cap.scope.reclaimer.publicationID = 14
	q := f.rq
	q.PublicationID = 14
	out, _, err := f.w.effective.runSearchReclaimer(t.Context(), f.w, f.cap, q, nil, false)
	if !errors.Is(err, ErrDisclosureDenied) || out != (searchPublisherCommandResult{}) {
		t.Fatal("unrelated publication admitted by owner", out, err)
	}
	f.cap.scope.reclaimer.publicationID = 13
	if _, err := f.e.db.ExecContext(t.Context(), `UPDATE catalog_libraries SET library_id='replacement' WHERE id=3`); err != nil {
		t.Fatal(err)
	}
	out, _, err = runReclaimer(t, f, f.reclaim, false)
	if !errors.Is(err, ErrDisclosureDenied) || out != (searchPublisherCommandResult{}) {
		t.Fatal("replacement library admitted", out, err)
	}
}

// Behavioural regression for1557: no golden/source hash is consulted by this
// test. Even an unchecked effect-vector cannot delete another entity's row.
func TestSearchReclaimMismatchedOwnerCannotDelete(t *testing.T) {
	e, r, original := searchReclaimNativeFixture(t)
	request := original.request
	request.snapshot.fragment.entityID = 42
	c, err := newSearchFragmentReclaimCommand(original.identity, request)
	if err != nil {
		t.Fatal(err)
	}
	turn := searchPublisherComponentTurn(t, e, r, SearchFragmentReclaim)
	out, err := writeSearchFragmentReclaim(t.Context(), turn, c, c.request, 1000)
	if !errors.Is(err, ErrConflict) || out != (searchFragmentReclaimResult{}) {
		t.Fatal("mismatched owner deleted native row", out, err)
	}
	if _, e := turn.transaction.rollback(err); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	var owner, receipts int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT owner_entity,(SELECT count(*) FROM search_reclaim_receipts) FROM search_fragments WHERE id=601`).Scan(&owner, &receipts); err != nil || owner != 41 || receipts != 0 {
		t.Fatal("foreign row or receipt changed", owner, receipts, err)
	}
	for _, q := range []string{`SELECT rowid FROM search_fragment_prefix WHERE search_fragment_prefix MATCH '00006f00006c000064'`, `SELECT rowid FROM search_fragment_lexical WHERE search_fragment_lexical MATCH 'old'`, `SELECT rowid FROM search_fragment_grams WHERE search_fragment_grams MATCH 'old'`} {
		var id int64
		if err := e.db.QueryRowContext(t.Context(), q).Scan(&id); err != nil || id != 601 {
			t.Fatal("ownership refusal lost original native bytes", id, err)
		}
	}
}

func TestSearchReclaimerClosedPlanAndOriginalBinding(t *testing.T) {
	f := newPublisherGovernedFixture(t, SearchFragmentReclaim, false)
	q := f.rq
	q.Receipt.RequestDigest[0] ^= 1
	out, _, err := f.w.effective.runSearchReclaimer(t.Context(), f.w, f.cap, q, f.reclaim, false)
	if !errors.Is(err, ErrInvalid) || out != (searchPublisherCommandResult{}) {
		t.Fatal("changed original request admitted", out, err)
	}
	f.w.effective.publisherPlans[2] = nil
	out, _, err = runReclaimer(t, f, f.reclaim, false)
	if !errors.Is(err, ErrNotImplemented) || out != (searchPublisherCommandResult{}) || f.a.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("borrowed another installed plan", out, err)
	}
	for _, d := range searchPublisherCommandDefinitions(SearchFragmentReclaim) {
		if searchPublisherMaxUses(SearchFragmentReclaim, d.ID) == 0 {
			t.Fatal("unreserved", d.ID)
		}
	}
	if searchPublisherMaxUses(SearchFragmentReclaim, "unknown") != 0 || searchPublisherPurpose(SearchFragmentReclaim) {
		t.Fatal("resource eligibility became publisher-job authority")
	}
	// An unmapped purpose has no closed plan index; it must refuse rather than
	// index the array. SearchImageStage has its own closed facade elsewhere.
	if index, ok := searchPublisherPlanIndex(SearchImageStage); ok || index != 0 {
		t.Fatal("unmapped publisher purpose received a plan index")
	}
	// A typed reclaimer envelope cannot carry even a zero-valued publisher request.
	v, err := prepareSearchReclaimerCommand(t.Context(), f.w, f.cap, f.rq, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	v.request = PublisherReceiptRequest{JobID: 2}
	if _, err = validatePublisherInvocation(t.Context(), f.w, &v); !errors.Is(err, ErrInvalid) {
		t.Fatal("mixed receipt namespaces", err)
	}
}
