package scan

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

// Count the public evidence value independently of production's manual charge
// list. This catches omission of a whole family, including future string fields.
func iaEvidenceStringBytes(v reflect.Value) int64 {
	var total int64
	switch v.Kind() {
	case reflect.String:
		return int64(v.Len())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			total += iaEvidenceStringBytes(v.Field(i))
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			total += iaEvidenceStringBytes(v.Index(i))
		}
	}
	return total
}

func TestIAMediaEvidenceIndependentBudgetControls(t *testing.T) {
	file := iaEvidenceFile("Show/Season 2/Show.S02E01-E03.mkv")
	file.SourceKey = strings.Repeat("k", 4096)
	if e, err := PrepareMediaEvidence("tv", file, 512); !errors.Is(err, ErrCapacity) || e.Bytes() != 0 {
		t.Fatalf("4096-byte raw key escaped 512-byte budget: %+v %v", e, err)
	}
	file.SourceKey = "existing-source-key"
	raw := int64(len("tv")) + iaEvidenceStringBytes(reflect.ValueOf(file))
	if e, err := PrepareMediaEvidence("tv", file, raw); !errors.Is(err, ErrCapacity) || e.Bytes() != 0 {
		t.Fatalf("derived names escaped raw-only budget %d: %+v %v", raw, e, err)
	}
	full := raw + iaEvidenceStringBytes(reflect.ValueOf(naming.Read(file.Witness.RelativePath, "tv")))
	e, err := PrepareMediaEvidence("tv", file, full)
	if err != nil || e.Bytes() != full {
		t.Fatalf("independent byte total %d != %d: %v", full, e.Bytes(), err)
	}
}

func TestIAMediaEvidenceAudiobookFolder(t *testing.T) {
	file := iaEvidenceFile("Terry Pratchett/Discworld/Book 8 - Guards! Guards!/01.mp3")
	e, err := PrepareMediaEvidence("audiobook", file, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	book, ok := e.AudiobookFolder()
	if !ok || book.Title != "Guards! Guards!" || book.Author != "Terry Pratchett" || book.Series != "Discworld" || book.Sequence != "8" {
		t.Fatalf("structured book grouping lost: %+v %v", book, ok)
	}
	// The generic parser's album/artist alternatives remain present separately.
	if r := e.Naming(); len(r.Albums) == 0 || r.Albums[0].Value != "Book 8 - Guards! Guards!" {
		t.Fatal("generic naming alternatives were overwritten")
	}
	file = iaEvidenceFile("Author/Series/Book 2 - 1999 - Title {Jim Dale & Stephen Fry} [B0015T963C]/Disc 2/01.mp3")
	e, err = PrepareMediaEvidence("audiobook", file, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	book, ok = e.AudiobookFolder()
	if !ok || book.Title != "Title" || book.Author != "Author" || book.Series != "Series" || book.Sequence != "2" || book.Year != "1999" || book.ASIN != "B0015T963C" || !reflect.DeepEqual(book.Narrators, []string{"Jim Dale", "Stephen Fry"}) {
		t.Fatalf("folder alternatives lost: %+v", book)
	}
	book.Narrators[0] = "changed"
	again, _ := e.AudiobookFolder()
	if again.Narrators[0] != "Jim Dale" {
		t.Fatal("narrator slice escaped")
	}
	// A budget covering raw input and all generic naming still cannot omit the
	// separate folder family. This expected bound never consults e.Bytes().
	raw := int64(len("audiobook")) + iaEvidenceStringBytes(reflect.ValueOf(file))
	withoutFolder := raw + iaEvidenceStringBytes(reflect.ValueOf(naming.Read(file.Witness.RelativePath, "audiobook")))
	if result, err := PrepareMediaEvidence("audiobook", file, withoutFolder); !errors.Is(err, ErrCapacity) || result.Bytes() != 0 {
		t.Fatalf("book fields escaped budget: %+v %v", result, err)
	}
	full := withoutFolder + iaEvidenceStringBytes(reflect.ValueOf(naming.ReadAudiobookFolder(file.Witness.RelativePath)))
	if result, err := PrepareMediaEvidence("audiobook", file, full); err != nil || result.Bytes() != full {
		t.Fatalf("independent audiobook byte total %d != %d: %v", full, result.Bytes(), err)
	}
	for _, kind := range []string{"audiobook", "music"} {
		e, err := PrepareMediaEvidence(kind, iaEvidenceFile("01.mp3"), 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if book, ok := e.AudiobookFolder(); ok || book.Title != "" || len(book.Narrators) != 0 {
			t.Fatalf("absent folder became metadata: %+v", book)
		}
	}
}
