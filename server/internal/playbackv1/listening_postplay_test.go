package playbackv1

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"testing"
)

func TestListeningQueueIgnoresVideoEpisodePreferences(t *testing.T) {
	for _, audio := range []bool{false, true} {
		f := newCarryFixture(t, audio)
		f.service.PostPlay = func(context.Context, *sql.Tx, identity.Principal) (PostPlayPolicy, error) {
			return PostPlayPolicy{Autoplay: false, CountdownMs: 15000, StartDuringCredits: true, Protection: true, Episodes: 2}, nil
		}
		q := f.queue()
		if q.Queue.PostPlay == nil {
			t.Fatal("queue omitted its actual post-play policy")
		}
		policy := q.Queue.PostPlay
		if audio && (!policy.Autoplay || policy.CountdownMs != 0 || policy.StartDuringCredits || policy.PassoutCheckDue) {
			t.Fatal("video preferences reached a listening queue", policy)
		}
		if !audio && (policy.Autoplay || policy.CountdownMs != 15000 || !policy.StartDuringCredits) {
			t.Fatal("video episode preferences were changed", policy)
		}
	}
}

// The real available-next projection and accepting writer must agree: protection
// never creates a confirmation hold that has no admitted successor to confirm.
func TestPassoutProtectionNeedsAnAvailableAutomaticSuccessor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, tc := range []struct {
		name     string
		autoplay bool
		next     string
		due      bool
	}{
		{"available", true, "ready", true},
		{"end", true, "end", false},
		{"withheld", true, "unavailable", false},
		{"autoplay off", false, "ready", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCarryFixture(t, false)
			q := f.queue()
			f.catalog.Exec(`UPDATE catalog_entities SET kind=? WHERE public_id IN(pid_blob(?),pid_blob(?))`, compactcatalog.Episode, f.items[0].Public, f.items[1].Public)
			f.service.PostPlay = func(context.Context, *sql.Tx, identity.Principal) (PostPlayPolicy, error) {
				return PostPlayPolicy{Autoplay: tc.autoplay, Protection: true, Episodes: 1}, nil
			}
			if tc.next == "end" {
				f.catalog.Exec(`UPDATE queues_v1 SET current_position=1,current_entry_id=? WHERE id=?`, *q.Queue.Next.EntryID, q.Queue.ID)
			}
			if tc.next == "unavailable" {
				f.service.Visibility = func(context.Context, *sql.Tx, identity.Principal, string) (string, []any, error) {
					return "i.public_id != pid_blob(?)", []any{f.items[1].Public}, nil
				}
			}
			actual, err := f.service.Queue(context.Background(), f.caller, q.Queue.ID)
			if err != nil {
				t.Fatal(err)
			}
			if actual.PostPlay == nil || actual.Next == nil || actual.Next.Reason != tc.next || actual.PostPlay.PassoutCheckDue != tc.due {
				t.Fatalf("post-play: %+v next: %+v", actual.PostPlay, actual.Next)
			}
			if tc.next == "ready" && !tc.due {
				return
			} // explicit viewer advancement is unchanged
			_, err = f.service.Advance(context.Background(), f.caller, q.Queue.ID, actual.Revision, AdvanceRequest{Reason: "completion"})
			if tc.due {
				var refusal *TransitionError
				if !errors.As(err, &refusal) || refusal.Reason != "passout_check_required" {
					t.Fatalf("available edge lost protection: %v", err)
				}
			} else if !errors.Is(err, ErrQueueEnded) {
				t.Fatalf("no admitted edge must end ordinarily: %v", err)
			}
			after, err := f.service.Queue(context.Background(), f.caller, q.Queue.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Revision != actual.Revision || after.Current.EntryID != actual.Current.EntryID {
				t.Fatal("refused/ended completion changed occurrence")
			}
		})
	}
}
