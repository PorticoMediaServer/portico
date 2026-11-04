package ingestion

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"io"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
)

func TestEpisodeThumbnailScanCallbackKeepsExistingJobPendingUntilSettled(t *testing.T) {
	for _, outcome := range []string{"missing", "pending", "complete", "unsupported", "revoked"} {
		t.Run(outcome, func(t *testing.T) {
			c := catalogtest.Open(t)
			library := c.Library("episodes", "Episodes", "tv", "/episodes")
			show := c.Show(library, "A show", 2020)
			season := c.Season(show, 1)
			episode := c.Episode(show, season, 1, "/episodes/Show.S01E01.mkv")
			c.Exec(`UPDATE library_sources SET identity_confirmed=1,root_identity='root-a' WHERE id='episodes'`)
			c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state,analysis_revision) SELECT 'object',id,?,incarnation,'Show.S01E01.mkv','revision-a','{}',1000,1,'available','revision-a' FROM library_sources WHERE id='episodes'`, episode.Token)
			cat := catalog.New(c.DB)
			scanner := New(c.DB, cat, assets.Probe{})
			if err := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
				// This callback fixture starts after the preceding packet recipe has
				// applied its canonical stream facts. No source bytes belong to
				// the callback oracle below.
				if err := assets.PersistStreams(tx, episode.Token, 1000, 1, []assets.Stream{{Index: 0, Type: "video", Codec: "h264", Detail: assets.StreamDetail{MaxBitRate: 2400, StreamBreadth: assets.StreamBreadth{PacketPeakRevision: assets.PacketPeakRevision}}}}); err != nil {
					return err
				}
				return catalog.ApplyScanPolicyTx(context.Background(), tx, "episodes", catalog.ScanPolicy{Tier: "custom", Operations: []string{"probe", catalog.EpisodeThumbnailOperation}})
			}); err != nil {
				t.Fatal(err)
			}

			job, err := scanner.QueueContext(context.Background(), "episodes", "episodes", nil)
			if err != nil {
				t.Fatal(err)
			}
			c.Exec(`UPDATE jobs SET status='running' WHERE id=?`, job.ID)
			c.Exec(`INSERT INTO inventory_analysis_queue(job_id,object_id,revision,policy_revision,state) SELECT ?,'object','revision-a',revision,'pending' FROM library_scan_policies WHERE library_id='episodes'`, job.ID)
			generic, err := mediaanalysis.New(mediaanalysis.Options{DB: c.DB, Directory: t.TempDir(), Open: func(context.Context, string, string) (mediaanalysis.Input, error) {
				t.Fatal("unrelated source opened")
				return nil, nil
			}, Decoder: func(mediaanalysis.Input) mediaanalysis.Decode {
				return func(context.Context, string, decoder.AnalysisSpec, func(io.Reader) error) error {
					t.Fatal("unrelated decode")
					return nil
				}
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer generic.Close()
			scanner.DeepAnalysis = generic
			source, err := cat.LibrarySource(context.Background(), "episodes")
			if err != nil {
				t.Fatal(err)
			}
			p, err := cat.ScanPolicy(context.Background(), "episodes")
			if err != nil {
				t.Fatal(err)
			}
			if !p.Allows(catalog.EpisodeThumbnailOperation) {
				t.Fatal("saved registered operation missing", p)
			}
			// The generic runner acknowledges the canonical packet marker in
			// its own quantum before the thumbnail callback is due. Settle that
			// predecessor without consuming the inventory queue's callback ACK.
			settled := false
			for range 4 {
				done, warning, err := generic.RunOne(context.Background(), mediaanalysis.Request{JobID: job.ID, ObjectID: "object", SourceRevision: "revision-a", PolicyRevision: p.Revision})
				if err != nil || warning != "" {
					t.Fatal("preceding stage did not settle", warning, err)
				}
				if done {
					settled = true
					break
				}
			}
			if !settled {
				t.Fatal("preceding internal stage still pending")
			}
			var before string
			if err := c.DB.QueryRow(`SELECT state FROM inventory_analysis_queue WHERE job_id=?`, job.ID).Scan(&before); err != nil || before != "pending" {
				t.Fatal("generic predecessor acknowledged the callback's queue entry", before, err)
			}
			calls := 0
			if outcome != "missing" {
				scanner.EpisodeThumbnails = func(ctx context.Context, j, s, o, r string, rev int64) (bool, string, error) {
					calls++
					if j != job.ID || s != source.ID || o != "object" || r != "revision-a" || rev != p.Revision {
						t.Fatal("caller lost actual job/source/revisions")
					}
					if outcome == "revoked" {
						return false, "", errors.New("revoked source")
					}
					if outcome == "unsupported" {
						return true, "decoder_or_stream_unsupported", nil
					}
					return outcome == "complete", "", nil
				}
			}
			err = scanner.analyzeDeep(context.Background(), job.ID, "object", "revision-a", p, source)
			if (err != nil) != (outcome == "revoked") {
				t.Fatal(outcome, err)
			}
			var state string
			if err = c.DB.QueryRow(`SELECT state FROM inventory_analysis_queue WHERE job_id=?`, job.ID).Scan(&state); err != nil {
				t.Fatal(err)
			}
			want := "pending"
			if outcome == "complete" {
				want = "done"
			}
			if outcome == "unsupported" {
				want = "warning"
			}
			if state != want || outcome != "missing" && calls != 1 {
				t.Fatalf("%s: state=%s calls=%d", outcome, state, calls)
			}
		})
	}
}
