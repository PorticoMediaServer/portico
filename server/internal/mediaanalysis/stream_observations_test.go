package mediaanalysis

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

type observationInput struct{}

func (*observationInput) Evidence() string { return "retained-fixture" }
func (*observationInput) Size() int64      { return 1000 }
func (*observationInput) ReadExtent(context.Context, int64, int64) ([]byte, error) {
	return nil, io.EOF
}
func (*observationInput) Validate(context.Context) error { return nil }
func (*observationInput) Close() error                   { return nil }

type observationFixture struct {
	*analysisConsentFixture
	service    *Service
	request    Request
	probes     []decoder.AnalysisProbeKind
	opens      int
	raw        []byte
	afterProbe func(decoder.AnalysisProbeKind)
}

func frameObservationFixture(t *testing.T) *observationFixture {
	return frameObservationFixtureForSource(t, false)
}
func frameObservationFixtureForSource(t *testing.T, remote bool) *observationFixture {
	t.Helper()
	f := &observationFixture{analysisConsentFixture: newAnalysisConsentFixture(t, remote, true)}
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var asset int64
		if err := tx.QueryRow(`SELECT id FROM catalog_assets WHERE token=?`, f.asset).Scan(&asset); err != nil {
			return err
		}
		if err := compactcatalog.SetAssetTx(ctx, tx, asset, map[string]any{"size": int64(1000), "modified_ns": int64(1), "container": "mkv", "video_codec": "hevc", "audio_codec": "aac", "duration": float64(60)}); err != nil {
			return err
		}
		if err := assets.PersistStreams(tx, f.asset, 1000, 1, []assets.Stream{{Index: 0, Type: "video", Codec: "hevc", ColorTransfer: "smpte2084", Detail: assets.StreamDetail{DynamicRange: assets.RangeHDR10, Width: 1920, Height: 1080, StreamBreadth: assets.StreamBreadth{InterlaceAnalysis: assets.InterlaceFieldOrderProgressive}}}, {Index: 1, Type: "audio", Codec: "aac", Language: "en"}}); err != nil {
			return err
		}
		return catalog.ApplyScanPolicyTx(ctx, tx, "movies", catalog.ScanPolicy{Tier: "custom", Operations: []string{"probe", catalog.PlaybackFramesOperation}})
	})
	f.c.Drain()
	var policy int64
	if err := f.c.DB.QueryRow(`SELECT revision FROM library_scan_policies WHERE library_id='movies'`).Scan(&policy); err != nil {
		t.Fatal(err)
	}
	f.c.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('frame-job','movies','running','unit')`)
	f.c.Exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision) SELECT 'frame-job',id,generation,incarnation,root_identity,? FROM library_sources WHERE id=?`, policy, f.source)
	if remote {
		f.c.Exec(`INSERT INTO remote_scan_generations(job_id,source_id,generation) VALUES('frame-job',?,'5')`, f.remote)
	}
	f.c.Exec(`INSERT INTO inventory_analysis_queue(job_id,object_id,revision,policy_revision,state) VALUES('frame-job',?,'r1',?,'pending')`, f.object, policy)
	f.request = Request{JobID: "frame-job", ObjectID: f.object, SourceRevision: "r1", PolicyRevision: policy}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f.service, err = New(Options{DB: f.c.DB, Directory: t.TempDir(), FFprobe: exe,
		Open: func(context.Context, string, string) (Input, error) { f.opens++; return &observationInput{}, nil },
		Decoder: func(Input) Decode {
			return func(context.Context, string, decoder.AnalysisSpec, func(io.Reader) error) error {
				t.Fatal("frame-only grant invoked FFmpeg")
				return nil
			}
		},
		Probe: func(input Input) Probe {
			return func(ctx context.Context, _ string, spec decoder.AnalysisProbeSpec) ([]byte, error) {
				f.probes = append(f.probes, spec.Kind)
				if spec.PhysicalLock == nil {
					t.Error("lost physical producer lock")
				}
				if err := input.Validate(ctx); err != nil {
					return nil, err
				}
				if f.afterProbe != nil {
					f.afterProbe(spec.Kind)
				}
				if f.raw != nil {
					return f.raw, nil
				}
				if spec.Kind == decoder.ProbeFrameHDR {
					return []byte(`{"frames":[{"media_type":"video","width":1920,"height":1080,"side_data_list":[{"side_data_type":"HDR Dynamic Metadata SMPTE2094-40 (HDR10+)"}]}]}`), nil
				}
				frames := []map[string]any{}
				for i := range 10 {
					frames = append(frames, map[string]any{"best_effort_timestamp_time": fmtTimestamp(i), "interlaced_frame": 1, "repeat_pict": 0})
				}
				raw, _ := json.Marshal(map[string]any{"frames": frames})
				return raw, nil
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.service.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}

func fmtTimestamp(i int) string {
	return []string{"0.00", "0.04", "0.08", "0.12", "0.16", "0.20", "0.24", "0.28", "0.32", "0.36"}[i]
}

func TestPlaybackFramesPendingAndRestartDoNotDecodeAgain(t *testing.T) {
	f := frameObservationFixture(t)
	ctx := context.Background()
	for range 4 {
		done, warning, err := f.service.RunOne(ctx, f.request)
		if err != nil || done || warning != "" {
			t.Fatal("pending quantum", done, warning, err)
		}
	}
	if len(f.probes) != 2 || f.opens != 1 {
		t.Fatal("pending work decoded again", f.probes, f.opens)
	}
	var heads int
	if err := f.c.DB.QueryRow(`SELECT count(*) FROM analysis_heads WHERE stage='playback_frames'`).Scan(&heads); err != nil || heads != 0 {
		t.Fatal("premature stage ACK", heads, err)
	}
	breadth, err := assets.ReadStreamBreadth(ctx, f.c.DB, f.asset)
	if err != nil || breadth[0].PlaybackFramesRevision != "" {
		t.Fatal("pending marker published early", breadth, err)
	}
	f.c.Drain()
	// Restart loses only the bounded reuse cache, after canonical publication
	// and before stage ACK. The durable marker must bypass both Open and Probe.
	f.service.streamPending = map[Request]*streamObservation{}
	done, warning, err := f.service.RunOne(ctx, f.request)
	if err != nil || done || warning != "" {
		t.Fatal(done, warning, err)
	}
	if len(f.probes) != 2 || f.opens != 1 {
		t.Fatal("applied recipe decoded after restart", f.probes, f.opens)
	}
	done, warning, err = f.service.RunOne(ctx, f.request)
	if err != nil || !done || warning != "" {
		t.Fatal("stage did not finish", done, warning, err)
	}
	breadth, err = assets.ReadStreamBreadth(ctx, f.c.DB, f.asset)
	if err != nil || breadth[0].PlaybackFramesRevision != assets.PlaybackFramesRevision || breadth[0].ObservedInterlaced() == nil || !*breadth[0].ObservedInterlaced() || breadth[0].FrameRateMode != "constant" {
		t.Fatal("lost decoded observations/marker", breadth, err)
	}
	var hdr bool
	var detail string
	if err := f.c.DB.QueryRow(`SELECT detail_json FROM asset_streams WHERE asset_id=? AND stream_index=0`, f.asset).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	var stream assets.Stream
	stream.DecodeDetail(detail)
	hdr = stream.Detail.HDR10Plus
	if !hdr || stream.Detail.DynamicRange != assets.RangeHDR10 {
		t.Fatal("lost HDR/header", stream)
	}
	var language string
	if err := f.c.DB.QueryRow(`SELECT language FROM asset_streams WHERE asset_id=? AND stream_index=1`, f.asset).Scan(&language); err != nil || language != "en" {
		t.Fatal("lost audio stream", language, err)
	}
}

func TestPlaybackFramesMalformedRecipeRetriesWithoutPublishingAbsence(t *testing.T) {
	f := frameObservationFixture(t)
	f.raw = []byte(`{"frames":[`)
	ctx := context.Background()
	for range 3 {
		if _, _, err := f.service.RunOne(ctx, f.request); err != nil {
			t.Fatal(err)
		}
		f.c.Exec(`UPDATE analysis_stage_runs SET next_ms=0 WHERE job_id='frame-job'`)
	}
	done, warning, err := f.service.RunOne(ctx, f.request)
	if err != nil || !done || warning == "" {
		t.Fatal("failed recipe loops", done, warning, err)
	}
	breadth, err := assets.ReadStreamBreadth(ctx, f.c.DB, f.asset)
	if err != nil || breadth[0].PlaybackFramesRevision != "" || breadth[0].FrameRateMode != "" || breadth[0].InterlaceAnalysis != assets.InterlaceFieldOrderProgressive {
		t.Fatal("failed recipe changed header-only facts", breadth, err)
	}
	if len(f.probes) != 3 {
		t.Fatal("unbounded retries", f.probes)
	}
}

func TestPlaybackFramesGrantNeverIncludesPacketPeak(t *testing.T) {
	f := frameObservationFixture(t)
	ctx := context.Background()
	tx, err := f.c.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	b, err := f.service.bindingTx(ctx, tx, f.request)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ops  []string
		want string
	}{
		{[]string{"probe", catalog.PlaybackFramesOperation}, ""},
		{[]string{"checksum"}, ""},
		{[]string{"probe", "checksum"}, "checksum"},
	} {
		grant, err := f.service.stageGrantTx(ctx, tx, b, catalog.ScanPolicy{Tier: "custom", Operations: tc.ops}, packetPeakStage)
		if err != nil || grant != tc.want {
			t.Fatal(tc.ops, grant, err)
		}
	}
}

func TestPlaybackFramesRefusesDriftAfterObservedBytes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remote bool
		mutate func(*observationFixture)
	}{
		{"job", false, func(f *observationFixture) { f.c.Exec(`UPDATE jobs SET status='canceled' WHERE id='frame-job'`) }},
		{"operation", false, func(f *observationFixture) {
			f.c.Exec(`UPDATE library_scan_policies SET operations_json='["probe"]' WHERE library_id='movies'`)
		}},
		{"object", false, func(f *observationFixture) {
			f.c.Exec(`UPDATE inventory_objects SET revision='replacement' WHERE id=?`, f.object)
		}},
		{"source", false, func(f *observationFixture) {
			f.c.Exec(`UPDATE library_sources SET generation=generation+1 WHERE id=?`, f.source)
		}},
		{"consent", true, func(f *observationFixture) { f.setConsent(3, false) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := frameObservationFixtureForSource(t, tc.remote)
			f.afterProbe = func(kind decoder.AnalysisProbeKind) {
				if kind == decoder.ProbeFrameHDR {
					tc.mutate(f)
				}
			}
			_, _, _ = f.service.RunOne(context.Background(), f.request)
			if len(f.probes) != 2 {
				t.Fatal("fixture failed before the drift point", f.probes)
			}
			var accepted, heads int
			if err := f.c.DB.QueryRow(`SELECT count(*) FROM catalog_info_changes WHERE phase BETWEEN 1 AND 3`).Scan(&accepted); err != nil {
				t.Fatal(err)
			}
			if err := f.c.DB.QueryRow(`SELECT count(*) FROM analysis_heads WHERE stage='playback_frames'`).Scan(&heads); err != nil {
				t.Fatal(err)
			}
			saved, err := assets.ReadStreamBreadth(context.Background(), f.c.DB, f.asset)
			if err != nil || accepted != 0 || heads != 0 || saved[0].PlaybackFramesRevision != "" || saved[0].FrameRateMode != "" {
				t.Fatal("changed admission published/ACKed", accepted, heads, saved, err)
			}
		})
	}
}

func TestPlaybackFramesInapplicableDoesNotOpenOrdinaryInput(t *testing.T) {
	for _, container := range []string{"audio", "disc", "strm"} {
		t.Run(container, func(t *testing.T) {
			f := frameObservationFixture(t)
			f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
				var id int64
				if err := tx.QueryRow(`SELECT id FROM catalog_assets WHERE token=?`, f.asset).Scan(&id); err != nil {
					return err
				}
				values := map[string]any{"container": container}
				if container == "audio" {
					values["container"] = "flac"
					values["video_codec"] = ""
				}
				return compactcatalog.SetAssetTx(ctx, tx, id, values)
			})
			f.c.Drain()
			_, _, err := f.service.RunOne(context.Background(), f.request)
			if container != "strm" && err != nil {
				t.Fatal(err)
			}
			if f.opens != 0 || len(f.probes) != 0 {
				t.Fatal("inapplicable stage read ordinary bytes", f.opens, f.probes)
			}
			saved, e := assets.ReadStreamBreadth(context.Background(), f.c.DB, f.asset)
			if e != nil || saved[0].PlaybackFramesRevision != "" {
				t.Fatal("inapplicable stage claimed observed recipe", saved, e)
			}
		})
	}
}

func TestPacketPeakPublishesOnlyUnderOldDeepGrant(t *testing.T) {
	f := frameObservationFixture(t)
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return catalog.ApplyScanPolicyTx(ctx, tx, "movies", catalog.ScanPolicy{Tier: "custom", Operations: []string{"probe", "checksum"}})
	})
	if err := f.c.DB.QueryRow(`SELECT revision FROM library_scan_policies WHERE library_id='movies'`).Scan(&f.request.PolicyRevision); err != nil {
		t.Fatal(err)
	}
	f.c.Exec(`UPDATE inventory_runs SET policy_revision=? WHERE job_id='frame-job'`, f.request.PolicyRevision)
	f.c.Exec(`UPDATE inventory_analysis_queue SET policy_revision=? WHERE job_id='frame-job'`, f.request.PolicyRevision)
	f.raw = []byte("0.1,100\n0.5,200\n")
	if _, _, err := f.service.RunOne(context.Background(), f.request); err != nil {
		t.Fatal(err)
	}
	if len(f.probes) != 1 || f.probes[0] != decoder.ProbePacketPeak {
		t.Fatal("packet work ran frame recipe", f.probes)
	}
	f.c.Drain()
	if _, _, err := f.service.RunOne(context.Background(), f.request); err != nil {
		t.Fatal(err)
	}
	saved, err := assets.ReadStreamBreadth(context.Background(), f.c.DB, f.asset)
	if err != nil || saved[0].PacketPeakRevision != assets.PacketPeakRevision || saved[0].PlaybackFramesRevision != "" {
		t.Fatal("packet/frame markers mixed", saved, err)
	}
	view, err := f.service.View(context.Background(), Access{ServerID: "server", LibraryID: "movies", ItemID: f.item, Authority: "local", AccountID: "owner", ProfileID: "profile", Owner: true, Authorize: func(*sql.Tx) error { return nil }}, Target{SourceID: f.asset}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range view.Results {
		if result.Stage == packetPeakStage {
			t.Fatal("internal packet bookkeeping exposed as a public operation")
		}
	}
	for _, job := range view.Jobs {
		if job.Stage == packetPeakStage {
			t.Fatal("internal packet job exposed as a public operation")
		}
	}
}

func TestPlaybackFramesInconclusiveMarkerDoesNotInventAbsentFacts(t *testing.T) {
	f := frameObservationFixture(t)
	f.raw = []byte(`{"frames":[{"media_type":"video","width":1920,"height":1080}]}`)
	var before int64
	if err := f.c.DB.QueryRow(`SELECT revision FROM asset_stream_facts WHERE asset_id=?`, f.asset).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.service.RunOne(context.Background(), f.request); err != nil {
		t.Fatal(err)
	}
	f.c.Drain()
	f.service.streamPending = map[Request]*streamObservation{}
	if _, _, err := f.service.RunOne(context.Background(), f.request); err != nil {
		t.Fatal(err)
	}
	if len(f.probes) != 2 || f.opens != 1 {
		t.Fatal("marker-only restart redecoded", f.probes, f.opens)
	}
	var after int64
	if err := f.c.DB.QueryRow(`SELECT revision FROM asset_stream_facts WHERE asset_id=?`, f.asset).Scan(&after); err != nil || after <= before {
		t.Fatal("marker was not in technical fingerprint", before, after, err)
	}
	breadth, err := assets.ReadStreamBreadth(context.Background(), f.c.DB, f.asset)
	if err != nil || breadth[0].PlaybackFramesRevision != assets.PlaybackFramesRevision || breadth[0].FrameRateMode != "" || breadth[0].DolbyVisionMetadataKnown {
		t.Fatal("inconclusive recipe asserted missing facts", breadth, err)
	}
}

func TestPlaybackFramesPublicationRetryKeepsDecodedObservation(t *testing.T) {
	f := frameObservationFixture(t)
	f.c.Exec(`CREATE TRIGGER frame_publication_pause BEFORE INSERT ON catalog_info_new_streams BEGIN SELECT RAISE(ABORT,'resumable publication interruption'); END`)
	if _, _, err := f.service.RunOne(context.Background(), f.request); err != nil {
		t.Fatal(err)
	}
	if len(f.service.streamPending) != 1 {
		t.Fatal("resumable writer failure discarded decoded observation")
	}
	f.c.Exec(`UPDATE analysis_stage_runs SET next_ms=9223372036854 WHERE job_id='frame-job'`)
	for range 3 {
		if _, _, err := f.service.RunOne(context.Background(), f.request); err != nil {
			t.Fatal(err)
		}
	}
	var attempt int
	if err := f.c.DB.QueryRow(`SELECT attempt FROM analysis_stage_runs WHERE job_id='frame-job' AND stage='playback_frames'`).Scan(&attempt); err != nil || attempt != 1 {
		t.Fatal("publication backoff ignored", attempt, err)
	}
	f.c.Exec(`DROP TRIGGER frame_publication_pause`)
	f.c.Exec(`UPDATE analysis_stage_runs SET next_ms=0 WHERE job_id='frame-job'`)
	if _, _, err := f.service.RunOne(context.Background(), f.request); err != nil {
		t.Fatal(err)
	}
	f.c.Drain()
	if _, _, err := f.service.RunOne(context.Background(), f.request); err != nil {
		t.Fatal(err)
	}
	if len(f.probes) != 2 || f.opens != 1 || len(f.service.streamPending) != 0 {
		t.Fatal("writer retry decoded again or failed to ACK", f.probes, f.opens, len(f.service.streamPending))
	}
}

func TestPlaybackFramesPublicationFailureBudgetReleasesPrefix(t *testing.T) {
	f := frameObservationFixture(t)
	f.c.Exec(`CREATE TRIGGER frame_publication_pause BEFORE INSERT ON catalog_info_new_streams BEGIN SELECT RAISE(ABORT,'resumable publication interruption'); END`)
	for range 4 {
		if _, _, err := f.service.RunOne(context.Background(), f.request); err != nil {
			t.Fatal(err)
		}
		f.c.Exec(`UPDATE analysis_stage_runs SET next_ms=0 WHERE job_id='frame-job'`)
	}
	var live int
	if err := f.c.DB.QueryRow(`SELECT count(*) FROM catalog_info_changes WHERE phase=0 AND copy_aux>=0`).Scan(&live); err != nil || live != 0 {
		t.Fatal("exhausted publication left live prefix", live, err)
	}
	done, warning, err := f.service.RunOne(context.Background(), f.request)
	if err != nil || !done || warning == "" || len(f.probes) != 2 {
		t.Fatal("publication retry loops or decodes again", done, warning, err, f.probes)
	}
	f.c.Drain()
}

func TestPlaybackFramesPublicationConsentLossReleasesPrefix(t *testing.T) {
	f := frameObservationFixtureForSource(t, true)
	f.c.Exec(`CREATE TRIGGER frame_publication_pause BEFORE INSERT ON catalog_info_new_streams BEGIN SELECT RAISE(ABORT,'resumable publication interruption'); END`)
	if _, _, err := f.service.RunOne(context.Background(), f.request); err != nil {
		t.Fatal(err)
	}
	f.setConsent(3, false)
	f.c.Exec(`UPDATE analysis_stage_runs SET next_ms=0 WHERE job_id='frame-job'`)
	if _, _, err := f.service.RunOne(context.Background(), f.request); err != nil {
		t.Fatal(err)
	}
	var live int
	if err := f.c.DB.QueryRow(`SELECT count(*) FROM catalog_info_changes WHERE phase=0 AND copy_aux>=0`).Scan(&live); err != nil || live != 0 || len(f.service.streamPending) != 0 || len(f.probes) != 2 {
		t.Fatal("revoked consent stranded prefix/observation", live, len(f.service.streamPending), f.probes, err)
	}
	f.c.Drain()
}

func TestPlaybackFramesTimedDiscAssociationDoesNotOpenOrdinaryInput(t *testing.T) {
	f := frameObservationFixture(t)
	// Timed title media can retain an ordinary demuxer name. Container text is
	// not proof that its stream revision is free of disc associations.
	f.c.Exec(`INSERT INTO inventory_disc_facts(object_id,asset_id,source_revision,source_generation,root_incarnation,kind,packaging,encryption,fingerprint,media_title_key,media_stream_revision) VALUES(?,?,'r1',1,'inc','dvd','iso','none',lower(hex(zeroblob(32))),'timed-title',1)`, f.object, f.asset)
	if _, _, err := f.service.RunOne(context.Background(), f.request); err != nil {
		t.Fatal(err)
	}
	if f.opens != 0 || len(f.probes) != 0 {
		t.Fatal("timed disc was passed to ordinary frame probing", f.opens, f.probes)
	}
}
