package operations

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMediaPathsApplyOnlyAfterRestart(t *testing.T) {
	saved := startupMediaSettings.Load()
	defer startupMediaSettings.Store(saved)
	startupMediaSettings.Store(nil)
	store, principal, auth := consoleFixture(t)
	ctx := context.Background()
	startup, err := StartupMediaSettings(ctx, store.DB)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Settings(ctx, auth)
	if err != nil {
		t.Fatal(err)
	}
	values := before.Requested
	values.FFmpegPath = filepath.Join(t.TempDir(), "ffmpeg")
	values.TemporaryDirectory = t.TempDir()
	values.SessionStorageMiB = 16384
	after, err := store.ApplySettings(ctx, principal, auth, SettingsChange{ExpectedRevision: before.Revision, IdempotencyKey: "media-paths", Values: values})
	if err != nil {
		t.Fatal(err)
	}
	if after.Effective.TemporaryDirectory != startup.TemporaryDirectory || after.Effective.FFmpegPath != startup.FFmpegPath || len(after.RestartFields) != 2 || after.Effective.SessionStorageMiB != 16384 {
		t.Fatal(after)
	}
	adopted, err := StartupMediaSettings(ctx, store.DB)
	if err != nil {
		t.Fatal(err)
	}
	if adopted.TemporaryDirectory != values.TemporaryDirectory || adopted.FFmpegPath != values.FFmpegPath {
		t.Fatal(adopted)
	}
	after, err = store.Settings(ctx, auth)
	if err != nil || len(after.RestartFields) != 0 {
		t.Fatal(after, err)
	}
	if ConversionDirectory("/state", values.TemporaryDirectory) != filepath.Join(values.TemporaryDirectory, "portico-hls") {
		t.Fatal("configured folder was not used")
	}
}

func TestConversionSettingsRejectInvalidLimitsAndPaths(t *testing.T) {
	values := DefaultSettings()
	values.FFmpegPath = "relative/ffmpeg"
	if validateSettings(&values) == nil {
		t.Fatal("accepted relative executable")
	}
	values = DefaultSettings()
	values.SessionStorageMiB = 1
	if validateSettings(&values) == nil {
		t.Fatal("accepted unsafe storage budget")
	}
}

func TestRejectedFFmpegOverrideStaysRequested(t *testing.T) {
	before := startupMediaSettings.Load()
	defer startupMediaSettings.Store(before)
	v := DefaultSettings()
	v.FFmpegPath = "/missing/ffmpeg"
	v.TemporaryDirectory = "/working"
	startupMediaSettings.Store(&v)
	UseBundledFFmpeg()
	effective, pending := effectiveMediaSettings(v)
	if effective.FFmpegPath != "" || effective.TemporaryDirectory != "/working" || len(pending) != 1 || pending[0] != "ffmpegPath" || v.FFmpegPath != "/missing/ffmpeg" {
		t.Fatal(effective, pending)
	}
}

func TestAppliedDirectoryPreservesFFmpegRestartState(t *testing.T) {
	before := startupMediaSettings.Load()
	defer startupMediaSettings.Store(before)
	started := DefaultSettings()
	startupMediaSettings.Store(&started)
	requested := started
	requested.FFmpegPath = filepath.Join(t.TempDir(), "ffmpeg")
	requested.TemporaryDirectory = t.TempDir()
	effective, pending := effectiveMediaSettings(requested)
	document := SettingsDocument{Requested: requested, Effective: effective, RestartFields: pending}
	applied := started.TemporaryDirectory
	store := Store{AppliedTemporaryDirectory: &applied}
	store.projectAppliedSettings(&document)
	if len(document.RestartFields) != 2 || document.Effective.FFmpegPath != "" || document.Effective.TemporaryDirectory != applied {
		t.Fatal(document)
	}
	requested.TemporaryDirectory = applied
	document.Requested = requested
	store.projectAppliedSettings(&document)
	if len(document.RestartFields) != 1 || document.RestartFields[0] != "ffmpegPath" {
		t.Fatal(document.RestartFields)
	}
}

func TestConnectionLimitReportsItsAppliedStartupValue(t *testing.T) {
	prior := startupMediaSettings.Load()
	defer startupMediaSettings.Store(prior)
	started := DefaultSettings()
	started.ConnectionLimit = 100
	startupMediaSettings.Store(&started)
	requested := started
	requested.ConnectionLimit = 200
	effective, fields := effectiveMediaSettings(requested)
	if effective.ConnectionLimit != 100 || len(fields) != 1 || fields[0] != "connectionLimit" {
		t.Fatal("requested connection cap reported as active", effective.ConnectionLimit, fields)
	}
}
