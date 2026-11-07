package writer

import (
	"context"
	"math"
	"reflect"
	"testing"
)

func TestMPReadingFormatSpecificLocations(t *testing.T) {
	p := mpReadingPublication{selection: mpReadingSelection{format: "epub"}, sections: []string{"text/one.xhtml"}, sourceLife: context.Background()}
	good := mpReadingLocationValue{format: "epub", cfi: "epubcfi(/6/2!/4/1:0)", section: "text/one.xhtml", progression: .3}
	if err := mpValidateReadingLocation(p, good); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*mpReadingLocationValue){"unselected_section": func(l *mpReadingLocationValue) { l.section = "text/other.xhtml" }, "page_on_epub": func(l *mpReadingLocationValue) { l.hasPage = true }, "bad_cfi": func(l *mpReadingLocationValue) { l.cfi = "<script>" }, "traversal": func(l *mpReadingLocationValue) { l.section = "../one.xhtml" }, "nan": func(l *mpReadingLocationValue) { l.progression = math.NaN() }} {
		t.Run(name, func(t *testing.T) {
			bad := good
			change(&bad)
			if mpValidateReadingLocation(p, bad) == nil {
				t.Fatal("invalid source-bound EPUB location admitted")
			}
		})
	}
	for _, format := range []string{"pdf", "cbz", "cb7", "cbr"} {
		p.selection.format = format
		p.pages = 5
		l := mpReadingLocationValue{format: format, page: 4, hasPage: true, progression: .8}
		if err := mpValidateReadingLocation(p, l); err != nil {
			t.Fatal(format, err)
		}
		l.page = 5
		if mpValidateReadingLocation(p, l) == nil {
			t.Fatal("page outside captured publication")
		}
		l.page = 0
		l.cfi = good.cfi
		if mpValidateReadingLocation(p, l) == nil {
			t.Fatal("CFI converted to page")
		}
	}
}
func TestMPReadingHasValuesOnlyPersonalBoundary(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeFor[mpReadingEffect](), reflect.TypeFor[mpReadingPersonalValues](), reflect.TypeFor[mpQueueSaveSlice]()} {
		for i := 0; i < typ.NumField(); i++ {
			k := typ.Field(i).Type.Kind()
			if k == reflect.Func || k == reflect.Interface || k == reflect.Pointer {
				t.Fatal("live capability escaped values boundary", typ, typ.Field(i).Name)
			}
		}
	}
	for _, d := range mpReadingDefinitions() {
		if d.Kind != "read" {
			t.Fatal("reading duplicated UO personal writer", d.ID)
		}
	}
}
