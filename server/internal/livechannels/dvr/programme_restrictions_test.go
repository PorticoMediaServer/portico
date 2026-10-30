package dvr

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

func TestDVRHidesProgrammeFactsAfterRatingRestriction(t *testing.T) {
	for _, scope := range []string{"profile", "account"} {
		t.Run(scope, func(t *testing.T) {
			s, source, p, a := sharingStore(t)
			ctx := context.Background()
			owner := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
			if _, e := s.db.Exec(`UPDATE accounts SET username='Owner' WHERE id='account'`); e != nil {
				t.Fatal(e)
			}
			if _, e := s.db.Exec(`UPDATE live_programme_metadata SET facts=json_set(facts,'$.rating',json('{"system":"mpaa","value":"R"}')) WHERE generation_id=? AND id=?`, source.Generation, p.ID); e != nil {
				t.Fatal(e)
			}
			input := ScheduleInput{RequestID: strings.Repeat("c", 48), Occurrence: Occurrence{source.ID, p.ChannelID, source.Generation, p.ID}}
			r, e := s.Schedule(ctx, a, owner, input)
			if e != nil {
				t.Fatal(e)
			}
			if scope == "profile" {
				_, e = s.db.Exec(`UPDATE profile_restrictions SET maximum_age=7,allow_unrated=0 WHERE profile_id='profile'`)
			} else {
				_, e = s.db.Exec(`INSERT INTO access_limits(account_id,revision,updated_ms,body) VALUES('account',1,1,'{"maxContentRating":"PG","allowUnrated":false}')`)
			}
			if e != nil {
				t.Fatal(e)
			}
			page, e := s.List(ctx, a, owner, Query{State: "all", Limit: 50})
			if e != nil {
				t.Fatal(e)
			}
			detail, e := s.Detail(ctx, a, owner, r.ID, "")
			if e != nil {
				t.Fatal(e)
			}
			for _, value := range []any{page, detail} {
				body, e := json.Marshal(value)
				if e != nil {
					t.Fatal(e)
				}
				if strings.Contains(string(body), "A description.") || strings.Contains(string(body), "The episode") || strings.Contains(string(body), `"title":"Show"`) {
					t.Fatalf("restricted DVR facts leaked: %s", body)
				}
			}
			if _, e = s.Schedule(ctx, a, owner, input); e == nil {
				t.Fatal("restricted receipt replay exposed programme")
			}
			if c, _, e := s.claimNext(ctx); e != nil || c != nil {
				t.Fatalf("restricted programme claimed: %+v %v", c, e)
			}
		})
	}
}

func TestDVRCancellationReceiptDoesNotReplayRestrictedFacts(t *testing.T) {
	s, source, p, a := sharingStore(t)
	ctx := context.Background()
	o := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
	if _, e := s.db.Exec(`UPDATE accounts SET username='Owner' WHERE id='account'`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(`UPDATE live_programme_metadata SET facts=json_set(facts,'$.rating',json('{"value":"R"}')) WHERE generation_id=? AND id=?`, source.Generation, p.ID); e != nil {
		t.Fatal(e)
	}
	r, e := s.Schedule(ctx, a, o, ScheduleInput{RequestID: strings.Repeat("c", 48), Occurrence: Occurrence{source.ID, p.ChannelID, source.Generation, p.ID}})
	if e != nil {
		t.Fatal(e)
	}
	mutation := Mutation{RequestID: strings.Repeat("d", 48), ExpectedRevision: r.Revision}
	if _, e = s.Cancel(ctx, a, o, r.ID, mutation); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`UPDATE profile_restrictions SET maximum_age=7 WHERE profile_id='profile'`); e != nil {
		t.Fatal(e)
	}
	replayed, e := s.Cancel(ctx, a, o, r.ID, mutation)
	if e != nil {
		t.Fatal(e)
	}
	if replayed.Programme.Title != livechannels.RestrictedProgrammeTitle || replayed.Programme.Description != "" || replayed.SeriesTitle != "" {
		t.Fatalf("restricted cancellation receipt: %+v", replayed)
	}
}

func TestActiveDVRCaptureRechecksRatingPolicy(t *testing.T) {
	s, c, _ := sharedClaims(t)
	if _, e := s.db.Exec(`UPDATE accounts SET username='Owner' WHERE id='account' AND profile_id=?`, c.request.Owner.ProfileID); e != nil {
		t.Fatal(e)
	}
	c.request.Recording.Programme.Rating = &livechannels.ProgrammeRating{Value: "R"}
	if e := s.renew(context.Background(), c, false); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(`UPDATE profile_restrictions SET maximum_age=7 WHERE profile_id=?`, c.request.Owner.ProfileID); e != nil {
		t.Fatal(e)
	}
	if e := s.renew(context.Background(), c, false); e == nil {
		t.Fatal("active restricted capture remained authorized")
	}
}
