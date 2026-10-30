//go:build linux

package decoder

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

// These controlled filesystem/runner fixtures exercise admission and retirement
// fences. Only the canonical target fixtures establish real DV conversion.
func qualifiedBundleFixture(t *testing.T, edit func(map[string]any, map[string]any)) (string, string, ToolchainFacts, []qualifiedBundleCohort) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	source := fakeFFmpeg(t)
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	toolSHA := bundleDigest(data)
	for _, name := range []string{"ffmpeg", "ffprobe", "portico-disc"} {
		if err := os.WriteFile(filepath.Join(root, "bin", name), data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	ffmpeg, ffprobe := filepath.Join(root, "bin/ffmpeg"), filepath.Join(root, "bin/ffprobe")
	sourceSHA := strings.Repeat("a", 64)
	profile := map[string]any{"schema": 1, "qualified": true, "ffmpegSHA256": toolSHA, "sourceArchiveSHA256": sourceSHA,
		"profileHelp": " -mode <int> (from 2 to 2) (default 2)",
		"packetTests": map[string]any{"passed": true, "convertedPackets": 96, "wrongRpuProfilesRejected": 3, "invalidConfigurationsRejected": 4, "corruptRpuRejected": 1, "noRpuRejected": true, "modeIntrospection": true}}
	layers := []any{}
	for range 2 {
		layers = append(layers, map[string]any{"packets": 24, "officialMode2RpuIdentical": true, "baseDecodedFramesIdentical": true})
	}
	cases := make([]any, 20)
	for i := range cases {
		cases[i] = map[string]any{"controlled": true}
	}
	video := map[string]any{"schema": 1, "qualified": true, "target": "linux-x64", "sourceArchiveSHA256": sourceSHA,
		"binarySHA256": map[string]string{"ffmpeg": toolSHA, "ffprobe": toolSHA},
		"video":        map[string]any{"passed": true, "layers": layers},
		"hdr10plus":    map[string]any{"passed": true, "frames": 24, "perFrameMetadataIdentical": true},
		"join":         map[string]any{"passed": true, "binarySha256": toolSHA, "probeSha256": toolSHA, "joinedCases": 7, "refusalCases": 12, "fixtures": cases}}
	if edit != nil {
		edit(profile, video)
	}
	library := map[string]any{"version": "controlled", "archive": "lib/fixture.a", "sha256": sourceSHA}
	disc := map[string]any{"schema": 1, "tool": "portico-disc", "target": "linux-x64", "sourceCommit": strings.Repeat("a", 40), "sourceSha256": sourceSHA, "binarySha256": toolSHA, "decryption": false,
		"staticLibraries": map[string]any{"libbluray": library, "libudfread": library, "dvdnav": library, "dvdread": library}}
	files := map[string][]byte{"corresponding-source.tar.xz": []byte("controlled source fixture"), "NOTICE.md": []byte("controlled notice"), "sources.lock.json": []byte("{}"), "requirements.v1.json": []byte("{}"), "LICENSES/portico-disc.txt": []byte("controlled licence")}
	for name, value := range map[string]any{"dovi-profile-build.json": profile, "dovi-video-build.json": video, "portico-disc-build.json": disc} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = raw
	}
	inventory := map[string]string{}
	for name, raw := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		inventory[name] = bundleDigest(raw)
	}
	for _, name := range []string{"ffmpeg", "ffprobe", "portico-disc"} {
		inventory["bin/"+name] = toolSHA
	}
	raw, err := json.Marshal(map[string]any{"schemaVersion": 1, "licenseMode": "gpl3", "target": "linux-x64", "files": inventory})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "toolchain-manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	identity, complete, err := inventoryIdentity(context.Background(), ffmpeg)
	if err != nil || !complete {
		t.Fatal(identity, complete, err)
	}
	facts := ToolchainFacts{Identity: identity, inventoryCacheable: true, BitstreamFilters: map[string]bool{"dovi_profile": true}}
	return ffmpeg, ffprobe, facts, []qualifiedBundleCohort{{manifest: bundleDigest(raw), ffmpeg: toolSHA, ffprobe: toolSHA}}
}

func qualifiedOptionRunner(t *testing.T, action func(mediaexec.Job)) toolchainProbeRunner {
	return func(ctx context.Context, job mediaexec.Job) (string, error) {
		t.Helper()
		if job.Hardware || len(job.ReadPaths) != 0 || len(job.WriteDirs) != 0 || len(job.Args) != 3 {
			t.Fatal("option introspection gained media/GPU authority", job)
		}
		if action != nil {
			action(job)
		}
		switch job.Args[2] {
		case "bsf=dovi_profile":
			return " -mode <int> (from 2 to 2) (default 2)", nil
		case "demuxer=mpegts":
			return " -merge_dovi <boolean> (default false)", nil
		case "encoder=libx265":
			return " -hdr10plus <boolean> (default false)", nil
		}
		t.Fatal(job.Args)
		return "", nil
	}
}

func TestQualifiedBundleRequiresSelectedPairAndAllEvidence(t *testing.T) {
	ff, fp, facts, cohorts := qualifiedBundleFixture(t, nil)
	got, err := qualifyToolchainBundle(context.Background(), ff, fp, facts, cohorts, qualifiedOptionRunner(t, nil))
	if err != nil || !got.DolbyVisionProfile7To81 || !got.DolbyVisionDualLayerJoin || got.VulkanAvailable || got.OpenCLAvailable {
		t.Fatal(got, err)
	}
	// A real manifest but no checked cohort may not obtain grants by names/options.
	got, err = qualifyToolchainBundle(context.Background(), ff, fp, got, nil, func(context.Context, mediaexec.Job) (string, error) {
		t.Fatal("unknown cohort ran options")
		return "", nil
	})
	if err != nil || got.DolbyVisionProfile7To81 || got.DolbyVisionDualLayerJoin {
		t.Fatal(got, err)
	}
	got, err = qualifyToolchainBundle(context.Background(), ff, filepath.Join(t.TempDir(), "ffprobe"), facts, cohorts, qualifiedOptionRunner(t, nil))
	if err != nil || got.DolbyVisionProfile7To81 || got.DolbyVisionDualLayerJoin {
		t.Fatal("mixed pair granted features", got, err)
	}
}

func TestQualifiedBundleRetiresUnqualifiedHDR10PlusFact(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		ff, fp, facts, _ := qualifiedBundleFixture(t, nil)
		facts.HDR10PlusLibX265Encode = true
		ctx, cancel := context.WithCancel(context.Background())
		if cancelled {
			cancel()
		}
		got, err := qualifyToolchainBundle(ctx, ff, fp, facts, nil, qualifiedOptionRunner(t, nil))
		cancel()
		if got.HDR10PlusLibX265Encode || (cancelled && !errors.Is(err, context.Canceled)) || (!cancelled && err != nil) {
			t.Fatal("unqualified incoming HDR10+ fact survived retirement", got, err)
		}
	}
}

func qualifiedHDR10PlusFixture(t *testing.T, edit func(map[string]any)) (string, string, ToolchainFacts, []qualifiedBundleCohort) {
	t.Helper()
	ff, fp, facts, cohorts := qualifiedBundleFixture(t, func(_, video map[string]any) {
		pair := video["binarySHA256"].(map[string]string)
		proof := map[string]any{"passed": true, "perFrameMetadataIdentical": true, "optionDefaultFalse": true,
			"encoder": "libx265", "encoderOption": "hdr10plus", "fixtureSHA256": strings.Repeat("c", 64),
			"ffmpegSHA256": pair["ffmpeg"], "ffprobeSHA256": pair["ffprobe"], "frames": 24, "metadataFrames": 24,
			"defaultOffFrames": 24, "defaultOffMetadataFrames": 0, "explicitOffFrames": 24, "explicitOffMetadataFrames": 0}
		if edit != nil {
			edit(proof)
		}
		video["hdr10plusEncode"] = proof
	})
	cohorts[0].hdr10plus = true
	facts.Encoders = map[string]bool{"libx265": true}
	return ff, fp, facts, cohorts
}

func TestQualifiedBundleHDR10PlusRequiresTargetProofAndOptIn(t *testing.T) {
	ff, fp, facts, cohorts := qualifiedHDR10PlusFixture(t, nil)
	got, err := qualifyToolchainBundle(context.Background(), ff, fp, facts, cohorts, qualifiedOptionRunner(t, nil))
	if err != nil || !got.HDR10PlusLibX265Encode {
		t.Fatal("qualified opt-in did not survive the selected identity fences", got, err)
	}
	cohorts[0].hdr10plus = false
	got, err = qualifyToolchainBundle(context.Background(), ff, fp, got, cohorts, qualifiedOptionRunner(t, nil))
	if err != nil || got.HDR10PlusLibX265Encode {
		t.Fatal("older cohort obtained a grant from an added report", got, err)
	}
	for _, key := range []string{"passed", "perFrameMetadataIdentical", "optionDefaultFalse", "encoder", "encoderOption", "fixtureSHA256", "ffmpegSHA256", "ffprobeSHA256", "frames", "metadataFrames", "defaultOffFrames", "defaultOffMetadataFrames", "explicitOffFrames", "explicitOffMetadataFrames"} {
		t.Run(key, func(t *testing.T) {
			ff, fp, facts, cohorts := qualifiedHDR10PlusFixture(t, func(proof map[string]any) { delete(proof, key) })
			got, err := qualifyToolchainBundle(context.Background(), ff, fp, facts, cohorts, qualifiedOptionRunner(t, nil))
			if err == nil || got.HDR10PlusLibX265Encode {
				t.Fatal("incomplete encoder evidence was admitted", got, err)
			}
		})
	}
}

func TestQualifiedBundleHDR10PlusRejectsChangedDefaultAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		ff, fp, facts, cohorts := qualifiedHDR10PlusFixture(t, nil)
		ctx, cancel := context.WithCancel(context.Background())
		base := qualifiedOptionRunner(t, nil)
		runner := func(ctx context.Context, job mediaexec.Job) (string, error) {
			if job.Args[2] == "encoder=libx265" {
				if cancelled {
					cancel()
				}
				return " -hdr10plus <boolean> (default true)", nil
			}
			return base(ctx, job)
		}
		got, err := qualifyToolchainBundle(ctx, ff, fp, facts, cohorts, runner)
		cancel()
		if err == nil || got.HDR10PlusLibX265Encode {
			t.Fatal("changed defaults or cancellation obtained an encode grant", got, err)
		}
	}
}

func TestQualifiedBundleRejectsIncompleteTargetProof(t *testing.T) {
	for name, edit := range map[string]func(map[string]any, map[string]any){
		"packet count":    func(p, v map[string]any) { p["packetTests"].(map[string]any)["convertedPackets"] = 95 },
		"no RPU refusal":  func(p, v map[string]any) { p["packetTests"].(map[string]any)["noRpuRejected"] = false },
		"join cases":      func(p, v map[string]any) { v["join"].(map[string]any)["refusalCases"] = 11 },
		"join identity":   func(p, v map[string]any) { v["join"].(map[string]any)["probeSha256"] = "other" },
		"encoded proof":   func(p, v map[string]any) { v["video"].(map[string]any)["passed"] = false },
		"source mismatch": func(p, v map[string]any) { v["sourceArchiveSHA256"] = strings.Repeat("b", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			ff, fp, facts, cohorts := qualifiedBundleFixture(t, edit)
			got, err := qualifyToolchainBundle(context.Background(), ff, fp, facts, cohorts, qualifiedOptionRunner(t, nil))
			if err == nil || got.DolbyVisionProfile7To81 || got.DolbyVisionDualLayerJoin {
				t.Fatal(got, err)
			}
		})
	}
}

func TestQualifiedBundleRejectsOptionsSubstitutionAndCancellation(t *testing.T) {
	for _, name := range []string{"mode", "selected probe replacement", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			ff, fp, facts, cohorts := qualifiedBundleFixture(t, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runner := qualifiedOptionRunner(t, func(job mediaexec.Job) {
				if job.Args[2] != "bsf=dovi_profile" {
					return
				}
				if name == "cancelled" {
					cancel()
				}
				if name == "selected probe replacement" {
					raw, err := os.ReadFile(fp)
					if err != nil {
						t.Fatal(err)
					}
					next := fp + ".new"
					if err = os.WriteFile(next, raw, 0700); err != nil {
						t.Fatal(err)
					}
					if err = os.Rename(next, fp); err != nil {
						t.Fatal(err)
					}
				}
			})
			if name == "mode" {
				runner = func(context.Context, mediaexec.Job) (string, error) {
					return " -mode <int> (from 1 to 2) (default 2)", nil
				}
			}
			got, err := qualifyToolchainBundle(ctx, ff, fp, facts, cohorts, runner)
			if err == nil || got.DolbyVisionProfile7To81 || got.DolbyVisionDualLayerJoin {
				t.Fatal(got, err)
			}
			if name == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestQualifiedBundleCanonicalRuntime(t *testing.T) {
	root := os.Getenv("PORTICO_TEST_QUALIFIED_BUNDLE")
	if root == "" {
		t.Skip("set PORTICO_TEST_QUALIFIED_BUNDLE to the exact checked Linux bundle for this target integration gate")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	ff, fp := filepath.Join(root, "bin/ffmpeg"), filepath.Join(root, "bin/ffprobe")
	facts, err := ProbeToolchain(ctx, ff)
	if err != nil {
		t.Fatal(err)
	}
	qualified, err := QualifyToolchainBundle(ctx, ff, fp, facts)
	if err != nil || !qualified.DolbyVisionProfile7To81 || !qualified.DolbyVisionDualLayerJoin || qualified.Identity != facts.Identity || qualified.VulkanAvailable || qualified.OpenCLAvailable {
		t.Fatal("exact canonical runtime failed its bound feature producer", qualified, err)
	}
	if os.Getenv("PORTICO_TEST_HDR10PLUS_ENCODE") == "1" && !qualified.HDR10PlusLibX265Encode {
		t.Fatal("canonical opt-in encoder cohort did not qualify", qualified)
	}
}
