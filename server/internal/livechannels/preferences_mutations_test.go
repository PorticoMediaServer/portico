package livechannels

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

func TestPreferenceMutationAcceptsProtocolKeyAndReplaysCurrent(t *testing.T) {
	s := testStore(t)
	a := testAuthority("owner", true, true)
	source, err := s.Save(context.Background(), a, testInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('owner','owner',X'00','profile')`); err != nil {
		t.Fatal(err)
	}
	owner := Owner{Authority: "local", AccountID: "owner", ProfileID: "profile"}
	key := fmt.Sprintf("p251_%013d_%032x", time.Now().UnixMilli(), 1)
	ctx := usermutation.WithRequest(dbwork.WithDurableUser(context.Background()), usermutation.Binding{Authority: "local", AccountID: "owner", ProfileID: "profile", FamilyID: "family"}, usermutation.Envelope{Key: key, Operation: "post_channels_preferences", Digest: strings.Repeat("a", 64)})
	in := PreferenceInput{RequestID: key, SourceID: source.ID, ChannelID: id(source.ID, "one"), Favorite: true}
	first, err := s.SetPreference(ctx, a, owner, in)
	if err != nil || first != 1 {
		t.Fatalf("protocol format rejected: %d %v", first, err)
	}
	later := in
	later.RequestID = strings.Repeat("ef", 24)
	later.ExpectedRevision = first
	later.Favorite = false
	if _, err = s.SetPreference(context.Background(), a, owner, later); err != nil {
		t.Fatal(err)
	}
	revision, err := s.SetPreference(ctx, a, owner, in)
	if err != nil || revision != 2 {
		t.Fatalf("current replay %d %v", revision, err)
	}
	var n int
	if err = s.db.QueryRow(`SELECT count(*) FROM live_preference_receipts WHERE request_id=?`, key).Scan(&n); err != nil || n != 0 {
		t.Fatalf("legacy protocol receipt %d %v", n, err)
	}
}
