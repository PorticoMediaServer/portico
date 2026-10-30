//go:build linux

package mediaexec

import (
	"debug/elf"
	"os"
	"testing"
)

func TestLibraryClosureCacheReusesUnchangedMetadataWitness(t *testing.T) {
	tool := copyCacheELF(t)
	if _, err := ResolveLibraries(tool, tool, nil); err != nil {
		t.Fatal(err)
	}
	key := tool + "\x00" + tool
	libraryClosures.Lock()
	before := libraryClosures.entries[key]
	libraryClosures.Unlock()
	if before == nil || before.watch[tool].metadata == nil {
		t.Fatal("ordinary ELF closure did not retain its bounded metadata witness")
	}
	for range 3 {
		if _, err := ResolveLibraries(tool, tool, nil); err != nil {
			t.Fatal(err)
		}
		libraryClosures.Lock()
		after := libraryClosures.entries[key]
		libraryClosures.Unlock()
		if after != before {
			t.Fatal("unchanged ELF unnecessarily bypassed its cached resolver result")
		}
	}
}

func TestLibraryClosureCacheRejectsInterpreterStatCollision(t *testing.T) {
	tool := copyCacheELF(t)
	if _, err := ResolveLibraries(tool, tool, nil); err != nil {
		t.Fatal(err)
	}
	f, err := elf.Open(tool)
	if err != nil {
		t.Fatal(err)
	}
	var offset uint64
	for _, program := range f.Progs {
		if program.Type == elf.PT_INTERP {
			offset = program.Off
			break
		}
	}
	f.Close()
	if offset == 0 {
		t.Fatal("ELF test executable must declare a dynamic interpreter")
	}
	raw, err := os.ReadFile(tool)
	if err != nil || offset >= uint64(len(raw)) || raw[offset] != '/' {
		t.Fatal("invalid interpreter fixture", err)
	}
	// The ELF header stays intact. Change the interpreter bytes consumed via
	// Prog.Open, so checking only magic/header bytes cannot satisfy this guard.
	raw[offset] = '!'
	if err = os.WriteFile(tool, raw, 0700); err != nil {
		t.Fatal(err)
	}
	key := tool + "\x00" + tool
	libraryClosures.Lock()
	cached := libraryClosures.entries[key]
	if cached == nil {
		libraryClosures.Unlock()
		t.Fatal("fixture closure was not memoized")
	}
	node := cached.watch[tool]
	// Model a filesystem reporting identical stat generations deterministically;
	// keep the old metadata content witness while making all stat checks match.
	node.entry, err = os.Lstat(tool)
	if err == nil {
		node.target, err = os.Stat(tool)
	}
	cached.watch[tool] = node
	libraryClosures.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ResolveLibraries(tool, tool, nil); err == nil {
		t.Fatal("changed ELF interpreter retained its closure when stat generations collided")
	}
}
