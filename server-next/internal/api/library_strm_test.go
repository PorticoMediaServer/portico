package api

import "testing"

// The switch is off for existing/default libraries and survives an owner edit and read.
func TestLibrarySTRMSwitch(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	var lib AdminLibrary
	create := LibraryCreate{Name: "Films", Kind: "movies", MetadataProvider: "local", Folders: []string{t.TempDir()}}
	if w := call(t, s, "POST", "/v1/admin/libraries", owner, create, &lib); w.Code != 200 || lib.Options.STRM {
		t.Fatalf("default library: %d %s", w.Code, w.Body)
	}
	path := "/v1/admin/libraries/" + lib.ID
	for _, enabled := range []bool{true, false} {
		options := lib.Options
		options.STRM = enabled
		if w := call(t, s, "PATCH", path, owner, LibraryEdit{Options: &options}, &lib); w.Code != 200 || lib.Options.STRM != enabled {
			t.Fatalf("setting strm=%t: %d %s", enabled, w.Code, w.Body)
		}
		lib = get[AdminLibrary](t, s, owner, path)
		if lib.Options.STRM != enabled {
			t.Fatalf("read strm=%t after saving %t", lib.Options.STRM, enabled)
		}
	}
}
