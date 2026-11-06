package scan

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

func preparedBasicShape(in BasicInput) BasicShapeInput {
	return BasicShapeInput{LibraryID: in.File.Source.LibraryID, OrdinaryOwners: 1,
		VersionAssetIDs: []int64{11}, AuthoredParentIDs: []int64{13},
		Streams: []ObservedStream{{Index: 0, Type: "video"}}, RawStreamCount: 1, RawChapterCount: 2}
}

func TestPrepareBasicOwnsCompleteVectorAndShape(t *testing.T) {
	in := basicInput()
	// Small views of a large input must not retain its entire backing payload.
	large := strings.Repeat("x", 1<<20)
	in.NFO.Revision = large[:8]
	raw := preparedBasicShape(in)
	p, err := PrepareBasic(in, raw, 4096)
	if err != nil || p.Input() != in || p.Bytes() <= 0 {
		t.Fatal(p, err)
	}
	if unsafe.StringData(p.Input().NFO.Revision) == unsafe.StringData(in.NFO.Revision) {
		t.Fatal("prepared input retained the raw backing payload")
	}
	raw.VersionAssetIDs[0], raw.Streams[0].Index = 99, 7
	shape := p.Shape()
	if shape.VersionAssetIDs[0] != 11 || shape.Streams[0].Index != 0 || shape.RawChapterCount != 2 {
		t.Fatal("raw slices mutated prepared shape", shape)
	}
	shape.AuthoredParentIDs[0], shape.Streams[0].Index = 99, 8
	if p.Shape().AuthoredParentIDs[0] != 13 || p.Shape().Streams[0].Index != 0 {
		t.Fatal("consumer mutated prepared shape")
	}
	if got, err := PrepareBasic(in, preparedBasicShape(in), p.Bytes()-1); !errors.Is(err, ErrCapacity) || !reflect.DeepEqual(got, PreparedBasic{}) {
		t.Fatal("partial over-budget input", got, err)
	}
}

func TestPrepareBasicKnownAbsenceAndIncompleteInput(t *testing.T) {
	in := basicInput()
	in.NFO, in.Artwork, in.Subtitles = LocalWitness{Observed: true}, LocalWitness{Observed: true}, LocalWitness{Observed: true}
	if _, err := PrepareBasic(in, preparedBasicShape(in), 4096); err != nil {
		t.Fatal("known absence refused", err)
	}
	for _, edit := range []func(*BasicInput){
		func(v *BasicInput) { v.NFO.Observed = false },
		func(v *BasicInput) { v.Artwork.Observed = false },
		func(v *BasicInput) { v.Subtitles.Observed = false },
		func(v *BasicInput) { v.Available = false },
		func(v *BasicInput) { v.ProbeRecipe = "" },
	} {
		bad := in
		edit(&bad)
		if p, err := PrepareBasic(bad, preparedBasicShape(bad), 4096); !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(p, PreparedBasic{}) {
			t.Fatal("incomplete input prepared", p, err)
		}
	}
}

func TestPrepareBasicBudgetAndRawCapsBeforeCleanup(t *testing.T) {
	in := basicInput()
	raw := preparedBasicShape(in)
	in.Subtitles.Revision = strings.Repeat("x", 1<<20)
	if _, err := PrepareBasic(in, raw, 1); !errors.Is(err, ErrCapacity) {
		t.Fatal("late payload escaped preflight", err)
	}
	in = basicInput()
	raw.RawChapterCount = writer.MaxRawChapters + 1
	if p, err := PrepareBasic(in, raw, 4096); !errors.Is(err, writer.ErrCap) || !reflect.DeepEqual(p, PreparedBasic{}) {
		t.Fatal("raw chapters cleaned before cap", p, err)
	}
	raw = preparedBasicShape(in)
	raw.LibraryID++
	if _, err := PrepareBasic(in, raw, 4096); !errors.Is(err, ErrInvalid) {
		t.Fatal("foreign library prepared", err)
	}
	if _, err := PrepareBasic(in, preparedBasicShape(in), 0); !errors.Is(err, ErrCapacity) {
		t.Fatal("zero payload budget", err)
	}
}

func TestPrepareBasicNativeFilenameCause(t *testing.T) {
	for _, c := range []struct {
		name, path string
		cause      error
	}{
		{"invalid_utf8", "a\xff.mkv", writer.ErrFilenameUnrepresentable},
		{"nul", "a\x00.mkv", ErrInvalid},
		{"mixed_nul_bad_utf8", "a\xff\x00.mkv", ErrInvalid},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := basicInput()
			in.File.RelativePath = c.path
			before := in
			got, err := PrepareBasic(in, preparedBasicShape(in), 4096)
			if !errors.Is(err, c.cause) || !reflect.DeepEqual(got, PreparedBasic{}) || in != before {
				t.Fatal(got, err, in)
			}
			if c.cause == ErrInvalid && errors.Is(err, writer.ErrFilenameUnrepresentable) {
				t.Fatal("invalid native input classified as poison", err)
			}
		})
	}
	in := basicInput()
	in.File.RelativePath = "a\xff.mkv"
	in.NFO.Revision = strings.Repeat("x", 4096)
	if got, err := PrepareBasic(in, preparedBasicShape(in), 1); !errors.Is(err, ErrCapacity) || !reflect.DeepEqual(got, PreparedBasic{}) {
		t.Fatal("filename classification bypassed complete capacity preflight", got, err)
	}
	in = basicInput()
	in.NFO.Revision = "\xff"
	if got, err := PrepareBasic(in, preparedBasicShape(in), 4096); !errors.Is(err, ErrInvalid) || errors.Is(err, writer.ErrFilenameUnrepresentable) || !reflect.DeepEqual(got, PreparedBasic{}) {
		t.Fatal("opaque local witness became filename poison", got, err)
	}
}
