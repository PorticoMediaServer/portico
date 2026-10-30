package storage

import (
	"context"
	"errors"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"strings"
	"testing"
	"time"
)

func TestVersionedDescriptorActualBytesAndReplacement(t *testing.T) {
	c, path := versionedLocalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := c.ReadVersionedDescriptor(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	// SHA256 of the literal 0123456789, independently supplied known digest.
	if first.Data != "0123456789" || first.Digest != "84d89877f0d4041efb6bf91a16f0248f2fd573e6af05c19f96bedb9f882f7882" || first.Version.Evidence().Size != 10 {
		t.Fatalf("wrong actual descriptor snapshot %+v", first)
	}
	again, err := c.ReadVersionedDescriptor(ctx, path)
	if err != nil || again.Version.ID() != first.Version.ID() || again.Digest != first.Digest {
		t.Fatalf("unchanged descriptor drift %v", err)
	}
	replacement := path + ".replacement"
	if err := os.WriteFile(replacement, []byte("abcdefghij"), 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(0, first.ModifiedNS)
	if err := os.Chtimes(replacement, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	next, err := c.ReadVersionedDescriptor(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if next.Data != "abcdefghij" || next.ModifiedNS != first.ModifiedNS || next.Version.ID() == first.Version.ID() || next.Digest == first.Digest {
		t.Fatal("replacement bytes/version not distinguished")
	}
}

func TestVersionedDescriptorBoundsQualificationAndCancellation(t *testing.T) {
	c, path := versionedLocalFixture(t)
	if err := os.WriteFile(path, []byte(strings.Repeat("a", (64<<10)+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadVersionedDescriptor(context.Background(), path); !errors.Is(err, ErrDescriptorBounds) {
		t.Fatalf("oversized descriptor accepted: %v", err)
	}
	c.VersionPolicy = nil
	if _, err := c.ReadVersionedDescriptor(context.Background(), path); !errors.Is(err, mediasource.ErrIdentityRequired) {
		t.Fatalf("unqualified descriptor accepted: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	// Use a qualified fixture to verify caller cancellation propagates independently
	// of policy rejection and no successful snapshot can outlive a canceled read.
	c, path = versionedLocalFixture(t)
	if _, err := c.ReadVersionedDescriptor(canceled, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled descriptor accepted: %v", err)
	}
}

func TestVersionedDescriptorReplacementBetweenDiscoveryAndRead(t *testing.T) {
	c, path := versionedLocalFixture(t)
	replacement := path + ".replacement"
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, []byte("abcdefghij"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	policy := c.VersionPolicy
	calls := 0
	c.VersionPolicy = func(p string) (LocalVersionPolicy, error) {
		calls++
		// The second policy call admits the actual versioned reader after discovery.
		// Inject an owned atomic replacement at that boundary, without scheduler races.
		if calls == 2 {
			if err := os.Rename(replacement, path); err != nil {
				return LocalVersionPolicy{}, err
			}
		}
		return policy(p)
	}
	snapshot, err := c.ReadVersionedDescriptor(context.Background(), path)
	if !errors.Is(err, mediasource.ErrSourceChanged) || snapshot.Data != "" || snapshot.Version.ID() != "" {
		t.Fatalf("mixed discovery/read snapshot accepted: %+v %v", snapshot, err)
	}
}
