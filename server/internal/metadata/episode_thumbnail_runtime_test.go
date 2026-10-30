package metadata

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
)

type episodeAvailabilitySources struct {
	t                 *testing.T
	supported         bool
	unsupportedSource string
	target            EpisodeThumbnailTarget
}

func (s *episodeAvailabilitySources) ResolveEpisodeThumbnailTx(context.Context, *sql.Tx, EpisodeThumbnailInvocation) (EpisodeThumbnailSource, error) {
	s.t.Fatal("availability resolved authority or opened media")
	return nil, nil
}
func (s *episodeAvailabilitySources) EpisodeRangesSupportedTx(_ context.Context, _ *sql.Tx, target EpisodeThumbnailTarget) (bool, error) {
	s.target = target
	return s.supported && target.LibrarySourceID != s.unsupportedSource, nil
}

type episodeRuntimeInput struct{ closed, checks, reads int }

func (i *episodeRuntimeInput) Evidence() string { return "actual-test-observation" }
func (i *episodeRuntimeInput) Size() int64      { return 1 << 40 }
func (i *episodeRuntimeInput) ReadExtent(_ context.Context, off, n int64) ([]byte, error) {
	if off < 0 || n <= 0 || n > 1<<20 || off+n > i.Size() {
		return nil, mediaanalysis.ErrBudget
	}
	i.reads++
	return make([]byte, n), nil
}
func (i *episodeRuntimeInput) Validate(context.Context) error { i.checks++; return nil }
func (i *episodeRuntimeInput) Close() error                   { i.closed++; return nil }

type episodeRuntimeSource struct {
	thumbnailPublicationAuthority
	input                 *episodeRuntimeInput
	manual                bool
	opens, authorizations int
	refuseAt              int
}

func (s *episodeRuntimeSource) Authorize(_ context.Context, operation, cost string, manual bool) error {
	s.authorizations++
	if operation != catalog.EpisodeThumbnailOperation || cost != mediaanalysis.EpisodeThumbnailCostClass || manual != s.manual || s.refuseAt == s.authorizations {
		return mediaanalysis.ErrConflict
	}
	return nil
}
func (s *episodeRuntimeSource) Open(context.Context) (mediaanalysis.Input, error) {
	s.opens++
	return s.input, nil
}
func (s *episodeRuntimeSource) Close() error { return nil }

func thumbnailRuntime(t *testing.T, s *Service, decoded func()) *EpisodeThumbnailRuntime {
	t.Helper()
	return &EpisodeThumbnailRuntime{
		Decoder: func(input mediaanalysis.Input) mediaanalysis.Decode {
			return func(ctx context.Context, _ string, spec decoder.AnalysisSpec, consume func(io.Reader) error) error {
				if spec.Kind != "image" {
					t.Fatal("not a single frame", spec)
				}
				// A separate writer is available while media is read and decoded.
				checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				defer cancel()
				g, err := dbwork.Begin(checkCtx, s.db, dbwork.ClassBackgroundMedia)
				if err != nil {
					return err
				}
				g.Rollback()
				if _, err = input.ReadExtent(ctx, 4096, 65536); err != nil {
					return err
				}
				var data bytes.Buffer
				if err = jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 320, 180)), nil); err != nil {
					return err
				}
				if decoded != nil {
					decoded()
				}
				return consume(&data)
			}
		},
		ArtworkContext: func(ctx context.Context) (context.Context, func(context.Context, *sql.Tx) error, error) {
			return ctx, func(context.Context, *sql.Tx) error { return nil }, nil
		},
	}
}

func TestEpisodeThumbnailRuntimePublishesOneExplicitFrameForEachKnownMode(t *testing.T) {
	for _, mode := range []string{"off", "provider", "provider_then_made"} {
		t.Run(mode, func(t *testing.T) {
			s, c, target, other := episodePublicationFixture(t)
			captured := capturePublication(t, s, target)
			s.EpisodeThumbnails = thumbnailRuntime(t, s, nil)
			source := &episodeRuntimeSource{input: &episodeRuntimeInput{}, manual: true}
			_, err := s.generateEpisodeThumbnail(context.Background(), captured, mode, true, source, MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}, func(*sql.Tx) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			if source.opens != 1 || source.authorizations != 3 || source.input.closed != 1 || source.input.reads != 1 || source.calls != 1 {
				t.Fatalf("wrong bounded lifecycle: %+v input=%+v", source, source.input)
			}
			var n int
			if err = c.DB.QueryRow(`SELECT COUNT(*) FROM artwork_selections WHERE entity_id=?`, c.ID(other.ID)).Scan(&n); err != nil || n != 0 {
				t.Fatal("another episode changed", n, err)
			}
		})
	}
}

func TestEpisodeThumbnailRuntimeRejectsRevocationAndConcurrentOwnerDecision(t *testing.T) {
	for _, change := range []string{"revoked_before_open", "revoked_after_decode", "owner_edit"} {
		t.Run(change, func(t *testing.T) {
			s, c, target, _ := episodePublicationFixture(t)
			captured := capturePublication(t, s, target)
			source := &episodeRuntimeSource{input: &episodeRuntimeInput{}, manual: true}
			if change == "revoked_before_open" {
				source.refuseAt = 1
			}
			if change == "revoked_after_decode" {
				source.refuseAt = 3
			}
			s.EpisodeThumbnails = thumbnailRuntime(t, s, func() {
				if change == "owner_edit" {
					c.Exec(`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('item',?,'title','Owner title','',1,'local:owner:profile','now')`, c.ID(target.ID))
				}
			})
			_, err := s.generateEpisodeThumbnail(context.Background(), captured, "off", true, source, MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}, func(*sql.Tx) error { return nil })
			if err == nil {
				t.Fatal("stale work published")
			}
			if change == "owner_edit" && !errors.Is(err, ErrRepairConflict) {
				t.Fatal(err)
			}
			if change == "revoked_before_open" && source.opens != 0 {
				t.Fatal("unauthorized source opened")
			}
			var n int
			if err = c.DB.QueryRow(`SELECT COUNT(*) FROM artwork_selections`).Scan(&n); err != nil || n != 0 {
				t.Fatal("refused work selected art", n, err)
			}
		})
	}
}

func TestEpisodeThumbnailRuntimeFailedDecodeCannotPublish(t *testing.T) {
	s, c, target, _ := episodePublicationFixture(t)
	captured := capturePublication(t, s, target)
	s.EpisodeThumbnails = thumbnailRuntime(t, s, nil)
	decodeErr := errors.New("decode failed after output")
	decode := s.EpisodeThumbnails.Decoder
	s.EpisodeThumbnails.Decoder = func(input mediaanalysis.Input) mediaanalysis.Decode {
		run := decode(input)
		return func(ctx context.Context, operation string, spec decoder.AnalysisSpec, consume func(io.Reader) error) error {
			if err := run(ctx, operation, spec, consume); err != nil {
				return err
			}
			return decodeErr
		}
	}
	s.EpisodeThumbnails.ArtworkContext = func(context.Context) (context.Context, func(context.Context, *sql.Tx) error, error) {
		t.Fatal("failed decode reached artwork encoding")
		return nil, nil, nil
	}
	source := &episodeRuntimeSource{input: &episodeRuntimeInput{}, manual: true}
	_, err := s.generateEpisodeThumbnail(context.Background(), captured, "off", true, source, MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}, func(*sql.Tx) error {
		t.Fatal("failed decode reached artwork writer")
		return nil
	})
	if !errors.Is(err, decodeErr) || source.opens != 1 || source.authorizations != 3 || source.input.checks != 2 || source.input.closed != 1 || source.calls != 0 {
		t.Fatalf("failed decode was retried, unchecked or published: source=%+v input=%+v err=%v", source, source.input, err)
	}
	var selections int
	if err = c.DB.QueryRow(`SELECT COUNT(*) FROM artwork_selections`).Scan(&selections); err != nil || selections != 0 {
		t.Fatal("failed decode changed selected art", selections, err)
	}
}

func TestEpisodeThumbnailRuntimeReencodesCurrentPolicyWithoutRepeatingDecode(t *testing.T) {
	s, _, target, _ := episodePublicationFixture(t)
	captured := capturePublication(t, s, target)
	decodes, encodings := 0, 0
	s.EpisodeThumbnails = thumbnailRuntime(t, s, func() { decodes++ })
	s.EpisodeThumbnails.ArtworkContext = func(ctx context.Context) (context.Context, func(context.Context, *sql.Tx) error, error) {
		encodings++
		edgeRevision := encodings
		return ctx, func(context.Context, *sql.Tx) error {
			if edgeRevision < 3 {
				return ErrEpisodeThumbnailEncodingStale
			}
			return nil
		}, nil
	}
	source := &episodeRuntimeSource{input: &episodeRuntimeInput{}, manual: true}
	_, err := s.generateEpisodeThumbnail(context.Background(), captured, "off", true, source, MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}, func(*sql.Tx) error { return nil })
	if err != nil || decodes != 1 || encodings != 3 || source.opens != 1 || source.calls != 3 {
		t.Fatalf("current size retry repeated media or accepted stale bytes: decodes=%d encodings=%d source=%+v err=%v", decodes, encodings, source, err)
	}
}

func TestEpisodeThumbnailAvailabilityUsesRealVideoAndRangesWithoutAutomaticGates(t *testing.T) {
	s, c, target, _ := episodePublicationFixture(t)
	sources := &episodeAvailabilitySources{t: t, supported: true}
	s.EpisodeThumbnails = thumbnailRuntime(t, s, nil)
	s.EpisodeThumbnails.Sources = sources
	s.Admission = func(context.Context, *sql.Tx, string) (bool, error) {
		t.Fatal("explicit availability consulted automatic window")
		return false, nil
	}
	read := func() bool {
		g, err := dbwork.BeginSnapshot(context.Background(), s.db)
		if err != nil {
			t.Fatal(err)
		}
		defer g.Rollback()
		ok, err := s.CanMakeEpisodeThumbnailTx(context.Background(), g.Tx(), target)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	// Availability does not consult the automatic mode or provider policy;
	// an unknown duration is an invocation error, not an availability gate.
	if !read() || sources.target.EpisodeID != target.ID || sources.target.SourceRevision != "revision-a" {
		t.Fatal("real video/range target absent", sources.target)
	}
	c.Exec(`UPDATE catalog_assets SET duration=0`)
	if !read() {
		t.Fatal("unknown duration hid an otherwise supported explicit command")
	}
	sources.supported = false
	if read() {
		t.Fatal("range_unsupported command exposed")
	}
	sources.supported = true
	c.Exec(`INSERT INTO library_sources(id,library_id,kind,name,configured_root,root,root_identity,incarnation,identity_confirmed) VALUES('second','episodes','local','Second','/second','/second','root-b','root-b',1)`)
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state) SELECT 'second-object','second',asset_id,'root-b',relative_path,'revision-b','{}',size,modified_ns,'available' FROM inventory_objects WHERE source_id='episodes' AND id=?`, target.ID)
	sources.unsupportedSource = "episodes"
	if !read() || sources.target.LibrarySourceID != "second" {
		t.Fatal("supported alternate video was hidden", sources.target)
	}
	c.Exec(`UPDATE catalog_assets SET video_codec=''`)
	if read() {
		t.Fatal("episode without video offered Make")
	}
	s.EpisodeThumbnails = nil
	if read() {
		t.Fatal("absent engine offered Make")
	}
}
