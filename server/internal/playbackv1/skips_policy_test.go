package playbackv1

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestSkipEvidenceRequiresCurrentViewerVisibility(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, tc := range []struct {
		name, mode, provenance string
		confidence             float64
		approved               int
		want                   int
	}{
		{"manual below configured floor", "manual", "season_audio_match:intro:v1", .6, 0, 0},
		{"manual ambiguous", "manual", "season_audio_match:intro:semantic_ambiguous", 1, 0, 0},
		{"manual chapter disabled", "manual", "embedded_chapter_label:v1", .9, 0, 0},
		{"manual approved chapter", "manual", "embedded_chapter_label:v1", .1, 1, 1},
		{"manual visible", "manual", "visual_credits:v1", .7, 0, 1},
		{"manual approved bypass", "manual", "visual_credits:semantic_ambiguous", .1, 1, 1},
		{"automatic below automatic floor", "automatic", "season_audio_match:intro:v1", .9, 0, 0},
		{"automatic measured", "automatic", "season_audio_match:intro:v1", .95, 0, 1},
		{"automatic approved bypass", "automatic", "visual_credits:semantic_ambiguous", .1, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := catalogtest.Open(t)
			lib := c.Library("movies", "Movies", "movie", "/movies")
			movie := c.Movie(lib, "/movies/movie.mkv", "Movie", 2026)
			c.Exec(`INSERT INTO library_sources(id,library_id,configured_root,root) VALUES('source','movies','/markers','/markers')`)
			c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('object','source',?,'root','movie.mkv','revision','{}',1,1)`, movie.Token)
			c.Exec(`INSERT INTO analysis_markers(id,object_id,source_revision,source_binding,kind,start_us,end_us,confidence,provenance,result_id,approved) VALUES('marker','object','revision','binding','intro',0,1000000,?,?,'',?)`, tc.confidence, tc.provenance, tc.approved)
			c.Exec(`INSERT INTO server_settings(singleton,"segments.viewerConfidencePercent","segments.automaticConfidencePercent","segments.useChapterNames") VALUES(1,70,95,0) ON CONFLICT(singleton) DO UPDATE SET "segments.useChapterNames"=0,"segments.viewerConfidencePercent"=70,"segments.automaticConfidencePercent"=95`)
			s := &Service{DB: c.DB}
			if err := dbwork.WithWriteTxContext(context.Background(), c.DB, dbwork.ClassEstablishedPlayback, func(ctx context.Context, tx *sql.Tx) error {
				_, err := s.recordSkipTx(ctx, tx, row{id: "play", item: movie.Public, generation: 1}, SkipReport{MarkerID: "marker", Mode: tc.mode, PositionMs: 1000})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := c.DB.QueryRow(`SELECT count(*) FROM playback_marker_skips`).Scan(&count); err != nil || count != tc.want {
				t.Fatalf("count=%d want=%d: %v", count, tc.want, err)
			}
		})
	}
}
