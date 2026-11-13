package hosted

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestLinkedRootIsTheRootUsedByTheClient(t *testing.T) {
	original := OfficialRoot
	t.Cleanup(func() { OfficialRoot = original })
	key := bytes.Repeat([]byte{42}, 32)
	OfficialRoot = base64.RawURLEncoding.EncodeToString(key)
	if !bytes.Equal(Root(), key) || !bytes.Equal(New(Options{}).o.Root, key) {
		t.Fatal("linked root was not used")
	}
	for _, value := range []string{"", "broken", base64.RawURLEncoding.EncodeToString([]byte{42}), "Uj7jC87DWZ0XbmIlQudnqU2O3N148wQ9eX4YgmU_Ppo"} {
		OfficialRoot = value
		if _, err := linkedRoot(); err == nil {
			t.Fatalf("accepted invalid or development root %q", value)
		}
	}
}
