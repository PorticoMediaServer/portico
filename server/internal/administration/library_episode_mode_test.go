package administration

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// This exercises the existing whole-settings service, beyond the direct scan
// writer proof. Episode operation registration and the future facade/reset
// contract still need their own composed check before activation.
func TestLibrarySettingsSavePreservesEpisodeMode(t *testing.T) {
	for _, mode := range []string{"provider_then_made", "provider", "off"} {
		t.Run(mode, func(t *testing.T) {
			service, db, root := newService(t)
			ctx := context.Background()
			const library = "episode-policy"
			if _, err := db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES(?,'Shows','tv',?)`, library, root); err != nil {
				t.Fatal(err)
			}
			writeMode := func(value string) {
				t.Helper()
				if err := dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
					p, err := catalog.EpisodeThumbnailPolicyTx(ctx, tx, library)
					if err != nil {
						return err
					}
					return catalog.ApplyEpisodeThumbnailModeTx(ctx, tx, library, value, p.Revision)
				}); err != nil {
					t.Fatal(err)
				}
			}
			writeMode(mode)
			if err := dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
				return catalog.ApplyScanPolicyTx(ctx, tx, library, catalog.ScanPolicy{Tier: "complete"})
			}); err != nil {
				t.Fatal(err)
			}
			read := func(wantMode string) catalog.ScanPolicy {
				t.Helper()
				p, err := catalog.New(db).ScanPolicy(ctx, library)
				if err != nil {
					t.Fatal(err)
				}
				var stored string
				var revision int64
				if err := db.QueryRow(`SELECT episode_thumbnail_mode,revision FROM library_scan_policies WHERE library_id=?`, library).Scan(&stored, &revision); err != nil || stored != wantMode || revision != p.Revision {
					t.Fatalf("mode/revision: %q/%d, want %q/%d; %v", stored, revision, wantMode, p.Revision, err)
				}
				return p
			}
			doc, err := service.LibrarySettingsFor(ctx, allow, library)
			if err != nil {
				t.Fatal(err)
			}
			before := read(mode)
			settings := doc.Settings
			settings.AllowMediaDeletion = !settings.AllowMediaDeletion
			change := Change[LibrarySettings]{ExpectedRevision: doc.Revision, Settings: settings, OperationID: "episode-full-save"}
			saved, err := service.SaveLibrarySettings(ctx, allow, library, change)
			if err != nil || saved.Settings.AllowMediaDeletion != settings.AllowMediaDeletion {
				t.Fatalf("whole-settings save: %+v; %v", saved, err)
			}
			policy := read(mode)
			for _, op := range []string{"probe", "local_metadata", "subtitles", "chapter_images", "trickplay", "waveform", "loudness"} {
				if !policy.Allows(op) {
					t.Fatalf("unrelated save removed registered operation %q: %+v", op, policy)
				}
			}
			// Also retain the actual registered set, so later Complete-tier
			// additions fail here if the full-save mapping drops them.
			if len(policy.Operations) != len(before.Operations) {
				t.Fatalf("full save changed operation count: before=%v after=%v", before.Operations, policy.Operations)
			}
			for _, op := range before.Operations {
				if !policy.Allows(op) {
					t.Fatalf("full save lost registered operation %q", op)
				}
			}
			if policy.Tier != "custom" || policy.Revision != before.Revision+1 {
				t.Fatalf("full save scan revision: before=%+v, after=%+v", before, policy)
			}
			replayed, err := service.SaveLibrarySettings(ctx, allow, library, change)
			if err != nil || replayed.Revision != saved.Revision || !reflect.DeepEqual(read(mode), policy) {
				t.Fatalf("replay rewrote policy: %+v; %v", replayed, err)
			}
			current, err := service.SaveLibrarySettings(ctx, allow, library, Change[LibrarySettings]{ExpectedRevision: saved.Revision, Settings: saved.Settings, OperationID: "episode-scan-noop"})
			if err != nil || !reflect.DeepEqual(read(mode), policy) {
				t.Fatalf("no-op scan policy was rewritten: %+v; %v", current, err)
			}

			// The independent domain writer shares the scan-policy revision and
			// must invalidate an already captured administration document.
			nextMode := "off"
			if mode == "off" {
				nextMode = "provider"
			}
			writeMode(nextMode)
			if _, err := service.SaveLibrarySettings(ctx, allow, library, Change[LibrarySettings]{ExpectedRevision: current.Revision, Settings: current.Settings, OperationID: "episode-stale-save"}); !errors.Is(err, ErrConflict) {
				t.Fatalf("independent mode change did not invalidate full save: %v", err)
			}
			current, err = service.LibrarySettingsFor(ctx, allow, library)
			if err != nil {
				t.Fatal(err)
			}
			policy = read(nextMode)
			denied := errors.New("fixture administration authority revoked")
			if _, err := service.SaveLibrarySettings(ctx, func(context.Context, *sql.Tx) error { return denied }, library, Change[LibrarySettings]{ExpectedRevision: current.Revision, Settings: current.Settings, OperationID: "episode-denied-save"}); !errors.Is(err, denied) {
				t.Fatalf("denied full save: %v", err)
			}
			if !reflect.DeepEqual(read(nextMode), policy) {
				t.Fatal("denied save changed scan policy")
			}

			// Reject the document after applyLibraryPolicy has updated the scan
			// table. Both writes must roll back in the real service transaction.
			if _, err := db.Exec(`CREATE TRIGGER refuse_test_library_document BEFORE INSERT ON admin_documents BEGIN SELECT RAISE(ABORT,'fixture refuses document'); END`); err != nil {
				t.Fatal(err)
			}
			changed := current.Settings
			changed.Navigation.TrickplayTileWidth = 480
			if _, err := service.SaveLibrarySettings(ctx, allow, library, Change[LibrarySettings]{ExpectedRevision: current.Revision, Settings: changed, OperationID: "episode-rollback-save"}); err == nil || !strings.Contains(err.Error(), "fixture refuses document") {
				t.Fatalf("save did not reach the forced document persistence failure: %v", err)
			}
			if !reflect.DeepEqual(read(nextMode), policy) {
				t.Fatal("failed full save left a domain policy write committed")
			}
			after, err := service.LibrarySettingsFor(ctx, allow, library)
			if err != nil || after.Revision != current.Revision || after.Digest != current.Digest {
				t.Fatalf("failed save changed administration document: %+v; %v", after, err)
			}
		})
	}
}
