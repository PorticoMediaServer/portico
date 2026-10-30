package social

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// BenchmarkRoomRound is one "round" of a busy room: every member reports its
// readiness once (as a player does while buffering and playing) and then
// catches up on the room's events from where it was. Reported per member, the
// cost should stay flat as the room grows to MaxMembers.
//
//	go test ./internal/social -run '^$' -bench RoomRound -benchtime 40x
func BenchmarkRoomRound(b *testing.B) {
	for _, size := range []int{4, 8, 16, MaxMembers} {
		b.Run(fmt.Sprintf("members=%d", size), func(b *testing.B) {
			db, e := persistence.Open(filepath.Join(b.TempDir(), "social.sqlite"))
			if e != nil {
				b.Fatal(e)
			}
			defer db.Close()
			now := time.Now()
			store := New(db)
			store.Now = func() time.Time { return now }
			ctx := context.Background()
			viewers := make([]identity.Principal, size)
			for i := range viewers {
				viewers[i] = identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "account", ProfileID: fmt.Sprintf("profile-%02d", i)}}
			}
			group := seedRoom(b, store, viewers)
			cursor := make([]int64, size)
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				now = now.Add(time.Second)
				for i, p := range viewers {
					if _, e = store.ReportReadiness(ctx, p, group, ReadinessRequest{ProtocolVersion: Protocol, Readiness: "ready", PlaybackRevision: "1", PositionUS: fmt.Sprint(n * 1000000)}); e != nil {
						b.Fatal(e)
					}
					events, _, e := store.EventsSince(ctx, p, group, cursor[i])
					if e != nil {
						b.Fatal(e)
					}
					if len(events) > 0 {
						cursor[i] = events[len(events)-1].Ordinal
					}
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*size)/1e3, "µs/member-report")
		})
	}
}

// seedRoom writes one playing group whose first viewer is the host and the
// rest joined members.
func seedRoom(b *testing.B, store *Store, viewers []identity.Principal) string {
	b.Helper()
	now := store.ms()
	id := "grp_room"
	host := viewers[0]
	if _, e := store.DB.Exec(`INSERT INTO social_groups(id,name,authority,account_id,profile_id,host_member_id,host_authority,state,pause_after_seconds,end_after_seconds,host_seen_ms,position_updated_ms,created_at_ms,updated_at_ms) VALUES(?,?,?,?,?,?,'host-only','playing',?,?,?,?,?,?)`,
		id, "Film night", host.Authority, host.AccountID, host.ProfileID, "mem_00", HostPauseSeconds, HostEndSeconds, now, now, now, now); e != nil {
		b.Fatal(e)
	}
	for i, p := range viewers {
		role := "member"
		if i == 0 {
			role = "host"
		}
		if _, e := store.DB.Exec(`INSERT INTO social_group_members(id,group_id,authority,account_id,profile_id,display_name,role,state,readiness,readiness_at_ms,joined_at_ms,seen_at_ms) VALUES(?,?,?,?,?,?,?,'joined','ready',?,?,?)`,
			fmt.Sprintf("mem_%02d", i), id, p.Authority, p.AccountID, p.ProfileID, fmt.Sprintf("Viewer %d", i), role, now, now, now); e != nil {
			b.Fatal(e)
		}
	}
	return id
}
