package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInventoryIgnoreRulesPreserveCursorProgress(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".hidden", "@eaDir", "#recycle", "$RECYCLE.BIN", "lost+found", "Samples", "Film (2020)"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"._Film.mkv", "Film-sample.mkv", "sample.mkv", "Film.mkv", "Sample (2020).mkv", ".plexignore", ".ignore"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	r := InventoryRequest{Root: root, RelativePath: ".", Limit: 1}
	seen := map[string]bool{}
	issues := map[string]string{}
	for turn := 0; ; turn++ {
		if turn > 30 {
			t.Fatal("cursor failed to progress across exclusions")
		}
		p, err := LocalInventoryPage(r)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range p.Entries {
			if seen[e.Name] {
				t.Fatal("duplicate", e.Name)
			}
			seen[e.Name] = true
		}
		for _, issue := range p.Issues {
			issues[filepath.Base(issue.Path)] = issue.Reason
		}
		if p.Complete {
			break
		}
		if p.NextCursor == r.Cursor || p.NextCursor == "" {
			t.Fatal("stalled", p)
		}
		r.Cursor = p.NextCursor
		r.RootIdentity = p.RootIdentity
		r.DirectoryRevision = p.DirectoryRevision
	}
	// A sample directory is traversed so a full-size file inside it survives the
	// 300 MB sample boundary; everything else here is a built-in name or an
	// explicit ignore file.
	if len(seen) != 4 || !seen["Film (2020)"] || !seen["Film.mkv"] || !seen["Sample (2020).mkv"] || !seen["Samples"] {
		t.Fatal(seen)
	}
	for _, name := range []string{".hidden", "@eaDir", "#recycle", "$RECYCLE.BIN", "lost+found", "._Film.mkv", "Film-sample.mkv", "sample.mkv", ".plexignore", ".ignore"} {
		if issues[name] != "ignored_name" {
			t.Fatalf("%s was not excluded by its built-in name: %v", name, issues)
		}
	}
}
