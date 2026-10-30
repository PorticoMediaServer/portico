package downloads

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRetryKeepsTheSelectedDownloadVersion(t *testing.T) {
	for _, sameSource := range []bool{false, true} {
		t.Run(map[bool]string{false: "different-source", true: "same-source"}[sameSource], func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			first, err := h.service.Submit(ctx, h.viewer, Request{OperationID: "first", MediaID: h.item, VersionID: h.asset, Quality: QualityOriginal}, nil)
			if err != nil {
				t.Fatal(err)
			}
			claim := first.Items[0]
			if _, err = h.db.Exec(`UPDATE download_preparations SET state='unavailable' WHERE id=?`, claim.ID); err != nil {
				t.Fatal(err)
			}
			other := h.asset
			if !sameSource {
				path := filepath.Join(h.root, "Other.mp4")
				if err = os.WriteFile(path, []byte("other bytes"), 0600); err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				other, _ = addCompactFixtureAsset(t, h.db, h.itemID, path, info, "mp4", "h264", "aac", 3840, 2160, 60)
			}
			second, err := h.service.Submit(ctx, h.viewer, Request{OperationID: "second", MediaID: h.item, VersionID: other, Quality: QualityOriginal}, nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := h.service.Act(ctx, h.viewer, claim.ID, Command{OperationID: "retry", Action: ActionRetry, ExpectedRevision: claim.Revision})
			if err != nil {
				t.Fatal(err)
			}
			want := claim.ID
			if sameSource {
				want = second.Items[0].ID
			}
			if got.ID != want {
				t.Fatalf("retry returned version claim %s, want %s", got.ID, want)
			}
			var source string
			if err = h.db.QueryRow(`SELECT source_asset_id FROM download_preparations WHERE id=?`, got.ID).Scan(&source); err != nil {
				t.Fatal(err)
			}
			if source != h.asset {
				t.Fatalf("retry switched source to %s", source)
			}
		})
	}
}
