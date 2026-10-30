package metadata

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func TestP08BWorkerAutomaticSelectionAndContradictoryDetail(t *testing.T) {
	for _, contradiction := range []bool{false, true} {
		t.Run(map[bool]string{false: "agrees", true: "contradiction"}[contradiction], func(t *testing.T) {
			db, _ := mbDB(t)
			defer db.Close()
			mbCurrentExec(t, db, `UPDATE mb_jobs SET status='needs_selection' WHERE kind='album'`)
			svc := New(db, "")
			millis := 100000
			result := mbRecordingFixture()
			result.Title = "Local Song"
			result.LengthMillis = &millis
			svc.mb = mbFixture{searchRecord: func(context.Context, string, string) ([]metadataprovider.Recording, error) {
				return []metadataprovider.Recording{result}, nil
			}, record: func(_ context.Context, id string) (metadataprovider.RecordingLookup, error) {
				detail := result
				if contradiction {
					different := 150000
					detail.LengthMillis = &different
				}
				return metadataprovider.RecordingLookup{RequestedID: id, Recording: detail}, nil
			}}
			if e := svc.MusicBrainzStep(context.Background()); e != nil {
				t.Fatal(e)
			}
			state, e := svc.MusicBrainzState("song", mbPublicID(t, db, "Local Song"))
			if e != nil {
				t.Fatal(e)
			}
			var links int
			if e = db.QueryRow(`SELECT count(*) FROM mb_song_links WHERE item_id=(SELECT e.id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE cl.library_id='lib' AND e.kind=7)`).Scan(&links); e != nil {
				t.Fatal(e)
			}
			if contradiction {
				if links != 0 || state.Status != "needs_selection" {
					t.Fatal(state, links)
				}
			} else if links != 1 || state.Status != "matched" || state.Published == nil || state.Published.RecordingID != mbRecord {
				t.Fatal(state, links)
			}
		})
	}
}
func TestP08BPolicyCASAndOwnerAuthorizationFence(t *testing.T) {
	db, _ := mbDB(t)
	defer db.Close()
	svc := New(db, "")
	song := mbPublicID(t, db, "Local Song")
	state, e := svc.MusicBrainzState("song", song)
	if e != nil || state.Policy == nil {
		t.Fatal(state, e)
	}
	change := MusicPolicyChange{ExpectedRevision: state.Revision, ExpectedPolicyRevision: state.Policy.Revision, LocalMode: "prefer", MusicBrainzEnabled: true, AcoustIDEnabled: true}
	denied := errors.New("permission revoked")
	if e = svc.UpdateMusicPolicy("song", song, change, func(*sql.Tx) error { return denied }); !errors.Is(e, denied) {
		t.Fatal(e)
	}
	if e = svc.UpdateMusicPolicy("song", song, change, nil); e != nil {
		t.Fatal(e)
	}
	if e = svc.UpdateMusicPolicy("song", song, change, nil); !errors.Is(e, ErrMBConflict) {
		t.Fatal("stale policy replay accepted", e)
	}
	state, e = svc.MusicBrainzState("song", song)
	if e != nil || !state.Policy.AcoustIDEnabled || state.Policy.AcoustIDConfigured {
		t.Fatal(state, e)
	}
	// Missing credential/fingerprint does not erase recorded opt-in or disable MB.
	if !state.Policy.MusicBrainzEnabled {
		t.Fatal("AcoustID prerequisites disabled other matching")
	}
}
func TestP08BPolicyMutationFencesCandidateSelection(t *testing.T) {
	db, _ := mbDB(t)
	defer db.Close()
	mbCurrentExec(t, db, `UPDATE mb_jobs SET status='needs_selection' WHERE kind='album'`)
	svc := New(db, "")
	v := mbRecordingFixture()
	svc.mb = mbFixture{searchRecord: func(context.Context, string, string) ([]metadataprovider.Recording, error) {
		return []metadataprovider.Recording{v}, nil
	}}
	if e := svc.MusicBrainzStep(context.Background()); e != nil {
		t.Fatal(e)
	}
	song := mbPublicID(t, db, "Local Song")
	state, e := svc.MusicBrainzState("song", song)
	if e != nil || len(state.Candidates) != 1 {
		t.Fatal(state, e)
	}
	mbCurrentExec(t, db, `UPDATE audio_metadata_policies SET revision=revision+1,local_mode='off' WHERE library_id='lib'`)
	e = svc.SelectMusicBrainz("song", song, MBSelection{ExpectedRevision: state.Revision, ProviderID: mbRecord, Actor: MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}}, nil)
	if !errors.Is(e, ErrMBConflict) {
		t.Fatal("candidate from old library policy accepted", e)
	}
}
