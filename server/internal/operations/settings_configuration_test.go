package operations

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestSettingsConfigurationFileRuntimeAndDurableProjection(t *testing.T) {
	s, owner, authorize := consoleFixture(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "portico.toml")
	if err := os.WriteFile(path, []byte("[general]\nname='Library room'\n[streaming]\nupload_speed=25\n[updates]\nchannel='stable'\n[unrelated]\nkept=true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeConfiguration(ctx, path); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Settings(ctx, authorize)
	if err != nil || doc.Requested.Name != "Library room" || doc.Requested.UploadCapacityKbps != 25000 {
		t.Fatal(doc, err)
	}
	config, err := s.Configuration(ctx, authorize)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, field := range config.Fields {
		if field.ID == "streaming.uploadSpeed" {
			found = true
			if (field.Current == nil || *field.Current != float64(25)) || field.Default != nil || field.Source != "file" {
				t.Fatal(field)
			}
		}
	}
	if !found {
		t.Fatal("upload missing")
	}
	values := doc.Requested
	values.UploadCapacityKbps = 0
	values.OwnAddressEnabled = false
	if _, err = s.ApplySettings(ctx, owner, authorize, SettingsChange{doc.Revision, "write-empty-upload", values}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "kept = true") {
		t.Fatal("unrelated configuration lost", string(raw))
	}
	var pending, graphs int
	if err = s.DB.QueryRow(`SELECT pending_revision FROM server_configuration_state WHERE singleton=1`).Scan(&pending); err != nil || pending != 0 {
		t.Fatal(pending, err)
	}
	if err = s.DB.QueryRow(`SELECT count(*) FROM console_documents WHERE scope='server'`).Scan(&graphs); err != nil || graphs != 0 {
		t.Fatal("serialized server settings persisted", graphs, err)
	}
	// Simulate a process ending after its DB commit but before the file export.
	s.ConfigFile = nil
	current, _ := s.Settings(ctx, authorize)
	values = current.Requested
	values.Name = "After restart"
	if _, err = s.ApplySettings(ctx, owner, authorize, SettingsChange{current.Revision, "pending-projection", values}); err != nil {
		t.Fatal(err)
	}
	reloaded := New(s.DB)
	if err = reloaded.InitializeConfiguration(ctx, path); err != nil {
		t.Fatal(err)
	}
	current, err = reloaded.Settings(ctx, authorize)
	if err != nil || current.Requested.Name != "After restart" {
		t.Fatal(current, err)
	}
	// Invalid external values change neither the stored settings nor the revision.
	before := current.Revision
	if err = os.WriteFile(path, []byte("[remote]\nown_certificate=true\ncertificate_path='/cert.pem'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = reloaded.ConfigFile.Reload(); err != nil {
		t.Fatal(err)
	}
	if err = reloaded.ImportConfiguration(ctx); err == nil {
		t.Fatal("incomplete enabled certificate accepted")
	}
	current, err = reloaded.Settings(ctx, authorize)
	if err != nil || current.Revision != before || current.Requested.OwnAddressEnabled {
		t.Fatal("invalid config mutated runtime", current, err)
	}
	// The normal current-baseline opener accepts this normalized state.
	var dbPath string
	if err = s.DB.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&dbPath); err != nil {
		t.Fatal(err)
	}
	reopened, err := persistence.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	reopened.Close()
}
func TestOwnAddressStartsOffAndCanKeepIncompleteDraft(t *testing.T) {
	v := DefaultSettings()
	if v.OwnAddressEnabled {
		t.Fatal("own address must default off")
	}
	v.CustomCertificateDomain = "media.example.com"
	if err := validateSettings(&v); err != nil {
		t.Fatal("disabled draft refused", err)
	}
	v.OwnAddressEnabled = true
	if err := validateSettings(&v); err == nil {
		t.Fatal("enabled incomplete certificate accepted")
	}
	v.CustomCertificatePath = "/config/cert.pem"
	v.CustomCertificateKeyPath = "/config/key.pem"
	if err := validateSettings(&v); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsConfigurationNotifiesOnlyCommittedFileChanges(t *testing.T) {
	s, _, authorize := consoleFixture(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "portico.toml")
	if err := s.InitializeConfiguration(ctx, path); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.ConfigChanged = func(context.Context) { calls++ }
	importFile := func(contents string) error {
		t.Helper()
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ConfigFile.Reload(); err != nil {
			t.Fatal(err)
		}
		return s.ImportConfiguration(ctx)
	}
	if err := importFile("[streaming]\nallow_conversion='sound'\n"); err != nil || calls != 1 {
		t.Fatal("committed policy did not notify", calls, err)
	}
	before, err := s.Settings(ctx, authorize)
	if err != nil {
		t.Fatal(err)
	}
	if err = importFile("[streaming]\nallow_conversion='sound'\n[unrelated]\nkept=true\n"); err != nil || calls != 1 {
		t.Fatal("unchanged policy woke consumers", calls, err)
	}
	if err = importFile("[streaming]\nallow_conversion='invented'\n"); err == nil || calls != 1 {
		t.Fatal("invalid policy notified consumers", calls, err)
	}
	after, err := s.Settings(ctx, authorize)
	if err != nil || after.Revision != before.Revision || after.Requested.ConversionMode != "sound" {
		t.Fatal("invalid/no-op import changed canonical policy", after, err)
	}
}

func TestSettingsConfigurationPreservesIndependentFileEditWhileAppProjectionPending(t *testing.T) {
	s, owner, authorize := consoleFixture(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "portico.toml")
	if err := os.WriteFile(path, []byte("[general]\nname='Before'\n[streaming]\nupload_speed=25\noutput_format='auto'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeConfiguration(ctx, path); err != nil {
		t.Fatal(err)
	}
	doc, _ := s.Settings(ctx, authorize)
	values := doc.Requested
	values.Name = "App edit"
	values.ConversionMode = "sound"
	loader := s.ConfigFile
	s.ConfigFile = nil // End the process between the DB commit and file write.
	if _, err := s.ApplySettings(ctx, owner, authorize, SettingsChange{doc.Revision, "pending-independent", values}); err != nil {
		t.Fatal(err)
	}
	s.ConfigFile = loader
	if err := os.WriteFile(path, []byte("[general]\nname='Before'\n[streaming]\nupload_speed=50\noutput_format='h264'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := s.ImportConfiguration(ctx); err != nil {
		t.Fatal(err)
	}
	doc, _ = s.Settings(ctx, authorize)
	if doc.Requested.Name != "App edit" || doc.Requested.UploadCapacityKbps != 50000 || doc.Requested.OutputFormat != "h264" || doc.Requested.ConversionMode != "sound" {
		t.Fatal("pending app write or independent external edit lost", doc)
	}
	if err := s.FlushConfiguration(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ImportConfiguration(ctx); err != nil {
		t.Fatal(err)
	}
	doc, _ = s.Settings(ctx, authorize)
	if doc.Requested.Name != "App edit" || doc.Requested.UploadCapacityKbps != 50000 || doc.Requested.OutputFormat != "h264" {
		t.Fatal("flush overwrote another registered field", doc)
	}
	var queued int
	if err := s.DB.QueryRow(`SELECT count(*) FROM server_configuration_pending`).Scan(&queued); err != nil || queued != 0 {
		t.Fatal("completed field projection was not drained", queued, err)
	}
}

func TestSettingsConfigurationRestartImportsIndependentEditsAfterDrainingProjection(t *testing.T) {
	s, owner, authorize := consoleFixture(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "portico.toml")
	if err := s.InitializeConfiguration(ctx, path); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Settings(ctx, authorize)
	if err != nil {
		t.Fatal(err)
	}
	values := doc.Requested
	values.Name = "Committed app name"
	s.ConfigFile = nil // Process ended after the database commit, before projection.
	if _, err = s.ApplySettings(ctx, owner, authorize, SettingsChange{doc.Revision, "restart-independent-edit", values}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("[general]\nname='Older file name'\n[streaming]\nmusic_away_quality='96'\noutput_format='h264'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	restarted := New(s.DB)
	if err = restarted.InitializeConfiguration(ctx, path); err != nil {
		t.Fatal(err)
	}
	current, err := restarted.Settings(ctx, authorize)
	if err != nil {
		t.Fatal(err)
	}
	if current.Requested.Name != "Committed app name" || current.Requested.MusicAwayQuality != "96" || current.Requested.OutputFormat != "h264" {
		t.Fatal("restart failed to combine the committed app edit with independent file policy", current)
	}
	// The flushed loader already knows these exact bytes: a watcher won't produce
	// another event to rescue a skipped startup import.
	if changed, err := restarted.ConfigFile.Reload(); err != nil || changed {
		t.Fatal(changed, err)
	}
}

func TestSettingsConfigurationCanonicalVideoPolicyAndManualUploadRange(t *testing.T) {
	s, owner, authorize := consoleFixture(t)
	ctx := context.Background()
	doc, _ := s.Settings(ctx, authorize)
	values := doc.Requested
	values.ConversionMode, values.OutputFormat, values.DownloadMaxQuality = "sound", "h264", "720p-high"
	values.UploadCapacityKbps = 100000000
	after, err := s.ApplySettings(ctx, owner, authorize, SettingsChange{doc.Revision, "media-policy", values})
	if err != nil {
		t.Fatal(err)
	}
	if after.Requested.ConversionMode != "sound" || after.Requested.OutputFormat != "h264" || after.Requested.DownloadMaxQuality != "720p-high" || !after.Requested.TranscodingEnabled {
		t.Fatal(after)
	}
	for _, invalid := range []string{"automatic", "invented"} {
		values.DownloadMaxQuality = invalid
		if err := validateSettings(&values); err == nil {
			t.Fatal("invalid owner download cap accepted", invalid)
		}
	}
	values.DownloadMaxQuality = "original"
	values.ConversionMode = "never"
	if err := validateSettings(&values); err != nil || values.TranscodingEnabled {
		t.Fatal("legacy gate did not derive from canonical conversion mode", err)
	}
	if _, exposed := SettingsValues(values)["transcodingEnabled"]; exposed {
		t.Fatal("independent legacy gate exposed")
	}
}

func TestSettingsConfigurationReportsExistingServerPolicySource(t *testing.T) {
	s, _, authorize := consoleFixture(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=0,per_account_cap=2 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Configuration(ctx, authorize)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, field := range doc.Fields {
		if field.ID == "streaming.allowConversion" || field.ID == "people.maxStreamsEach" {
			if field.Source != "server" || field.Current == nil {
				t.Fatal("stored server policy presented as factory default", field)
			}
			found[field.ID] = true
		}
	}
	if len(found) != 2 {
		t.Fatal("existing policy fields missing", found)
	}
}

func TestSettingsConfigurationProjectionAcknowledgementPreservesLowerDomainRevision(t *testing.T) {
	s, _, _ := consoleFixture(t)
	ctx := context.Background()
	enqueue := func(field string, revision int64) {
		t.Helper()
		if err := dbwork.WithWriteTx(ctx, s.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error { return QueueConfigurationProjectionTx(ctx, tx, field, revision) }); err != nil {
			t.Fatal(err)
		}
	}
	enqueue("name", 20)
	// The file writer captured this row before another normalized domain commits.
	written := map[string]int64{"name": 20}
	enqueue("musicAwayQuality", 2)
	if err := s.acknowledgeConfigurationProjection(ctx, written); err != nil {
		t.Fatal(err)
	}
	var pending, queued int64
	if err := s.DB.QueryRow(`SELECT pending_revision FROM server_configuration_state WHERE singleton=1`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT revision FROM server_configuration_pending WHERE field='musicAwayQuality'`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if pending != 2 || queued != 2 {
		t.Fatal("later domain commit became invisible to the file drainer", pending, queued)
	}
	if err := s.acknowledgeConfigurationProjection(ctx, map[string]int64{"musicAwayQuality": 2}); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT pending_revision FROM server_configuration_state WHERE singleton=1`).Scan(&pending); err != nil || pending != 0 {
		t.Fatal(pending, err)
	}
}
