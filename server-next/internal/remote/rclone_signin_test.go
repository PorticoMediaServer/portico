package remote_test

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
)

// rclone serves a remote whose sign-in is refused as an empty top, so a test that listed only
// what rclone served passed. The remote is asked first, with or without a folder, and a refused
// sign-in is said as one.
func TestARefusedSignInIsSaidBeforeServing(t *testing.T) {
	fakeRcloneBinary(t)
	wrong := "[g]\ntype = drive\nclient_id = own-client\ntoken = not-the-token\n"
	options, err := remote.ParseRcloneConfig(wrong)
	if err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{"", "Media"} {
		src := remote.NewRclone(options, folder)
		if _, err := src.List(ctx, ""); !errors.Is(err, remote.ErrSignIn) {
			t.Errorf("folder %q: %v", folder, err)
		}
		src.Close()
		if err := remote.Test(ctx, "rclone", remote.Config{Path: folder}, remote.Secret{Rclone: wrong}); !errors.Is(err, remote.ErrSignIn) {
			t.Errorf("Test with folder %q: %v", folder, err)
		}
	}
}

// A token the provider gave rclone during a test before saving is the one saved: the pasted
// one may already be retired.
func TestATokenFromATestIsTheOneSaved(t *testing.T) {
	fakeRcloneBinary(t)
	secret := remote.Secret{Rclone: "[g]\ntype = drive\nclient_id = own-client\ntoken = " + fakeToken + "\n"}
	if err := remote.TestKeepingToken(ctx, "rclone", remote.Config{Path: "Media"}, &secret); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(secret.Rclone, "token = "+refreshedToken) || strings.Contains(secret.Rclone, fakeToken) || strings.Count(secret.Rclone, "token =") != 1 {
		t.Fatalf("the secret to save: %q", secret.Rclone)
	}
}

// A saved source whose sign-in stopped working is found out by its Test, and marked as needing
// its sign-in again, although rclone would go on serving it as empty.
func TestASavedSourceWhoseSignInStoppedWorkingNeedsItAgain(t *testing.T) {
	fakeRcloneBinary(t)
	dir := t.TempDir()
	d, err := db.Open(context.Background(), filepath.Join(dir, "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	registry, err := remote.NewRegistry(d, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Close)
	sealed, err := registry.Seal("src_two", remote.Secret{Rclone: "[g]\ntype = drive\nclient_id = own-client\ntoken = revoked\n"})
	if err != nil {
		t.Fatal(err)
	}
	row, err := d.CreateRemoteSource(ctx, "src_two", db.NewRemoteSource{UserID: 1, Kind: "rclone", Name: "Drive", Config: `{}`, Secret: sealed, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Check(ctx, row.ID); !errors.Is(err, remote.ErrSignIn) {
		t.Fatalf("Check: %v", err)
	}
	if got, err := d.RemoteSourceByID(ctx, row.ID); err != nil || got.State != "sign_in" {
		t.Fatalf("the source's state: %v %q", err, got.State)
	}
}

// A test that fails (a mistyped folder) still hands back the token the provider gave during it:
// the token it started with may be retired.
func TestATokenFromAFailedTestIsHandedBack(t *testing.T) {
	fakeRcloneBinary(t)
	secret := remote.Secret{Rclone: "[g]\ntype = drive\nclient_id = own-client\ntoken = " + fakeToken + "\n"}
	if err := remote.TestKeepingToken(ctx, "rclone", remote.Config{Path: "Meda"}, &secret); !errors.Is(err, remote.ErrFolderMissing) {
		t.Fatalf("the test: %v", err)
	}
	if !strings.Contains(secret.Rclone, "token = "+refreshedToken) {
		t.Fatalf("the secret after a failed test: %q", secret.Rclone)
	}
}

// rclone serves a source it cannot reach, or one that asks for a rest, as an empty folder too.
// Asked first, an unreachable source is said to be unreachable, and a rate limit or quota is
// the source asking to slow down, which every request then waits for (rule 14).
func TestAnUnreachableOrRateLimitedRemoteIsSaidSo(t *testing.T) {
	fakeRcloneBinary(t)
	for _, tc := range []struct {
		token string
		check func(error) bool
	}{
		{"unreachable", func(err error) bool { var u *url.Error; return errors.As(err, &u) }},
		{"rate-limited", func(err error) bool { var s *remote.SlowDown; return errors.As(err, &s) }},
	} {
		options, err := remote.ParseRcloneConfig("[g]\ntype = drive\nclient_id = own-client\ntoken = " + tc.token + "\n")
		if err != nil {
			t.Fatal(err)
		}
		src := remote.NewRclone(options, "")
		_, err = src.List(ctx, "")
		src.Close()
		if !tc.check(err) || errors.Is(err, remote.ErrSignIn) {
			t.Errorf("%s: %v", tc.token, err)
		}
	}
}

// A test before saving that runs out of time while the source rests is the source asking to
// slow down, not a source that did not answer.
func TestATestCutShortByARestSaysSo(t *testing.T) {
	fakeRcloneBinary(t)
	short, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	err := remote.Test(short, "rclone", remote.Config{}, remote.Secret{Rclone: "[g]\ntype = drive\nclient_id = own-client\ntoken = rate-limited\n"})
	var slow *remote.SlowDown
	if !errors.As(err, &slow) {
		t.Fatalf("a test cut short by a rest: %v", err)
	}
}
