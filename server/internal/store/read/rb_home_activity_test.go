package read

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestRBHomeActivityWorkAndCurrentBookTarget(t *testing.T) {
	f := newRBHomeFixture(t)
	for _, v := range []struct {
		family             HomeActivityFamily
		kind, work, target int64
	}{{HomeContinueActivity, 1, 1, 1}, {HomeContinueActivity, 4, 2000, 3000}, {HomeListeningActivity, 7, 9001, 9200}, {HomeBookActivity, 9, 9100, 9300}} {
		in := HomeActivityInput{View: f.input, Family: v.family}
		in.View.Kind = v.kind
		page, _, e := f.reader.homeActivityWindow(t.Context(), homeRequest(), time.Second, f.output, in)
		if e != nil || !page.Exhausted || page.Next != nil || len(page.Items) != 1 || page.Items[0].Work.ID != v.work || page.Items[0].Target.ID != v.target || page.Items[0].State.ResumePosition != 5000 {
			t.Fatal(v, page, e)
		}
	}
	f.exec(t, `UPDATE personal_items SET dismissed_playback_id=resume_playback_id WHERE item_id=9100`)
	bookInput := HomeActivityInput{View: f.input, Family: HomeBookActivity}
	bookInput.View.Kind = 9
	dismissed, _, dismissalErr := f.reader.homeActivityWindow(t.Context(), homeRequest(), time.Second, f.output, bookInput)
	if dismissalErr != nil || len(dismissed.Items) != 0 {
		t.Fatal("dismissed current audiobook", dismissed, dismissalErr)
	}
	f.exec(t, `UPDATE personal_items SET dismissed_playback_id=NULL WHERE item_id=9100`)
	f.exec(t, `UPDATE personal_items SET resume_item_public_id=zeroblob(16) WHERE item_id=9100`)
	in := HomeActivityInput{View: f.input, Family: HomeBookActivity}
	in.View.Kind = 9
	page, _, e := f.reader.homeActivityWindow(t.Context(), homeRequest(), time.Second, f.output, in)
	if e != nil || len(page.Items) != 0 || !page.Exhausted {
		t.Fatal("stale audiobook part", page, e)
	}
}

func TestRBHomeActivityDismissalAndDuration(t *testing.T) {
	f := newRBHomeFixture(t)
	in := HomeActivityInput{View: f.input, Family: HomeContinueActivity}
	f.exec(t, `UPDATE personal_items SET dismissed_playback_id=resume_playback_id WHERE item_id=1`)
	page, _, e := f.reader.homeActivityWindow(t.Context(), homeRequest(), time.Second, f.output, in)
	if e != nil || len(page.Items) != 0 {
		t.Fatal("dismissed resume", page, e)
	}
	f.exec(t, `UPDATE personal_items SET dismissed_playback_id=NULL,resume_position=118000 WHERE item_id=1`)
	page, _, e = f.reader.homeActivityWindow(t.Context(), homeRequest(), time.Second, f.output, in)
	if e != nil || len(page.Items) != 0 {
		t.Fatal("near-complete resume", page, e)
	}
	f.exec(t, `UPDATE personal_items SET resume_position=5000 WHERE item_id=1`)
	f.exec(t, `UPDATE catalog_assets SET available=0 WHERE id=1`)
	page, _, e = f.reader.homeActivityWindow(t.Context(), homeRequest(), time.Second, f.output, in)
	if e != nil || len(page.Items) != 0 {
		t.Fatal("unavailable resume", page, e)
	}
}

func TestRBHomeActivityRawReachabilityAndCursor(t *testing.T) {
	f := newRBHomeFixture(t)
	for id := int64(10000); id < 10070; id++ {
		public := make([]byte, 16)
		binary.BigEndian.PutUint64(public[8:], uint64(id))
		f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,resume_position,activity_at) VALUES('p',?,?,1,5000,'2026-11-03T12:00:00.000000000Z')`, public, id)
	}
	in := HomeActivityInput{View: f.input, Family: HomeContinueActivity}
	page, _, e := f.reader.homeActivityWindow(t.Context(), homeRequest(), time.Second, f.output, in)
	if e != nil || len(page.Items) != 0 || page.Exhausted || page.Next == nil || page.RawPositions != 64 {
		t.Fatal("raw prefix lost", page, e)
	}
	in.After = page.Next
	page, _, e = f.reader.homeActivityWindow(t.Context(), homeRequest(), time.Second, f.output, in)
	if e != nil || !page.Exhausted || len(page.Items) != 1 || page.Items[0].Target.ID != 1 {
		t.Fatal("late eligible leaf lost", page, e)
	}
	f.exec(t, `UPDATE direct_profiles SET revision=revision+1 WHERE id='p'`)
	page, _, e = f.reader.homeActivityWindow(t.Context(), homeRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrStale) || !reflect.DeepEqual(page, HomeActivityPage{}) {
		t.Fatal("stale cursor", page, e)
	}
}
