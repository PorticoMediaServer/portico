package artwork

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestPictureNamesStayPrivateAndStableWithTheDataFolder(t *testing.T) {
	dir := t.TempDir()
	data := []byte("public provider picture")
	first, err := New(dir).Hash(data)
	if err != nil {
		t.Fatal(err)
	}
	again, err := New(dir).Hash(data)
	if err != nil || again != first {
		t.Fatal("reopening changed picture address", err)
	}
	other, err := New(t.TempDir()).Hash(data)
	if err != nil || other == first {
		t.Fatal("servers share a picture address", err)
	}
	sum := sha256.Sum256(data)
	if first == hex.EncodeToString(sum[:]) || !ValidID(first) {
		t.Fatal("public content hash exposed or unusable address")
	}
	if info, err := os.Stat(filepath.Join(dir, "secrets.key")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("key permissions", err)
	}
}
