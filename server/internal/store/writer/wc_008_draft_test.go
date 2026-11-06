package writer

import (
	"math"
	"testing"
)

func wcTest008Progress() wc008ProgressCommand {
	return wc008ProgressCommand{
		identity:      CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}},
		resultVersion: 1, oldControlBytes: 1000, authority: "local", lastAcceptedSequence: 17, profileKey: "viewer-one", accountKey: "account-one", profileID: "profile-one", libraryKey: "library-one",
		memberIncarnation: "member-birth", profileLifetime: "profile-birth", libraryID: 7, subjectID: 11,
		subjectPublicID: [16]byte{3}, proof: wcTestPlayingProof(),
		fromMS: 0, cursorMS: 0, untilMS: 1_000, startEventID: 1,
		appliedMS: 1_000, appliedAt: "1970-01-01T00:00:01Z",
	}
}

func TestWC008DraftPieceRetainsExactEdgesAndInt64Limit(t *testing.T) {
	cases := []struct{ from, cursor, until, want int64 }{
		{0, 0, 1_000, 1_000},
		{0, wcQuarterMS - 1, wcQuarterMS + 1, wcQuarterMS},
		{0, wcQuarterMS, 12 * 60 * 60 * 1_000, 2 * wcQuarterMS},
		{math.MaxInt64 - 100, math.MaxInt64 - 100, math.MaxInt64, math.MaxInt64},
	}
	for _, tc := range cases {
		q := wcTest008Progress()
		q.fromMS, q.cursorMS, q.untilMS = tc.from, tc.cursor, tc.until
		piece, err := q.piece()
		if err != nil || piece.fromMS != tc.cursor || piece.untilMS != tc.want {
			t.Fatalf("%+v: piece=%+v err=%v", tc, piece, err)
		}
	}
}

func TestWC008DraftRefusesMissingSourceAndSharedCounterAliases(t *testing.T) {
	changes := []func(*wc008ProgressCommand){
		func(q *wc008ProgressCommand) { q.startEventID = 0 },
		func(q *wc008ProgressCommand) { q.memberIncarnation = "" },
		func(q *wc008ProgressCommand) { q.profileLifetime = "" },
		func(q *wc008ProgressCommand) { q.proof.clockIncarnation = [16]byte{} },
		func(q *wc008ProgressCommand) { q.proof.occurrenceGeneration = 0 },
		func(q *wc008ProgressCommand) { q.proof.rememberGeneration = 0 },
		func(q *wc008ProgressCommand) { q.proof.activityGeneration = 0 },
		func(q *wc008ProgressCommand) { q.cursorMS = q.untilMS },
		func(q *wc008ProgressCommand) { q.identity.Sequence++ },
		func(q *wc008ProgressCommand) { q.last = 1 },
		func(q *wc008ProgressCommand) { q.appliedAt = "1970-01-01T01:00:01+01:00" },
	}
	for i, edit := range changes {
		q := wcTest008Progress()
		edit(&q)
		if piece, err := q.piece(); err == nil || piece != (wcPlayingInterval{}) {
			t.Fatalf("mutation%d: piece=%+v err=%v", i, piece, err)
		}
	}
}

func TestWC008DraftProgressNoTurnCannotExposeDomainPrefix(t *testing.T) {
	p, err := applyWC008ProgressMetrics(t.Context(), nil, wcTest008Progress())
	if err == nil || p != (wc008ProgressEffect{}) {
		t.Fatal("missing W turn returned progress effects", p, err)
	}
}

func TestWC008DraftSavedResultSurvivesFinalCursorClear(t *testing.T) {
	q := wcTest008Progress()
	result := wc008ProgressEffect{consumedFrom: 0, consumedUntil: 1000, creditedMS: 1000,
		nextCursor: 1000, finished: true, observationRevision: 3, historyRevision: 7, personalRevision: 9, controlVariableBytes: 1000, adminVariableBytes: 1000}
	if err := validateWC008ProgressEffect(q, result); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*wc008ProgressEffect){
		func(r *wc008ProgressEffect) { r.nextCursor = 0 }, // today's NULL is not the saved endpoint
		func(r *wc008ProgressEffect) { r.consumedUntil++ },
		func(r *wc008ProgressEffect) { r.creditedMS-- },
		func(r *wc008ProgressEffect) { r.finished = false },
		func(r *wc008ProgressEffect) { r.remainingUntil.Valid = true; r.remainingUntil.Int64 = 1000 },
		func(r *wc008ProgressEffect) { r.observationRevision = 0 },
		func(r *wc008ProgressEffect) { r.historyRevision = 0 },
	} {
		changed := result
		edit(&changed)
		if err := validateWC008ProgressEffect(q, changed); err == nil {
			t.Fatal("incomplete or reconstructed result accepted", changed)
		}
	}
}
