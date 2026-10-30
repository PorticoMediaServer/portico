package catalog

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

type fileInfoLaneFixture struct {
	c       *catalogtest.Catalog
	s       *Service
	r       *FileInfoRuntime
	store   *readprep.Store
	item    catalogtest.Item
	viewer  Viewer
	opening string
	owner   bool
}

func newFileInfoLaneFixture(t *testing.T) *fileInfoLaneFixture {
	t.Helper()
	c := catalogtest.Open(t)
	lib := c.Library("file-windows", "Files", "movie", "/file-windows")
	movie := c.Movie(lib, "/file-windows/one.mkv", "Film", 2026)
	c.Drain()
	c.Exec(`INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint) SELECT token,1,size,modified_ns,'lane' FROM catalog_assets WHERE id=?`, movie.Asset)
	detail := assets.Stream{Detail: assets.StreamDetail{Profile: "High", FrameRate: 23.976, BitDepth: 10}}.EncodeDetail()
	c.Exec(`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,detail_json) VALUES(?,0,'video','h264','','',0,'',1,0,'bt709',?), (?,3,'audio','aac','en','Main',2,'stereo',1,0,'',''),(?,9,'audio','flac','fr','Commentary',2,'stereo',0,0,'','')`, movie.Token, detail, movie.Token, movie.Token)
	c.Drain()
	store, e := readprep.New(readprep.Config{Workers: 1, MaxBytes: 1 << 20})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = store.Close() })
	f := &fileInfoLaneFixture{c: c, s: New(c.DB), item: movie, viewer: Viewer{Profile: "p", Fence: "authority", Libraries: []string{"file-windows"}}, store: store, owner: true}
	guard := func(ctx context.Context, caller readprep.Caller, id FileInfoIdentity) (FileInfoAdmission, error) {
		if caller != "family" {
			return FileInfoAdmission{}, readprep.ErrStale
		}
		ad, e := f.s.CaptureFileInfoAdmission(ctx, f.viewer, "server", movie.Public, f.owner, "actor", false)
		if e == nil && ad.Identity != id {
			return ad, readprep.ErrStale
		}
		return ad, e
	}
	f.r, e = NewFileInfoRuntime(c.DB, store, guard)
	if e != nil {
		t.Fatal(e)
	}
	f.open(t)
	return f
}
func (f *fileInfoLaneFixture) open(t *testing.T) {
	t.Helper()
	ad, e := f.s.CaptureFileInfoAdmission(context.Background(), f.viewer, "server", f.item.Public, f.owner, "actor", false)
	if e != nil {
		t.Fatal(e)
	}
	e = dbwork.WithReadSnapshot(context.Background(), f.c.DB, func(ctx context.Context) error {
		var e error
		f.opening, e = f.r.AcceptDetailOpening(ctx, "family", ad.Identity, ContentRevision{Catalog: 41, Viewer: 7})
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
}
func fileInfoAwait(t *testing.T, r *FileInfoRuntime, out FileInfoWindowResult, e error) FileInfoWindowResult {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for e == nil && out.Preparing != nil && time.Now().Before(deadline) {
		id := out.Preparing.QueryID
		out.Release()
		time.Sleep(time.Millisecond)
		out, e = r.Poll(context.Background(), "family", id)
	}
	if e != nil {
		t.Fatal(e)
	}
	if out.Preparing != nil {
		out.Release()
		t.Fatal("preparation did not finish")
	}
	return out
}
func (f *fileInfoLaneFixture) read(t *testing.T, req FileInfoReadRequest) FileInfoWindowResult {
	t.Helper()
	req.Opening = f.opening
	out, e := f.r.Read(context.Background(), "family", req)
	return fileInfoAwait(t, f.r, out, e)
}
func TestFileInfoRuntimeRealGroupsMembersRawAndHeadLanes(t *testing.T) {
	f := newFileInfoLaneFixture(t)
	groups := f.read(t, FileInfoReadRequest{Kind: "groups"})
	defer groups.Release()
	if groups.Groups == nil || len(groups.Groups.Groups) != 1 || groups.Groups.Surface.GroupCount != 1 {
		t.Fatal(groups.Groups)
	}
	g := groups.Groups.Groups[0]
	if g.Video == nil || g.Video.Profile != "High" || g.AudioCount != 2 || g.Ordinal != 1 || g.MemberCount != 1 {
		t.Fatal(g)
	}
	members := f.read(t, FileInfoReadRequest{Kind: "members", Group: g.ID})
	defer members.Release()
	if members.Members == nil || len(members.Members.Members) != 1 || members.Members.Members[0].Path != "/file-windows/one.mkv" {
		t.Fatal(members.Members)
	}
	tracks := f.read(t, FileInfoReadRequest{Kind: "tracks", Lane: "audio", Occurrence: g.HeadOccurrence})
	defer tracks.Release()
	if tracks.Tracks == nil || tracks.Tracks.Total != 2 || tracks.Tracks.Tracks[1].Title != "Commentary" || tracks.Tracks.Tracks[1].Ordinal != 2 {
		t.Fatal(tracks.Tracks)
	}
	head := f.read(t, FileInfoReadRequest{Kind: "head-tracks", Lane: "audio", Group: g.ID})
	defer head.Release()
	if head.HeadTracks == nil || head.HeadTracks.Total != 2 || head.HeadTracks.Tracks[0].ID != "a3" {
		t.Fatal(head.HeadTracks)
	}
	files := f.read(t, FileInfoReadRequest{Kind: "files"})
	defer files.Release()
	if files.Files == nil || files.Files.Surface.GroupCount != 1 || len(files.Files.Files) != 1 {
		t.Fatal(files.Files)
	}
	sources := f.read(t, FileInfoReadRequest{Kind: "sources"})
	defer sources.Release()
	if sources.Sources == nil || sources.Sources.Total != 1 || len(sources.Sources.Sources) != 1 {
		t.Fatal(sources.Sources)
	}
	if files.Files.Detail.Revision != (ContentRevision{Catalog: 41, Viewer: 7}) || sources.Sources.Detail != files.Files.Detail {
		t.Fatal("original Detail changed")
	}
	// Every independent lease is private; retiring one does not cancel another.
	if e := f.r.Cancel(context.Background(), "family", tracks.Tracks.Window.QueryID); e != nil {
		t.Fatal(e)
	}
	if _, e := f.r.Poll(context.Background(), "family", tracks.Tracks.Window.QueryID); !errors.Is(e, readprep.ErrStale) {
		t.Fatal(e)
	}
	if out, e := f.r.Poll(context.Background(), "family", groups.Groups.Window.QueryID); e != nil {
		t.Fatal(e)
	} else {
		out.Release()
	}
}
func TestFileInfoRuntimeLosslessWideEditionIsNeverCached(t *testing.T) {
	f := newFileInfoLaneFixture(t)
	edition := strings.Repeat("é", 1<<20)
	f.c.Exec(`UPDATE catalog_asset_links SET edition=? WHERE entity_id=? AND asset_id=?`, edition, f.item.ID, f.item.Asset)
	f.open(t)
	page := f.read(t, FileInfoReadRequest{Kind: "groups"})
	defer page.Release()
	if page.Groups == nil || len(page.Groups.Groups) != 1 || page.Groups.Groups[0].Edition != edition {
		t.Fatal("wide edition lost")
	}
	if stats := f.store.Stats(); stats.Bytes >= 1<<20 {
		t.Fatal("legal scalar cached", stats)
	}
}
func TestFileInfoRuntimeRealIncarnationAndSeparateAudience(t *testing.T) {
	f := newFileInfoLaneFixture(t)
	proof, ad, e := f.r.opening(context.Background(), "family", f.opening)
	if e != nil {
		t.Fatal(e)
	}
	f.owner = false
	if _, e = f.r.checkWindowBasis(context.Background(), "family", proof, fileInfoWitness(ad)); !errors.Is(e, readprep.ErrStale) {
		t.Fatal("owner loss did not revoke", e)
	}
	f.owner = true
	oldNonce := ad.ItemIncarnation
	f.c.Exec(`DELETE FROM catalog_entities WHERE id=?`, f.item.ID)
	// Restore the same public/internal id and epoch: only the real metadata head
	// incarnation distinguishes this new entity from the original opening.
	f.c.Exec(`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,info_desired_epoch,info_applied_epoch) VALUES(?,?,?,1,'Restored','restored','r',?,?)`, f.item.ID, ad.Identity.PublicItem[:], ad.Identity.Library, proof.InfoEpoch, proof.InfoEpoch)
	fresh, e := f.s.CaptureFileInfoAdmission(context.Background(), f.viewer, "server", f.item.Public, true, "actor", false)
	if e != nil {
		t.Fatal(e)
	}
	if fresh.ItemIncarnation == oldNonce {
		t.Fatal("reused item kept incarnation")
	}
	if _, _, e = f.r.opening(context.Background(), "family", f.opening); !errors.Is(e, readprep.ErrStale) {
		t.Fatal("opening accepted recreated item", e)
	}
}
