package writer

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

func basicCopyRequestFixture() basicCopyRequest {
	s := basicCopyPrepareSource()
	return basicCopyRequest{operation: basicCopySubtitleRow, source: s, position: basicCopyPosition{changeID: s.changeID, changeRevision: 13, phase: 2, afterEntity: 31, family: 8, id: 4095}, shapeID: 61, fileSizeRevision: 1, invalidation: 1, expectedRows: 4096, startOrdinal: 4095, rows: basicRowsRequest{family: 8, subtitles: []basicSubtitle{{id: "native-id", origin: "embedded", locator: "2", streamIndex: 2, format: "srt", language: "en", title: "original"}}}}
}

func TestBasicCopyRequestClosedVariants(t *testing.T) {
	good := basicCopyRequestFixture()
	for _, tc := range []struct {
		op   basicCopyOperation
		rows basicRowsRequest
	}{
		{basicCopyMediaRow, basicRowsRequest{family: 2, streams: []basicStreamRow{{index: 3, streamType: "audio"}}}},
		{basicCopyMediaRow, basicRowsRequest{family: 3, breadth: []basicStreamBreadth{{index: 3}}}},
		{basicCopyChapterFact, basicRowsRequest{family: 4, chapterFacts: []basicChapterFact{{revision: 1, status: "known", fingerprint: "chapter"}}}},
		{basicCopyMediaRow, basicRowsRequest{family: 5, chapters: []basicChapter{{index: 0, title: "chapter", end: 1}}}},
		{basicCopySubtitleFact, basicRowsRequest{family: 6, subtitleFacts: []basicSubtitleFact{{revision: 1, status: "known", fingerprint: "subtitle"}}}},
		{basicCopyMediaRow, basicRowsRequest{family: 7, sidecars: []basicSubtitleSidecar{{id: "s", path: "/media/a.srt", digest: "d"}}}},
		{basicCopyEvidence, basicRowsRequest{family: 9, evidence: []basicSubtitleEvidence{{revision: 1, evidence: "inventory"}}}},
	} {
		r := good
		r.operation = tc.op
		r.rows = tc.rows
		r.position.family = tc.rows.family
		r.startOrdinal = 0
		r.position.id = 0
		r.expectedRows = 1
		r.rawChapterCount = 1
		if _, err := freezeBasicCopyRequest(r); err != nil {
			t.Fatal(tc.op, tc.rows.family, err)
		}
	}
	fact := good
	fact.operation = basicCopyStreamFact
	fact.rows = basicRowsRequest{}
	fact.position.family = 0
	fact.position.id = 0
	fact.expectedRows = 0
	fact.startOrdinal = 0
	if _, err := freezeBasicCopyRequest(fact); err != nil {
		t.Fatal(err)
	}

	for _, mutate := range []func(*basicCopyRequest){
		func(r *basicCopyRequest) { r.operation = 0 },
		func(r *basicCopyRequest) { r.operation = 255 },
		func(r *basicCopyRequest) { r.prefence.stream.shapeID = 1 },
		func(r *basicCopyRequest) { r.beforeChapter.title = "unused" },
		func(r *basicCopyRequest) { r.rows.streams = []basicStreamRow{} },
		func(r *basicCopyRequest) { r.beforeSubtitle.id = "without hadBefore" },
		func(r *basicCopyRequest) { r.position.family = 7 },
		func(r *basicCopyRequest) { r.position.copyPhase = 1 },
		func(r *basicCopyRequest) { r.position.key = "unused" },
		func(r *basicCopyRequest) { r.startOrdinal = 0 },
		func(r *basicCopyRequest) { r.expectedRows = 4097 },
	} {
		r := good
		mutate(&r)
		if _, err := freezeBasicCopyRequest(r); err == nil {
			t.Fatal("accepted malformed variant", r)
		}
	}
	for _, op := range []basicCopyOperation{basicCopyEmptyFamily, basicCopyCleanupStreams, basicCopyCleanupMedia} {
		r := good
		r.operation = op
		r.startOrdinal = 0
		r.expectedRows = 0
		r.position.id = 0
		r.rows = basicRowsRequest{family: 2}
		r.position.family = 2
		if op == basicCopyCleanupMedia {
			r.rows.family = 3
			r.position.family = 3
		}
		if op != basicCopyEmptyFamily {
			r.expectedRows = 20
			r.position.copyPhase = 1
			r.position.aux = 1
			r.position.id = 99
		}
		if _, err := freezeBasicCopyRequest(r); err != nil {
			t.Fatal(op, err)
		}
		r.rows.streams = []basicStreamRow{}
		if _, err := freezeBasicCopyRequest(r); err == nil {
			t.Fatal("accepted empty nonnil cleanup payload")
		}
	}
}

func TestBasicCopyRequestFrozenNativeOrdinal(t *testing.T) {
	in := basicCopyRequestFixture()
	in.hadBefore = true
	in.beforeSubtitle = in.rows.subtitles[0]
	out, err := freezeBasicCopyRequest(in)
	if err != nil || !reflect.DeepEqual(in, out) {
		t.Fatal(out, err)
	}
	if &in.rows.subtitles[0] == &out.rows.subtitles[0] || unsafe.StringData(in.source.assetToken) == unsafe.StringData(out.source.assetToken) || unsafe.StringData(in.beforeSubtitle.title) == unsafe.StringData(out.beforeSubtitle.title) {
		t.Fatal("retained caller storage")
	}
	in.rows.subtitles[0].title = "changed"
	if out.rows.subtitles[0].title != "original" || out.startOrdinal != 4095 || out.expectedRows != 4096 || len(out.rows.subtitles) != 1 {
		t.Fatal(out)
	}
	s := basicCopyPrepareSource()
	p := basicOrdinaryInputAccept{stream: basicStreamAccept{source: s, changeRevision: 13, shapeID: 61, rawShape: ScanShape{LibraryID: 7, OrdinaryOwners: 1, VersionAssetIDs: []int64{41}, AuthoredParentIDs: []int64{}, Streams: []Stream{}}}, counts: [9]int64{1, 0, 0, 1, 0, 1, 0, 0, 1}, snapshot: basicProbeEffects{source: s, changeRevision: 13, assetPath: "/media/a", before: basicProbeScalars{videoCodec: "h264", duration: 1}, after: basicProbeScalars{videoCodec: "h264", duration: 1}, playbackIncarnation: [16]byte{1}, playbackRevision: 1, preparedRevision: 1, fileSizeRevision: 1, invalidationRevision: 1}}
	r := basicCopyRequest{operation: basicCopyPrefence, source: s, prefence: p}
	frozen, err := freezeBasicCopyRequest(r)
	if err != nil {
		t.Fatal(err)
	}
	r.prefence.stream.rawShape.VersionAssetIDs[0] = 99
	if frozen.prefence.stream.rawShape.VersionAssetIDs[0] != 41 || frozen.prefence.stream.rawShape.AuthoredParentIDs == nil || frozen.prefence.stream.rawShape.CUEOrdinals != nil || unsafe.StringData(frozen.prefence.snapshot.assetPath) == unsafe.StringData(r.prefence.snapshot.assetPath) {
		t.Fatal("prefence ownership/wire identity")
	}
}

func TestBasicCopyRequestWholeBytesBeforeAllocation(t *testing.T) {
	r := basicCopyRequestFixture()
	// Each individual text fits, but source + new + before exceeds the whole cap.
	text := strings.Repeat("x", maxCanonicalRequestBytes/2)
	r.source.fingerprint = text
	r.rows.subtitles[0].title = text
	r.hadBefore = true
	r.beforeSubtitle = r.rows.subtitles[0]
	var err error
	allocations := testing.AllocsPerRun(3, func() { _, err = freezeBasicCopyRequest(r) })
	if !errors.Is(err, ErrCap) || allocations != 0 {
		t.Fatal(err, allocations)
	}
	r = basicCopyRequestFixture()
	r.rows.subtitles = make([]basicSubtitle, 9)
	if _, err = basicCopyRequestBytes(r); !errors.Is(err, ErrCap) {
		t.Fatal(err)
	}
	a := basicCopyRequestFixture()
	b := a
	b.rows.streams = []basicStreamRow{}
	if reflect.DeepEqual(a, b) {
		t.Fatal("test lost nil/empty difference")
	}
	if _, err = freezeBasicCopyRequest(b); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
