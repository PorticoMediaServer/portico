//go:build linux

package storage

import (
	"testing"
	"time"
)

func TestMountInfoClassifiesNetworkAndFUSERoots(t *testing.T) {
	raw := []byte(`22 1 259:2 / / rw,relatime shared:1 - ext4 /dev/nvme0n1p2 rw
30 22 0:40 / /mnt/nas rw,relatime shared:2 - nfs4 nas:/media rw
31 22 0:41 / /mnt/cloud rw shared:3 - fuse.rclone rclone rw
32 22 0:42 / /mnt/with\040space rw shared:4 - xfs /dev/sdb1 rw
33 30 0:43 / /mnt/nas/local rw shared:5 - ext4 /dev/sdc1 rw
34 22 0:44 / /srv/library/nas-share rw shared:6 - cifs //nas/share rw
35 22 0:45 / /srv/library/disk2 rw shared:7 - xfs /dev/sdd1 rw
`)
	mounts := parseMountInfo(raw)
	previous := mountTable.entries
	mountTable.entries, mountTable.at = mounts, timeNowForTest()
	t.Cleanup(func() { mountTable.entries, mountTable.at = previous, timeZeroForTest() })
	for path, local := range map[string]bool{
		"/srv/media": true, "/mnt/nas/films": false, "/mnt/nas": false, "/mnt/cloud/x": false,
		"/mnt/with space/films": true, "/mnt/nas/local/films": true, "/mnt/nasx/films": true,
		// A network share mounted inside a local library makes the whole
		// root a helper root; a local disk mounted inside it doesn't.
		"/srv/library": false, "/srv": false, "/srv/library/disk2": true, "/srv/library-other": true,
	} {
		if got := mountIsLocal(path); got != local {
			t.Fatalf("%s: local=%v, want %v", path, got, local)
		}
	}
}

func timeNowForTest() time.Time  { return time.Now() }
func timeZeroForTest() time.Time { return time.Time{} }
