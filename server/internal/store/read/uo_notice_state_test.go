package read

import (
	"database/sql"
	"errors"
	"testing"
)

func TestUONoticeTwoBulkReadTimes(t *testing.T) {
	a := uoNoticeIdentity{sequence: 1, revision: 1}
	c := uoNoticeIdentity{sequence: 2, revision: 2}
	individualC := uoNoticeIndividualAck{exists: true, revision: 3, readMS: sql.NullInt64{Int64: 50, Valid: true}}
	first := uoNoticeReadThrough{exists: true, sequence: 2, revision: 4, readMS: 100}
	check := func(notice uoNoticeIdentity, individual uoNoticeIndividualAck, bulk uoNoticeReadThrough, want sql.NullInt64) {
		t.Helper()
		state, err := resolveUONoticeReadState(notice, individual, bulk)
		if err != nil || state.readMS != want {
			t.Fatalf("notice%+v got%+v want%+v err%v", notice, state, want, err)
		}
	}
	check(a, uoNoticeIndividualAck{}, first, sql.NullInt64{Int64: 100, Valid: true})
	check(c, individualC, first, sql.NullInt64{Int64: 50, Valid: true})
	// B arrived after the first read-all. The second covers A and B; C keeps
	// its individual time, as required by the approved pre-release contract.
	b := uoNoticeIdentity{sequence: 3, revision: 5}
	check(b, uoNoticeIndividualAck{}, first, sql.NullInt64{})
	second := uoNoticeReadThrough{exists: true, sequence: 3, revision: 6, readMS: 200}
	check(a, uoNoticeIndividualAck{}, second, sql.NullInt64{Int64: 200, Valid: true})
	check(b, uoNoticeIndividualAck{}, second, sql.NullInt64{Int64: 200, Valid: true})
	check(c, individualC, second, sql.NullInt64{Int64: 50, Valid: true})
}

func TestUONoticeUnreadAndRestatement(t *testing.T) {
	notice := uoNoticeIdentity{sequence: 1, revision: 1}
	bulk := uoNoticeReadThrough{exists: true, sequence: 3, revision: 6, readMS: 200}
	unread := uoNoticeIndividualAck{exists: true, revision: 7}
	state, err := resolveUONoticeReadState(notice, unread, bulk)
	if err != nil || state.readMS.Valid {
		t.Fatal("later explicit unread lost", state, err)
	}
	// Another notice's action leaves these facts unchanged. A new covering
	// read-all overrides the earlier unread and supplies its own accepted time.
	bulk.revision, bulk.readMS = 8, 300
	state, err = resolveUONoticeReadState(notice, unread, bulk)
	if err != nil || state.readMS != (sql.NullInt64{Int64: 300, Valid: true}) {
		t.Fatal("new read-all failed to cover unread", state, err)
	}
	notice.revision = 9
	state, err = resolveUONoticeReadState(notice, unread, bulk)
	if err != nil || state != (uoNoticeReadState{}) {
		t.Fatal("producer restatement retained stale read state", state, err)
	}
}

func TestUONoticeIndividualArchiveTime(t *testing.T) {
	notice := uoNoticeIdentity{sequence: 1, revision: 1}
	ack := uoNoticeIndividualAck{exists: true, revision: 2, readMS: sql.NullInt64{Int64: 20, Valid: true}, archivedMS: sql.NullInt64{Int64: 30, Valid: true}}
	bulk := uoNoticeReadThrough{exists: true, sequence: 5, revision: 4, readMS: 40}
	state, err := resolveUONoticeReadState(notice, ack, bulk)
	if err != nil || state.readMS != ack.readMS || state.archivedMS != ack.archivedMS {
		t.Fatal("bulk changed sparse individual times", state, err)
	}
	ack.readMS = sql.NullInt64{}
	state, err = resolveUONoticeReadState(notice, ack, bulk)
	if err != nil || state.readMS.Valid || state.archivedMS != ack.archivedMS {
		t.Fatal("read-all covered archived unread notice", state, err)
	}
	notice.revision = 5
	state, err = resolveUONoticeReadState(notice, ack, bulk)
	if err != nil || state != (uoNoticeReadState{}) {
		t.Fatal("restatement retained archived state", state, err)
	}
}

func TestUONoticeReadStateRejectsMalformedFacts(t *testing.T) {
	notice := uoNoticeIdentity{sequence: 1, revision: 1}
	for _, ack := range []uoNoticeIndividualAck{
		{revision: 1}, {exists: true},
		{exists: true, revision: 1, readMS: sql.NullInt64{Int64: 1}},
		{exists: true, revision: 1, archivedMS: sql.NullInt64{Int64: -1, Valid: true}},
	} {
		state, err := resolveUONoticeReadState(notice, ack, uoNoticeReadThrough{})
		if !errors.Is(err, ErrInput) || state != (uoNoticeReadState{}) {
			t.Fatal("malformed sparse state accepted", state, err)
		}
	}
	state, err := resolveUONoticeReadState(notice, uoNoticeIndividualAck{}, uoNoticeReadThrough{exists: true, sequence: 1, revision: 1, readMS: -1})
	if !errors.Is(err, ErrInput) || state != (uoNoticeReadState{}) {
		t.Fatal("malformed bulk head accepted", state, err)
	}
	state, err = resolveUONoticeReadState(notice, uoNoticeIndividualAck{}, uoNoticeReadThrough{exists: true, sequence: 1, revision: 1})
	if !errors.Is(err, ErrInput) || state != (uoNoticeReadState{}) {
		t.Fatal("uninitialized zero-time bulk head became a read", state, err)
	}
}
