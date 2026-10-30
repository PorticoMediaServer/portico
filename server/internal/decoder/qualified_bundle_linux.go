//go:build linux

package decoder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/mediatools"
)

type qualifiedBundleCohort struct {
	manifest, ffmpeg, ffprobe string
	hdr10plus                 bool
}

// This is product admission data for the actual MAIN Linux builds,
// not a capability inferred from a version, name, or operator-written report.
// Each future target/cohort needs its own successful canonical bundle gates.
func qualifiedLinuxCohorts() []qualifiedBundleCohort {
	if runtime.GOARCH != "amd64" {
		return nil
	}
	return []qualifiedBundleCohort{{
		manifest: "ec9305343d192a338721a8ba73468712644bc76ab027c1891a684a87009bb307",
		ffmpeg:   "e45b768839cb9d169196f6b94845b334b7bfc439e6cf6b301efe769c5f3b5b2e",
		ffprobe:  "b83289e34eda99fbc3950f2765819b915befbcc9e7fb8e4c352a06e977a2158d",
	}, {
		// 8b357b18 adds the protocol playlist minimum; its own packet,
		// encoded-video, join and short-disc decode gates all passed.
		manifest: "0752536dae07409853b1655dd491305b63824e17fae36abb6f8248e3758e07de",
		ffmpeg:   "3358b4460db76c75bf7c16a56dda3a5530d4275eadbcfd5405287f110dd2c1de",
		ffprobe:  "7fd702192189b604f7f8fb80f6f1bf1a87aa5bb357c5ee88e5cdcb5e2932c1f2",
	}}
}

var exactDoviMode = regexp.MustCompile(`(?m)^\s+-mode\s+<int>.*\(from 2 to 2\).*\(default 2\)`)
var defaultOffDoviJoin = regexp.MustCompile(`(?m)^\s+-merge_dovi\s+<boolean>.*\(default false\)`)
var defaultOffHDR10Plus = regexp.MustCompile(`(?m)^\s+-hdr10plus\s+<boolean>.*\(default false\)`)

func qualifiedBundleText(root, name string) ([]byte, error) {
	path := filepath.Join(root, name)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return nil, ErrInvalidConfiguration
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return nil, ErrInvalidConfiguration
	}
	return raw, nil
}

func bundleDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// QualifyToolchainBundle grants only explicitly checked CPU bundle features.
// Call after final tool selection on the background lifecycle, with facts from
// that executable's ProbeToolchain/ProbeToolchainRuntime. Unknown/custom pairs
// retain normal software facts and receive no bundle-only grant. It neither
// installs facts nor starts GPU probes, and it never caches a positive result.
func QualifyToolchainBundle(ctx context.Context, ffmpeg, ffprobe string, facts ToolchainFacts) (ToolchainFacts, error) {
	return qualifyToolchainBundle(ctx, ffmpeg, ffprobe, facts, qualifiedLinuxCohorts(), runToolchainProbe)
}

func qualifyToolchainBundle(ctx context.Context, ffmpeg, ffprobe string, facts ToolchainFacts, cohorts []qualifiedBundleCohort, runner toolchainProbeRunner) (ToolchainFacts, error) {
	facts.DolbyVisionProfile7To81, facts.DolbyVisionDualLayerJoin = false, false
	facts.HDR10PlusLibX265Encode = false
	if err := ctx.Err(); err != nil {
		return facts, err
	}
	if !filepath.IsAbs(ffmpeg) || filepath.Clean(ffmpeg) != ffmpeg || !filepath.IsAbs(ffprobe) || filepath.Clean(ffprobe) != ffprobe {
		return facts, nil
	}
	root := filepath.Dir(filepath.Dir(ffmpeg))
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || canonical != root || ffmpeg != filepath.Join(root, "bin/ffmpeg") || ffprobe != filepath.Join(root, "bin/ffprobe") {
		return facts, nil
	}
	raw, err := qualifiedBundleText(root, "toolchain-manifest.json")
	if errors.Is(err, os.ErrNotExist) {
		return facts, nil
	}
	if err != nil {
		return facts, err
	}
	manifestSHA := bundleDigest(raw)
	var cohort *qualifiedBundleCohort
	for i := range cohorts {
		if cohorts[i].manifest == manifestSHA {
			cohort = &cohorts[i]
			break
		}
	}
	if cohort == nil {
		return facts, nil
	}
	var manifest struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return facts, err
	}
	if manifest.Files["bin/ffmpeg"] != cohort.ffmpeg || manifest.Files["bin/ffprobe"] != cohort.ffprobe {
		return facts, errors.New("qualified bundle pair does not match its cohort")
	}
	before, complete, err := mediaexec.RuntimeIdentity(ctx, ffmpeg)
	if err != nil {
		return facts, err
	}
	if !complete || before != facts.Identity {
		return facts, errors.New("selected executable differs from qualified inventory")
	}
	probeBefore, probeComplete, err := mediaexec.RuntimeIdentity(ctx, ffprobe)
	if err != nil || !probeComplete {
		if err == nil {
			err = errors.New("selected probe lacks a qualified native runtime identity")
		}
		return facts, err
	}
	if err := mediatools.VerifyBundleContext(ctx, root); err != nil {
		return facts, err
	}
	profileRaw, err := qualifiedBundleText(root, "dovi-profile-build.json")
	if err != nil {
		return facts, err
	}
	videoRaw, err := qualifiedBundleText(root, "dovi-video-build.json")
	if err != nil {
		return facts, err
	}
	if bundleDigest(profileRaw) != manifest.Files["dovi-profile-build.json"] || bundleDigest(videoRaw) != manifest.Files["dovi-video-build.json"] {
		return facts, errors.New("qualified feature evidence changed")
	}
	if err := checkedDolbyBundleEvidence(profileRaw, videoRaw, *cohort); err != nil {
		return facts, err
	}
	if !facts.BitstreamFilters["dovi_profile"] {
		return facts, errors.New("qualified profile filter is absent from runtime inventory")
	}
	type optionCheck struct {
		help    string
		pattern *regexp.Regexp
	}
	checks := []optionCheck{{"bsf=dovi_profile", exactDoviMode}, {"demuxer=mpegts", defaultOffDoviJoin}}
	if cohort.hdr10plus {
		if !facts.Encoders["libx265"] {
			return facts, errors.New("qualified HDR10+ encoder is absent from runtime inventory")
		}
		if err := checkedHDR10PlusEncodeEvidence(videoRaw, *cohort); err != nil {
			return facts, err
		}
		checks = append(checks, optionCheck{"encoder=libx265", defaultOffHDR10Plus})
	}
	for _, check := range checks {
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		text, err := runner(bounded, mediaexec.Job{Executable: ffmpeg, Args: []string{"-hide_banner", "-h", check.help}})
		cancel()
		if err != nil {
			return facts, err
		}
		if err := ctx.Err(); err != nil {
			return facts, err
		}
		if !check.pattern.MatchString(text) {
			return facts, errors.New("qualified feature options differ from installed executable")
		}
	}
	if err := ctx.Err(); err != nil {
		return facts, err
	}
	after, complete, err := mediaexec.RuntimeIdentity(ctx, ffmpeg)
	if err != nil || !complete || after != before {
		return facts, errors.New("selected executable runtime changed during bundle qualification")
	}
	probeAfter, complete, err := mediaexec.RuntimeIdentity(ctx, ffprobe)
	if err != nil || !complete || probeAfter != probeBefore {
		return facts, errors.New("selected probe runtime changed during bundle qualification")
	}
	current, err := qualifiedBundleText(root, "toolchain-manifest.json")
	if err != nil || bundleDigest(current) != manifestSHA {
		return facts, errors.New("selected bundle changed during qualification")
	}
	if err := ctx.Err(); err != nil {
		return facts, err
	}
	facts.DolbyVisionProfile7To81, facts.DolbyVisionDualLayerJoin = true, true
	facts.HDR10PlusLibX265Encode = cohort.hdr10plus
	return facts, nil
}

func checkedHDR10PlusEncodeEvidence(raw []byte, cohort qualifiedBundleCohort) error {
	// Keep the zero-valued metadata counts distinguishable from omitted proof.
	var evidence struct {
		Encode struct {
			Passed           bool   `json:"passed"`
			Identical        bool   `json:"perFrameMetadataIdentical"`
			DefaultFalse     bool   `json:"optionDefaultFalse"`
			Encoder          string `json:"encoder"`
			Option           string `json:"encoderOption"`
			Fixture          string `json:"fixtureSHA256"`
			FFmpeg           string `json:"ffmpegSHA256"`
			FFprobe          string `json:"ffprobeSHA256"`
			Frames           int    `json:"frames"`
			MetadataFrames   int    `json:"metadataFrames"`
			DefaultFrames    int    `json:"defaultOffFrames"`
			ExplicitFrames   int    `json:"explicitOffFrames"`
			DefaultMetadata  *int   `json:"defaultOffMetadataFrames"`
			ExplicitMetadata *int   `json:"explicitOffMetadataFrames"`
		} `json:"hdr10plusEncode"`
	}
	if err := json.Unmarshal(raw, &evidence); err != nil {
		return err
	}
	e := evidence.Encode
	if !e.Passed || !e.Identical || !e.DefaultFalse || e.Encoder != "libx265" || e.Option != "hdr10plus" ||
		len(e.Fixture) != 64 || e.FFmpeg != cohort.ffmpeg || e.FFprobe != cohort.ffprobe || e.Frames != 24 || e.MetadataFrames != 24 ||
		e.DefaultFrames != 24 || e.ExplicitFrames != 24 || e.DefaultMetadata == nil || e.ExplicitMetadata == nil ||
		*e.DefaultMetadata != 0 || *e.ExplicitMetadata != 0 {
		return errors.New("HDR10+ encode lacks qualified opt-in and default-off target proof")
	}
	return nil
}

func checkedDolbyBundleEvidence(profileRaw, videoRaw []byte, cohort qualifiedBundleCohort) error {
	var profile struct {
		Qualified bool   `json:"qualified"`
		FFmpeg    string `json:"ffmpegSHA256"`
		Source    string `json:"sourceArchiveSHA256"`
		Help      string `json:"profileHelp"`
		Packet    struct {
			Passed    bool `json:"passed"`
			Converted int  `json:"convertedPackets"`
			Wrong     int  `json:"wrongRpuProfilesRejected"`
			Invalid   int  `json:"invalidConfigurationsRejected"`
			Corrupt   int  `json:"corruptRpuRejected"`
			NoRPU     bool `json:"noRpuRejected"`
			Mode      bool `json:"modeIntrospection"`
		} `json:"packetTests"`
	}
	var video struct {
		Qualified bool              `json:"qualified"`
		Target    string            `json:"target"`
		Source    string            `json:"sourceArchiveSHA256"`
		Binaries  map[string]string `json:"binarySHA256"`
		Video     struct {
			Passed bool `json:"passed"`
			Layers []struct {
				Packets int  `json:"packets"`
				RPU     bool `json:"officialMode2RpuIdentical"`
				Pixels  bool `json:"baseDecodedFramesIdentical"`
			} `json:"layers"`
		} `json:"video"`
		HDR struct {
			Passed    bool `json:"passed"`
			Frames    int  `json:"frames"`
			Identical bool `json:"perFrameMetadataIdentical"`
		} `json:"hdr10plus"`
		Join struct {
			Passed   bool              `json:"passed"`
			FFmpeg   string            `json:"binarySha256"`
			FFprobe  string            `json:"probeSha256"`
			Positive int               `json:"joinedCases"`
			Refused  int               `json:"refusalCases"`
			Fixtures []json.RawMessage `json:"fixtures"`
		} `json:"join"`
	}
	if err := json.Unmarshal(profileRaw, &profile); err != nil {
		return err
	}
	if err := json.Unmarshal(videoRaw, &video); err != nil {
		return err
	}
	p := profile.Packet
	if !profile.Qualified || profile.FFmpeg != cohort.ffmpeg || len(profile.Source) != 64 || !exactDoviMode.MatchString(profile.Help) ||
		!p.Passed || p.Converted != 96 || p.Wrong != 3 || p.Invalid != 4 || p.Corrupt != 1 || !p.NoRPU || !p.Mode {
		return errors.New("profile conversion lacks the qualified mode-2 packet gate")
	}
	if !video.Qualified || video.Target != "linux-x64" || video.Source != profile.Source || video.Binaries["ffmpeg"] != cohort.ffmpeg || video.Binaries["ffprobe"] != cohort.ffprobe ||
		!video.Video.Passed || len(video.Video.Layers) != 2 || !video.HDR.Passed || video.HDR.Frames != 24 || !video.HDR.Identical {
		return errors.New("qualified encoded Dolby Vision/HDR10+ gate is incomplete")
	}
	for _, layer := range video.Video.Layers {
		if layer.Packets != 24 || !layer.RPU || !layer.Pixels {
			return errors.New("qualified encoded profile fixture failed")
		}
	}
	j := video.Join
	if !j.Passed || j.FFmpeg != cohort.ffmpeg || j.FFprobe != cohort.ffprobe || j.Positive != 7 || j.Refused != 12 || len(j.Fixtures) != 20 {
		return errors.New("dual-layer join lacks the corrected target fixture gate")
	}
	return nil
}
