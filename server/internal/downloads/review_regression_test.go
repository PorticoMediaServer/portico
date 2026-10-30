package downloads

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"testing"
	"time"
)

func TestReceiptReplayRespectsRevocation(t *testing.T) {
	for _, replay := range []string{"issue", "renew"} {
		t.Run(replay, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			ready := h.ready(t, "ready")
			issued, err := h.service.IssueReceipts(ctx, h.viewer, "server", "issue", []string{ready.ID})
			if err != nil {
				t.Fatal(err)
			}
			receipt := issued[0].ReceiptID
			if _, err = h.service.Revalidate(ctx, h.viewer, "server", "renew", []string{receipt}, nil); err != nil {
				t.Fatal(err)
			}
			if _, err = h.service.Revoke(ctx, h.viewer, "revoke", ReasonRevoked, []string{receipt}, false); err != nil {
				t.Fatal(err)
			}
			var out []ReceiptOutcome
			if replay == "issue" {
				out, err = h.service.IssueReceipts(ctx, h.viewer, "server", "issue", []string{ready.ID})
			} else {
				out, err = h.service.Revalidate(ctx, h.viewer, "server", "renew", []string{receipt}, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(out) != 1 || out[0].Outcome != OutcomeRefused || out[0].Receipt != nil {
				t.Fatalf("revoked permission returned on retry: %+v", out)
			}
		})
	}
}
func TestPreparationReadsConcealRestrictedTitle(t *testing.T) {
	h := newHarness(t)
	ready := h.ready(t, "ready")
	if _, err := h.db.Exec(`INSERT INTO profile_restrictions(profile_id,allow_unrated) VALUES(?,0)`, h.viewer.ProfileID); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := h.service.Get(ctx, h.viewer, ready.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("restricted claim returned: %v", err)
	}
	page, err := h.service.List(ctx, h.viewer, ListQuery{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Errorf("restricted title listed: %+v", page.Items)
	}
}
func TestGrantUsesTitlesCurrentLibrary(t *testing.T) {
	h := newHarness(t)
	ready := h.ready(t, "ready")
	ctx := context.Background()
	grant, err := h.service.IssueGrant(ctx, h.viewer, ready.ID, GrantRequest{OperationID: "grant"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('newlib','New','movie',?)`, h.root+"/new"); err != nil {
		t.Fatal(err)
	}
	if _, err = h.db.Exec(`UPDATE catalog_entities SET library_id=(SELECT id FROM catalog_libraries WHERE library_id='newlib') WHERE id=?`, h.itemID); err != nil {
		t.Fatal(err)
	}
	scope, err := h.service.ResolveGrant(ctx, grant.Token)
	if err != nil {
		t.Fatal(err)
	}
	if scope.LibraryID != "newlib" {
		t.Fatalf("grant checks old library %q", scope.LibraryID)
	}
}
func TestOlderOfflinePlayKeepsLatestPlayDate(t *testing.T) {
	h := newHarness(t)
	shareOfflineFixture(t, h)
	ctx := context.Background()
	for i, age := range []time.Duration{time.Minute, 2 * time.Minute} {
		play := []ProgressEntry{{PlayID: []string{"newer", "older"}[i], ItemID: h.item, PositionSeconds: 60, Ended: true, ObservedAt: h.now.Add(-age).Format(time.RFC3339)}}
		if _, err := h.service.SyncProgress(ctx, h.viewer, []string{"newer-op", "older-op"}[i], play); err != nil {
			t.Fatal(err)
		}
	}
	var latest string
	if err := h.db.QueryRow(`SELECT last_played_at FROM personal_items WHERE profile_id=? AND item_id=?`, identity.PersonalKey(h.viewer.Viewer), h.itemID).Scan(&latest); err != nil {
		t.Fatal(err)
	}
	at, err := time.Parse(time.RFC3339, latest)
	if err != nil || !at.Equal(h.now.Add(-time.Minute)) {
		t.Fatalf("latest play moved backwards to %q: %v", latest, err)
	}
	var lastCounted int64
	if err = h.db.QueryRow(`SELECT last_played_ms FROM play_counts WHERE item_id=?`, h.itemID).Scan(&lastCounted); err != nil || lastCounted != h.now.Add(-time.Minute).UnixMilli() {
		t.Fatalf("owner latest play regressed: %d %v", lastCounted, err)
	}
	var count int
	if err = h.db.QueryRow(`SELECT count(*) FROM personal_history`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("history count %d: %v", count, err)
	}
}
func TestLaterOfflineObservationReclaimsResume(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for i, play := range []string{"a", "b", "a"} {
		position := float64(10 + i*10)
		out, err := h.service.SyncProgress(ctx, h.viewer, []string{"op-a1", "op-b", "op-a2"}[i], []ProgressEntry{{PlayID: play, ItemID: h.item, PositionSeconds: position, ObservedAt: h.now.Add(time.Duration(i-3) * time.Minute).Format(time.RFC3339)}})
		if err != nil {
			t.Fatal(err)
		}
		if out.Applied != 1 || out.Entries[0].PositionSeconds != position {
			t.Fatalf("observation %d reports applied without moving resume to %v: %+v", i, position, out)
		}
	}
	stale, err := h.service.SyncProgress(ctx, h.viewer, "op-stale-a", []ProgressEntry{{PlayID: "a", ItemID: h.item, PositionSeconds: 5, ObservedAt: h.now.Add(-2 * time.Minute).Format(time.RFC3339)}})
	if err != nil || stale.Entries[0].Outcome != ProgressStale || stale.Entries[0].PositionSeconds != 30 {
		t.Fatalf("stale report displaced the fresh writer: %+v %v", stale, err)
	}
	var count int
	if err := h.db.QueryRow(`SELECT count(*) FROM personal_history`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("history count %d: %v", count, err)
	}
}

func TestLibraryWithdrawalStopsDownloadAccess(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	ready := h.ready(t, "ready")
	grant, err := h.service.IssueGrant(ctx, h.viewer, ready.ID, GrantRequest{OperationID: "grant"})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := h.service.IssueReceipts(ctx, h.viewer, "server", "issued", []string{ready.ID})
	if err != nil {
		t.Fatal(err)
	}
	receipt := issued[0].ReceiptID
	if _, err = h.service.Revalidate(ctx, h.viewer, "server", "renewed", []string{receipt}, nil); err != nil {
		t.Fatal(err)
	}
	h.service.SetLibraryAccess(func(context.Context, *sql.Tx, identity.Principal, string) error { return identity.ErrNotVisible })
	for _, check := range []struct {
		name string
		call func() error
	}{
		{"get", func() error { _, e := h.service.Get(ctx, h.viewer, ready.ID); return e }},
		{"bundle", func() error { _, e := h.service.Bundle(ctx, h.viewer, ready.ID); return e }},
		{"grant", func() error {
			_, e := h.service.IssueGrant(ctx, h.viewer, ready.ID, GrantRequest{OperationID: "grant"})
			return e
		}},
		{"submit replay", func() error {
			_, e := h.service.Submit(ctx, h.viewer, Request{OperationID: "ready", MediaID: h.item, Quality: QualityOriginal}, nil)
			return e
		}},
	} {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); !errors.Is(err, ErrNotFound) {
				t.Fatalf("access survived: %v", err)
			}
		})
	}
	if _, err = h.service.ResolveGrant(ctx, grant.Token); !errors.Is(err, ErrGrant) {
		t.Fatalf("transfer survived: %v", err)
	}
	page, err := h.service.List(ctx, h.viewer, ListQuery{}, false)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("list leaked: %+v %v", page, err)
	}
	for _, operation := range []string{"fresh", "issued"} {
		out, e := h.service.IssueReceipts(ctx, h.viewer, "server", operation, []string{ready.ID})
		if e != nil || len(out) != 1 || out[0].Receipt != nil || out[0].Outcome != OutcomeRefused {
			t.Fatalf("receipt permission survived: %+v %v", out, e)
		}
	}
	out, e := h.service.Revalidate(ctx, h.viewer, "server", "renewed", []string{receipt}, nil)
	if e != nil || out[0].Receipt != nil || out[0].Outcome != OutcomeRefused {
		t.Fatalf("renewal survived: %+v %v", out, e)
	}
	progress, e := h.service.SyncProgress(ctx, h.viewer, "play", []ProgressEntry{{PlayID: "p", ItemID: h.item, PositionSeconds: 20, ObservedAt: h.now.Format(time.RFC3339)}})
	if e != nil || progress.Entries[0].Outcome != ProgressMissing {
		t.Fatalf("progress accepted restricted title: %+v %v", progress, e)
	}
}

func TestTidyKeepsClaimUsedAfterCandidateRead(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	ready := h.ready(t, "ready")
	h.now = h.now.Add(31 * 24 * time.Hour)
	called := false
	ctx = context.WithValue(ctx, tidyCandidatesObserverKey{}, func() {
		called = true
		if _, err := h.service.IssueGrant(context.Background(), h.viewer, ready.ID, GrantRequest{OperationID: "fresh-grant"}); err != nil {
			t.Fatal(err)
		}
	})
	if err := h.service.tidy(ctx); err != nil {
		t.Fatal(err)
	}
	current, err := h.service.Get(context.Background(), h.viewer, ready.ID)
	if !called || err != nil || current.State != StateReady {
		t.Fatalf("freshly used claim expired: %+v %v", current, err)
	}
}

func TestStaleReportKeepsOwnerPlayDate(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for i, age := range []time.Duration{time.Minute, 2 * time.Minute} {
		if _, err := h.service.SyncProgress(ctx, h.viewer, []string{"new", "old"}[i], []ProgressEntry{{PlayID: "same-play", ItemID: h.item, PositionSeconds: 20, ObservedAt: h.now.Add(-age).Format(time.RFC3339)}}); err != nil {
			t.Fatal(err)
		}
	}
	var updated int64
	if err := h.db.QueryRow(`SELECT updated_ms FROM play_history`).Scan(&updated); err != nil || updated != h.now.Add(-time.Minute).UnixMilli() {
		t.Fatalf("owner play timestamp regressed: %d %v", updated, err)
	}
}

func TestSparsePreparationPageIsBoundedAndViewerScoped(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	own := h.ready(t, "own")
	other := h.viewer
	other.ProfileID = "other"
	foreign, err := h.service.Submit(ctx, other, Request{OperationID: "foreign", MediaID: h.item, Quality: QualityOriginal}, nil)
	if err != nil {
		t.Fatal(err)
	}
	page, err := h.service.List(ctx, h.viewer, ListQuery{IDs: []string{own.ID, foreign.Items[0].ID, "missing"}}, false)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != own.ID || page.NextCursor != "" {
		t.Fatalf("sparse page disclosed another viewer or unrelated claim: %+v %v", page, err)
	}
	for _, query := range []ListQuery{{IDs: []string{"../bad"}}, {IDs: []string{own.ID}, Cursor: encodeCursor(h.now.UnixMilli(), own.ID)}, {IDs: make([]string, 201)}} {
		if _, err := h.service.List(ctx, h.viewer, query, false); !errors.Is(err, ErrInput) {
			t.Fatalf("unbounded or malformed sparse read accepted: %v", err)
		}
	}
	h.service.SetLibraryAccess(func(context.Context, *sql.Tx, identity.Principal, string) error { return identity.ErrNotVisible })
	page, err = h.service.List(ctx, h.viewer, ListQuery{IDs: []string{own.ID}}, false)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("sparse read disclosed a restricted claim: %+v %v", page, err)
	}
}

func TestFilteredBatchReplayKeepsCountsConsistent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	request := Request{OperationID: "batch", MediaIDs: []string{h.item}, Quality: QualityOriginal}
	if _, err := h.service.Submit(ctx, h.viewer, request, nil); err != nil {
		t.Fatal(err)
	}
	h.service.SetLibraryAccess(func(context.Context, *sql.Tx, identity.Principal, string) error { return identity.ErrNotVisible })
	out, err := h.service.Submit(ctx, h.viewer, request, nil)
	if err != nil || len(out.Items) != 0 || out.Accepted != 0 || len(out.Rejected) != 1 || !out.Duplicate {
		t.Fatalf("filtered replay has invalid counts: %+v %v", out, err)
	}
}

func TestReceiptReplayDistinguishesGuardDenialFromFailure(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{false: "temporary failure", true: "denied"}[denied], func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			ready := h.ready(t, "ready")
			issued, err := h.service.IssueReceipts(ctx, h.viewer, "server", "issue", []string{ready.ID})
			if err != nil {
				t.Fatal(err)
			}
			id := issued[0].ReceiptID
			if _, err = h.service.Revalidate(ctx, h.viewer, "server", "renew", []string{id}, nil); err != nil {
				t.Fatal(err)
			}
			failure := errors.New("temporary policy read failure")
			guard := func(*sql.Tx, string) error {
				if denied {
					return identity.ErrNotVisible
				}
				return failure
			}
			out, err := h.service.Revalidate(ctx, h.viewer, "server", "renew", []string{id}, guard)
			var revoked int64
			if e := h.db.QueryRow(`SELECT revoked_ms FROM download_receipts WHERE id=?`, id).Scan(&revoked); e != nil {
				t.Fatal(e)
			}
			if denied {
				if err != nil || out[0].Outcome != OutcomeRefused || out[0].Receipt != nil || revoked == 0 {
					t.Fatalf("denied library renewed: %+v %v revoked=%d", out, err, revoked)
				}
			} else if !errors.Is(err, failure) || revoked != 0 {
				t.Fatalf("temporary failure revoked a copy: %+v %v revoked=%d", out, err, revoked)
			}
		})
	}
}
