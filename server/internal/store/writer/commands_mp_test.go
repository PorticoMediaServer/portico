package writer

import (
	"errors"
	"testing"
)

func TestMPPurposeMetadataDoesNotAdmitExecution(t *testing.T) {
	for _, d := range mpCommands.descriptors {
		got, err := Describe(d.ID)
		if err != nil || got != d || d.ID < 700 || d.ID > 799 || d.Durability != Normal {
			t.Fatalf("invalid media metadata: %+v %v", got, err)
		}
		// The MP family bodies/authority/receipts and measured resource recipes are
		// separate publication gates. Merely naming a purpose cannot acquire class.
		if d.ID == MPQueueRepeat {
			if !commandPlanPurpose(d.ID) {
				t.Fatal("missing closed first instance")
			}
			continue
		}
		if _, err = commandClass(d.ID); !errors.Is(err, ErrInvalid) {
			t.Fatalf("metadata admitted media purpose %d: %v", d.ID, err)
		}
	}
}
