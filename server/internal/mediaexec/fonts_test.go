package mediaexec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The font pack is written once, names its own directory in its fontconfig
// file, and is written again when something removed it.
func TestFontPackIsWrittenAndRestored(t *testing.T) {
	root := t.TempDir()
	was := fontRoot
	fontRoot = func() string { return root }
	defer func() { fontRoot = was }()
	pack, err := Fonts()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(pack.Dir) != root || pack.Config != filepath.Join(pack.Dir, "fonts.conf") {
		t.Fatalf("pack %+v", pack)
	}
	conf, err := os.ReadFile(pack.Config)
	if err != nil || !strings.Contains(string(conf), "<dir>"+fontXMLPath(pack.Dir)+"</dir>") || !strings.Contains(string(conf), "<cachedir>"+fontXMLPath(filepath.Join(os.TempDir(), "portico-fontconfig"))+"</cachedir>") {
		t.Fatalf("config %s: %v", conf, err)
	}
	face, err := os.ReadFile(filepath.Join(pack.Dir, "DejaVuSans.ttf"))
	if err != nil || len(face) != len(bundledFont) {
		t.Fatalf("face %d bytes: %v", len(face), err)
	}
	if err = os.RemoveAll(pack.Dir); err != nil {
		t.Fatal(err)
	}
	again, err := Fonts()
	if err != nil || again.Dir != pack.Dir {
		t.Fatalf("again %+v: %v", again, err)
	}
	if _, err = os.Stat(filepath.Join(again.Dir, "DejaVuSans.ttf")); err != nil {
		t.Fatal("the removed pack was not written again", err)
	}
}
