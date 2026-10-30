package playbackv1

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

func TestBatchQueueContract(t *testing.T) {
	for _, placement := range []string{"next", "end"} {
		for name, source := range map[string]Selector{
			"mixed playable ids": {Items: &ItemList{IDs: []string{"movie-1", "episode-1", "song-1", "audiobook-1"}}},
			"whole query":        {Query: &catalog.QueueQuery{LibraryID: "library-1", Pivot: "movie"}},
			"container":          {Container: &ContainerRef{Kind: "playlist", ID: "playlist-1"}},
			"saved view":         {Saved: &SavedRef{ID: "saved-1"}},
		} {
			t.Run(placement+"/"+name, func(t *testing.T) {
				request := BatchQueueRequest{Placement: placement, Source: source}
				if err := request.Validate(); err != nil {
					t.Fatal(err)
				}
				raw, err := json.Marshal(request)
				if err != nil {
					t.Fatal(err)
				}
				var decoded BatchQueueRequest
				if err = json.Unmarshal(raw, &decoded); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(request, decoded) {
					t.Fatalf("round trip: %s", raw)
				}
			})
		}
	}
	for name, request := range map[string]BatchQueueRequest{
		"no placement":            {Source: Selector{Items: &ItemList{IDs: []string{"movie-1"}}}},
		"arbitrary insertion":     {Placement: "after", Source: Selector{Items: &ItemList{IDs: []string{"movie-1"}}}},
		"no source":               {Placement: "next"},
		"empty list":              {Placement: "end", Source: Selector{Items: &ItemList{}}},
		"invalid id":              {Placement: "next", Source: Selector{Items: &ItemList{IDs: []string{"../private"}}}},
		"multiple sources":        {Placement: "next", Source: Selector{Items: &ItemList{IDs: []string{"song-1"}}, Saved: &SavedRef{ID: "saved-1"}}},
		"oversized explicit list": {Placement: "end", Source: Selector{Items: &ItemList{IDs: make([]string, 501)}}},
	} {
		t.Run(name, func(t *testing.T) {
			var field *FieldError
			if err := request.Validate(); !errors.As(err, &field) {
				t.Fatalf("wanted field error, got %v", err)
			}
		})
	}
}
