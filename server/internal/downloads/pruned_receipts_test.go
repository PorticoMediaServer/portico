package downloads

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

func issueFixtureReceipt(t *testing.T, h *harness, operation, preparation string) Receipt {
	t.Helper()
	out, err := h.service.IssueReceipts(context.Background(), h.viewer, "server", operation, []string{preparation})
	if err != nil || len(out) != 1 || out[0].Outcome != OutcomeIssued || out[0].Receipt == nil {
		t.Fatalf("issue receipt: %+v %v", out, err)
	}
	return *out[0].Receipt
}

func TestPrunePreservesPreparationsWithLiveReceipts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, state := range []string{StateCancelled, StateExpired, StateUnavailable} {
		for _, end := range []string{"expired", "revoked"} {
			t.Run(state+"/"+end, func(t *testing.T) {
				h := newHarness(t)
				ready := h.ready(t, "prepare")
				receipt := issueFixtureReceipt(t, h, "issue", ready.ID)
				if _, err := h.db.Exec(`UPDATE download_preparations SET state=?,updated_ms=? WHERE id=?`, state, h.now.Add(-finishedRowsKept-time.Hour).UnixMilli(), ready.ID); err != nil {
					t.Fatal(err)
				}
				if err := h.service.prune(context.Background()); err != nil {
					t.Fatal(err)
				}
				var present bool
				if err := h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM download_preparations WHERE id=?)`, ready.ID).Scan(&present); err != nil || !present {
					t.Fatalf("live receipt lost its preparation: present=%v %v", present, err)
				}
				query := `UPDATE download_receipts SET expires_ms=? WHERE id=?`
				if end == "revoked" {
					query = `UPDATE download_receipts SET revoked_ms=? WHERE id=?`
				}
				if _, err := h.db.Exec(query, h.now.UnixMilli(), receipt.ReceiptID); err != nil {
					t.Fatal(err)
				}
				if err := h.service.prune(context.Background()); err != nil {
					t.Fatal(err)
				}
				if err := h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM download_preparations WHERE id=?)`, ready.ID).Scan(&present); err != nil || present {
					t.Fatalf("ended receipt kept an old preparation: present=%v %v", present, err)
				}
			})
		}
	}
}

func TestRevalidatePrunedPreparationsKeepsMixedBatch(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	pruned := h.ready(t, "pruned")
	media := issueFixtureReceipt(t, h, "issue-pruned", pruned.ID)
	if _, err := h.db.Exec(`UPDATE download_preparations SET state='expired',updated_ms=? WHERE id=?`, h.now.Add(-finishedRowsKept-time.Hour).UnixMilli(), pruned.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(`UPDATE download_receipts SET expires_ms=? WHERE id=?`, h.now.Add(-time.Hour).UnixMilli(), media.ReceiptID); err != nil {
		t.Fatal(err)
	}
	if err := h.service.prune(ctx); err != nil {
		t.Fatal(err)
	}
	var present bool
	if err := h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM download_preparations WHERE id=?)`, pruned.ID).Scan(&present); err != nil || present {
		t.Fatalf("fixture preparation was not pruned: %v %v", present, err)
	}
	removedItem, removedID := h.item, h.itemID
	h.item, h.itemID = addCompactFixtureItem(t, h.db, "lib", compactcatalog.Movie, "intact", "Intact", 0, "", nil)
	addCompactFixtureLink(t, h.db, h.itemID, h.assetID)
	settleDownloadsProjection(t, h.db)
	intact := h.ready(t, "intact")
	current := issueFixtureReceipt(t, h, "issue-intact", intact.ID)
	reading, readingID := addCompactFixtureItem(t, h.db, "lib", compactcatalog.Ebook, "reading", "Reading", 0, "", nil)
	// The Reading preparation is already missing. Its durable receipt must
	// retain the publication requirement independently of the catalogue.
	if _, err := h.db.Exec(`INSERT INTO download_receipts(id,profile_key,authority,account_id,profile_id,item_id,item_public,library_id,preparation_id,quality,artifact_digest,artifact_bytes,key_id,issued_ms,expires_ms,sequence,publication_required)
 SELECT 'reading-receipt',profile_key,authority,account_id,profile_id,?,?,library_id,'reading-preparation',quality,artifact_digest,artifact_bytes,key_id,issued_ms,expires_ms,sequence,1 FROM download_receipts WHERE id=?`, readingID, reading, media.ReceiptID); err != nil {
		t.Fatal(err)
	}
	deleteCompactFixtureEntity(t, h.db, removedID)
	deleteCompactFixtureEntity(t, h.db, readingID)
	settleDownloadsProjection(t, h.db)
	out, err := h.service.Revalidate(ctx, h.viewer, "server", "renew-mixed", []string{"reading-receipt", media.ReceiptID, current.ReceiptID}, nil)
	if err != nil || len(out) != 3 {
		t.Fatalf("mixed renewal failed: %+v %v", out, err)
	}
	if out[0].Outcome != OutcomeRefused || out[0].Code != ReasonRevoked || out[0].Receipt != nil {
		t.Fatalf("Reading lost its publication requirement: %+v", out[0])
	}
	var revoked int64
	if err := h.db.QueryRow(`SELECT revoked_ms FROM download_receipts WHERE id='reading-receipt'`).Scan(&revoked); err != nil || revoked != h.now.UnixMilli() {
		t.Fatalf("Reading refusal did not commit: %d %v", revoked, err)
	}
	keys, err := h.service.ReceiptKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{1, 2} {
		entry := out[i]
		if entry.Outcome != OutcomeRenewed || entry.Receipt == nil || entry.TitleOnServer == nil || *entry.TitleOnServer != (i == 2) {
			t.Fatalf("media renewal %d: %+v", i, entry)
		}
		claims, err := VerifyReceipt(keys, *entry.Receipt, h.now)
		wantItem := h.item
		if i == 1 {
			wantItem = removedItem
		}
		if err != nil || claims.ItemID != wantItem || claims.Publication != nil || claims.Artifact != media.Claims.Artifact {
			t.Fatalf("renewed claims %d: %+v %v", i, claims, err)
		}
		var revision, expires int64
		if err := h.db.QueryRow(`SELECT revision,expires_ms FROM download_receipts WHERE id=?`, entry.ReceiptID).Scan(&revision, &expires); err != nil || revision != 2 || expires != h.now.Add(ReceiptTTL).UnixMilli() {
			t.Fatalf("renewal did not commit: revision=%d expires=%d %v", revision, expires, err)
		}
	}
}

func TestRevalidatePrunedMediaStillChecksLibraryAccess(t *testing.T) {
	h := newHarness(t)
	ready := h.ready(t, "prepare")
	receipt := issueFixtureReceipt(t, h, "issue", ready.ID)
	if _, err := h.db.Exec(`DELETE FROM download_preparations WHERE id=?`, ready.ID); err != nil {
		t.Fatal(err)
	}
	denied := func(*sql.Tx, string) error { return identity.ErrForbidden }
	out, err := h.service.Revalidate(context.Background(), h.viewer, "server", "renew-denied", []string{receipt.ReceiptID}, denied)
	if err != nil || len(out) != 1 || out[0].Outcome != OutcomeRefused || out[0].Code != ReasonItemDeleted || out[0].Receipt != nil {
		t.Fatalf("missing preparation bypassed access: %+v %v", out, err)
	}
}

func TestReceiptPublicationRequirementMatchesIssuedClaims(t *testing.T) {
	for _, required := range []bool{false, true} {
		name := "media"
		if required {
			name = "publication"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			ready := h.ready(t, "prepare")
			if required {
				doc := reading.OfflineDocument{ItemID: h.item, SourceID: "reading-source", Revision: ready.Artifact.SHA256, Format: "pdf", Size: 1,
					Metadata: reading.OfflineMetadata{Title: "Reading"}, Resources: []reading.OfflineResource{}, Spine: []string{}, PageNames: []string{}, Contents: []reading.OfflineContents{}}
				raw, err := json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := h.db.Exec(`UPDATE download_preparations SET publication_json=? WHERE id=?`, string(raw), ready.ID); err != nil {
					t.Fatal(err)
				}
			}
			receipt := issueFixtureReceipt(t, h, "issue", ready.ID)
			var stored bool
			if err := h.db.QueryRow(`SELECT publication_required FROM download_receipts WHERE id=?`, receipt.ReceiptID).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if stored != required || (receipt.Claims.Publication != nil) != required {
				t.Fatalf("publication requirement differs from issued claims: stored=%v claims=%+v", stored, receipt.Claims.Publication)
			}
		})
	}
}
