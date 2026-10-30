package storage

import "testing"

func TestSameRootRequiresFilesystemEvidenceForDeviceChanges(t *testing.T) {
	for _, c := range []struct {
		recorded, observed string
		same               bool
	}{
		{"dev:64:ino:2", "dev:64:ino:2", true},
		{"dev:64:ino:2", "dev:71:ino:2", false}, // no filesystem proof
		{"dev:64:ino:2:fsid:a", "dev:71:ino:2:fsid:a", true},
		{"dev:64:ino:2", "dev:64:ino:919", false}, // the empty mount point underneath
		{"dev:64:ino:2", "dev:71:ino:919", false},
		{"volume:7:file:0a", "volume:7:file:0a", true},
		{"volume:7:file:0a", "volume:8:file:0a", false}, // a Windows volume serial is stable: a change is real
		{"", "", false},
		{"dev:64:ino:2", "", false},
		{"dev:x:ino:2", "dev:y:ino:2", false},
	} {
		if got := SameRoot(c.recorded, c.observed); got != c.same {
			t.Errorf("SameRoot(%q,%q)=%v, want %v", c.recorded, c.observed, got, c.same)
		}
	}
}

func TestStableObjectIdentityDropsTheRootsDeviceNumber(t *testing.T) {
	for _, c := range []struct{ object, root, want string }{
		{"dev:64:ino:1234", "dev:64:ino:2", "ino:1234"},
		{"dev:71:ino:1234", "dev:71:ino:2", "ino:1234"},              // same file after a renumbering
		{"dev:90:ino:1234", "dev:64:ino:2", "dev:90:ino:1234"},       // another filesystem mounted beneath the root
		{"ino:1234", "dev:64:ino:2", "ino:1234"},                     // already stable
		{"volume:7:file:0a", "volume:7:file:01", "volume:7:file:0a"}, // Windows ids are stable as they are
		{"remote:abc", "dev:64:ino:2", "remote:abc"},
		{"", "dev:64:ino:2", ""},
	} {
		if got := StableObjectIdentity(c.object, c.root); got != c.want {
			t.Errorf("StableObjectIdentity(%q,%q)=%q, want %q", c.object, c.root, got, c.want)
		}
	}
}
