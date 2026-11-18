package api

import "testing"

// Start early is at most 30 minutes and finish late at most an hour (Justin, 18 Nov). A
// booking, a change to one and a series rule each refuse more: it would record the programmes
// that follow, whatever their rating, and hold a tuner other people's recordings need.
func TestRecordingPaddingHasALimit(t *testing.T) {
	s, owner, _ := recordable(t)
	day, quarter := int64(24*60*60*1000), int64(15*60*1000)
	programme := programmeID(t, s, owner, "Late News")
	if code := status(t, s, "POST", "/v1/dvr/recordings", owner, DvrRecordingBook{Programme: programme, PadAfterMs: &day}); code != 400 {
		t.Errorf("booking with a day of finish late answered %d, want 400", code)
	}
	if code := status(t, s, "POST", "/v1/dvr/rules", owner, DvrRuleSave{Programme: programmeID(t, s, owner, "Night Film"), PadBeforeMs: &day}); code != 400 {
		t.Errorf("a rule with a day of start early answered %d, want 400", code)
	}
	hour, hourAndASecond, halfHourAndASecond := int64(60*60*1000), int64(60*60*1000+1000), int64(30*60*1000+1000)
	if code := status(t, s, "POST", "/v1/dvr/recordings", owner, DvrRecordingBook{Programme: programme, PadAfterMs: &hourAndASecond}); code != 400 {
		t.Errorf("booking with just over an hour of finish late answered %d, want 400", code)
	}
	if code := status(t, s, "POST", "/v1/dvr/recordings", owner, DvrRecordingBook{Programme: programme, PadBeforeMs: &halfHourAndASecond}); code != 400 {
		t.Errorf("booking with just over half an hour of start early answered %d, want 400", code)
	}
	var rec DvrRecording
	if w := call(t, s, "POST", "/v1/dvr/recordings", owner, DvrRecordingBook{Programme: programme, PadAfterMs: &quarter}, &rec); w.Code != 200 || rec.PadAfterMs != quarter {
		t.Fatalf("booking with fifteen minutes of finish late answered %d: %s", w.Code, w.Body)
	}
	if code := status(t, s, "PATCH", "/v1/dvr/recordings/"+rec.ID, owner, DvrRecordingChange{PadAfterMs: &hour}); code != 200 {
		t.Errorf("changing a booking to exactly an hour of finish late answered %d, want 200", code)
	}
	if code := status(t, s, "PATCH", "/v1/dvr/recordings/"+rec.ID, owner, DvrRecordingChange{PadAfterMs: &day}); code != 400 {
		t.Errorf("changing a booking to a day of finish late answered %d, want 400", code)
	}
}
