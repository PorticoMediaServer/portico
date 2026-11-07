package writer

import (
	"errors"
	"strings"
	"testing"
)

func basicCopyPrepareSource() basicStreamFactStage {
	return basicStreamFactStage{changeID: 51, inputRevision: 7, libraryID: 7, assetID: 41, expectedEpoch: 17, assetToken: "asset", runID: "run", sourceID: "source", incarnation: "incarnation", objectID: "object", objectRevision: "opaque", fingerprint: "probe", size: 42}
}
func TestBasicCopyPrepareOwnsOnlySelectedNativeOrdinal(t *testing.T) {
	source := basicCopyPrepareSource()
	rows := []basicMediaRow{basicSubtitle{id: "native-id", origin: "embedded", locator: "2", streamIndex: 2, format: "srt", language: "en", title: "original"}}
	got, err := prepareBasicCopyFamily(source, 8, 4096, 4095, 0, rows)
	if err != nil || !got.valid || len(got.rows) != 4096 || got.rows[0] != nil || got.rows[4094] != nil {
		t.Fatal(got, err)
	}
	rows[0] = basicSubtitle{id: "changed"}
	if r := got.rows[4095].(basicSubtitle); r.id != "native-id" || r.title != "original" {
		t.Fatal(r)
	}
	if _, _, _, _, err := basicMediaValues(got.source, 0, got.rows[0]); !errors.Is(err, ErrInvalid) {
		t.Fatal("unselected payload was accepted", err)
	}
}
func TestBasicCopyPrepareWholeRawCapBeforeAllocation(t *testing.T) {
	source := basicCopyPrepareSource()
	rows := []basicMediaRow{basicStreamRow{index: 0, streamType: "video", detail: strings.Repeat("x", maxCanonicalRequestBytes+1)}}
	var got *basicMediaFamily
	var err error
	allocs := testing.AllocsPerRun(5, func() { got, err = prepareBasicCopyFamily(source, 2, 1, 0, 0, rows) })
	if got != nil || !errors.Is(err, ErrCap) || allocs != 0 {
		t.Fatal(got, err, allocs)
	}
	if got, err = prepareBasicCopyFamily(source, 8, 4097, 0, 0, nil); got != nil || !errors.Is(err, ErrCap) {
		t.Fatal(got, err)
	}
}
func TestBasicCopyPrepareRetainsRawChapterAndPathPolicy(t *testing.T) {
	source := basicCopyPrepareSource()
	if got, err := prepareBasicCopyFamily(source, 5, 1, 0, MaxRawChapters+1, nil); got != nil || !errors.Is(err, ErrCap) {
		t.Fatal(got, err)
	}
	row := []basicMediaRow{basicSubtitleSidecar{id: "s", path: string([]byte{'/', 0xff}), digest: "digest"}}
	if got, err := prepareBasicCopyFamily(source, 7, 1, 0, 0, row); got != nil || !errors.Is(err, ErrFilenameUnrepresentable) {
		t.Fatal(got, err)
	}
	if got, err := prepareBasicCopyFamily(source, 4, 0, 0, 0, nil); got != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal(got, err)
	}
}
