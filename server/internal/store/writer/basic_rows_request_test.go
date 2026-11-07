package writer

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/medialimits"
)

func TestBasicRowsRequestClosedAndFrozen(t *testing.T) {
	source := basicStreamFactStage{libraryID: 7, assetToken: "asset", runID: "run", sourceID: "source", incarnation: "incarnation", objectID: "object", objectRevision: "revision", fingerprint: "fingerprint"}
	for _, tc := range []struct {
		in  basicRowsRequest
		row basicMediaRow
	}{
		{basicRowsRequest{family: 2, streams: []basicStreamRow{{index: 3, streamType: "audio", codec: "aac", language: "en", title: "Audio", channels: 2, channelLayout: "stereo", colorTransfer: "bt709", colorPrimaries: "bt709", detail: `{"bitrate":128000}`, isDefault: true, isForced: true}}}, basicStreamRow{index: 3, streamType: "audio", codec: "aac", language: "en", title: "Audio", channels: 2, channelLayout: "stereo", colorTransfer: "bt709", colorPrimaries: "bt709", detail: `{"bitrate":128000}`, isDefault: true, isForced: true}},
		{basicRowsRequest{family: 3, breadth: []basicStreamBreadth{{index: 3, fpsNumerator: 24, fpsDenominator: 1, audioDescription: true, telecine: true, doviRPU: true, doviBase: true, doviKnown: true, audioVariant: "main", chroma: "4:2:0", frameMode: "constant", interlace: "progressive", doviEnhancement: "mel", doviCM: "4", playbackFramesRevision: "frames", packetPeakRevision: "peak"}}}, basicStreamBreadth{index: 3, fpsNumerator: 24, fpsDenominator: 1, audioDescription: true, telecine: true, doviRPU: true, doviBase: true, doviKnown: true, audioVariant: "main", chroma: "4:2:0", frameMode: "constant", interlace: "progressive", doviEnhancement: "mel", doviCM: "4", playbackFramesRevision: "frames", packetPeakRevision: "peak"}},
		{basicRowsRequest{family: 4, chapterFacts: []basicChapterFact{{37, "known", "chapters:37"}}}, basicChapterFact{37, "known", "chapters:37"}},
		{basicRowsRequest{family: 5, rawChapterCount: 2, chapters: []basicChapter{{1, "Opening", 0, 3}}}, basicChapter{1, "Opening", 0, 3}},
		{basicRowsRequest{family: 6, subtitleFacts: []basicSubtitleFact{{41, 12, true, "known", "subtitles:41"}}}, basicSubtitleFact{41, 12, true, "known", "subtitles:41"}},
		{basicRowsRequest{family: 7, sidecars: []basicSubtitleSidecar{{"sidecar", "/media/a.srt", "digest", 30, -7}}}, basicSubtitleSidecar{"sidecar", "/media/a.srt", "digest", 30, -7}},
		{basicRowsRequest{family: 8, subtitles: []basicSubtitle{{id: "subtitle", origin: "sidecar", locator: "/media/a.srt", sidecarID: "sidecar", format: "srt", language: "en", title: "English", reason: "found", streamIndex: -1, isDefault: true, isForced: true, hearingImpaired: true}}}, basicSubtitle{id: "subtitle", origin: "sidecar", locator: "/media/a.srt", sidecarID: "sidecar", format: "srt", language: "en", title: "English", reason: "found", streamIndex: -1, isDefault: true, isForced: true, hearingImpaired: true}},
		{basicRowsRequest{family: 9, evidence: []basicSubtitleEvidence{{41, "inventory:41"}}}, basicSubtitleEvidence{41, "inventory:41"}},
	} {
		frozen, err := freezeBasicRowsRequest(source, tc.in)
		if err != nil || !reflect.DeepEqual(frozen, tc.in) {
			t.Fatal("freeze changed typed values", tc.in.family, frozen, err)
		}
		before, after := reflect.ValueOf(tc.in), reflect.ValueOf(frozen)
		for field := 2; field < before.NumField(); field++ {
			a, b := before.Field(field), after.Field(field)
			if a.Len() == 0 {
				continue
			}
			if a.Pointer() == b.Pointer() {
				t.Fatal("retained caller slice", tc.in.family)
			}
			for field := 0; field < a.Index(0).NumField(); field++ {
				x, y := a.Index(0).Field(field), b.Index(0).Field(field)
				if x.Kind() == reflect.String && x.Len() > 0 && unsafe.StringData(x.String()) == unsafe.StringData(y.String()) {
					t.Fatal("retained caller text", tc.in.family, field)
				}
			}
		}
		prepared, err := prepareBasicRowsRequest(source, frozen)
		if err != nil || prepared == nil || !prepared.valid || prepared.source != source || prepared.family != tc.in.family || prepared.rawChapterCount != tc.in.rawChapterCount || len(prepared.rows) != 1 || !reflect.DeepEqual(prepared.rows[0], tc.row) {
			t.Fatal("derived family lost exact typed row/source", tc.in.family, prepared, err)
		}
		texts, _, _, base, _ := basicMediaTextFields(source, tc.row)
		want := uint64(base)
		for _, s := range [...]string{source.assetToken, source.runID, source.sourceID, source.incarnation, source.objectID, source.objectRevision, source.fingerprint} {
			want += uint64(len(s))
		}
		for _, s := range texts {
			want += uint64(len(s))
		}
		if n, err := basicRowsRequestBytes(source, tc.in); err != nil || n != want {
			t.Fatal("incomplete byte contribution", tc.in.family, n, want, err)
		}
		if allocations := testing.AllocsPerRun(5, func() { _, _ = basicRowsRequestBytes(source, tc.in) }); allocations != 0 {
			t.Fatal("byte preflight allocated", tc.in.family, allocations)
		}
	}

	// Each inactive field must be nil, even if its length is zero.
	for _, in := range []basicRowsRequest{
		{family: 9, evidence: []basicSubtitleEvidence{{1, "e"}}, streams: []basicStreamRow{}},
		{family: 9, evidence: []basicSubtitleEvidence{{1, "e"}}, breadth: []basicStreamBreadth{}},
		{family: 9, evidence: []basicSubtitleEvidence{{1, "e"}}, chapterFacts: []basicChapterFact{}},
		{family: 9, evidence: []basicSubtitleEvidence{{1, "e"}}, chapters: []basicChapter{}},
		{family: 9, evidence: []basicSubtitleEvidence{{1, "e"}}, subtitleFacts: []basicSubtitleFact{}},
		{family: 9, evidence: []basicSubtitleEvidence{{1, "e"}}, sidecars: []basicSubtitleSidecar{}},
		{family: 9, evidence: []basicSubtitleEvidence{{1, "e"}}, subtitles: []basicSubtitle{}},
		{family: 4, chapterFacts: []basicChapterFact{{1, "known", "c"}}, evidence: []basicSubtitleEvidence{}},
		{family: 0}, {family: 1}, {family: 10},
	} {
		if got, err := freezeBasicRowsRequest(source, in); !reflect.DeepEqual(got, basicRowsRequest{}) || !errors.Is(err, ErrInvalid) {
			t.Fatal("accepted inactive/unknown variant", in, got, err)
		}
	}
	for _, in := range []basicRowsRequest{
		{family: 2}, {family: 2, streams: []basicStreamRow{}},
		{family: 3}, {family: 3, breadth: []basicStreamBreadth{}},
		{family: 5}, {family: 5, chapters: []basicChapter{}},
		{family: 7}, {family: 7, sidecars: []basicSubtitleSidecar{}},
		{family: 8}, {family: 8, subtitles: []basicSubtitle{}},
	} {
		if got, err := freezeBasicRowsRequest(source, in); err != nil || !reflect.DeepEqual(got, in) {
			t.Fatal("selected nil/empty identity changed", in, got, err)
		}
	}
	for _, family := range []int64{4, 6, 9} {
		if _, err := freezeBasicRowsRequest(source, basicRowsRequest{family: family}); !errors.Is(err, ErrInvalid) {
			t.Fatal("empty singleton accepted", family, err)
		}
	}
}

func TestBasicRowsRequestExistingValidation(t *testing.T) {
	source := basicStreamFactStage{libraryID: 7, assetToken: "asset"}
	for _, path := range []string{string([]byte{'/', 0xff}), string([]byte{'/', 0, 0xff})} {
		for _, in := range []basicRowsRequest{
			{family: 7, sidecars: []basicSubtitleSidecar{{id: "sidecar", path: path, digest: "digest"}}},
			{family: 8, subtitles: []basicSubtitle{{id: "subtitle", origin: "sidecar", locator: path, sidecarID: "sidecar", streamIndex: -1}}},
		} {
			_, err := freezeBasicRowsRequest(source, in)
			if strings.IndexByte(path, 0) >= 0 {
				if !errors.Is(err, ErrInvalid) || errors.Is(err, ErrFilenameUnrepresentable) {
					t.Fatal("NUL lost invalid-input cause", err)
				}
			} else {
				var policy *PolicyError
				if !errors.Is(err, ErrFilenameUnrepresentable) || !errors.As(err, &policy) || policy.Field != "basic_subtitle_path" {
					t.Fatal("native path lost typed representability cause", err)
				}
			}
		}
	}
	streams := make([]basicStreamRow, MaxStreamsPerClass+1)
	for n := range streams {
		streams[n] = basicStreamRow{index: int64(n), streamType: "audio"}
	}
	if _, err := freezeBasicRowsRequest(source, basicRowsRequest{family: 2, streams: streams[:MaxStreamsPerClass]}); err != nil {
		t.Fatal("255 streams refused", err)
	}
	for _, tc := range []struct {
		in    basicRowsRequest
		cause error
	}{
		{basicRowsRequest{family: 2, streams: streams}, ErrCap},
		{basicRowsRequest{family: 2, streams: []basicStreamRow{{index: 3, streamType: "audio"}, {index: 3, streamType: "video"}}}, ErrInvalid},
		{basicRowsRequest{family: 5, rawChapterCount: MaxRawChapters + 1}, ErrCap},
		{basicRowsRequest{family: 5, rawChapterCount: -1}, ErrInvalid},
		{basicRowsRequest{family: 5, rawChapterCount: 1, chapters: []basicChapter{{0, "a", 0, 2}, {1, "b", 2, 4}}}, ErrInvalid},
		{basicRowsRequest{family: 5, rawChapterCount: 2, chapters: []basicChapter{{0, "a", 0, 3}, {1, "b", 2, 4}}}, ErrInvalid},
		{basicRowsRequest{family: 7, sidecars: make([]basicSubtitleSidecar, medialimits.MaxSubtitleTracks+1)}, ErrCap},
		{basicRowsRequest{family: 8, subtitles: make([]basicSubtitle, medialimits.MaxSubtitleTracks+1)}, ErrCap},
		{basicRowsRequest{family: 7, sidecars: []basicSubtitleSidecar{{id: "s", path: "/a.srt", digest: string([]byte{0xff})}}}, ErrInvalid},
	} {
		if _, err := freezeBasicRowsRequest(source, tc.in); !errors.Is(err, tc.cause) {
			t.Fatal("existing family validation changed", tc.in.family, err, tc.cause)
		}
	}
	if _, err := freezeBasicRowsRequest(source, basicRowsRequest{family: 5, rawChapterCount: MaxRawChapters}); err != nil {
		t.Fatal("raw chapter cap refused after cleanup", err)
	}
}

func TestBasicRowsRequestWholePreflight(t *testing.T) {
	source := basicStreamFactStage{assetToken: "asset", runID: "run", sourceID: "source", incarnation: "incarnation", objectID: "object", objectRevision: "revision", fingerprint: "fingerprint"}
	// The first path is malformed; a late byte overflow must win before any
	// validation, interface slice, map or clone allocation in either adapter.
	in := basicRowsRequest{family: 7, sidecars: []basicSubtitleSidecar{
		{id: "a", path: string([]byte{'/', 0xff}), digest: "a"},
		{id: "b", path: "/b.srt"},
	}}
	base, err := basicRowsRequestBytes(source, in)
	if err != nil {
		t.Fatal(err)
	}
	backing := strings.Repeat("x", maxCanonicalRequestBytes+1)
	in.sidecars[1].digest = backing[:uint64(maxCanonicalRequestBytes)-base]
	if n, err := basicRowsRequestBytes(source, in); err != nil || n != maxCanonicalRequestBytes {
		t.Fatal("exact whole-family byte cap refused", n, err)
	}
	in.sidecars[1].digest = backing[:uint64(maxCanonicalRequestBytes)-base+1]
	for _, check := range []struct {
		name string
		run  func() error
	}{
		{"bytes", func() error { _, err := basicRowsRequestBytes(source, in); return err }},
		{"freeze", func() error { _, err := freezeBasicRowsRequest(source, in); return err }},
		{"prepare", func() error { _, err := prepareBasicRowsRequest(source, in); return err }},
	} {
		if err := check.run(); !errors.Is(err, ErrCap) || errors.Is(err, ErrFilenameUnrepresentable) {
			t.Fatal("late whole-family cap lost precedence", check.name, err)
		}
		if allocations := testing.AllocsPerRun(5, func() { _ = check.run() }); allocations != 0 {
			t.Fatal("allocated before complete byte admission", check.name, allocations)
		}
	}
	// Include every source field even when the selected complete family is empty.
	source.fingerprint = backing[:maxCanonicalRequestBytes]
	if _, err := basicRowsRequestBytes(source, basicRowsRequest{family: 5}); !errors.Is(err, ErrCap) {
		t.Fatal("empty rows bypassed source length admission", err)
	}
}
