package operations

import (
	"context"
	"slices"
	"testing"
)

func TestConnectionLimitAppliesOnlyAfterRestart(t *testing.T) {
	saved := startupMediaSettings.Load()
	defer startupMediaSettings.Store(saved)
	startupMediaSettings.Store(nil)
	store, owner, auth := consoleFixture(t)
	ctx := context.Background()
	before, err := store.Settings(ctx, auth)
	if err != nil {
		t.Fatal(err)
	}
	startup := before.Requested
	startupMediaSettings.Store(&startup)
	applied := startup.ConnectionLimit
	store.AppliedConnectionLimit = &applied
	store.AppliedTemporaryDirectory = &startup.TemporaryDirectory
	values := before.Requested
	values.ConnectionLimit = 128
	values.TemporaryDirectory = t.TempDir()
	values.FFmpegPath = t.TempDir() + "/ffmpeg"
	after, err := store.ApplySettings(ctx, owner, auth, SettingsChange{ExpectedRevision: before.Revision, IdempotencyKey: "connection-ceiling", Values: values})
	if err != nil {
		t.Fatal(err)
	}
	if after.Requested.ConnectionLimit != 128 || after.Effective.ConnectionLimit != applied || !slices.Contains(after.RestartFields, "connectionLimit") {
		t.Fatalf("requested=%d effective=%d pending=%v", after.Requested.ConnectionLimit, after.Effective.ConnectionLimit, after.RestartFields)
	}
	for _, key := range []string{"temporaryDirectory", "ffmpegPath"} {
		if !slices.Contains(after.RestartFields, key) {
			t.Fatalf("lost other restart field %s: %v", key, after.RestartFields)
		}
	}
	reread, err := store.Settings(ctx, auth)
	if err != nil {
		t.Fatal(err)
	}
	if reread.Effective.ConnectionLimit != applied || !slices.Contains(reread.RestartFields, "connectionLimit") {
		t.Fatal("read lost applied ceiling", reread)
	}
	restarted := New(store.DB)
	newLimit := 128
	restarted.AppliedConnectionLimit = &newLimit
	reread, err = restarted.Settings(ctx, auth)
	if err != nil {
		t.Fatal(err)
	}
	if reread.Effective.ConnectionLimit != 128 || slices.Contains(reread.RestartFields, "connectionLimit") {
		t.Fatal("restart did not adopt ceiling", reread)
	}
}
