package storage

import "testing"

func TestRootIdentityRejectsDifferentFilesystems(t *testing.T) {
	for _, c := range [][2]string{{"dev:10:ino:2", "dev:11:ino:2"}, {"dev:10:ino:2:fsid:a", "dev:11:ino:2:fsid:b"}} {
		if SameRoot(c[0], c[1]) {
			t.Fatalf("different filesystem roots considered equal: %q %q", c[0], c[1])
		}
	}
}
