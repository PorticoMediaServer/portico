package api

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The owner sees where a title's files are and what reading them found; a member is refused
// without being told whether the title exists.
func TestOnlyTheOwnerSeesWhereFilesAre(t *testing.T) {
	s, member, _ := seeded(t)
	owner := onMain(t, s, signIn(t, s, 1)).AccessToken
	libraries, err := s.db.AllLibraries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	films := get[ItemPage](t, s, owner, "/v1/items?library="+libraries[0].PublicID+"&kind=movie&limit=1")
	path := "/v1/items/" + films.Items[0].ID + "/files"
	files := get[ItemFiles](t, s, owner, path)
	if len(files.Versions) == 0 || len(files.Versions[0].Files) == 0 {
		t.Fatalf("the owner was shown no files: %+v", files)
	}
	f := files.Versions[0].Files[0]
	if root := libraries[0].Paths[0].Path; !strings.HasPrefix(f.Path, filepath.Clean(root)+string(filepath.Separator)) || f.Size <= 0 {
		t.Fatalf("file %+v is not a full path under the library's folder %q", f, root)
	}
	for _, id := range []string{films.Items[0].ID, "itm_nothing"} {
		if code := status(t, s, "GET", "/v1/items/"+id+"/files", member, nil); code != 403 {
			t.Errorf("a member asking for %s's files got %d, want 403", id, code)
		}
	}
	if code := status(t, s, "GET", "/v1/items/itm_nothing/files", owner, nil); code != 404 {
		t.Errorf("the owner asking for an unknown title's files got %d, want 404", code)
	}
}
