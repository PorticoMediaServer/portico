package backup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"strings"
	"testing"
)

func TestBackupConfigurationUsesCanonicalPolicyAndSanitizedProjection(t *testing.T) {
	service, policy, state := policyFixture(t)
	ctx := context.Background()
	configuration := operations.New(service.db)
	configuration.ConfigurationDomains = []operations.ConfigurationDomain{Configuration{Store: policy}}
	path := filepath.Join(state, "portico.toml")
	if err := os.WriteFile(path, []byte("[backup]\ninclude_artwork=true\n[unrelated]\nkeep=true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := configuration.InitializeConfiguration(ctx, path); err != nil {
		t.Fatal(err)
	}
	snapshot, err := policy.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.clear()
	if !snapshot.IncludeArtwork {
		t.Fatal("runtime ignored imported backup policy")
	}
	doc, err := configuration.Configuration(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, field := range doc.Fields {
		if field.ID == "storage.backupArtwork" {
			found = field.Source == "file" && field.Current != nil && *field.Current == true && field.Default != nil && *field.Default == false
		}
	}
	if !found {
		t.Fatal("backup policy source or actual factory default absent")
	}
	policy.ConfigurationCommitted = configuration.FlushConfiguration
	if _, err = policy.Update(ctx, SettingsPatch{IncludeArtwork: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "include_artwork = false") || !strings.Contains(string(raw), "keep = true") {
		t.Fatal("app policy did not share the nonsecret file projection")
	}
	phrase := "a private backup fixture phrase"
	if _, err = policy.Update(ctx, SettingsPatch{PassphraseProtected: ptr(true), Passphrase: &phrase}); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err = configuration.Configuration(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{raw, encoded} {
		if strings.Contains(string(data), phrase) || strings.Contains(string(data), "passphrase_sealed") || strings.Contains(string(data), "passphraseSet") {
			t.Fatal("secret or presence-only state entered config projection")
		}
	}
	current, err := policy.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer current.clear()
	if !current.PassphraseProtected || current.material == nil {
		t.Fatal("effective protected backup lost private key capability")
	}
}

func TestBackupConfigurationInvalidProtectionRollsBackAllDomains(t *testing.T) {
	service, policy, state := policyFixture(t)
	ctx := context.Background()
	configuration := operations.New(service.db)
	configuration.ConfigurationDomains = []operations.ConfigurationDomain{Configuration{Store: policy}}
	path := filepath.Join(state, "portico.toml")
	if err := configuration.InitializeConfiguration(ctx, path); err != nil {
		t.Fatal(err)
	}
	before, err := configuration.Settings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("[general]\nname='Must not commit'\n[backup]\ninclude_artwork=true\npassphrase_protected=true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = configuration.ConfigFile.Reload(); err != nil {
		t.Fatal(err)
	}
	if err = configuration.ImportConfiguration(ctx); err == nil {
		t.Fatal("enabled protection without a stored key")
	}
	after, err := configuration.Settings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := policy.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || after.Requested.Name != before.Requested.Name || settings.IncludeArtwork || settings.PassphraseProtected {
		t.Fatal("invalid backup import partially committed")
	}
	var sources int
	if err = service.db.QueryRow(`SELECT count(*) FROM server_setting_sources WHERE field LIKE 'backup:%'`).Scan(&sources); err != nil || sources != 0 {
		t.Fatal("rolled-back source metadata escaped", sources, err)
	}
}

func TestBackupConfigurationRecoversCommittedAppPolicyBeforeExternalImport(t *testing.T) {
	service, policy, state := policyFixture(t)
	ctx := context.Background()
	if _, err := policy.Update(ctx, SettingsPatch{IncludeArtwork: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	configuration := operations.New(service.db)
	configuration.ConfigurationDomains = []operations.ConfigurationDomain{Configuration{Store: policy}}
	path := filepath.Join(state, "portico.toml")
	if err := os.WriteFile(path, []byte("[general]\nname='Independent owner name'\n[backup]\ninclude_artwork=false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := configuration.InitializeConfiguration(ctx, path); err != nil {
		t.Fatal(err)
	}
	current, err := policy.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	server, err := configuration.Settings(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	if !current.IncludeArtwork || server.Requested.Name != "Independent owner name" {
		t.Fatal("startup lost a committed app edit or independent file edit")
	}
	var pending int
	if err = service.db.QueryRow(`SELECT pending_revision FROM server_configuration_state WHERE singleton=1`).Scan(&pending); err != nil || pending != 0 {
		t.Fatal(pending, err)
	}
}
