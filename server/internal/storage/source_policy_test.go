package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSourcePolicyProtectsServerStateWithoutRestrictingOtherMedia(t *testing.T) {
	base := t.TempDir()
	media := filepath.Join(base, "media")
	state := filepath.Join(base, "state")
	binary := filepath.Join(base, "bin")
	for _, dir := range []string{media, state, binary} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	mount := filepath.Join(state, "mounts")
	if err := os.Mkdir(mount, 0700); err != nil {
		t.Fatal(err)
	}
	p := SourcePolicy{StateDirectory: state, ManagedMountDirectory: mount}
	if err := p.Check(media); err != nil {
		t.Fatal(err)
	}
	if err := p.Check(binary); err != nil {
		t.Fatal("ordinary readable folder denied", err)
	}
	if err := p.Check(mount); err != nil {
		t.Fatal("managed mount denied", err)
	}
	for _, path := range []string{base, state, filepath.Join(state, "server.sqlite")} {
		if err := p.Check(path); !errors.Is(err, ErrSourceRootDenied) {
			t.Fatalf("%s: %v", path, err)
		}
	}
	caseVariant := filepath.Join(base, "STATE")
	if err := os.Mkdir(caseVariant, 0700); err == nil {
		if err := p.Check(caseVariant); err != nil {
			t.Fatalf("distinct case-sensitive sibling denied: %v", err)
		}
	} else if info, statErr := os.Stat(caseVariant); statErr == nil {
		stateInfo, _ := os.Stat(state)
		if !os.SameFile(info, stateInfo) || !errors.Is(p.Check(caseVariant), ErrSourceRootDenied) {
			t.Fatal("case alias of state directory was permitted")
		}
	}
	if err := p.CheckBrowse(base); err != nil {
		t.Fatal("state parent should remain browsable", err)
	}
	if err := p.CheckBrowse(state); !errors.Is(err, ErrSourceRootDenied) {
		t.Fatal("state directory was browsable", err)
	}
	if err := p.CheckBrowse(mount); err != nil {
		t.Fatal("managed mount was not browsable", err)
	}
	if err := os.Symlink(state, filepath.Join(media, "escape")); err == nil {
		if err := p.Check(filepath.Join(media, "escape")); !errors.Is(err, ErrSourceRootDenied) {
			t.Fatalf("symlink escape: %v", err)
		}
	}
	limited := SourcePolicy{Roots: []string{media}, StateDirectory: state}
	if limited.CheckBrowse(binary) == nil || limited.Check(binary) != nil {
		t.Fatal("operator picker limit affected source authority")
	}
}
