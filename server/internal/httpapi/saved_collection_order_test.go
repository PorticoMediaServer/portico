package httpapi

import (
	"reflect"
	"testing"
)

// A personal collection lists, and plays, in its own order; PUT …/order moves
// entries, fenced by the collection's revision.
func TestSavedCollectionOrderRoute(t *testing.T) {
	f := newV1Fixture(t, 3)
	type receipt struct {
		Receipt struct {
			ResourceID string `json:"resourceId"`
			Revision   int64  `json:"revision"`
		} `json:"receipt"`
	}
	var created receipt
	f.call("POST", "/v1/saved-resources", nil, map[string]any{"operationId": "order-create", "expectedRevision": 0, "kind": "collection", "name": "Picks"}, 200, &created)
	id := created.Receipt.ResourceID
	var added receipt
	f.call("PUT", "/v1/saved-resources/"+id+"/entries", nil, map[string]any{"operationId": "order-add", "expectedRevision": created.Receipt.Revision, "addItemIds": []string{f.items[2], f.items[0], f.items[1]}}, 200, &added)
	read := func() (entries []string, items []string) {
		var page struct {
			Entries []struct {
				ID    string `json:"id"`
				Media *struct {
					ID string `json:"id"`
				} `json:"media"`
			} `json:"entries"`
		}
		f.call("GET", "/v1/saved-resources/"+id+"/content", nil, nil, 200, &page)
		for _, e := range page.Entries {
			entries = append(entries, e.ID)
			if e.Media != nil {
				items = append(items, e.Media.ID)
			}
		}
		return entries, items
	}
	entries, items := read()
	if !reflect.DeepEqual(items, []string{f.items[2], f.items[0], f.items[1]}) {
		t.Fatalf("listed in the order added: %v", items)
	}
	var moved receipt
	f.call("PUT", "/v1/saved-resources/"+id+"/order", nil, map[string]any{"operationId": "order-move", "expectedRevision": added.Receipt.Revision, "entryIds": []string{entries[2]}, "afterEntryId": ""}, 200, &moved)
	if _, items = read(); !reflect.DeepEqual(items, []string{f.items[1], f.items[2], f.items[0]}) {
		t.Fatalf("after the move: %v", items)
	}
	// The collection changed since: the old revision is refused, with nothing moved.
	w := f.raw("PUT", "/v1/saved-resources/"+id+"/order", f.owner.AccessToken, nil, map[string]any{"operationId": "order-stale", "expectedRevision": added.Receipt.Revision, "entryIds": []string{entries[0]}, "afterEntryId": ""})
	if w.Code != 412 || v1Code(w) != "revision_mismatch" {
		t.Fatalf("stale move: %d %s", w.Code, w.Body.String())
	}
	if w = f.raw("PUT", "/v1/saved-resources/"+id+"/order", f.owner.AccessToken, nil, map[string]any{"operationId": "order-bad", "expectedRevision": moved.Receipt.Revision, "entryIds": []string{}}); w.Code != 400 {
		t.Fatalf("an empty move: %d %s", w.Code, w.Body.String())
	}
	if _, items = read(); !reflect.DeepEqual(items, []string{f.items[1], f.items[2], f.items[0]}) {
		t.Fatalf("a refused move changed the order: %v", items)
	}
}
