package httpapi

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// The admission race (MS2 follow-up): admission counts the account's streams,
// but the stream it admits only becomes countable when its session is written.
// Starts of one account that overlap there must still admit exactly up to
// maxStreams: eight simultaneous starts at maxStreams 1 admit one, every time.
func TestMemberLimitsSimultaneousStartsAdmitExactlyTheLimit(t *testing.T) {
	f := memberLimitsFixture(t, 0)
	member, film := f.member()
	setMemberLimits(t, f, `{"maxStreams":1}`)
	for round := 0; round < 5; round++ {
		var wg sync.WaitGroup
		codes := make([]int, 8)
		bodies := make([]string, 8)
		for i := range codes {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				w := f.raw("POST", "/v1/playback/sessions", member, map[string]string{"Idempotency-Key": fmt.Sprintf("race-%02d-start-%02d", round, i)}, startBody(film, nil))
				codes[i], bodies[i] = w.Code, w.Body.String()
			}(i)
		}
		wg.Wait()
		admitted := []string{}
		for i, code := range codes {
			switch code {
			case 201:
				var s playbackv1.SessionView
				if err := json.Unmarshal([]byte(bodies[i]), &s); err != nil {
					t.Fatal(err)
				}
				admitted = append(admitted, s.ID)
			case 409:
			default:
				t.Fatalf("round %d start %d: %d %s", round, i, code, bodies[i])
			}
		}
		if len(admitted) != 1 {
			t.Fatalf("round %d: %d starts admitted at maxStreams 1", round, len(admitted))
		}
		f.callAs(member, "DELETE", "/v1/playback/sessions/"+admitted[0], nil, nil, 204, nil)
	}
}
