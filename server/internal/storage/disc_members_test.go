package storage

import (
	"os"
	"path/filepath"
	"testing"
)

// The manifest observes real consumed identities and structure directories,
// refuses a missing required member or non-regular one, tolerates an absent
// optional file, and notices media replacement or a new title-bearing file.
func TestDiscMemberManifestObservesConsumedFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Film")
	for _, sub := range []string{"BDMV/PLAYLIST", "BDMV/STREAM", "BDMV/CLIPINF"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	playlist := filepath.Join(dir, "BDMV", "PLAYLIST", "00001.mpls")
	stream := filepath.Join(dir, "BDMV", "STREAM", "00001.m2ts")
	if err := os.WriteFile(playlist, []byte("playlist"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stream, []byte("stream"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := InventoryRequest{Root: root, RelativePath: "Film"}
	required := []string{"BDMV/STREAM/00001.m2ts", "BDMV/PLAYLIST/00001.mpls"}
	optional := []string{"BDMV/MovieObject.bdmv"}
	directories := []string{"BDMV", "BDMV/PLAYLIST", "BDMV/STREAM"}
	before, err := LocalDiscMemberManifest(request, required, optional, directories)
	if err != nil || len(before) != 5 {
		t.Fatalf("%+v %v", before, err)
	}
	byPath := map[string]DiscMember{}
	for _, member := range before {
		byPath[member.Path] = member
	}
	if !byPath["BDMV"].Directory || !byPath["BDMV"].Required || byPath["BDMV/PLAYLIST"].ObjectIdentity == "" {
		t.Fatalf("directories not witnessed: %+v", before)
	}
	if err = os.WriteFile(stream, []byte("replaced stream"), 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := LocalDiscMemberManifest(request, required, optional, directories)
	if err != nil || len(after) != 5 {
		t.Fatal(err)
	}
	streamBefore, streamAfter := DiscMember{}, DiscMember{}
	for _, member := range before {
		if member.Path == "BDMV/STREAM/00001.m2ts" {
			streamBefore = member
		}
	}
	for _, member := range after {
		if member.Path == "BDMV/STREAM/00001.m2ts" {
			streamAfter = member
		}
	}
	if streamAfter.Size == streamBefore.Size && streamAfter.ModifiedNS == streamBefore.ModifiedNS {
		t.Fatal("a replaced consumed file kept its revision")
	}
	// A title-bearing file added to a witnessed directory is a new source.
	added, err := LocalDiscMemberManifest(request, required, optional, directories)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "BDMV", "PLAYLIST", "00002.mpls"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	withTitle, err := LocalDiscMemberManifest(request, required, optional, directories)
	if err != nil {
		t.Fatal(err)
	}
	if sameManifest(added, withTitle) {
		t.Fatal("a new playlist did not change the structure witness")
	}
	for name, err := range map[string]error{
		"missing required":    errOf(LocalDiscMemberManifest(request, []string{"BDMV/STREAM/missing.m2ts"}, nil, directories)),
		"nonregular":          errOf(LocalDiscMemberManifest(request, []string{"BDMV/STREAM"}, nil, directories)),
		"empty":               errOf(LocalDiscMemberManifest(request, nil, nil, nil)),
		"escape":              errOf(LocalDiscMemberManifest(request, []string{"../outside"}, nil, nil)),
		"optional nonregular": errOf(LocalDiscMemberManifest(request, required, []string{"BDMV/CLIPINF"}, directories)),
		"missing directory":   errOf(LocalDiscMemberManifest(request, required, nil, []string{"BDMV/NOPE"})),
	} {
		if err == nil {
			t.Errorf("%s: admitted", name)
		}
	}
}

// errOf keeps the table above to one observed call per case.
func errOf(_ []DiscMember, err error) error { return err }

// sameManifest is the analysis fence's comparison, reduced to what a test needs.
func sameManifest(a, b []DiscMember) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Path != b[i].Path || a[i].ObjectIdentity != b[i].ObjectIdentity || a[i].Size != b[i].Size || a[i].ModifiedNS != b[i].ModifiedNS || a[i].Directory != b[i].Directory {
			return false
		}
	}
	return true
}
