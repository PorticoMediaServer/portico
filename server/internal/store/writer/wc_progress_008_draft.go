package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Private adopted-008 components for W's SAME already-admitted, fresh turn. Native
// fields follow adopted CHANGE-008 schema debd5c77. This is not an
// entry point, constructor, receipt codec, outbox helper or runtime. W must save
// the closed result/owner sequence/debt and THREE bounded events before its
// sole physical terminal path can report Applied. Every error rolls back ALL
// domain effects; these internal effects alone never acknowledge the source.
type wc008ProgressCommand struct {
	identity                                             CommandIdentity
	last, acknowledged                                   int64
	lastAcceptedSequence, resultVersion, oldControlBytes int64
	authority                                            string
	acceptedOrdinal, receiptRows, receiptVariableBytes   int64
	profileKey, accountKey, profileID, libraryKey        string
	memberIncarnation, profileLifetime                   string
	libraryID, subjectID                                 int64
	subjectPublicID                                      [16]byte
	proof                                                wcPlayingProof
	fromMS, untilMS, cursorMS, startEventID              int64
	appliedMS                                            int64
	appliedAt                                            string // supplied by W's trusted command clock
}

type wc008ProgressEffect struct {
	consumedFrom, consumedUntil, creditedMS                                    int64
	nextCursor                                                                 int64 // saved consumed endpoint survives native final NULL clear
	remainingUntil                                                             sql.NullInt64
	finished                                                                   bool
	observationRevision, historyRevision                                       int64
	viewerRevision, personalRevision, controlVariableBytes, adminVariableBytes int64
}

const (
	wc008ProgressPending             stmt.ID = "wc.progress.pending"
	wc008ProgressAdjacent            stmt.ID = "wc.progress.adjacent"
	wc008ProgressCollision           stmt.ID = "wc.progress.collision"
	wc008ProgressExtend              stmt.ID = "wc.progress.extend"
	wc008ProgressSpanFind            stmt.ID = "wc.progress.span_find"
	wc008ProgressSpanUpdate          stmt.ID = "wc.progress.span_update"
	wc008ProgressSpan                stmt.ID = "wc.progress.span"
	wc008ProgressItemBucketRead      stmt.ID = "wc.progress.item_bucket_read"
	wc008ProgressItemBucketUpdate    stmt.ID = "wc.progress.item_bucket_update"
	wc008ProgressProfileBucketRead   stmt.ID = "wc.progress.profile_bucket_read"
	wc008ProgressProfileBucketUpdate stmt.ID = "wc.progress.profile_bucket_update"
	wc008ProgressItemBucket          stmt.ID = "wc.progress.item_bucket"
	wc008ProgressProfileBucket       stmt.ID = "wc.progress.profile_bucket"
	wc008ProgressLeaf                stmt.ID = "wc.progress.leaf"
	wc008ProgressViewerRevision      stmt.ID = "wc.progress.viewer_revision"
	wc008ProgressHistoryRevision     stmt.ID = "wc.progress.history_revision"
	wc008ProgressAdminTime           stmt.ID = "wc.progress.admin_time"
	wc008ProgressAdvance             stmt.ID = "wc.progress.advance"
	wc008ProgressClear               stmt.ID = "wc.progress.clear"
)

func (q wc008ProgressCommand) validateSource() error {
	if q.identity.Incarnation == [16]byte{} || q.identity.OperationID == [16]byte{} || q.identity.Sequence <= 0 ||
		q.last < 0 || q.lastAcceptedSequence < -1 || q.resultVersion <= 0 || q.oldControlBytes <= 0 || q.authority == "" || q.acknowledged != q.last || q.identity.Sequence <= q.last || q.identity.Sequence-q.last != 1 ||
		q.profileKey == "" || q.accountKey == "" || q.profileID == "" || q.libraryKey == "" ||
		q.memberIncarnation == "" || q.profileLifetime == "" || q.libraryID <= 0 || q.subjectID <= 0 ||
		q.subjectPublicID == [16]byte{} || q.appliedMS < 0 ||
		q.appliedAt != time.UnixMilli(q.appliedMS).UTC().Format(time.RFC3339Nano) ||
		q.proof.playbackID == "" || q.proof.occurrenceGeneration <= 0 || q.proof.reportSequence < 0 ||
		q.proof.activityGeneration <= 0 || q.proof.rememberGeneration <= 0 || q.proof.clockIncarnation == [16]byte{} ||
		q.fromMS < 0 || q.cursorMS < q.fromMS || q.cursorMS >= q.untilMS {
		return ErrConflict
	}

	return nil
}

func (q wc008ProgressCommand) piece() (wcPlayingInterval, error) {
	if err := q.validateSource(); err != nil {
		return wcPlayingInterval{}, err
	}
	if q.startEventID <= 0 {
		return wcPlayingInterval{}, ErrConflict
	}
	until := q.untilMS
	if step := wcQuarterMS - q.cursorMS%wcQuarterMS; step < until-q.cursorMS {
		until = q.cursorMS + step
	}
	return wcPlayingInterval{q.cursorMS, until}, nil
}

func applyWC008ProgressMetrics(ctx context.Context, turn *commandTurn, q wc008ProgressCommand) (out wc008ProgressEffect, err error) {
	if turn == nil {
		return out, ErrInvalid
	}
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	piece, err := q.piece()
	if err != nil {
		return out, err
	}
	if _, err = wc008DraftInteger(ctx, turn, wc008ProgressPending,
		q.proof.playbackID, q.profileKey, q.subjectID, q.libraryID, q.identity.Incarnation[:],
		q.memberIncarnation, q.profileLifetime, q.last, q.acknowledged, q.proof.occurrenceGeneration,
		q.proof.reportSequence, q.fromMS, q.untilMS, q.cursorMS, q.proof.activityGeneration,
		q.proof.rememberGeneration, q.proof.clockIncarnation[:], q.startEventID, q.accountKey, q.profileID, q.libraryKey, q.authority, q.accountKey, q.profileID,
		q.acceptedOrdinal, q.lastAcceptedSequence, q.oldControlBytes, q.subjectPublicID[:]); err != nil {
		return out, err
	}
	quarter := piece.fromMS - piece.fromMS%wcQuarterMS
	// ONE native adjacent candidate including copies!=1; no filtered tie walk.
	adjacent, err := wc008DraftQuery(ctx, turn, wc008ProgressAdjacent,
		q.profileKey, q.proof.activityGeneration, q.subjectID, piece.fromMS, quarter)
	if err != nil {
		return out, err
	}
	var id, from, until, copies int64
	err = adjacent.Scan(&id, &from, &until, &copies)
	extend := false
	if err == nil {
		if id <= 0 || from < quarter || from >= until || until != piece.fromMS || copies <= 0 {
			return out, ErrResolveRequired
		}
		if copies == 1 {
			collision, e := wc008DraftQuery(ctx, turn, wc008ProgressCollision,
				q.profileKey, q.proof.activityGeneration, q.subjectID, piece.untilMS, from)
			if e != nil {
				return out, e
			}
			var found int64
			e = collision.Scan(&found)
			if errors.Is(e, sql.ErrNoRows) {
				extend = true
			} else if e != nil {
				return out, e
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if extend {
		if err = wc008DraftChanged(ctx, turn, wc008ProgressExtend, piece.untilMS, id, q.profileKey,
			q.proof.activityGeneration, q.subjectID, from, piece.fromMS); err != nil {
			return out, err
		}
	} else {
		found, e := wc008DraftQuery(ctx, turn, wc008ProgressSpanFind, q.profileKey, q.proof.activityGeneration, q.subjectID, piece.untilMS, piece.fromMS)
		if e != nil {
			return out, e
		}
		var copies int64
		e = found.Scan(&copies)
		if errors.Is(e, sql.ErrNoRows) {
			_, err = wc008DraftInteger(ctx, turn, wc008ProgressSpan, q.profileKey, q.proof.activityGeneration, q.subjectID, piece.fromMS, piece.untilMS, int64(1))
		} else if e != nil {
			return out, e
		} else {
			if copies <= 0 || copies == math.MaxInt64 {
				return out, ErrResolveRequired
			}
			_, err = wc008DraftInteger(ctx, turn, wc008ProgressSpanUpdate, copies+1, q.profileKey, q.proof.activityGeneration, q.subjectID, piece.untilMS, piece.fromMS, copies)
		}
		if err != nil {
			return out, err
		}
	}

	credit := piece.untilMS - piece.fromMS
	at := time.UnixMilli(piece.fromMS).UTC()
	month := time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	buckets := [3]struct {
		grain string
		start int64
	}{{"quarter", quarter}, {"day", piece.fromMS - piece.fromMS%86_400_000}, {"month", month}}
	for _, bucket := range buckets {
		if err = wc008ProgressBucket(ctx, turn, q, bucket.grain, bucket.start, credit, true); err != nil {
			return out, err
		}
		if err = wc008ProgressBucket(ctx, turn, q, bucket.grain, bucket.start, credit, false); err != nil {
			return out, err
		}
	}

	leaf, err := wc008DraftQuery(ctx, turn, wc008ProgressLeaf, credit, q.profileKey, q.subjectID,
		q.subjectPublicID[:], q.proof.activityGeneration, int64(math.MaxInt64)-credit)
	if err != nil {
		return out, err
	}
	var playedMS int64
	if err = leaf.Scan(&playedMS, &out.personalRevision); err != nil {
		return out, err
	}
	if playedMS < credit || out.personalRevision <= 0 {
		return out, ErrResolveRequired
	}
	viewer, err := wc008DraftQuery(ctx, turn, wc008ProgressViewerRevision, q.profileKey, q.libraryKey, int64(math.MaxInt64))
	if err != nil {
		return out, err
	}
	if err = viewer.Scan(&out.observationRevision, &out.viewerRevision); err != nil {
		return out, err
	}
	if out.observationRevision <= 0 || out.viewerRevision < 0 {
		return out, ErrResolveRequired
	}
	out.historyRevision, err = wc008DraftInteger(ctx, turn, wc008ProgressHistoryRevision,
		q.profileKey, q.proof.activityGeneration, q.proof.rememberGeneration, int64(math.MaxInt64))
	if err != nil {
		return wc008ProgressEffect{}, err
	}
	out.adminVariableBytes, err = appendWC008Time(ctx, turn, q, credit, quarter)
	if err != nil {
		return out, err
	}

	out.consumedFrom, out.consumedUntil, out.creditedMS = piece.fromMS, piece.untilMS, credit
	out.nextCursor, out.finished = piece.untilMS, piece.untilMS == q.untilMS
	args := []any{q.proof.playbackID, q.identity.Incarnation[:], q.last, q.acknowledged,
		q.cursorMS, q.proof.reportSequence, q.startEventID, q.proof.occurrenceGeneration}
	if out.finished {
		out.controlVariableBytes, err = wc008DraftInteger(ctx, turn, wc008ProgressClear, append([]any{nil, nil, nil, nil, nil, nil, nil, nil}, args...)...)
	} else {
		out.remainingUntil = sql.NullInt64{Int64: q.untilMS, Valid: true}
		args = append([]any{piece.untilMS}, args...)
		out.controlVariableBytes, err = wc008DraftInteger(ctx, turn, wc008ProgressAdvance, args...)
	}
	if err != nil {
		return wc008ProgressEffect{}, err
	}
	return out, turn.checkLifetimes(ctx)
}

// Definitions are literal candidates, NOT captured plans. INT DDL and HN native
// cohorts/goldens remain required before W can register or execute them.
func wcProgressDefinitions() []stmt.Definition {
	return append([]stmt.Definition{
		{ID: wc008ProgressPending, Owner: "store/writer WC progress", Kind: stmt.Read, CallSites: []string{"store/writer/wc_progress_008_draft.go"}, SQL: `SELECT 1 FROM playback_personal_claims AS p
 INNER JOIN personal_activity_revisions AS a ON a.profile_id=p.profile_key
 INNER JOIN play_history AS h ON h.id=p.pending_time_start_event_id
 INNER JOIN catalog_subjects AS s ON s.library_id=p.command_library_id AND s.id=p.item_id
 INNER JOIN catalog_libraries AS l ON l.id=p.command_library_id
 WHERE p.playback_id=? AND p.profile_key=? AND p.item_id=? AND p.command_library_id=?
 AND p.command_incarnation=? AND p.command_member_incarnation=? AND p.command_profile_lifetime=?
 AND p.command_last_sequence=? AND p.command_acknowledged_sequence=? AND p.source_occurrence_generation=?
 AND p.pending_time_sequence=? AND p.pending_time_from_ms=? AND p.pending_time_until_ms=? AND p.pending_time_cursor_ms=?
 AND p.pending_time_activity_generation=? AND p.pending_time_remember_generation=? AND p.pending_time_clock_incarnation=?
 AND p.pending_time_start_event_id=? AND a.activity_generation=p.pending_time_activity_generation
 AND a.history_policy_generation=p.pending_time_remember_generation
 AND h.event_kind='started' AND h.account_key=? AND h.profile_id=? AND h.library_public_id=?
 AND h.playback_id=p.playback_id AND h.occurrence_generation=p.source_occurrence_generation
 AND h.subject_id=p.item_id AND h.item_public_id=s.public_id AND h.library_public_id=l.library_id
 AND h.membership_incarnation=p.command_member_incarnation AND h.profile_lifetime=p.command_profile_lifetime AND h.started_ms IS NOT NULL
 AND p.command_authority=? AND p.command_account_id=? AND p.command_profile_id=?
 AND p.accepted_ordinal=? AND p.last_accepted_sequence=? AND p.control_variable_bytes=? AND h.item_public_id=?
 AND a.policy_authority=p.command_authority AND a.policy_account_id=p.command_account_id
 AND a.policy_profile_id=p.command_profile_id AND a.policy_member_incarnation=p.command_member_incarnation
 AND a.policy_profile_lifetime=p.command_profile_lifetime`, Params: []stmt.Param{{Name: "playback", Type: stmt.Text}, {Name: "profile_key", Type: stmt.Text}, {Name: "subject", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}, {Name: "incarnation", Type: stmt.Blob}, {Name: "member_birth", Type: stmt.Text}, {Name: "profile_birth", Type: stmt.Text}, {Name: "last", Type: stmt.Integer}, {Name: "ack", Type: stmt.Integer}, {Name: "occurrence", Type: stmt.Integer}, {Name: "report", Type: stmt.Integer}, {Name: "from", Type: stmt.Integer}, {Name: "until", Type: stmt.Integer}, {Name: "cursor", Type: stmt.Integer}, {Name: "activity", Type: stmt.Integer}, {Name: "history_policy", Type: stmt.Integer}, {Name: "clock", Type: stmt.Blob}, {Name: "start_id", Type: stmt.Integer}, {Name: "account_key", Type: stmt.Text}, {Name: "profile", Type: stmt.Text}, {Name: "library_key", Type: stmt.Text}, {Name: "authority", Type: stmt.Text}, {Name: "account", Type: stmt.Text}, {Name: "profile_scope", Type: stmt.Text}, {Name: "accepted_ordinal", Type: stmt.Integer}, {Name: "last_accepted", Type: stmt.Integer}, {Name: "old_control_bytes", Type: stmt.Integer}, {Name: "public_id", Type: stmt.Blob}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{"playback-音", "viewer-one", int64(11), int64(7), []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", int64(0), int64(0), int64(5), int64(13), int64(0), int64(1000), int64(0), int64(1), int64(1), []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(1), "account-one", "profile-one", "library-one", "local", "account-one", "profile-one", int64(71), int64(17), int64(153), []byte{3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_personal_claims_2", "sqlite_autoindex_personal_activity_revisions_1"}, PrimaryKeys: []string{"h", "s", "l"}}},
		{ID: wc008ProgressAdjacent, Owner: "store/writer WC progress", Kind: stmt.Read, CallSites: []string{"store/writer/wc_progress_008_draft.go"}, SQL: `SELECT id,from_ms,until_ms,copies FROM listening_time_spans INDEXED BY listening_time_span_identity
 WHERE profile_id=? AND activity_generation=? AND subject_id=? AND until_ms=? AND from_ms>=?
 ORDER BY from_ms LIMIT 1`, Params: []stmt.Param{{Name: "profile", Type: stmt.Text}, {Name: "activity", Type: stmt.Integer}, {Name: "subject", Type: stmt.Integer}, {Name: "until", Type: stmt.Integer}, {Name: "quarter", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{"viewer-one", int64(1), int64(11), int64(1000), int64(0)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"listening_time_span_identity"}}},
		{ID: wc008ProgressCollision, Owner: "store/writer WC progress", Kind: stmt.Read, CallSites: []string{"store/writer/wc_progress_008_draft.go"}, SQL: `SELECT id FROM listening_time_spans INDEXED BY listening_time_span_identity WHERE profile_id=? AND activity_generation=? AND subject_id=? AND until_ms=? AND from_ms=?`, Params: []stmt.Param{{Name: "profile", Type: stmt.Text}, {Name: "activity", Type: stmt.Integer}, {Name: "subject", Type: stmt.Integer}, {Name: "until", Type: stmt.Integer}, {Name: "from", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{"viewer-one", int64(1), int64(11), int64(1000), int64(0)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"listening_time_span_identity"}}},
		{ID: wc008ProgressExtend, Owner: "store/writer WC progress", Kind: stmt.Write, CallSites: []string{"store/writer/wc_progress_008_draft.go"}, SQL: `UPDATE listening_time_spans SET until_ms=? WHERE id=? AND profile_id=? AND activity_generation=? AND subject_id=? AND from_ms=? AND until_ms=? AND copies=1`, Params: []stmt.Param{{Name: "next_until", Type: stmt.Integer}, {Name: "id", Type: stmt.Integer}, {Name: "profile", Type: stmt.Text}, {Name: "activity", Type: stmt.Integer}, {Name: "subject", Type: stmt.Integer}, {Name: "from", Type: stmt.Integer}, {Name: "until", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{int64(1000), int64(1), "viewer-one", int64(1), int64(11), int64(0), int64(1000)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"listening_time_spans"}}},
		{ID: wc008ProgressSpanFind, Owner: "store/writer WC progress", Kind: stmt.Read, CallSites: []string{"store/writer/wc_progress_008_draft.go"}, SQL: `SELECT copies FROM listening_time_spans INDEXED BY listening_time_span_identity WHERE profile_id=? AND activity_generation=? AND subject_id=? AND until_ms=? AND from_ms=?`, Params: []stmt.Param{{Name: "profile", Type: stmt.Text}, {Name: "activity", Type: stmt.Integer}, {Name: "subject", Type: stmt.Integer}, {Name: "until", Type: stmt.Integer}, {Name: "from", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{"viewer-one", int64(1), int64(11), int64(1000), int64(0)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"listening_time_span_identity"}}},
		{ID: wc008ProgressSpanUpdate, Owner: "store/writer WC progress", Kind: stmt.Write, CallSites: []string{"store/writer/wc_progress_008_draft.go"}, SQL: `UPDATE listening_time_spans INDEXED BY listening_time_span_identity SET copies=? WHERE profile_id=? AND activity_generation=? AND subject_id=? AND until_ms=? AND from_ms=? AND copies=? RETURNING id`, Params: []stmt.Param{{Name: "next", Type: stmt.Integer}, {Name: "profile", Type: stmt.Text}, {Name: "activity", Type: stmt.Integer}, {Name: "subject", Type: stmt.Integer}, {Name: "until", Type: stmt.Integer}, {Name: "from", Type: stmt.Integer}, {Name: "old", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{int64(1000), "viewer-one", int64(1), int64(11), int64(1000), int64(0), int64(1)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"listening_time_span_identity"}}},
		{ID: wc008ProgressSpan, Owner: "store/writer WC progress", Kind: stmt.Write, CallSites: []string{"store/writer/wc_progress_008_draft.go"}, SQL: `INSERT INTO listening_time_spans(profile_id,activity_generation,subject_id,from_ms,until_ms,copies) VALUES(?,?,?,?,?,?) RETURNING id`, Params: []stmt.Param{{Name: "profile", Type: stmt.Text}, {Name: "activity", Type: stmt.Integer}, {Name: "subject", Type: stmt.Integer}, {Name: "from", Type: stmt.Integer}, {Name: "until", Type: stmt.Integer}, {Name: "copies", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{"viewer-one", int64(1), int64(11), int64(0), int64(1000), int64(1)}}}, Plan: stmt.Plan{Hot: true}},
		{ID: wc008ProgressItemBucketRead, Owner: "store/writer WC progress", Kind: stmt.Read, CallSites: []string{"store/writer/wc_progress_008_draft.go"}, SQL: `SELECT started_count,completed_count,played_ms FROM listening_item_buckets WHERE profile_id=? AND activity_generation=? AND grain=? AND bucket_ms=? AND subject_id=?`, Params: []stmt.Param{{Name: "profile", Type: stmt.Text}, {Name: "activity", Type: stmt.Integer}, {Name: "grain", Type: stmt.Text}, {Name: "bucket", Type: stmt.Integer}, {Name: "subject", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{"viewer-one", int64(1), "quarter", int64(0), int64(11)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"listening_item_buckets"}}},
		{ID: wc008ProgressItemBucketUpdate, Owner: "store/writer WC progress", Kind: stmt.Write, CallSites: []string{"store/writer/wc_progress_008_draft.go"}, SQL: `UPDATE listening_item_buckets SET started_count=?,completed_count=?,played_ms=? WHERE profile_id=? AND activity_generation=? AND grain=? AND bucket_ms=? AND subject_id=? AND started_count=? AND completed_count=? AND played_ms=? RETURNING played_ms`, Params: []stmt.Param{{Name: "next_started", Type: stmt.Integer}, {Name: "next_completed", Type: stmt.Integer}, {Name: "next_played", Type: stmt.Integer}, {Name: "profile", Type: stmt.Text}, {Name: "activity", Type: stmt.Integer}, {Name: "grain", Type: stmt.Text}, {Name: "bucket", Type: stmt.Integer}, {Name: "subject", Type: stmt.Integer}, {Name: "old_started", Type: stmt.Integer}, {Name: "old_completed", Type: stmt.Integer}, {Name: "old_played", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{int64(1), int64(0), int64(1000), "viewer-one", int64(1), "quarter", int64(0), int64(11), int64(0), int64(0), int64(0)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"listening_item_buckets"}}},
		{ID: wc008ProgressItemBucket, Owner: "store/writer WC progress", Kind: stmt.Write, CallSites: []string{"store/writer/wc_progress_008_draft.go"}, SQL: `INSERT INTO listening_item_buckets(profile_id,activity_generation,grain,bucket_ms,subject_id,started_count,completed_count,played_ms) VALUES(?,?,?,?,?,?,?,?) RETURNING played_ms`, Params: []stmt.Param{{Name: "profile", Type: stmt.Text}, {Name: "activity", Type: stmt.Integer}, {Name: "grain", Type: stmt.Text}, {Name: "bucket", Type: stmt.Integer}, {Name: "subject", Type: stmt.Integer}, {Name: "started", Type: stmt.Integer}, {Name: "completed", Type: stmt.Integer}, {Name: "credit", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{"viewer-one", int64(1), "quarter", int64(0), int64(11), int64(0), int64(0), int64(1000)}}}, Plan: stmt.Plan{Hot: true}},
		{ID: wc008ProgressProfileBucketRead, Owner: "store/writer WC progress", Kind: stmt.Read, CallSites: []string{"store/writer/wc_progress_008_draft.go"}, SQL: `SELECT started_count,completed_count,played_ms FROM listening_profile_buckets WHERE profile_id=? AND activity_generation=? AND grain=? AND bucket_ms=?`, Params: []stmt.Param{{Name: "profile", Type: stmt.Text}, {Name: "activity", Type: stmt.Integer}, {Name: "grain", Type: stmt.Text}, {Name: "bucket", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{"viewer-one", int64(1), "quarter", int64(0)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"listening_profile_buckets"}}},
		{ID: wc008ProgressProfileBucketUpdate, Owner: "store/writer WC progress", Kind: stmt.Write, CallSites: []string{"store/writer/wc_progress_008_draft.go"}, SQL: `UPDATE listening_profile_buckets SET started_count=?,completed_count=?,played_ms=? WHERE profile_id=? AND activity_generation=? AND grain=? AND bucket_ms=? AND started_count=? AND completed_count=? AND played_ms=? RETURNING played_ms`, Params: []stmt.Param{{Name: "next_started", Type: stmt.Integer}, {Name: "next_completed", Type: stmt.Integer}, {Name: "next_played", Type: stmt.Integer}, {Name: "profile", Type: stmt.Text}, {Name: "activity", Type: stmt.Integer}, {Name: "grain", Type: stmt.Text}, {Name: "bucket", Type: stmt.Integer}, {Name: "old_started", Type: stmt.Integer}, {Name: "old_completed", Type: stmt.Integer}, {Name: "old_played", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{int64(1), int64(0), int64(1000), "viewer-one", int64(1), "quarter", int64(0), int64(0), int64(0), int64(0)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"listening_profile_buckets"}}},
		{ID: wc008ProgressProfileBucket, Owner: "store/writer WC progress", Kind: stmt.Write, CallSites: []string{"store/writer/wc_progress_008_draft.go"}, SQL: `INSERT INTO listening_profile_buckets(profile_id,activity_generation,grain,bucket_ms,started_count,completed_count,played_ms) VALUES(?,?,?,?,?,?,?) RETURNING played_ms`, Params: []stmt.Param{{Name: "profile", Type: stmt.Text}, {Name: "activity", Type: stmt.Integer}, {Name: "grain", Type: stmt.Text}, {Name: "bucket", Type: stmt.Integer}, {Name: "started", Type: stmt.Integer}, {Name: "completed", Type: stmt.Integer}, {Name: "credit", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{"viewer-one", int64(1), "quarter", int64(0), int64(0), int64(0), int64(1000)}}}, Plan: stmt.Plan{Hot: true}},
		{ID: wc008ProgressLeaf, Owner: "store/writer WC progress", Kind: stmt.Write, CallSites: []string{"store/writer/wc_progress_008_draft.go"}, SQL: `UPDATE personal_items INDEXED BY personal_current_live SET played_ms=played_ms+? WHERE profile_id=? AND item_id=? AND item_public_id=? AND activity_generation=? AND played_ms<=? RETURNING played_ms,revision`, Params: []stmt.Param{{Name: "credit", Type: stmt.Integer}, {Name: "profile", Type: stmt.Text}, {Name: "subject", Type: stmt.Integer}, {Name: "public_id", Type: stmt.Blob}, {Name: "activity", Type: stmt.Integer}, {Name: "max_old", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{int64(1000), "viewer-one", int64(11), []byte{3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(1), int64(9223372036854775807)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"personal_current_live"}}},
		{ID: wc008ProgressViewerRevision, Owner: "store/writer WC progress", Kind: stmt.Write, CallSites: []string{"store/writer/wc_progress_008_draft.go"}, SQL: `UPDATE viewer_revisions SET observation_revision=observation_revision+1 WHERE profile_id=? AND library_id=? AND observation_revision>=0 AND observation_revision<? RETURNING observation_revision,revision`, Params: []stmt.Param{{Name: "profile", Type: stmt.Text}, {Name: "library", Type: stmt.Text}, {Name: "max_old", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{"viewer-one", "library-one", int64(9223372036854775807)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_viewer_revisions_1"}}},
		{ID: wc008ProgressHistoryRevision, Owner: "store/writer WC progress", Kind: stmt.Write, CallSites: []string{"store/writer/wc_progress_008_draft.go"}, SQL: `UPDATE personal_activity_revisions SET revision=revision+1 WHERE profile_id=? AND activity_generation=? AND history_policy_generation=? AND revision>=0 AND revision<? RETURNING revision`, Params: []stmt.Param{{Name: "profile", Type: stmt.Text}, {Name: "activity", Type: stmt.Integer}, {Name: "history_policy", Type: stmt.Integer}, {Name: "max_old", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{"viewer-one", int64(1), int64(1), int64(9223372036854775807)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_personal_activity_revisions_1"}}},

		{ID: wc008ProgressAdvance, Owner: "store/writer WC progress", Kind: stmt.Write, CallSites: []string{"store/writer/wc_progress_008_draft.go"}, SQL: `UPDATE playback_personal_claims SET pending_time_cursor_ms=? WHERE playback_id=? AND command_incarnation=?
 AND command_last_sequence=? AND command_acknowledged_sequence=? AND pending_time_cursor_ms=? AND pending_time_sequence=?
 AND pending_time_start_event_id=? AND source_occurrence_generation=? RETURNING control_variable_bytes`, Params: []stmt.Param{{Name: "next", Type: stmt.Integer}, {Name: "playback", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "last", Type: stmt.Integer}, {Name: "ack", Type: stmt.Integer}, {Name: "cursor", Type: stmt.Integer}, {Name: "report", Type: stmt.Integer}, {Name: "start_id", Type: stmt.Integer}, {Name: "occurrence", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{int64(1000), "playback-音", []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(0), int64(0), int64(0), int64(13), int64(1), int64(5)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_personal_claims_2"}}},
		{ID: wc008ProgressClear, Owner: "store/writer WC progress", Kind: stmt.Write, CallSites: []string{"store/writer/wc_progress_008_draft.go"}, SQL: `UPDATE playback_personal_claims SET pending_time_from_ms=?,pending_time_until_ms=?,pending_time_cursor_ms=?,
 pending_time_activity_generation=?,pending_time_remember_generation=?,pending_time_clock_incarnation=?,
 pending_time_start_event_id=?,pending_time_occurrence_generation=?
 WHERE playback_id=? AND command_incarnation=? AND command_last_sequence=? AND command_acknowledged_sequence=?
 AND pending_time_cursor_ms=? AND pending_time_sequence=? AND pending_time_start_event_id=? AND source_occurrence_generation=? RETURNING control_variable_bytes`, Params: []stmt.Param{{Name: "clear_from", Type: stmt.Integer, Nullable: true}, {Name: "clear_until", Type: stmt.Integer, Nullable: true}, {Name: "clear_cursor", Type: stmt.Integer, Nullable: true}, {Name: "clear_activity", Type: stmt.Integer, Nullable: true}, {Name: "clear_policy", Type: stmt.Integer, Nullable: true}, {Name: "clear_clock", Type: stmt.Blob, Nullable: true}, {Name: "clear_start", Type: stmt.Integer, Nullable: true}, {Name: "clear_occurrence", Type: stmt.Integer, Nullable: true}, {Name: "playback", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "last", Type: stmt.Integer}, {Name: "ack", Type: stmt.Integer}, {Name: "cursor", Type: stmt.Integer}, {Name: "report", Type: stmt.Integer}, {Name: "start_id", Type: stmt.Integer}, {Name: "occurrence", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{nil, nil, nil, nil, nil, nil, nil, nil, "playback-音", []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(0), int64(0), int64(0), int64(13), int64(1), int64(5)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_personal_claims_2"}}},
		{ID: wc008ProgressReceipt, Owner: "store/writer WC progress", Kind: stmt.Write, CallSites: []string{"store/writer/wc_progress_receipts_008_draft.go"}, SQL: `INSERT INTO playback_claim_command_receipts(playback_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,applied_ms,source_accepted_ordinal,source_last_accepted_sequence,source_report_sequence,source_occurrence_generation,source_activity_generation,source_history_policy_generation,source_clock_incarnation,source_start_event_id,source_pending_from_ms,source_pending_until_ms,source_pending_cursor_ms,result_start_event_id,result_report_sequence,result_pending_sequence,result_pending_from_ms,result_pending_until_ms,result_pending_cursor_ms,result_viewer_revision,result_observation_revision,result_history_revision,result_personal_revision,consumed_from_ms,consumed_until_ms,result_next_cursor_ms,result_remaining_until_ms,finished,credited_ms,result_count,result_digest)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`, Params: []stmt.Param{{Name: "playback", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "sequence", Type: stmt.Integer}, {Name: "operation", Type: stmt.Blob}, {Name: "request", Type: stmt.Blob}, {Name: "kind", Type: stmt.Text}, {Name: "result_version", Type: stmt.Integer}, {Name: "applied", Type: stmt.Integer}, {Name: "ordinal", Type: stmt.Integer}, {Name: "last_accepted", Type: stmt.Integer}, {Name: "report", Type: stmt.Integer}, {Name: "occurrence", Type: stmt.Integer}, {Name: "activity", Type: stmt.Integer}, {Name: "policy", Type: stmt.Integer}, {Name: "clock", Type: stmt.Blob}, {Name: "start", Type: stmt.Integer}, {Name: "from", Type: stmt.Integer}, {Name: "until", Type: stmt.Integer}, {Name: "cursor", Type: stmt.Integer}, {Name: "result_start", Type: stmt.Integer}, {Name: "result_report", Type: stmt.Integer}, {Name: "result_pending_sequence", Type: stmt.Integer}, {Name: "result_pending_from", Type: stmt.Integer, Nullable: true}, {Name: "result_pending_until", Type: stmt.Integer, Nullable: true}, {Name: "result_pending_cursor", Type: stmt.Integer, Nullable: true}, {Name: "viewer", Type: stmt.Integer}, {Name: "observation", Type: stmt.Integer}, {Name: "history", Type: stmt.Integer}, {Name: "personal", Type: stmt.Integer}, {Name: "consumed_from", Type: stmt.Integer}, {Name: "consumed_until", Type: stmt.Integer}, {Name: "next", Type: stmt.Integer}, {Name: "remaining", Type: stmt.Integer, Nullable: true}, {Name: "finished", Type: stmt.Integer}, {Name: "credit", Type: stmt.Integer}, {Name: "count", Type: stmt.Integer}, {Name: "result", Type: stmt.Blob}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{"playback-音", []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(1), []byte{2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, []byte{4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "drain_piece", int64(1), int64(1000), int64(71), int64(17), int64(13), int64(5), int64(1), int64(1), []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(1), int64(0), int64(1000), int64(0), int64(1), int64(17), int64(13), nil, nil, nil, int64(23), int64(42), int64(32), int64(19), int64(0), int64(1000), int64(1000), nil, int64(1), int64(1000), int64(1), []byte{5, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}}}}, Plan: stmt.Plan{Hot: true}},
		{ID: wc008ProgressOwnerAdvance, Owner: "store/writer WC progress", Kind: stmt.Write, CallSites: []string{"store/writer/wc_progress_receipts_008_draft.go"}, SQL: `UPDATE playback_personal_claims SET command_last_sequence=?,receipt_rows=?,receipt_variable_bytes=?
 WHERE playback_id=? AND command_incarnation=? AND command_last_sequence=? AND command_acknowledged_sequence=?
 AND receipt_rows=? AND receipt_variable_bytes=?`, Params: []stmt.Param{{Name: "next", Type: stmt.Integer}, {Name: "rows", Type: stmt.Integer}, {Name: "bytes", Type: stmt.Integer}, {Name: "playback", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "last", Type: stmt.Integer}, {Name: "ack", Type: stmt.Integer}, {Name: "old_rows", Type: stmt.Integer}, {Name: "old_bytes", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{int64(1000), int64(1), int64(137), "playback-音", []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(0), int64(0), int64(0), int64(0)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_personal_claims_2"}}},
	}, append(wc008HistoryDefinitions(), wc008CaptureDefinitions()...)...)
}
