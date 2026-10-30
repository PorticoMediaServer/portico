package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"runtime"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/access"
)

func TestBackupContainsOnlyConfigDatabaseMetadataAndKeys(t *testing.T) {
	service, data, db := testService(t)
	policy := NewPolicyStore(db, data)
	include := true
	if _, err := policy.Update(context.Background(), SettingsPatch{IncludeArtwork: &include}); err != nil {
		t.Fatal(err)
	}
	service.SetPolicyStore(policy)
	for _, name := range []string{"portico.toml", "Metadata/artwork/poster.jpg", "Metadata/people/person.jpg", "Metadata/subtitles/subtitle.srt", "Metadata/analysis-artifacts/preview", "Keys/networking-keys/identity", "Keys/managed-storage/provider.conf", "Cache/derived", "Logs/messages.log", "Temp/work", "Transcode/segment.ts"} {
		if err := writeTestFile(filepath.Join(data, filepath.FromSlash(name)), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Use the real producer: the invitation key introduced by People belongs
	// under Keys and must survive the data-layout backup scope.
	if err := access.New(service.db).LoadInvitationKey(filepath.Join(data, "Keys")); err != nil {
		t.Fatal(err)
	}
	invitationKey, err := os.ReadFile(filepath.Join(data, "Keys", "invitation-link.key"))
	if err != nil || len(invitationKey) != 32 {
		t.Fatal("invitation key", err)
	}
	id, err := service.Create(context.Background(), KindManual)
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(data, BackupsDir, id)
	manifest, err := readManifest(folder)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, entry := range manifest.Files {
		seen[entry.Name] = true
		name := entry.Name
		if name != "portico.toml" && name != DatabaseName && !strings.HasPrefix(name, "Metadata/") && !strings.HasPrefix(name, "Keys/") {
			t.Errorf("unexpected backup payload %s", name)
		}
	}
	for _, name := range []string{"portico.toml", DatabaseName, "Metadata/artwork/poster.jpg", "Metadata/people/person.jpg", "Metadata/subtitles/subtitle.srt", "Metadata/analysis-artifacts/preview", "Keys/networking-keys/identity", "Keys/managed-storage/provider.conf", "Keys/invitation-link.key"} {
		if !seen[name] {
			t.Errorf("missing backup payload %s", name)
		}
	}
	backedUpKey, err := os.ReadFile(filepath.Join(folder, "Keys", "invitation-link.key"))
	if err != nil || !bytes.Equal(backedUpKey, invitationKey) {
		t.Fatal("backup did not retain the actual invitation key", err)
	}
	for _, name := range []string{"Cache", "Logs", "Temp", "Transcode"} {
		if _, err := os.Stat(filepath.Join(folder, name)); !os.IsNotExist(err) {
			t.Errorf("excluded directory %s copied: %v", name, err)
		}
	}
}

// Exercise the authentication producers against the same portable Data root used
// by main; passing Keys as the identity root would silently create Keys/Keys.
func TestBackupPortableAuthenticationFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, configured := range []bool{false, true} {
		name := "setup"
		if configured {
			name = "owner-reset"
		}
		t.Run(name, func(t *testing.T) {
			service, data, db := testService(t)
			layout := storagepaths.At(data)
			ident, err := identity.New(db, layout.Data)
			if err != nil {
				t.Fatal(err)
			}
			if err = ident.ConfigureResetFile(layout.Data, layout.Keys, true); err != nil {
				t.Fatal(err)
			}
			setup, err := os.ReadFile(storagepaths.Path(data, "setup-token"))
			if err != nil || len(setup) != 43 {
				t.Fatal("actual setup token", err)
			}
			if err = access.New(db).LoadInvitationKey(layout.Keys); err != nil {
				t.Fatal(err)
			}
			invitation, err := os.ReadFile(filepath.Join(layout.Keys, "invitation-link.key"))
			if err != nil || len(invitation) != 32 {
				t.Fatal("actual invitation key", err)
			}
			payload := map[string][]byte{"Keys/invitation-link.key": invitation}
			if configured {
				if _, err = ident.Setup(string(setup), "owner", "Portable-test-password1!", "Fixture"); err != nil {
					t.Fatal(err)
				}
				location, err := ident.RequestOwnerReset(context.Background(), identity.OwnerResetRequest{Username: "unknown"})
				if err != nil {
					t.Fatal(err)
				}
				if location.DataDirectory != layout.Data || location.FileDirectory != layout.Keys || location.FileName != "reset-code.txt" || location.SystemKind != "docker" {
					t.Fatalf("reset does not use the actual portable layout: %+v", location)
				}
				raw, err := os.ReadFile(filepath.Join(location.FileDirectory, location.FileName))
				if err != nil || len(strings.Split(string(raw), "\n")) < 4 {
					t.Fatal("actual reset file", err)
				}
				payload["Keys/reset-code.txt"] = raw
				if _, err = os.Stat(storagepaths.Path(data, "setup-token")); !os.IsNotExist(err) {
					t.Fatal("setup secret survived setup", err)
				}
			} else {
				payload["Keys/setup-token"] = setup
			}
			for name := range payload {
				info, err := os.Stat(filepath.Join(data, filepath.FromSlash(name)))
				if err != nil {
					t.Fatal(err)
				}
				if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
					t.Fatalf("non-private authentication file %s: %v", name, info.Mode())
				}
			}
			for _, relative := range []string{"setup-token", "reset-code.txt", "invitation-link.key", "Keys/Keys"} {
				if _, err = os.Stat(filepath.Join(data, relative)); !os.IsNotExist(err) {
					t.Fatalf("unexpected flat or doubly nested private path %s: %v", relative, err)
				}
			}
			id, err := service.Create(context.Background(), KindManual)
			if err != nil {
				t.Fatal(err)
			}
			folder := filepath.Join(layout.Backups, id)
			for name, want := range payload {
				got, err := os.ReadFile(filepath.Join(folder, filepath.FromSlash(name)))
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("backup changed actual authentication payload %s: %v", name, err)
				}
			}
		})
	}
}
