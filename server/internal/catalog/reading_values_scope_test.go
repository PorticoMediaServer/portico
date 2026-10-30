package catalog

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// The walk reports native folders and inventory.go stores filepath.Dir of the
// native relative path, while the logical Reading key is slash-normalized. The
// bindings must therefore carry the platform separator, not a hard-coded slash.
func TestReadingDirectoryBindingsUseNativeSeparator(t *testing.T) {
	type binding struct {
		logical, directory, root, separator string
		base, lower, upper, prefix          string
	}
	for _, tc := range []struct {
		name string
		binding
	}{
		{"slash-folder", binding{"Author One", "Author One", "/srv/books", "/", "Author One", "Author One/", "Author One0", "/srv/books/"}},
		{"backslash-folder", binding{"Author One", `Author One`, `C:\Books`, `\`, `Author One`, `Author One\`, `Author One]`, `C:\Books\`}},
		{"backslash-nested", binding{"Author One/Series", `Author One\Series`, `C:\Books`, `\`, `Author One\Series`, `Author One\Series\`, `Author One\Series]`, `C:\Books\`}},
		{"slash-root", binding{"", ".", "/", "/", ".", "", "", "/"}},
		{"drive-root", binding{"", ".", `C:\`, `\`, ".", "", "", `C:\`}},
		{"unc-root", binding{"Shared/Books", `Shared\Books`, `\\server\share`, `\`, `Shared\Books`, `Shared\Books\`, `Shared\Books]`, `\\server\share\`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, lower, upper, prefix := readingDirectoryBindings(tc.logical, tc.directory, tc.root, tc.separator)
			if base != tc.base || lower != tc.lower || upper != tc.upper || prefix != tc.prefix {
				t.Fatalf("bindings=%q/%q/%q/%q want %q/%q/%q/%q", base, lower, upper, prefix, tc.base, tc.lower, tc.upper, tc.prefix)
			}
		})
	}
}

// The published predicate runs against real SQLite with Windows-style stored
// paths on Linux. Directory scope is the exact folder or its native subtree and
// the path guard is filepath.Join(root, relative); siblings that only share a
// text prefix, foreign roots and slash-joined paths stay out.
func TestReadingDirectoryScopeSQLUsesNativeSeparator(t *testing.T) {
	query := `SELECT o.relative_path FROM reading_scope_probe o JOIN reading_scope_probe a ON a.relative_path=o.relative_path WHERE ` + readingDirectoryScopeSQL + ` AND ` + readingDirectoryPathSQL + ` ORDER BY o.relative_path`
	for _, tc := range []struct {
		name                     string
		logical, directory, root string
		separator                string
		rows                     [][3]string
		want                     []string
	}{
		{
			name: "windows-folder", logical: "Author One", directory: `Author One`, root: `C:\Books`, separator: `\`,
			rows: [][3]string{
				{`Author One`, `Author One\Dune.epub`, `C:\Books\Author One\Dune.epub`},
				{`Author One\Series`, `Author One\Series\Dune Messiah.epub`, `C:\Books\Author One\Series\Dune Messiah.epub`},
				{`Author One2`, `Author One2\Near.epub`, `C:\Books\Author One2\Near.epub`},
				{`Author Two`, `Author Two\Outside.epub`, `C:\Books\Author Two\Outside.epub`},
				{`Author One`, `Author One\Foreign.epub`, `C:\Other\Author One\Foreign.epub`},
				{`Author One`, `Author One\Slashy.epub`, `C:\Books/Author One/Slashy.epub`},
			},
			want: []string{`Author One\Dune.epub`, `Author One\Series\Dune Messiah.epub`},
		},
		{
			name: "slash-folder", logical: "Author One", directory: "Author One", root: "/srv/books", separator: "/",
			rows: [][3]string{
				{"Author One", "Author One/Dune.epub", "/srv/books/Author One/Dune.epub"},
				{"Author One/Series", "Author One/Series/Dune Messiah.epub", "/srv/books/Author One/Series/Dune Messiah.epub"},
				{"Author One0", "Author One0/Near.epub", "/srv/books/Author One0/Near.epub"},
				{"Author One2", "Author One2/Near.epub", "/srv/books/Author One2/Near.epub"},
				{"Author Two", "Author Two/Outside.epub", "/srv/books/Author Two/Outside.epub"},
				{"Author One", "Author One/Foreign.epub", "/srv/other/Author One/Foreign.epub"},
				{"Author One", "Author One/Backslash.epub", `C:\Books\Author One\Backslash.epub`},
			},
			want: []string{"Author One/Dune.epub", "Author One/Series/Dune Messiah.epub"},
		},
		{
			name: "slash-root", logical: "", directory: ".", root: "/", separator: "/",
			rows: [][3]string{
				{".", "Root.epub", "/Root.epub"},
				{".", "Double.epub", "//Double.epub"},
				{"Author", "Author/Nested.epub", "/Author/Nested.epub"},
			},
			want: []string{"Root.epub"},
		},
		{
			name: "drive-root", logical: "", directory: ".", root: `C:\`, separator: `\`,
			rows: [][3]string{
				{".", `Root.epub`, `C:\Root.epub`},
				{".", `SlashRoot.epub`, `C:/Root.epub`},
				{`Author`, `Author\Nested.epub`, `C:\Author\Nested.epub`},
			},
			want: []string{`Root.epub`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := catalogtest.Open(t)
			c.Exec(`CREATE TABLE reading_scope_probe(directory_path TEXT NOT NULL,relative_path TEXT NOT NULL,path TEXT NOT NULL)`)
			for _, row := range tc.rows {
				c.Exec(`INSERT INTO reading_scope_probe(directory_path,relative_path,path) VALUES(?,?,?)`, row[0], row[1], row[2])
			}
			base, lower, upper, prefix := readingDirectoryBindings(tc.logical, tc.directory, tc.root, tc.separator)
			rows, err := c.DB.Query(query, nil, nil, base, lower, upper, prefix)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var got []string
			for rows.Next() {
				var relative string
				if err = rows.Scan(&relative); err != nil {
					t.Fatal(err)
				}
				got = append(got, relative)
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("matched %q want %q", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("matched %q want %q", got, tc.want)
				}
			}
		})
	}
}
