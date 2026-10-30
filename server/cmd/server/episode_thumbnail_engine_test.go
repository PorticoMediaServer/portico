package main

import (
	"context"
	"database/sql"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/remotesources"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitlevideo"
	"github.com/PorticoMediaServer/portico/server/internal/workpolicy"
)

// The optional selected-pair gate exercises the actual confined local source,
// ranged decoder, current artwork encoder and publication authority together.
// Its procedural video is temporary; supplied licensed media is never used.
func TestEpisodeActualLocalDecodeAndPublication(t *testing.T) {
	ffmpeg, ffprobe := os.Getenv("PORTICO_EPISODE_TEST_FFMPEG"), os.Getenv("PORTICO_EPISODE_TEST_FFPROBE")
	if ffmpeg == "" || ffprobe == "" {
		t.Skip("set explicit same-bundle FFmpeg/ffprobe for actual Episode engine qualification")
	}
	if !filepath.IsAbs(ffmpeg) || !filepath.IsAbs(ffprobe) || filepath.Dir(ffmpeg) != filepath.Dir(ffprobe) {
		t.Fatal("selected pair must have one explicit absolute bundle directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	root := t.TempDir()
	file := filepath.Join(root, "episode.mp4")
	generate := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24:duration=4", "-an", "-c:v", "libx264", "-threads", "1", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-movflags", "+faststart", file)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("procedural fixture: %v %s", err, output)
	}
	facts, err := (assets.Probe{Binary: ffprobe}).Inspect(ctx, file)
	if err != nil || facts.VideoCodec != "h264" || facts.Width != 320 || facts.Height != 180 || facts.Duration != 4 {
		t.Fatalf("actual source facts: %+v %v", facts, err)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, invocation := range []string{"off", "provider", "provider_then_made", "automatic"} {
		t.Run(invocation, func(t *testing.T) {
			automatic := invocation == "automatic"
			mode := invocation
			tier := "file_list_only"
			if automatic {
				mode, tier = "provider_then_made", "complete"
			}
			c := catalogtest.Open(t)
			local := storage.New(helper)
			managed, err := mounts.New(c.DB, t.TempDir(), "", helper, local)
			if err != nil {
				t.Fatal(err)
			}
			sources, err := remotesources.New(c.DB, t.TempDir(), managed)
			if err != nil {
				t.Fatal(err)
			}
			identities, err := identity.New(c.DB, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			c.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',X'00','profile',1)`)
			grant, err := identities.Issue("account", "profile", "local", "owner", 1)
			if err != nil {
				t.Fatal(err)
			}
			owner, err := identities.Authenticate(grant.AccessToken)
			if err != nil {
				t.Fatal(err)
			}
			library := c.Library("episodes", "Episodes", "tv", root)
			show := c.Show(library, "Actual episode", 2026)
			episode := c.Episode(show, c.Season(show, 1), 1, file)
			observedRoot, err := local.InspectRoot(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			observed, err := local.Stat(ctx, "episode-engine-fixture", file)
			if err != nil {
				t.Fatal(err)
			}
			c.Exec(`UPDATE library_sources SET identity_confirmed=1,root_identity=? WHERE id='episodes'`, storage.RootIdentity(observedRoot))
			source, err := catalog.New(c.DB).LibrarySource(ctx, "episodes")
			if err != nil {
				t.Fatal(err)
			}
			revision, evidence, err := catalog.ObservationRevision(source, observed)
			if err != nil {
				t.Fatal(err)
			}
			c.Exec(`UPDATE catalog_assets SET size=?,modified_ns=?,container=?,video_codec=?,width=?,height=?,duration=? WHERE token=?`, observed.Size, observed.ModifiedNS, facts.Container, facts.VideoCodec, facts.Width, facts.Height, facts.Duration, episode.Token)
			c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state) VALUES('object','episodes',?,?,'episode.mp4',?,?,?,?, 'available')`, episode.Token, source.Incarnation, revision, evidence, observed.Size, observed.ModifiedNS)
			if err := dbwork.WithWriteTx(ctx, c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
				if err := catalog.ApplyEpisodeThumbnailModeTx(ctx, tx, "episodes", mode, 1); err != nil {
					return err
				}
				// Explicit generation is permitted independently of this automatic tier.
				return catalog.ApplyScanPolicyTx(ctx, tx, "episodes", catalog.ScanPolicy{Tier: tier})
			}); err != nil {
				t.Fatal(err)
			}
			if automatic {
				c.Exec(`INSERT INTO library_metadata_agents(library_id,agent) VALUES('episodes','local') ON CONFLICT(library_id) DO UPDATE SET agent='local'`)
				c.Exec(`UPDATE inventory_objects SET analysis_revision=? WHERE id='object'`, revision)
			}
			c.Drain()
			meta := metadata.New(c.DB, "")
			if err := meta.SetArtworkDirectory(t.TempDir()); err != nil {
				t.Fatal(err)
			}
			inputs, err := subtitlevideo.New(subtitlevideo.Options{DB: c.DB, Storage: local, Mounts: managed, Directory: t.TempDir(), FFmpeg: ffmpeg, FFprobe: ffprobe})
			if err != nil {
				t.Fatal(err)
			}
			defer inputs.Close()
			meta.EpisodeThumbnails = episodeThumbnailRuntime(meta, sources, identities, local, inputs)
			meta.Admission = (workpolicy.Service{DB: c.DB}).Admit
			authorize := func(tx *sql.Tx) error {
				current, err := identities.PrincipalByDigestTx(ctx, tx, owner.Hash)
				if err != nil {
					return err
				}
				if current.Viewer != owner.Viewer || current.Epoch != owner.Epoch || current.Grant != owner.Grant || current.Authority != "local" || current.Role != "owner" {
					return identity.ErrForbidden
				}
				return identity.DirectAllowed(tx, current, "episodes")
			}
			target := metadata.RepairTarget{Kind: "item", ID: episode.Public}
			before, err := meta.RepairState(ctx, target, authorize)
			if err != nil {
				t.Fatal(err)
			}

			var result metadata.RepairState
			if automatic {
				scanner := ingestion.New(c.DB, catalog.New(c.DB), assets.Probe{Binary: ffprobe})
				job, err := scanner.QueueContext(ctx, "episodes", "episodes", authorize)
				if err != nil {
					t.Fatal(err)
				}
				c.Exec(`UPDATE jobs SET status='running' WHERE id=?`, job.ID)
				policy, err := catalog.New(c.DB).ScanPolicy(ctx, "episodes")
				if err != nil {
					t.Fatal(err)
				}
				c.Exec(`INSERT INTO inventory_analysis_queue(job_id,object_id,revision,policy_revision,state) VALUES(?,'object',?,?,'pending')`, job.ID, revision, policy.Revision)
				done, warning, err := meta.ScanEpisodeThumbnails(ctx, job.ID, "episodes", "object", revision, policy.Revision)
				if err != nil || done || warning != "" {
					t.Fatal("actual automatic decode quantum", done, warning, err)
				}
				done, warning, err = meta.ScanEpisodeThumbnails(ctx, job.ID, "episodes", "object", revision, policy.Revision)
				if err != nil || !done || warning != "" {
					t.Fatal("automatic stage did not settle", done, warning, err)
				}
				var stage string
				if err := c.DB.QueryRow(`SELECT state FROM analysis_stage_runs WHERE job_id=? AND object_id='object' AND stage='episode_thumbnail'`, job.ID).Scan(&stage); err != nil || stage != "complete" {
					t.Fatal(stage, err)
				}
				result, err = meta.RepairState(ctx, target, authorize)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				result, err = meta.MakeEpisodeThumbnail(ctx, target, metadata.RepairCommand{Action: "make_episode_thumbnail", ExpectedRevision: before.Revision}, owner, authorize)
				if err != nil {
					t.Fatal("actual decode/publication", err)
				}
			}

			if result.Revision == before.Revision {
				t.Fatal("decoded frame did not change publication")
			}
			picture, mime, err := meta.Artwork(ctx, episode.Public, "still")
			if err != nil {
				t.Fatal("published image bytes", err)
			}
			dimensions, err := jpeg.DecodeConfig(picture)
			picture.Close()
			if err != nil || mime != "image/jpeg" || dimensions.Width != 320 || dimensions.Height != 180 {
				t.Fatal("actual selected picture", dimensions, mime, err)
			}
			var provider, storedMode, storedTier string
			var locked bool
			if err := c.DB.QueryRow(`SELECT candidate.provider,selection.locked FROM artwork_selections selection JOIN artwork_candidates candidate ON candidate.id=selection.candidate_id WHERE selection.kind='item' AND selection.entity_id=? AND selection.role='still'`, episode.ID).Scan(&provider, &locked); err != nil {
				t.Fatal(err)
			}
			if err := c.DB.QueryRow(`SELECT episode_thumbnail_mode,tier FROM library_scan_policies WHERE library_id='episodes'`).Scan(&storedMode, &storedTier); err != nil {
				t.Fatal(err)
			}
			if provider != "generated" || locked == automatic || storedMode != mode || storedTier != tier {
				t.Fatal("publication fabricated a provider or changed automatic policy", provider, locked, storedMode, storedTier)
			}
		})
	}
}
