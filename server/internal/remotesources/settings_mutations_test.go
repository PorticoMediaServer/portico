package remotesources

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

func TestSourceReadSettingsMutationUsesTinyReceiptAndOriginalRevision(t *testing.T) {
	f, _, identities, owner, family, _ := nativeSaveFixture(t)
	before, err := f.s.ReadSourceReadSettings(context.Background(), identities, owner, "remote")
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("p251_%013d_%032x", time.Now().UnixMilli(), 1)
	binding := usermutation.Binding{Authority: owner.Authority, AccountID: owner.AccountID, ProfileID: owner.ProfileID, FamilyID: family}
	envelope := usermutation.Envelope{Key: key, Operation: "put_storage_source_read_profile", Digest: strings.Repeat("a", 64)}
	ctx := usermutation.WithRequest(dbwork.WithDurableUser(context.Background()), binding, envelope)
	profile, _ := SourceReadPreset("everything")
	in := SourceReadSettingsChange{OperationID: key, ExpectedGeneration: before.Generation, ExpectedRevision: before.Revision, Values: sourceReadValues(profile)}
	first, err := f.s.SaveSourceReadSettings(ctx, identities, owner, "remote", in)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.s.SaveSourceReadSettings(ctx, identities, owner, "remote", in)
	if err != nil || replay.ReadSettings == nil || replay.ReadSettings.Revision != first.ReadSettings.Revision || in.ExpectedRevision != before.Revision {
		t.Fatalf("replay %+v %v", replay, err)
	}
	var n int
	if err = f.s.db.QueryRow(`SELECT count(*) FROM remote_source_operations WHERE operation_id=?`, key).Scan(&n); err != nil || n != 0 {
		t.Fatalf("full response persisted %d %v", n, err)
	}
	envelope.Digest = strings.Repeat("b", 64)
	changed := usermutation.WithRequest(dbwork.WithDurableUser(context.Background()), binding, envelope)
	if _, err = f.s.SaveSourceReadSettings(changed, identities, owner, "remote", in); !errors.Is(err, usermutation.ErrConflict) {
		t.Fatalf("changed digest: %v", err)
	}
}
