package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Measure each real producer statement with and without previously drained
// debt. Marked fixtures are produced by the retirement engine, never by
// fabricating a completed marker. No endpoint-wide cost claim is made here.
func TestSeparateFolderProducerReopenPointCosts(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: measured normal parent 9.11s")
	}
	cases := []struct {
		name, query string
		args        func(*separateFolderRetirementFixture) []any
		reopens     bool
	}{
		{"grant insert", `INSERT INTO download_grants(hash,preparation_id,profile_key,item_id,issued_ms,expires_ms) SELECT 'cost-grant',id,profile_key,item_id,1,99 FROM download_preparations WHERE id='prep-live'`, nil, true},
		{"grant reactivate", `UPDATE download_grants SET revoked=0 WHERE hash='grant-live'`, nil, true},
		{"grant rebind", `UPDATE download_grants SET preparation_id='prep-live',item_id=?,revoked=0 WHERE hash='grant-sibling'`, func(f *separateFolderRetirementFixture) []any { return []any{f.item.ID} }, true},
		{"receipt insert", `INSERT INTO download_receipts(id,profile_key,authority,account_id,profile_id,item_id,library_id,preparation_id,quality,publication_required,artifact_digest,artifact_bytes,key_id,issued_ms,expires_ms,sequence) SELECT 'cost-receipt',profile_key,authority,account_id,profile_id,item_id,library_id,preparation_id,quality,publication_required,artifact_digest,artifact_bytes,key_id,issued_ms,expires_ms,0 FROM download_receipts WHERE id='receipt-live'`, nil, true},
		{"receipt reactivate", `UPDATE download_receipts SET revoked_ms=0 WHERE id='receipt-live'`, nil, true},
		{"receipt rebind", `UPDATE download_receipts SET preparation_id='prep-live',item_id=?,revoked_ms=0 WHERE id='receipt-sibling'`, func(f *separateFolderRetirementFixture) []any { return []any{f.item.ID} }, true},
		{"request insert", `INSERT INTO download_request_items(request_id,ordinal,item_id,preparation_id) SELECT 'request-sibling',1,item_id,id FROM download_preparations WHERE id='prep-live'`, nil, true},
		{"request reactivate", `UPDATE download_request_items SET reason='' WHERE request_id='request-1' AND ordinal=0`, nil, true},
		{"request rebind", `UPDATE download_request_items SET preparation_id='prep-live',item_id=?,reason='' WHERE request_id='request-sibling' AND ordinal=0`, func(f *separateFolderRetirementFixture) []any { return []any{f.item.ID} }, true},
		{"preparation reactivate", `UPDATE download_preparations SET state='queued' WHERE id='prep-live'`, nil, true},
		{"preparation item rebind", `UPDATE download_preparations SET item_id=? WHERE id='prep-live'`, func(f *separateFolderRetirementFixture) []any { return []any{f.sibling.ID} }, true},
		{"preparation source rebind", `UPDATE download_preparations SET source_asset_id=? WHERE id='prep-live'`, func(f *separateFolderRetirementFixture) []any { return []any{f.sibling.Token} }, true},
		{"grant harmless use", `UPDATE download_grants SET uses=uses+1 WHERE hash='grant-live'`, nil, false},
		{"receipt harmless refresh", `UPDATE download_receipts SET expires_ms=expires_ms+1 WHERE id='receipt-live'`, nil, false},
		{"preparation harmless progress", `UPDATE download_preparations SET bytes_done=bytes_done+1 WHERE id='prep-live'`, nil, false},
	}
	for _, tc := range cases {
		for _, marked := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/marked=%v", tc.name, marked), func(t *testing.T) {
				f := newSeparateFolderRetirementFixture(t)
				f.seedClaims(t)
				if marked {
					f.drive(t, 96)
				}
				want := int64(0)
				if marked {
					want = 1
				}
				if got := retirementInt(t, f.c, `SELECT separate_retired FROM download_preparations WHERE id='prep-live'`); got != want {
					t.Fatalf("fixture marker %d, want %d", got, want)
				}
				other := retirementInt(t, f.c, `SELECT separate_retired FROM download_preparations WHERE id='prep-sibling'`)
				var args []any
				if tc.args != nil {
					args = tc.args(f)
				}
				dbwork.ResetPlans()
				ctx, measured := dbwork.Measure(context.Background())
				ctx = dbwork.WithPlan(ctx, dbwork.DefaultPlan(dbwork.ClassInteractive))
				err := dbwork.WithWriteTx(ctx, f.c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
					result, err := tx.ExecContext(ctx, tc.query, args...)
					if err != nil {
						return err
					}
					if n, err := result.RowsAffected(); err != nil || n != 1 {
						return fmt.Errorf("producer point affected %d rows: %w", n, err)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				stats := dbwork.Plans()
				rows := int64(1)
				// Existing list-notification triggers are part of the baseline
				// producer cost: one for receipt insert/revoked_ms updates,
				// two for preparation state updates. The new debt trigger may
				// add exactly one point update on top of those existing effects.
				switch tc.name {
				case "receipt insert", "receipt reactivate", "receipt rebind":
					rows++
				case "preparation reactivate":
					rows += 2
				}
				if marked && tc.reopens {
					rows++
					want = 0
				}
				if stats.Measured != 1 || stats.Largest.Rows != rows || stats.Overruns != 0 {
					t.Fatalf("point/trigger fanout: %+v, want %d changed rows and no budget overrun", stats, rows)
				}
				if got := retirementInt(t, f.c, `SELECT separate_retired FROM download_preparations WHERE id='prep-live'`); got != want {
					t.Fatalf("producer left marker %d, want %d", got, want)
				}
				if got := retirementInt(t, f.c, `SELECT separate_retired FROM download_preparations WHERE id='prep-sibling'`); got != other {
					t.Fatal("producer reopened an unrelated preparation")
				}
				raw, err := json.Marshal(map[string]any{"case": tc.name, "marked": marked, "cost": measured(), "pointCost": stats.Largest, "plan": dbwork.DefaultPlan(dbwork.ClassInteractive)})
				if err != nil {
					t.Fatal(err)
				}
				t.Log(string(raw))
			})
		}
	}
}
