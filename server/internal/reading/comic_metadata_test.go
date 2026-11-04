package reading

import (
	"bytes"
	"context"
	"testing"
)

// RD-3: macOS "Compress" adds __MACOSX/._* resource forks with image
// extensions. The local reader-core archive reader excludes them
// (comic/archive.ts); the server must list the same pages.
func TestRDComicExcludesMacOSMetadataPages(t *testing.T) {
	raw := fixtureZIP(t, [][2]string{
		{"001.jpg", "page-one"},
		{"002.jpg", "page-two"},
		{"__MACOSX/._001.jpg", "\x00\x05\x16\x07appledouble"},
		{"._002.jpg", "\x00\x05\x16\x07appledouble"},
	})
	for _, sequential := range []bool{false, true} {
		var c *Comic
		var err error
		if sequential {
			opens := 0
			c, err = OpenSequentialComic(context.Background(), sequentialFixture(raw, &opens))
		} else {
			c, err = OpenComic(context.Background(), bytes.NewReader(raw), int64(len(raw)))
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(c.Names) != 2 || c.Names[0] != "001.jpg" || c.Names[1] != "002.jpg" {
			t.Fatalf("comic pages include metadata: %v", c.Names)
		}
	}
}

func TestComicPageMemberClassifierRejectsNestedMetadata(t *testing.T) {
	for _, name := range []string{"__MACOSX/page.png", "folder/__MACOSX/page.jpg", "folder/._page.webp", "._nested/page.png"} {
		if isImageMember(name) || safeMember(name, false, 16) {
			t.Fatalf("metadata admitted as an image: %s", name)
		}
	}
	for _, name := range []string{"page.JPG", "folder/page.webp", "folder/page.png"} {
		if !isImageMember(name) || !safeMember(name, false, 16) {
			t.Fatalf("real page excluded: %s", name)
		}
	}
}
