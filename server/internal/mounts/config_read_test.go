package mounts

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const accountConfigFixture = `# the server account's own rclone configuration
[nas]
type = sftp
host = storage.example
user = media

[archive]
type = s3
provider = AWS
access_key_id = public
secret_access_key = private-secret

[oldftp]
type = ftp
host = legacy.example

[wrapped]
type = crypt
remote = archive:private
password = RCLONE_ENCRYPT_V0:not-plaintext

[shortcut]
type = alias
remote = archive:private

[bracket]
type = http
url = https://files.example/[media]
`

func writeAccountConfig(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "rclone"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "rclone", "rclone.conf"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", root)
	return filepath.Join(root, "rclone", "rclone.conf")
}

func TestAccountConfigRemotesProjectNamesAndReasonsOnly(t *testing.T) {
	path := writeAccountConfig(t, accountConfigFixture)
	got, remotes, truncated, err := ReadAccountRcloneConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got != path || truncated {
		t.Fatalf("path %q truncated %v, want %q and false", got, truncated, path)
	}
	want := []ConfigRemote{
		{Name: "archive", Kind: "s3", Supported: true},
		{Name: "bracket", Kind: "http", Supported: true},
		{Name: "nas", Kind: "sftp", Supported: true},
		{Name: "oldftp", Kind: "ftp", Reason: "kind"},
		{Name: "shortcut", Kind: "alias", Reason: "wrapper"},
		{Name: "wrapped", Kind: "crypt", Reason: "encrypted"},
	}
	if len(remotes) != len(want) {
		t.Fatalf("remotes %#v", remotes)
	}
	for index, remote := range remotes {
		if remote != want[index] {
			t.Fatalf("remote %d = %#v, want %#v", index, remote, want[index])
		}
	}
}

func TestAccountConfigRemotesAreEmptyWhenAbsent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	path, remotes, _, err := ReadAccountRcloneConfig()
	if err != nil {
		t.Fatal(err)
	}
	if path != "" || len(remotes) != 0 {
		t.Fatalf("path %q remotes %#v, want none", path, remotes)
	}
	if _, err = ReadAccountRcloneSection("archive"); !errors.Is(err, ErrRemoteNotConfigured) {
		t.Fatalf("section error %v, want ErrRemoteNotConfigured", err)
	}
}

func TestAccountConfigSectionIsPlainAndValidated(t *testing.T) {
	writeAccountConfig(t, accountConfigFixture)
	config, err := ReadAccountRcloneSection("archive")
	if err != nil {
		t.Fatal(err)
	}
	want := "[archive]\ntype = s3\nprovider = AWS\naccess_key_id = public\nsecret_access_key = private-secret\n"
	if config != want {
		t.Fatalf("config %q, want %q", config, want)
	}
	if err = validateConfig(config, "archive:"); err != nil {
		t.Fatalf("section is not valid for save: %v", err)
	}
	for _, remote := range []string{"oldftp", "wrapped", "shortcut", "missing", "bad name", "archive:"} {
		if _, err = ReadAccountRcloneSection(remote); err == nil {
			t.Fatalf("%q was accepted", remote)
		}
	}
}

func TestAccountConfigReportsItsOwnListBound(t *testing.T) {
	body := strings.Builder{}
	for index := 0; index < configRemoteLimit+3; index++ {
		body.WriteString("[remote" + strconv.Itoa(index) + "]\ntype = s3\n")
	}
	writeAccountConfig(t, body.String())
	_, remotes, truncated, err := ReadAccountRcloneConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(remotes) != configRemoteLimit {
		t.Fatalf("remotes %d truncated %v, want %d and true", len(remotes), truncated, configRemoteLimit)
	}
	for index := 1; index < len(remotes); index++ {
		if remotes[index-1].Name >= remotes[index].Name {
			t.Fatalf("window is not sorted at %d", index)
		}
	}
}

func TestAccountConfigReceiptIdentityIgnoresMutableContent(t *testing.T) {
	service := &Service{}
	one := Command{OperationID: "1788580000000-12345678-1234-1234-1234-123456789012", Action: "create", Name: "Archive", Remote: "archive:", Config: "first", receiptKey: accountConfigReceiptKey}
	two := one
	two.Config = "second"
	if service.fingerprint("actor", "", one) != service.fingerprint("actor", "", two) {
		t.Fatal("a receipt with an identity key must not depend on configuration content")
	}
	plain := one
	plain.receiptKey = ""
	other := plain
	other.Config = "second"
	if service.fingerprint("actor", "", plain) == service.fingerprint("actor", "", other) {
		t.Fatal("an ordinary receipt still binds its configuration content")
	}
}

func TestAccountConfigDuplicateNameIsNeverSilentlyFirst(t *testing.T) {
	writeAccountConfig(t, `[archive]
type = s3
secret_access_key = first

[archive]
type = s3
secret_access_key = second
`)
	_, remotes, _, err := ReadAccountRcloneConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(remotes) != 2 || remotes[0].Reason != "duplicate" || remotes[1].Reason != "duplicate" || remotes[0].Supported {
		t.Fatalf("duplicate remotes %#v", remotes)
	}
	if _, err = ReadAccountRcloneSection("archive"); !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("duplicate section error %v, want ErrConfigInvalid", err)
	}
}

func TestAccountConfigAmbiguousGrammarIsWithheld(t *testing.T) {
	for _, body := range []string{
		"[archive]\ntype = s3\ntype = b2\nsecret_access_key = value\n",
		"[archive]\ntype = s3\nnot a key value pair\n",
		"[archive\ntype = s3\n",
	} {
		writeAccountConfig(t, body)
		_, remotes, _, err := ReadAccountRcloneConfig()
		if err != nil {
			t.Fatal(err)
		}
		if body == "[archive]\ntype = s3\ntype = b2\nsecret_access_key = value\n" {
			if len(remotes) != 1 || remotes[0].Reason != "ambiguous" || remotes[0].Supported {
				t.Fatalf("repeated type remotes %#v", remotes)
			}
		} else if len(remotes) != 0 {
			t.Fatalf("ambiguous grammar listed %#v", remotes)
		}
		if _, err = ReadAccountRcloneSection("archive"); !errors.Is(err, ErrConfigInvalid) {
			t.Fatalf("ambiguous grammar section error %v, want ErrConfigInvalid", err)
		}
	}
}

func TestAccountConfigRejectsNonRegularFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	if err := os.MkdirAll(filepath.Join(root, "rclone", "rclone.conf"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ReadAccountRcloneConfig(); !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("directory config error %v, want ErrConfigInvalid", err)
	}
	if _, err := ReadAccountRcloneSection("archive"); !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("directory section error %v, want ErrConfigInvalid", err)
	}
}
