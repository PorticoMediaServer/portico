package reading

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestLocationFormatBoundaries(t *testing.T) {
	zero := int64(0)
	negative := int64(-1)
	for _, tc := range []struct {
		location Location
		valid    bool
	}{{Location{Format: "epub", CFI: "epubcfi(/6/2!/4/2:0)", Progression: .2}, true}, {Location{Format: "pdf", PageIndex: &zero, Progression: 0}, true}, {Location{Format: "cbz", PageIndex: &zero, Progression: 1}, true}, {Location{Format: "pdf", CFI: "epubcfi(/6/2)", PageIndex: &zero}, false}, {Location{Format: "epub", CFI: "epubcfi(/6/2)", PageIndex: &zero}, false}, {Location{Format: "cbz", PageIndex: &negative}, false}, {Location{Format: "pdf", PageIndex: &zero, Progression: math.NaN()}, false}, {Location{Format: "pdf", PageIndex: &zero, Progression: math.Inf(1)}, false}, {Location{Format: "epub", CFI: "fake"}, false}, {Location{Format: "epub", CFI: "epubcfi(/6/2)", Section: "../other"}, false}} {
		if err := tc.location.Validate(); (err == nil) != tc.valid {
			t.Fatalf("%+v => %v", tc.location, err)
		}
	}
}

// Format discrimination only. Actual page bounds and RAR5 admission still
// require the original source/session and the bounded archive reader.
func TestCBRFixedPageLocation(t *testing.T) {
	zero, negative := int64(0), int64(-1)
	if err := (Location{Format: "cbr", PageIndex: &zero}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, location := range []Location{
		{Format: "cbr"}, {Format: "cbr", PageIndex: &negative},
		{Format: "cbr", PageIndex: &zero, CFI: "epubcfi(/6/2)"},
		{Format: "cbr", PageIndex: &zero, Section: "page.jpg"},
	} {
		if !errors.Is(location.Validate(), ErrInvalid) {
			t.Fatalf("invalid CBR location admitted: %+v", location)
		}
	}
}

func TestLocalComicRemoteRefusalPrecedesControllerAndSession(t *testing.T) {
	// Deliberately no DB/controller: neither is a prerequisite for refusing a
	// local-only format, and no provisional remote session may be allocated.
	service := &Service{}
	for _, format := range []string{"cb7", "cbr"} {
		if _, err := service.openRemote(context.Background(), identity.Principal{}, Selection{Format: format}, nil); !errors.Is(err, ErrArchiveFormat) {
			t.Fatalf("%s remote admission reached another path: %v", format, err)
		}
	}
}
