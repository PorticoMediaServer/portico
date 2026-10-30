package administration

import (
	"context"
	"errors"
	"testing"
)

func TestAdministrationRequiresReplayKey(t *testing.T) {
	s, db, root := newService(t)
	library, _, _ := seedLibrary(t, db, root)
	ctx := context.Background()
	doc, err := s.LibrarySettingsFor(ctx, allow, library)
	if err != nil {
		t.Fatal(err)
	}
	request := Change[LibrarySettings]{ExpectedRevision: doc.Revision, Settings: doc.Settings}
	if _, err = s.SaveLibrarySettings(ctx, allow, library, request); !errors.Is(err, ErrInput) {
		t.Fatalf("missing key: %v", err)
	}
	request.OperationID = "policy-replay-key"
	first, err := s.SaveLibrarySettings(ctx, allow, library, request)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.SaveLibrarySettings(ctx, allow, library, request)
	if err != nil || again.Revision != first.Revision {
		t.Fatalf("replay: %+v %v", again, err)
	}
	request.Settings.AllowMediaDeletion = !request.Settings.AllowMediaDeletion
	if _, err = s.SaveLibrarySettings(ctx, allow, library, request); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("reused key: %v", err)
	}
}
