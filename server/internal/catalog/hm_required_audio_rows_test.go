package catalog

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// collections.html frames 23/24a/25b draw Continue Watching, Continue Listening
// and Continue your books with the lock and "Always on Home", and the frame 23
// caption says rows the server requires show that lock instead of a switch. The
// first two are the two audio rows; only Continue Watching is required today.
func TestHomeCustomizeDrawsAudioRowsAlwaysOn(t *testing.T) {
	db := homeFixture(t)
	service := New(db)
	request := homeRequestFixture()
	view, err := service.HomeLayoutRows(request)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"continue": true, "listen_next": true, "continue_books": true}
	for _, row := range view.Rows {
		required, checked := want[row.ID]
		if !checked {
			continue
		}
		delete(want, row.ID)
		if row.Required != required {
			t.Errorf("%s required=%v, want %v (Always on Home)", row.ID, row.Required, required)
		}
	}
	if len(want) != 0 {
		t.Fatalf("rows missing from Customize Home: %v", want)
	}
	// A required row may not be hidden, and the refusal names it.
	for _, row := range []string{"listen_next", "continue_books"} {
		if err := service.ValidateHomeLayout(request, nil, []string{row}); !errors.Is(err, ErrHomeLayoutRow) {
			t.Errorf("required row %s could be hidden: %v", row, err)
		}
	}
	_ = dbwork.Snapshot
}
