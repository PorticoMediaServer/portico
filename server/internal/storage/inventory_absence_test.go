package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInventoryAbsenceHelperVerifiesRootParentAndLeaf(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	observed, err := LocalInventoryDirectory(InventoryRequest{Root: root, RelativePath: "."})
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client := New(binary)
	req := InventoryRequest{Root: root, RootIdentity: RootIdentity(observed), RelativePath: filepath.Join("folder", "gone.mkv")}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	absent, err := client.InventoryAbsence(ctx, "source", req)
	if err != nil || !absent {
		t.Fatalf("readable known root: absent=%v err=%v", absent, err)
	}
	if err = os.WriteFile(filepath.Join(root, req.RelativePath), []byte("present"), 0600); err != nil {
		t.Fatal(err)
	}
	absent, err = client.InventoryAbsence(ctx, "source", req)
	if err != nil || absent {
		t.Fatalf("present file: absent=%v err=%v", absent, err)
	}
	req.RootIdentity = "different-root"
	absent, err = client.InventoryAbsence(ctx, "source", req)
	if err == nil || absent {
		t.Fatalf("changed root: absent=%v err=%v", absent, err)
	}
	req.RootIdentity = RootIdentity(observed)
	req.RelativePath = filepath.Join("unreadable-or-absent", "gone.mkv")
	absent, err = client.InventoryAbsence(ctx, "source", req)
	if err == nil || absent {
		t.Fatalf("absent parent: absent=%v err=%v", absent, err)
	}
	req.RelativePath = filepath.Join("..", "outside.mkv")
	absent, err = client.InventoryAbsence(ctx, "source", req)
	if err == nil || absent {
		t.Fatalf("escaping path: absent=%v err=%v", absent, err)
	}
}

func TestInventoryAbsenceRejectsOutsideParentLink(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "outside")); err != nil {
		t.Skip(err)
	}
	observed, err := LocalInventoryDirectory(InventoryRequest{Root: root, RelativePath: "."})
	if err != nil {
		t.Fatal(err)
	}
	absent, err := LocalInventoryAbsence(InventoryRequest{Root: root, RootIdentity: RootIdentity(observed), RelativePath: filepath.Join("outside", "gone.mkv"), FollowSymlinks: true})
	if err == nil || absent {
		t.Fatalf("outside parent: absent=%v err=%v", absent, err)
	}
}
