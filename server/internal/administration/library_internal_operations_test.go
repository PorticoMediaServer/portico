package administration

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestLibraryDepthTransitionsRespectInternalEpisodeAdmissionWithoutChangingMode(t *testing.T) {
	service, db, root := newService(t)
	ctx := context.Background()
	const library = "episode-depth"
	if _, err := db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES(?,'Shows','tv',?)`, library, root); err != nil {
		t.Fatal(err)
	}
	if err := dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		mode, err := catalog.EpisodeThumbnailPolicyTx(ctx, tx, library)
		if err != nil {
			return err
		}
		if err = catalog.ApplyEpisodeThumbnailModeTx(ctx, tx, library, "provider", mode.Revision); err != nil {
			return err
		}
		return catalog.ApplyScanPolicyTx(ctx, tx, library, catalog.ScanPolicy{Tier: "complete"})
	}); err != nil {
		t.Fatal(err)
	}
	for _, depth := range []string{"basic", "file_list_only", "complete"} {
		doc, err := service.LibrarySettingsFor(ctx, allow, library)
		if err != nil {
			t.Fatal(err)
		}
		settings := doc.Settings
		settings.ScanDepth = depth
		// Use the same drawn six-step presets while keeping the independent
		// picture choices. No internal operation is supplied in the UI payload.
		settings.Analysis = []string{"probe", "chapter_images", "trickplay"}
		if depth != "file_list_only" {
			settings.Analysis = append(settings.Analysis, "local_metadata", "subtitle_discovery")
		}
		if depth == "complete" {
			settings.Analysis = append(settings.Analysis, "waveform", "loudness")
		}
		if _, err = service.SaveLibrarySettings(ctx, allow, library, Change[LibrarySettings]{ExpectedRevision: doc.Revision, OperationID: "episode-depth-" + depth, Settings: settings}); err != nil {
			t.Fatal(depth, err)
		}
		policy, err := catalog.New(db).ScanPolicy(ctx, library)
		if err != nil || policy.Allows(catalog.EpisodeThumbnailOperation) != (depth == "complete") {
			t.Fatal("depth changed private episode admission incorrectly", depth, policy, err)
		}
		var mode string
		if err = db.QueryRow(`SELECT episode_thumbnail_mode FROM library_scan_policies WHERE library_id=?`, library).Scan(&mode); err != nil || mode != "provider" {
			t.Fatal("depth save replaced the independent mode", depth, mode, err)
		}
	}
}
