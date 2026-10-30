package downloads

import (
	"context"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestNextEpisodeDateTargetOrdersByAirDateAndEntity(t *testing.T) {
	h := newHarness(t)
	_, show := addCompactFixtureEntity(t, h.db, "lib", compactcatalog.Show, "dated-show", "Daily Show", 0, "", 0, map[string]any{"local_key": "dated-show"})
	episodes := map[string]string{}
	// Insert the latest date first, so entity order alone is wrong; use two
	// entries on the first date to exercise the entity-id tie breaker.
	for i, spec := range []struct {
		name string
		date any
	}{
		{"last", 20002}, {"first", 20000}, {"same-date", 20000}, {"next-date", 20001}, {"unknown-date", nil},
	} {
		public, entity := addCompactFixtureEntity(t, h.db, "lib", compactcatalog.Episode, spec.name, spec.name, 0, "", show, map[string]any{"show_id": show, "numbering": "date", "number": i + 1, "air_date": spec.date})
		episodes[spec.name] = public
		addCompactFixtureLink(t, h.db, entity, h.assetID)
	}
	_, otherShow := addCompactFixtureEntity(t, h.db, "lib", compactcatalog.Show, "other-show", "Other Show", 0, "", 0, map[string]any{"local_key": "other-show"})
	addCompactFixtureEntity(t, h.db, "lib", compactcatalog.Episode, "other-episode", "Other episode", 0, "", otherShow, map[string]any{"show_id": otherShow, "numbering": "date", "number": 1, "air_date": 20000})
	addCompactFixtureEntity(t, h.db, "lib", compactcatalog.Episode, "absolute-episode", "Absolute episode", 0, "", show, map[string]any{"show_id": show, "numbering": "absolute", "number": 10, "air_date": 20000})
	settleDownloadsProjection(t, h.db)
	for i, pair := range [][2]string{{"first", "same-date"}, {"same-date", "next-date"}, {"next-date", "last"}} {
		next, err := h.service.Submit(context.Background(), h.viewer, Request{OperationID: fmt.Sprintf("date-next-%d", i), NextAfterMediaID: episodes[pair[0]], Quality: QualityOriginal}, nil)
		if err != nil || next.Accepted != 1 || len(next.Items) != 1 {
			t.Fatalf("after %s: %+v %v", pair[0], next, err)
		}
		if next.Items[0].ItemID != episodes[pair[1]] || next.Items[0].Origin != OriginNext {
			t.Fatalf("after %s: %+v, want %s", pair[0], next.Items[0], pair[1])
		}
	}
	for _, last := range []string{"last", "unknown-date"} {
		_, err := h.service.Submit(context.Background(), h.viewer, Request{OperationID: "date-end-" + last, NextAfterMediaID: episodes[last], Quality: QualityOriginal}, nil)
		if err != ErrNotFound {
			t.Fatalf("after %s: %v, want ErrNotFound", last, err)
		}
	}
}
