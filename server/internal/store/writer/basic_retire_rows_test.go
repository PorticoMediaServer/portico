package writer

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

func basicRetirePrefixTestSource() basicStreamFactStage {
	return basicStreamFactStage{libraryID: 7, changeID: 51, inputRevision: 7, assetID: 41, expectedEpoch: 17, assetToken: "asset", runID: "run", sourceID: "source", incarnation: "incarnation", objectID: "object", objectRevision: "revision", fingerprint: "fingerprint"}
}

func basicRetirePrefixTestChapters(n int) []basicChapter {
	out := make([]basicChapter, n)
	for i := range out {
		out[i] = basicChapter{index: int64(i), title: "Chapter", start: float64(2 * i), end: float64(2*i + 1)}
	}
	return out
}

func basicRetirePrefixTestInterfaces(rows basicRowsRequest) []basicMediaRow {
	var out []basicMediaRow
	switch rows.family {
	case 2:
		out = make([]basicMediaRow, len(rows.streams))
		for n := range rows.streams {
			out[n] = rows.streams[n]
		}
	case 3:
		out = make([]basicMediaRow, len(rows.breadth))
		for n := range rows.breadth {
			out[n] = rows.breadth[n]
		}
	case 4:
		out = make([]basicMediaRow, len(rows.chapterFacts))
		for n := range rows.chapterFacts {
			out[n] = rows.chapterFacts[n]
		}
	case 5:
		out = make([]basicMediaRow, len(rows.chapters))
		for n := range rows.chapters {
			out[n] = rows.chapters[n]
		}
	case 6:
		out = make([]basicMediaRow, len(rows.subtitleFacts))
		for n := range rows.subtitleFacts {
			out[n] = rows.subtitleFacts[n]
		}
	case 7:
		out = make([]basicMediaRow, len(rows.sidecars))
		for n := range rows.sidecars {
			out[n] = rows.sidecars[n]
		}
	case 8:
		out = make([]basicMediaRow, len(rows.subtitles))
		for n := range rows.subtitles {
			out[n] = rows.subtitles[n]
		}
	case 9:
		out = make([]basicMediaRow, len(rows.evidence))
		for n := range rows.evidence {
			out[n] = rows.evidence[n]
		}
	}
	return out
}

func TestBasicRetirePrefixClosedFamilies(t *testing.T) {
	source := basicRetirePrefixTestSource()
	for _, tc := range []struct {
		rows     basicRowsRequest
		ordinals []int64
		expected int64
	}{
		{basicRowsRequest{family: 2, streams: []basicStreamRow{{index: 0, streamType: "audio", codec: "aac"}, {index: 1, streamType: "video", codec: "h264"}}}, []int64{1, 4}, 5},
		{basicRowsRequest{family: 3, breadth: []basicStreamBreadth{{index: 0, frameMode: "constant"}, {index: 1}}}, []int64{0, 2}, 3},
		{basicRowsRequest{family: 4, chapterFacts: []basicChapterFact{{revision: 37, status: "known", fingerprint: "chapters:37"}}}, []int64{0}, 1},
		{basicRowsRequest{family: 5, chapters: []basicChapter{{index: 0, title: "Opening", start: 0, end: 3}, {index: 1, title: "Closing", start: 4, end: 8}}}, []int64{8, 10}, 11},
		{basicRowsRequest{family: 6, subtitleFacts: []basicSubtitleFact{{revision: 41, originUS: 12, timingKnown: true, status: "known", fingerprint: "subtitles:41"}}}, []int64{0}, 1},
		{basicRowsRequest{family: 7, sidecars: []basicSubtitleSidecar{{id: "a", path: "/media/a.srt", digest: "digest-a", size: 30, modifiedNS: -7}, {id: "b", path: "/media/b.srt", digest: "digest-b", size: 31}}}, []int64{2, 5}, 6},
		{basicRowsRequest{family: 8, subtitles: []basicSubtitle{{id: "embedded-1", origin: "embedded", locator: "2", streamIndex: 2, format: "srt", language: "en", title: "English"}, {id: "sidecar-1", origin: "sidecar", locator: "/media/a.srt", sidecarID: "a", format: "srt", language: "en", title: "English", reason: "found", streamIndex: -1}}}, []int64{0, 3}, 4},
		{basicRowsRequest{family: 9, evidence: []basicSubtitleEvidence{{revision: 1, evidence: "inventory:1"}}}, []int64{0}, 1},
	} {
		bytes, err := basicRetirePrefixBytes(source, tc.rows, tc.ordinals, tc.expected)
		if err != nil || bytes == 0 {
			t.Fatal("complete family refused", tc.rows.family, bytes, err)
		}
		if allocations := testing.AllocsPerRun(5, func() {
			_, _ = basicRetirePrefixBytes(source, tc.rows, tc.ordinals, tc.expected)
		}); allocations != 0 {
			t.Fatal("byte preflight allocated", tc.rows.family, allocations)
		}

		frozen, frozenOrdinals, err := freezeBasicRetirePrefixRows(source, tc.rows, tc.ordinals, tc.expected)
		if err != nil || !reflect.DeepEqual(frozen, tc.rows) || !reflect.DeepEqual(frozenOrdinals, tc.ordinals) {
			t.Fatal("freeze changed captured values", tc.rows.family, frozen, frozenOrdinals, err)
		}
		if frozen.rawChapterCount != 0 || &frozenOrdinals[0] == &tc.ordinals[0] {
			t.Fatal("freeze retained inactivity/ordinal storage", tc.rows.family)
		}
		before, after := reflect.ValueOf(tc.rows), reflect.ValueOf(frozen)
		for field := 2; field < after.NumField(); field++ {
			selected := field == int(tc.rows.family)
			if selected == after.Field(field).IsNil() {
				t.Fatal("selected/unselected slice identity changed", tc.rows.family, field)
			}
			if !selected {
				continue
			}
			a, b := before.Field(field), after.Field(field)
			if a.Pointer() == b.Pointer() {
				t.Fatal("retained caller slice", tc.rows.family)
			}
			for field := 0; field < a.Index(0).NumField(); field++ {
				x, y := a.Index(0).Field(field), b.Index(0).Field(field)
				if x.Kind() == reflect.String && x.Len() > 0 && unsafe.StringData(x.String()) == unsafe.StringData(y.String()) {
					t.Fatal("retained caller text", tc.rows.family, field)
				}
			}
		}

		prepared, err := prepareBasicRetirePrefixFamily(source, tc.rows, tc.ordinals, tc.expected)
		if err != nil || prepared == nil || !prepared.valid || prepared.source != source || prepared.family != tc.rows.family || prepared.rawChapterCount != 0 || !reflect.DeepEqual(prepared.rows, basicRetirePrefixTestInterfaces(tc.rows)) {
			t.Fatal("reclaim family lost exact values", tc.rows.family, prepared, err)
		}
		if unsafe.StringData(prepared.source.assetToken) == unsafe.StringData(source.assetToken) {
			t.Fatal("reclaim family retained caller source", tc.rows.family)
		}
		for n, row := range prepared.rows {
			family, _, _, _, err := basicMediaValues(prepared.source, tc.ordinals[n], row)
			if err != nil || family != tc.rows.family {
				t.Fatal("reclaim family rejected native ordinal", tc.rows.family, tc.ordinals[n], family, err)
			}
		}
	}
}

func TestBasicRetirePrefixKeepsCapturedNativeOrdinals(t *testing.T) {
	source := basicRetirePrefixTestSource()
	rows := basicRowsRequest{family: 5, chapters: []basicChapter{{index: 0, title: "Opening", start: 0, end: 3}, {index: 1, title: "Closing", start: 4, end: 8}}}
	ordinals := []int64{8, 10}

	frozen, frozenOrdinals, err := freezeBasicRetirePrefixRows(source, rows, ordinals, 11)
	if err != nil || frozen.family != 5 || frozen.rawChapterCount != 0 || len(frozen.chapters) != 2 || !reflect.DeepEqual(frozenOrdinals, []int64{8, 10}) {
		t.Fatal("gapped native keys were reconstructed as offsets", frozen, frozenOrdinals, err)
	}
	prepared, err := prepareBasicRetirePrefixFamily(source, rows, ordinals, 11)
	if err != nil || prepared.family != 5 || prepared.rawChapterCount != 0 || len(prepared.rows) != 2 {
		t.Fatal("reclaim family invented raw chapter counts or offsets", prepared, err)
	}
	for n, want := range ordinals {
		family, _, args, _, err := basicMediaValues(prepared.source, want, prepared.rows[n])
		if err != nil || family != 5 || len(args) == 0 || !reflect.DeepEqual(args[0], want) {
			t.Fatal("actual captured ordinal lost", want, args, err)
		}
	}
	if &frozen.chapters[0] == &rows.chapters[0] || &frozenOrdinals[0] == &ordinals[0] {
		t.Fatal("retained caller storage")
	}

	// The retained bytes include the source text, each concrete base/text
	// charge, and the captured ordinal slice.
	plain := basicRetirePrefixTestSource()
	plain.fingerprint = ""
	got, err := basicRetirePrefixBytes(plain, rows, ordinals, 11)
	want := uint64(8 * len(ordinals))
	for _, text := range [...]string{plain.assetToken, plain.runID, plain.sourceID, plain.incarnation, plain.objectID, plain.objectRevision, plain.fingerprint} {
		want += uint64(len(text))
	}
	want += 2 * (184 + uint64(len(plain.assetToken)))
	want += uint64(len("Opening") + len("Closing"))
	if err != nil || got != want {
		t.Fatal("incomplete captured-prefix contribution", got, want, err)
	}

	rows.chapters[0].title = "mutated"
	rows.chapters[1].index = 99
	ordinals[0], ordinals[1] = 0, 1
	source.assetToken = "mutated"
	if frozen.chapters[0].title != "Opening" || frozen.chapters[1].index != 1 || frozen.rawChapterCount != 0 {
		t.Fatal("caller row mutation reached frozen rows", frozen)
	}
	if !reflect.DeepEqual(frozenOrdinals, []int64{8, 10}) {
		t.Fatal("caller ordinal mutation reached frozen keys", frozenOrdinals)
	}
	if prepared.source.assetToken != "asset" || prepared.rawChapterCount != 0 {
		t.Fatal("caller source mutation reached reclaim family", prepared.source)
	}
	if row := prepared.rows[0].(basicChapter); row.title != "Opening" {
		t.Fatal("caller row mutation reached reclaim family", row)
	}
}

func TestBasicRetirePrefixRejectsInventedScope(t *testing.T) {
	source := basicRetirePrefixTestSource()
	two := basicRetirePrefixTestChapters(2)
	for _, tc := range []struct {
		name     string
		rows     basicRowsRequest
		ordinals []int64
		expected int64
		cause    error
	}{
		{"fake raw chapter count", basicRowsRequest{family: 5, rawChapterCount: 2, chapters: two}, []int64{8, 10}, 11, ErrInvalid},
		{"negative raw chapter count", basicRowsRequest{family: 5, rawChapterCount: -1, chapters: two}, []int64{8, 10}, 11, ErrInvalid},
		{"extra empty family", basicRowsRequest{family: 5, chapters: two, streams: []basicStreamRow{}}, []int64{8, 10}, 11, ErrInvalid},
		{"extra populated family", basicRowsRequest{family: 5, chapters: two, streams: []basicStreamRow{{index: 0, streamType: "audio"}}}, []int64{8, 10}, 11, ErrInvalid},
		{"nine rows", basicRowsRequest{family: 5, chapters: basicRetirePrefixTestChapters(9)}, []int64{0, 1, 2, 3, 4, 5, 6, 7, 8}, 9, ErrCap},
		{"unselected nil", basicRowsRequest{family: 5}, nil, 11, ErrInvalid},
		{"empty selected family", basicRowsRequest{family: 5, chapters: []basicChapter{}}, nil, 11, ErrInvalid},
		{"nil ordinals", basicRowsRequest{family: 5, chapters: two}, nil, 11, ErrInvalid},
		{"short ordinals", basicRowsRequest{family: 5, chapters: two}, []int64{8}, 11, ErrInvalid},
		{"long ordinals", basicRowsRequest{family: 5, chapters: two}, []int64{8, 10, 12}, 11, ErrInvalid},
		{"unsorted ordinals", basicRowsRequest{family: 5, chapters: two}, []int64{10, 8}, 11, ErrInvalid},
		{"duplicate ordinals", basicRowsRequest{family: 5, chapters: two}, []int64{8, 8}, 11, ErrInvalid},
		{"negative ordinal", basicRowsRequest{family: 5, chapters: two}, []int64{-1, 10}, 11, ErrInvalid},
		{"ordinal at bound", basicRowsRequest{family: 5, chapters: two}, []int64{8, 11}, 11, ErrInvalid},
		{"zero expected rows", basicRowsRequest{family: 5, chapters: two}, []int64{8, 10}, 0, ErrInvalid},
		{"negative expected rows", basicRowsRequest{family: 5, chapters: two}, []int64{8, 10}, -1, ErrInvalid},
		{"family zero", basicRowsRequest{}, nil, 1, ErrInvalid},
		{"family one", basicRowsRequest{family: 1}, nil, 1, ErrInvalid},
		{"family ten", basicRowsRequest{family: 10}, nil, 1, ErrInvalid},
	} {
		if _, err := basicRetirePrefixBytes(source, tc.rows, tc.ordinals, tc.expected); !errors.Is(err, tc.cause) {
			t.Fatal("byte preflight accepted invented scope", tc.name, err, tc.cause)
		}
		frozen, frozenOrdinals, err := freezeBasicRetirePrefixRows(source, tc.rows, tc.ordinals, tc.expected)
		if !errors.Is(err, tc.cause) || !reflect.DeepEqual(frozen, basicRowsRequest{}) || frozenOrdinals != nil {
			t.Fatal("freeze accepted invented scope", tc.name, frozen, frozenOrdinals, err, tc.cause)
		}
		prepared, err := prepareBasicRetirePrefixFamily(source, tc.rows, tc.ordinals, tc.expected)
		if !errors.Is(err, tc.cause) || prepared != nil {
			t.Fatal("reclaim family accepted invented scope", tc.name, prepared, err, tc.cause)
		}
	}
	// Singleton families still require the single captured key.
	for _, tc := range []struct {
		family int64
		rows   basicRowsRequest
	}{
		{4, basicRowsRequest{family: 4, chapterFacts: []basicChapterFact{{revision: 1, status: "known", fingerprint: "a"}, {revision: 2, status: "known", fingerprint: "b"}}}},
		{6, basicRowsRequest{family: 6, subtitleFacts: []basicSubtitleFact{{revision: 1, status: "known", fingerprint: "a"}, {revision: 2, status: "known", fingerprint: "b"}}}},
		{9, basicRowsRequest{family: 9, evidence: []basicSubtitleEvidence{{revision: 1, evidence: "a"}, {revision: 2, evidence: "b"}}}},
	} {
		ordinals := []int64{0, 1}
		if _, err := basicRetirePrefixBytes(source, tc.rows, ordinals, 2); err != nil {
			t.Fatal("singleton preflight should only admit bytes", tc.family, err)
		}
		if _, _, err := freezeBasicRetirePrefixRows(source, tc.rows, ordinals, 2); !errors.Is(err, ErrInvalid) {
			t.Fatal("freeze accepted two singleton rows", tc.family, err)
		}
		if _, err := prepareBasicRetirePrefixFamily(source, tc.rows, ordinals, 2); !errors.Is(err, ErrInvalid) {
			t.Fatal("reclaim family accepted two singleton rows", tc.family, err)
		}
	}
}

func TestBasicRetirePrefixWholePreflightBeforeClones(t *testing.T) {
	source := basicRetirePrefixTestSource()
	rows := basicRowsRequest{family: 7, sidecars: []basicSubtitleSidecar{
		{id: "a", path: string([]byte{'/', 0xff}), digest: "a"},
		{id: "b", path: "/b.srt", digest: ""},
	}}
	ordinals := []int64{2, 5}
	base, err := basicRetirePrefixBytes(source, rows, ordinals, 6)
	if err != nil {
		t.Fatal(err)
	}
	backing := strings.Repeat("x", maxCanonicalRequestBytes+1)
	rows.sidecars[1].digest = backing[:uint64(maxCanonicalRequestBytes)-base+1]
	for _, check := range []struct {
		name string
		run  func() error
	}{
		{"bytes", func() error { _, err := basicRetirePrefixBytes(source, rows, ordinals, 6); return err }},
		{"freeze", func() error { _, _, err := freezeBasicRetirePrefixRows(source, rows, ordinals, 6); return err }},
		{"prepare", func() error { _, err := prepareBasicRetirePrefixFamily(source, rows, ordinals, 6); return err }},
	} {
		if err := check.run(); !errors.Is(err, ErrCap) || errors.Is(err, ErrFilenameUnrepresentable) {
			t.Fatal("late whole-prefix cap lost precedence", check.name, err)
		}
		if allocations := testing.AllocsPerRun(5, func() { _ = check.run() }); allocations != 0 {
			t.Fatal("allocated before complete byte admission", check.name, allocations)
		}
	}

	// Exact complete cap succeeds; one more byte fails before any clone.
	source = basicRetirePrefixTestSource()
	source.fingerprint = ""
	chapters := basicRowsRequest{family: 5, chapters: []basicChapter{{index: 0, title: "Opening", start: 0, end: 3}, {index: 1, title: "Closing", start: 4, end: 8}}}
	n, err := basicRetirePrefixBytes(source, chapters, []int64{8, 10}, 11)
	if err != nil {
		t.Fatal(err)
	}
	source.fingerprint = strings.Repeat("y", int(uint64(maxCanonicalRequestBytes)-n))
	if n, err := basicRetirePrefixBytes(source, chapters, []int64{8, 10}, 11); err != nil || n != maxCanonicalRequestBytes {
		t.Fatal("exact complete prefix cap refused", n, err)
	}
	if _, _, err := freezeBasicRetirePrefixRows(source, chapters, []int64{8, 10}, 11); err != nil {
		t.Fatal("freeze refused exact cap", err)
	}
	if _, err := prepareBasicRetirePrefixFamily(source, chapters, []int64{8, 10}, 11); err != nil {
		t.Fatal("prepare refused exact cap", err)
	}
	source.fingerprint += "y"
	for _, check := range []func() error{
		func() error { _, err := basicRetirePrefixBytes(source, chapters, []int64{8, 10}, 11); return err },
		func() error {
			_, _, err := freezeBasicRetirePrefixRows(source, chapters, []int64{8, 10}, 11)
			return err
		},
		func() error {
			_, err := prepareBasicRetirePrefixFamily(source, chapters, []int64{8, 10}, 11)
			return err
		},
	} {
		if err := check(); !errors.Is(err, ErrCap) {
			t.Fatal("oversize prefix admitted", err)
		}
	}
}
