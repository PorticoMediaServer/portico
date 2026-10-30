package main

import (
	"context"
	"database/sql"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strings"
	"testing"
)

func TestLiveInitializationNeedsNoKeyWithReferences(t *testing.T) {
	state := t.TempDir()
	db, e := persistence.Open(storagepaths.Path(state, "server.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	store, e := initializeLiveChannels(db, state)
	if e != nil {
		t.Fatal(e)
	}
	owner := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "owner", func(string, string) bool { return true }, nil
	}
	_, e = store.Save(context.Background(), owner, livechannels.SourceInput{ID: strings.Repeat("ab", 24), RequestID: strings.Repeat("cd", 24), Name: "Source", Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\",One\nhttps://fixture.invalid/one\n"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = initializeLiveChannels(db, state); e != nil {
		t.Fatal("valid reopen failed", e)
	}
	key := storagepaths.Path(state, "keys", "live-sources.v1.key")
	if _, e = os.Stat(key); !os.IsNotExist(e) {
		t.Fatal("key file present")
	}
	// Source rows are plaintext: initialization succeeds with references and
	// creates no key.
	if _, e = initializeLiveChannels(db, state); e != nil {
		t.Fatal("referenced plaintext sources refused", e)
	}
	if _, e = os.Stat(key); !os.IsNotExist(e) {
		t.Fatal("key was recreated")
	}
}
