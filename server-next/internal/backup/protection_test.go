package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestProtectedBackupNeedsItsPassphraseAndChecksTheWholeFile(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	phrase := t.Name()
	if err := s.SetPassphrase(phrase); err != nil {
		t.Fatal(err)
	}
	if !s.Protected() {
		t.Fatal("protection is off")
	}
	id := "portico-20261113-030000"
	if err := s.make(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	archive, err := s.Path(id)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(raw, envelopeMagic) || bytes.Contains(raw, []byte(phrase)) || bytes.Contains(raw, []byte("SQLite format 3")) {
		t.Fatal("the backup is not protected")
	}
	list, err := s.List()
	if err != nil || len(list) != 1 || !list[0].Protected {
		t.Fatal(list, err)
	}
	if _, err := Inspect(ctx, archive, s.DataDir); err == nil {
		t.Fatal("inspection accepted no phrase")
	}
	if _, err := InspectWithPassphrase(ctx, archive, s.DataDir, "different"); err == nil {
		t.Fatal("inspection accepted the wrong phrase")
	}
	if _, err := InspectWithPassphrase(ctx, archive, s.DataDir, phrase); err != nil {
		t.Fatal(err)
	}
	if err := StageKeepingWithPassphrase(ctx, archive, s.DataDir, id, s.Now(), false, nil, phrase); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(s.DataDir, pendingDir, "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	damaged := filepath.Join(s.DataDir, "damaged.tar.gz")
	// Drop the authenticated final record, after the complete compressed archive.
	if err := os.WriteFile(damaged, raw[:len(raw)-20], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectWithPassphrase(ctx, damaged, s.DataDir, phrase); err == nil {
		t.Fatal("inspection accepted a truncated envelope")
	}
	if err := StageKeepingWithPassphrase(ctx, damaged, s.DataDir, "damaged", s.Now(), true, nil, phrase); err == nil {
		t.Fatal("staging accepted a truncated envelope")
	}
	after, err := os.ReadFile(filepath.Join(s.DataDir, pendingDir, "main.db"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed restore changed the pending restore", err)
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, pendingDir, protectionFile)); !os.IsNotExist(err) {
		t.Fatal("backup carried its opening key", err)
	}
	if err := s.SetPassphrase(""); err != nil {
		t.Fatal(err)
	}
	if s.Protected() {
		t.Fatal("protection is still on")
	}
	if _, err := InspectWithPassphrase(ctx, archive, s.DataDir, phrase); err != nil {
		t.Fatal("turning protection off broke old backups", err)
	}
	if err := s.make(ctx, "portico-20261114-030000", false); err != nil {
		t.Fatal(err)
	}
	plain, _ := s.Path("portico-20261114-030000")
	if _, err := Inspect(ctx, plain, s.DataDir); err != nil {
		t.Fatal("plain backups stopped working", err)
	}
}
