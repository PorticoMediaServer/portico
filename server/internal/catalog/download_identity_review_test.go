package catalog

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func TestMergeDoesNotRetargetPlaylistDownloadRequest(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("tv", "TV", "tv", "/tv")
	from := c.Show(lib, "From", 2000)
	into := c.Show(lib, "Into", 2000)
	c.Exec(`INSERT INTO catalog_playlists(id,token,owner_authority,owner_account,owner_profile,name,creation_operation,creation_hash,created_at) VALUES(?,'playlist','local','account','profile','List','create','hash','now')`, from.ID)
	for _, kind := range []string{"playlist", "show"} {
		c.Exec(`INSERT INTO download_requests(id,profile_key,operation_id,request_hash,principal,device_id,target_kind,target_id,quality,episodes,keep_next,selection_fence,created_ms,updated_ms) VALUES(?,'viewer',?,'hash','{}','device',?,?,'original','all',0,'fence',0,0)`, kind, kind, kind, from.ID)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error { return MergeTitlesTx(ctx, tx, from.ID, into.ID) })
	for _, kind := range []string{"playlist", "show"} {
		var got int64
		if err := c.DB.QueryRow(`SELECT target_id FROM download_requests WHERE id=?`, kind).Scan(&got); err != nil {
			t.Fatal(err)
		}
		want := into.ID
		if kind == "playlist" {
			want = from.ID
		}
		if got != want {
			t.Errorf("%s request target = %d, want %d", kind, got, want)
		}
	}
}
