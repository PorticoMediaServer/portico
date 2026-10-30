package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

// This fixture verifies HTTP/identity/storage composition. Its input and size
// guard do not qualify Remote's producer or the production artwork-size adapter.
type episodeHTTPInput struct{ reads, closed int }

func (*episodeHTTPInput) Evidence() string                     { return "synthetic-http-episode-input" }
func (*episodeHTTPInput) Size() int64                          { return 1 << 20 }
func (i *episodeHTTPInput) Validate(ctx context.Context) error { return ctx.Err() }
func (i *episodeHTTPInput) Close() error                       { i.closed++; return nil }
func (i *episodeHTTPInput) ReadExtent(ctx context.Context, offset, length int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if offset < 0 || length <= 0 || length > 1<<20 || offset > i.Size()-length {
		return nil, mediaanalysis.ErrBudget
	}
	i.reads++
	return make([]byte, length), nil
}

type episodeHTTPSource struct {
	d                     Dependencies
	input                 episodeHTTPInput
	invocation            metadata.EpisodeThumbnailInvocation
	resolved, opened      int
	authorized, published int
	closed                int
}

func (s *episodeHTTPSource) ResolveEpisodeThumbnailTx(_ context.Context, _ *sql.Tx, invocation metadata.EpisodeThumbnailInvocation) (metadata.EpisodeThumbnailSource, error) {
	s.resolved++
	s.invocation = invocation
	return s, nil
}
func (*episodeHTTPSource) EpisodeRangesSupportedTx(context.Context, *sql.Tx, metadata.EpisodeThumbnailTarget) (bool, error) {
	return true, nil
}
func (s *episodeHTTPSource) Authorize(ctx context.Context, operation, cost string, manual bool) error {
	s.authorized++
	if operation != catalog.EpisodeThumbnailOperation || cost != mediaanalysis.EpisodeThumbnailCostClass || !manual || s.invocation.Purpose != metadata.EpisodeThumbnailExplicit || s.invocation.Owner.Role != "owner" {
		return identity.ErrForbidden
	}
	g, err := dbwork.BeginSnapshot(ctx, s.d.DB)
	if err != nil {
		return err
	}
	defer g.Rollback()
	return s.d.manualMetadataAuthorize(ctx, g.Tx(), s.invocation.Owner)
}
func (s *episodeHTTPSource) Open(context.Context) (mediaanalysis.Input, error) {
	s.opened++
	return &s.input, nil
}
func (s *episodeHTTPSource) CheckTx(ctx context.Context, tx *sql.Tx) error {
	s.published++
	return s.d.manualMetadataAuthorize(ctx, tx, s.invocation.Owner)
}
func (s *episodeHTTPSource) Close() error { s.closed++; return nil }

type episodeHTTPFixture struct {
	h              http.Handler
	owner, member  string
	c              *catalogtest.Catalog
	target, other  string
	source         *episodeHTTPSource
	decoded        int
	afterDecode    func()
	policyRevision int64
}

func newEpisodeHTTPFixture(t *testing.T, mode string) *episodeHTTPFixture {
	t.Helper()
	d, owner, member := tl6SupportFixture(t)
	f := &episodeHTTPFixture{owner: owner.AccessToken, member: member.AccessToken, c: catalogtest.New(t, d.DB)}
	lib := f.c.Library("episodes", "Episodes", "tv", "/episodes")
	show := f.c.Show(lib, "A show", 2020)
	season := f.c.Season(show, 1)
	for n := 1; n <= 2; n++ {
		it := f.c.Episode(show, season, n, "/episodes/episode"+string(rune('0'+n))+".mkv")
		if n == 1 {
			f.target = it.Public
		} else {
			f.other = it.Public
		}
		f.c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state) SELECT ?,id,?,incarnation,?,'revision-a','{}',1000,1,'available' FROM library_sources WHERE id='episodes'`, it.Public, it.Token, it.Public+".mkv")
	}
	f.c.Exec(`UPDATE library_sources SET identity_confirmed=1,root_identity='root-a' WHERE id='episodes'`)
	ctx := context.Background()
	if err := dbwork.WithWriteTx(ctx, d.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		if err := catalog.ApplyEpisodeThumbnailModeTx(ctx, tx, "episodes", mode, 1); err != nil {
			return err
		}
		return catalog.ApplyScanPolicyTx(ctx, tx, "episodes", catalog.ScanPolicy{Tier: "file_list_only"})
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.DB.QueryRow(`SELECT revision FROM library_scan_policies WHERE library_id='episodes'`).Scan(&f.policyRevision); err != nil {
		t.Fatal(err)
	}
	f.source = &episodeHTTPSource{d: d}
	d.Metadata = metadata.New(d.DB, "test-token")
	if err := d.Metadata.SetArtworkDirectory(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	d.Metadata.Admission = func(context.Context, *sql.Tx, string) (bool, error) {
		t.Fatal("explicit HTTP command consulted automatic window")
		return false, nil
	}
	d.Metadata.EpisodeThumbnails = &metadata.EpisodeThumbnailRuntime{
		Sources: f.source,
		ArtworkContext: func(ctx context.Context) (context.Context, func(context.Context, *sql.Tx) error, error) {
			return ctx, func(context.Context, *sql.Tx) error { return nil }, nil
		},
		Decoder: func(input mediaanalysis.Input) mediaanalysis.Decode {
			return func(ctx context.Context, operation string, spec decoder.AnalysisSpec, consume func(io.Reader) error) error {
				f.decoded++
				if operation != catalog.EpisodeThumbnailOperation || spec.Kind != "image" {
					t.Fatal("not a bounded episode recipe")
				}
				if _, err := input.ReadExtent(ctx, 0, 128); err != nil {
					return err
				}
				var data bytes.Buffer
				if err := jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 320, 180)), nil); err != nil {
					return err
				}
				if f.afterDecode != nil {
					f.afterDecode()
				}
				return consume(&data)
			}
		},
	}
	f.h = New(d)
	return f
}

func TestEpisodeThumbnailRepairHTTPPublishesOneFrameUnderEveryStoredMode(t *testing.T) {
	for _, mode := range []string{"off", "provider", "provider_then_made"} {
		t.Run(mode, func(t *testing.T) {
			f := newEpisodeHTTPFixture(t, mode)
			state := editorState(t, f.h, f.owner, f.target)
			raw, err := json.Marshal(metadata.RepairCommand{Action: "make_episode_thumbnail", ExpectedRevision: state.Revision})
			if err != nil {
				t.Fatal(err)
			}
			path := "/v1/metadata/item/" + f.target
			for _, token := range []string{"", f.member} {
				response := editorRequest(t, f.h, "POST", path, token, "application/json", raw)
				if response.Code != tl6RefusedStatus(token) || f.source.resolved != 0 {
					t.Fatal("non-owner reached source", response.Code, f.source.resolved)
				}
			}
			response := editorRequest(t, f.h, "POST", path, f.owner, "application/json", raw)
			if response.Code != 200 {
				t.Fatalf("owner Make: %d %s", response.Code, response.Body.String())
			}
			tl6AssertSpecResponse(t, "POST", "/v1/metadata/{kind}/{id}", response)
			if f.decoded != 1 || f.source.resolved != 1 || f.source.opened != 1 || f.source.authorized != 3 || f.source.published != 1 || f.source.closed != 1 || f.source.input.closed != 1 || f.source.input.reads != 1 {
				t.Fatalf("wrong retained HTTP lifecycle: source=%+v decoded=%d", f.source, f.decoded)
			}
			inv := f.source.invocation
			if inv.Owner.AccountID != "owner" || inv.Owner.Authority != "local" || inv.Owner.Hash == "" || inv.JobID != "" || inv.Target != (metadata.EpisodeThumbnailTarget{LibraryID: "episodes", LibrarySourceID: "episodes", ObjectID: f.target, SourceRevision: "revision-a", EpisodeID: f.target}) {
				t.Fatalf("lost actual HTTP principal/target: %+v", inv)
			}
			var stored, tier string
			var revision int64
			if err = f.c.DB.QueryRow(`SELECT episode_thumbnail_mode,tier,revision FROM library_scan_policies WHERE library_id='episodes'`).Scan(&stored, &tier, &revision); err != nil || stored != mode || tier != "file_list_only" || revision != f.policyRevision {
				t.Fatal("HTTP command changed automatic policy", stored, tier, revision, err)
			}
			var other int
			if err = f.c.DB.QueryRow(`SELECT COUNT(*) FROM artwork_selections WHERE entity_id=?`, f.c.ID(f.other)).Scan(&other); err != nil || other != 0 {
				t.Fatal("another episode changed", other, err)
			}
			after := editorState(t, f.h, f.owner, f.target)
			if after.Revision == state.Revision || len(after.Snapshot.Artwork) != 1 || !after.Snapshot.Artwork[0].Locked {
				t.Fatal("HTTP command did not select one owner still", after)
			}
		})
	}
}

func TestEpisodeThumbnailRepairHTTPRejectsActualOwnerRevocationDuringDecode(t *testing.T) {
	f := newEpisodeHTTPFixture(t, "off")
	state := editorState(t, f.h, f.owner, f.target)
	f.afterDecode = func() { f.c.Exec(`UPDATE accounts SET epoch=epoch+1 WHERE id='owner'`) }
	raw, err := json.Marshal(metadata.RepairCommand{Action: "make_episode_thumbnail", ExpectedRevision: state.Revision})
	if err != nil {
		t.Fatal(err)
	}
	response := editorRequest(t, f.h, "POST", "/v1/metadata/item/"+f.target, f.owner, "application/json", raw)
	if response.Code != 401 || f.decoded != 1 || f.source.authorized != 3 || f.source.published != 0 || f.source.closed != 1 || f.source.input.closed != 1 {
		t.Fatalf("revoked owner published or leaked: status=%d body=%s source=%+v", response.Code, response.Body.String(), f.source)
	}
	var count int
	if err = f.c.DB.QueryRow(`SELECT COUNT(*) FROM artwork_selections`).Scan(&count); err != nil || count != 0 {
		t.Fatal("revoked command selected artwork", count, err)
	}
}
