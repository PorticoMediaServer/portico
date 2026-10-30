//go:build windows

package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsLinkedRootKeepsTargetDirectory(t *testing.T) {
	client, target := registrationFixture(t)
	link := filepath.Join(t.TempDir(), "library-link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("directory symlink creation needs Developer Mode or privilege: %v", err)
	}
	if err := checkLocalRoot(link); err != nil {
		t.Fatal("linked root refused", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registration, err := client.RegisterRoot(ctx, link, "", "", ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer registration.Close()
	lease, err := registration.Borrow("tiny")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	info, err := lease.Directory.Stat()
	expected, expectedErr := os.Stat(target)
	if err != nil || expectedErr != nil || !os.SameFile(info, expected) {
		t.Fatal("root did not retain target identity", err, expectedErr)
	}
}
