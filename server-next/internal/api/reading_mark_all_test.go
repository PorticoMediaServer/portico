package api

import (
	"net/url"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A Read library's "Mark all read" is the set_state job over the library's unread titles, the
// same view its count read. A book already read that the person is reading again is not in that
// view: its place survives, and only the unread titles are written.
func TestMarkAllReadMarksOnlyTheUnreadAndKeepsAReadBooksPlace(t *testing.T) {
	b := newBookShelf(t)
	token := b.owner.AccessToken
	var books string
	for _, lib := range get[Libraries](t, b.s, token, "/v1/libraries").Items {
		if lib.Kind == db.LibraryBooks {
			books = lib.ID
		}
	}
	nineteen, kindle := b.find(db.KindBook, "Nineteen"), b.find(db.KindBook, "Kindle Book")
	place := `{"format":"epub","progression":0.4,"cfi":"epubcfi(/6/2!/4/2/1:0)","fingerprint":"0123456789abcdef"}`
	status(t, b.s, "PUT", "/v1/items/"+nineteen+"/state", token, map[string]any{"watched": true})
	status(t, b.s, "PUT", "/v1/items/"+nineteen+"/state", token, map[string]any{"position": place})
	if st := get[Item](t, b.s, token, "/v1/items/"+nineteen).State; !st.Watched || st.Position != place {
		t.Fatalf("a read book being read again: watched %v, place %q", st.Watched, st.Position)
	}

	view := url.Values{"library": {books}, "kind": {"book,comic,issue"}, "watched": {"false"}}.Encode()
	if unread := get[ItemPage](t, b.s, token, "/v1/items?"+view+"&limit=1&total=true"); *unread.Total != 1 {
		t.Fatalf("%d unread books, want the one not read", *unread.Total)
	}
	var job Job
	call(t, b.s, "POST", "/v1/jobs", token, JobStart{Kind: stateJobKind, Target: JobTarget{Filter: view}, Params: JSON(`{"watched": true}`)}, &job)
	if job = waitForJob(t, b.s, token, job.ID); job.State != db.JobDone || job.Progress == nil || job.Progress.Done != 1 {
		t.Fatalf("the job ended %s with %+v: %s", job.State, job.Progress, job.Error)
	}
	if !get[Item](t, b.s, token, "/v1/items/"+kindle).State.Watched {
		t.Fatal("the unread book was not marked read")
	}
	if st := get[Item](t, b.s, token, "/v1/items/"+nineteen).State; !st.Watched || st.Position != place {
		t.Fatalf("the book already read lost its place: watched %v, place %q", st.Watched, st.Position)
	}
}
