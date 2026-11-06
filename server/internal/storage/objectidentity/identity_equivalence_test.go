package objectidentity

import (
	"math"
	"testing"
)

// Exercise malformed Unix fields as well as valid device/inode and opaque
// Windows/remote identities. The frozen reference is the pre-extraction code,
// not another call to the implementation under test.
func TestLegacyIdentityEquivalence(t *testing.T) {
	identities := []string{"", "ino:31", "remote:abc", "volume:7:file:0a", "volume:8:file:0a",
		"dev:", "dev:1", "dev:1:ino:", "dev:1:ino:2:other", "dev:1:ino:2:fsid:a:fsid:b",
		"dev:1:ino:2:fsid:\x00", "dev:1:ino:2:fsid:\xff", "\xff", "\x00", " dev:1:ino:2"}
	fields := []string{"0", "1", "2", "64", "071", "18446744073709551615", "18446744073709551616", "-1", "+1", "x"}
	suffixes := []string{"", ":fsid:a", ":fsid:b", ":fsid:", ":fsid:0"}
	for _, device := range fields {
		for _, inode := range fields {
			for _, suffix := range suffixes {
				identities = append(identities, "dev:"+device+":ino:"+inode+suffix)
			}
		}
	}
	comparisons := 0
	for _, root := range identities {
		for _, object := range identities {
			if got, want := SameRoot(root, object), legacySameRoot(root, object); got != want {
				t.Fatalf("SameRoot(%q,%q)=%v, pre-extraction=%v", root, object, got, want)
			}
			if got, want := StableObjectIdentity(object, root), legacyStableObjectIdentity(object, root); got != want {
				t.Fatalf("StableObjectIdentity(%q,%q)=%q, pre-extraction=%q", object, root, got, want)
			}
			comparisons += 2
		}
	}
	t.Logf("%d identity inputs, %d old-vs-new root/object comparisons", len(identities), comparisons)
}

func TestLegacyChangeTokenEquivalence(t *testing.T) {
	inodes := []uint64{0, 1, 31, math.MaxUint32, math.MaxUint64}
	values := []int64{math.MinInt64, -11, -1, 0, 1, 42, math.MaxInt64}
	comparisons := 0
	for _, inode := range inodes {
		for _, size := range values {
			for _, modifiedNS := range values {
				got := LocalChangeToken(inode, size, modifiedNS)
				if want := legacyLocalChangeToken(inode, size, modifiedNS); got != want {
					t.Fatalf("LocalChangeToken(%d,%d,%d)=%q, pre-extraction=%q", inode, size, modifiedNS, got, want)
				}
				comparisons++
			}
		}
	}
	t.Logf("%d old-vs-new inode/size/mtime token comparisons", comparisons)
}
