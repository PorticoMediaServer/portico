package operations_test

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/notify"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"testing"
)

func TestNormalizedSettingsWriterChangesLivePolicyReaders(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("tv", "Shows", "tv", "/tv")
	c.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('owner','owner',X'00','primary')`)
	ctx := context.Background()
	store := operations.New(c.DB)
	owner := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "owner", ProfileID: "primary", Role: "owner", ServerID: "server"}}
	doc, err := store.Settings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	values := doc.Requested
	values.MissingTitles = "never"
	values.MissingTitlesDays = 7
	values.SegmentDetection = "off"
	values.NotificationDays = 7
	if _, err = store.ApplySettings(ctx, owner, operations.AllowServerScope, operations.SettingsChange{ExpectedRevision: doc.Revision, IdempotencyKey: "normalized-live-readers", Values: values}); err != nil {
		t.Fatal(err)
	}
	// A conflicting retired graph must neither supply values nor shadow the
	// actual normalized owner write.
	c.Exec(`INSERT INTO console_documents VALUES('server',99,'{"library.missingTitles":"immediately","library.segmentDetection":"thorough","notificationDays":180}',0)`)
	rule := catalog.New(c.DB).MissingRule(ctx)
	if rule.Mode != "never" || rule.Days != 7 {
		t.Fatal("missing-title policy ignored owner write", rule)
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, c.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Rollback()
	mode, err := mediaanalysis.SegmentLibraryModeTx(ctx, snapshot.Tx(), "tv")
	if err != nil || mode != "off" {
		t.Fatal("segment policy ignored owner write", mode, err)
	}
	if days := notify.RetentionDays(snapshot.Tx()); days != 7 {
		t.Fatal("notification retention ignored owner write", days)
	}
}
