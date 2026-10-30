package integrationids

import (
	"context"
	"encoding/hex"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"testing"
)

func TestCatalogueGUIDIsTheSame128BitsAndRoundTrips(t *testing.T) {
	raw := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	native := entityid.Encode(raw)
	s := Store{}
	guid, err := s.Encode(context.Background(), "item", native)
	if err != nil || guid != hex.EncodeToString(raw) {
		t.Fatal("catalogue encoding changed identity")
	}
	got, err := s.Decode(context.Background(), "item", guid)
	if err != nil || got != native {
		t.Fatal("catalogue roundtrip failed")
	}
	dashed := guid[:8] + "-" + guid[8:12] + "-" + guid[12:16] + "-" + guid[16:20] + "-" + guid[20:]
	got, err = s.Decode(context.Background(), "item", dashed)
	if err != nil || got != native {
		t.Fatal("canonical dashed GUID failed")
	}
	for _, bad := range []string{"", "1", "--" + guid, guid + "00", "00000000x0000x0000x0000x000000000000"} {
		if _, err = s.Decode(context.Background(), "item", bad); err == nil {
			t.Fatal("malformed GUID accepted")
		}
	}
}
