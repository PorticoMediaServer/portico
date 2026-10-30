package identity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/phasedpurge"
)

// The profile purge is the physical half of profile retirement. Retirement already
// committed the denial: direct_profiles.deleted=1 under one lifetime, the exact
// account/profile lifetime in security_purge_jobs, and the immediate authority
// fence. This owner runs only the compiled units in profilePurgeRegistry through
// the shared phasedpurge executor, one durable FULL turn at a time, below
// foreground traffic.
//
// This file keeps only the profile owner's part: literal SQL scopes, job
// selection, target proof, cursor save and finalization. Cursor typing, the turn
// loop, the read/effect meters and verification live in phasedpurge.Turn.
//
// Library media, completed recordings and the owner's play history are not
// profile-private purge resources. No unit changes recordings or retained plays;
// creator attribution continues to point at the sanitized removed-profile row.
const (
	// purgeOwnerReadBound reserves the reads the owner spends outside any unit:
	// three indexed job-selection probes, the target/custody proof, and the two
	// total_changes() meter reads, Save, and the final target/sanitization/delete probes.
	purgeOwnerReadBound = 12
	purgeParentProbe    = 16
	purgeVerifyPhase    = 90
	purgeFinalizePhase  = 91
	// purgeHardBlockNextMS is the safe-integer sentinel that parks a job whose
	// stored cursor/plan/budget/progress contradicts the compiled plan. It is not
	// a backoff: an endless retry of the same defect would hide the defect.
	purgeHardBlockNextMS = int64(9007199254740991)
	// purgePlanVersion changes whenever a builder or predicate semantic changes.
	// It is mixed into the cursor digest together with the literal SQL of every
	// unit, so a builder edit cannot silently reinterpret an old cursor.
	purgePlanVersion = "p2-62-profile-purge-v6-share-edges"
)

// purgeRevokeFamilySQL is the one compiled statement of the revocation unit.
const purgeRevokeFamilySQL = `SELECT id FROM authorization_session_families WHERE authority='local' AND account_id=? AND profile_id=? AND revoked=0 ORDER BY id LIMIT 1`

// purgeProcessEpoch names this process's claim on a running job. A restart is a
// new epoch: a job left running is resumed, never treated as finished.
var purgeProcessEpoch = Token()[:22]

var (
	errPurgeTargetChanged = errors.New("profile purge target lifetime no longer matches the retired root")
	errPurgeCursorDigest  = errors.New("profile purge cursor does not belong to this compiled plan")
)

// purgePlanDigest is the deterministic identity of the compiled plan. It covers
// every unit's name, phase, table, action, scope, predicates, guards, keys and
// read bound, so a cursor written by another plan (or a tampered cursor) is
// rejected as blocked instead of being applied to a different plan. It never
// contains process randomness: a restart of the same binary must accept its own
// cursor.
var purgePlanDigest = func() string {
	h := sha256.New()
	write := func(parts ...string) {
		for _, part := range parts {
			fmt.Fprintf(h, "%d:%s|", len(part), part)
		}
		fmt.Fprint(h, "\n")
	}
	for i := range profilePurgeRegistry {
		st := &profilePurgeRegistry[i]
		write("unit", st.name, st.table, strconv.Itoa(st.phase), strconv.Itoa(int(st.action)),
			strconv.Itoa(int(st.scope)), st.extra, st.hopExtra, st.set, st.keys, st.hopScopeName(), strconv.Itoa(st.readBound),
			strconv.FormatBool(st.scan), strconv.FormatBool(st.rewind), strconv.FormatBool(st.tokenDrain), strconv.FormatBool(st.avatarObject), strconv.FormatBool(st.preparationRoot))
		for _, h := range st.of {
			write("hop", h.table, h.keys, h.fk)
		}
		for _, g := range st.guards {
			write("guard", g.table, g.fk, g.keys)
		}
		// Literal builder output, not just the descriptor: a changed predicate,
		// guard alias, CTE form or keyset builder must change the digest.
		write("sql", purgeStepSQL(st, ""))
		write("keyset", purgeStepSQL(st, " AND "+purgeTuple("p", st.keys)+" > "+purgePlaceholders(len(purgeSplit(st.keys)))))
		if st.action == purgeRevokeFamily {
			write("revoke", purgeRevokeFamilySQL)
		}
	}
	write("version", purgePlanVersion)
	write("token-probe", purgeTokenProbeSQL)
	write("token-delete", purgeTokenDrainSQL)
	write("preparation", purgePreparationActorSQL)
	for _, q := range purgeTokenChildSQL() {
		write("token-child", q)
	}
	for _, k := range []string{"effect", strconv.Itoa(phasedpurge.EffectLimit), "read", strconv.Itoa(phasedpurge.ReadLimit)} {
		write("engine", k)
	}
	return hex.EncodeToString(h.Sum(nil))
}()

// hopScopeName renders the hop scope for the digest without printing an integer.
func (st *purgeStep) hopScopeName() string { return strconv.Itoa(int(st.hopScope)) }

type purgeAction uint8

const (
	purgeDelete purgeAction = iota + 1
	purgeScrub
	purgeRevokeFamily
)

// purgeScope names the exact predicate that ties a row to the retired profile.
// Values are always derived from the job row, never from a request.
type purgeScope uint8

const (
	scopeNone          purgeScope = iota
	scopePersonal                 // profile_id = PersonalKey(local, account, profile)
	scopeTuple                    // authority/account_id/profile_id
	scopeTuple2                   // account_id/profile_id
	scopeViewerKey                // profile_key = JSON ["local",account,profile]
	scopeOwnerKey                 // owner_key = PersonalKey
	scopeOwned                    // owner_authority/owner_account/owner_profile
	scopeActor                    // actor = JSON ["local",account,profile]
	scopeConsole                  // scope IN (viewer, profile:, device:web|mobile|television, report:)
	scopeAudiences                // api_change_pending/api_events audiences of this profile
	scopeProfileID                // profile_id = the raw profile id
	scopeDeviceLast               // account_id + last_profile_id
	scopeRecipient                // recipient_profile_id
	scopeP251                     // account scope row + profile id + exact profile incarnation
	scopeRawActor                 // actor authority/account/profile, the raw tuple
	scopeCursor                   // profile_cursor = the retiring personal key
	scopeRoot                     // account + profile + exact profile incarnation (P251 root)
	scopeRetained                 // retained_title_state: raw profile id OR canonical owner key
	scopePreparedActor            // prepared_media_jobs principal_json actor tuple
)

type purgeHop struct{ table, keys, fk string }

func hop(table, keys, fk string) purgeHop { return purgeHop{table: table, keys: keys, fk: fk} }

type purgeGuard struct{ table, fk, keys string }

func guard(table, fk, keys string) purgeGuard { return purgeGuard{table: table, fk: fk, keys: keys} }

// purgeStep is one compiled physical unit. All identifiers are constants in this
// package; the job supplies values only.
type purgeStep struct {
	phase           int
	name            string
	table           string
	action          purgeAction
	scope           purgeScope // predicate on the step's own table, if any
	hopScope        purgeScope // predicate at the root of the parent chain
	of              []purgeHop
	guards          []purgeGuard
	extra           string
	hopExtra        string
	set             string
	keys            string // keyset columns, table primary-key order
	keyKinds        []phasedpurge.Kind
	readBound       int  // compiled per-turn read proof for this unit
	scan            bool // scope-derived unit: own the typed cursor, return Next
	tokenDrain      bool // one token: drain its scope rows, then the token
	avatarObject    bool // unshared avatar rendition object of the retiring link
	preparationRoot bool // atomic final actor+root after private children
	// rewind marks a parent unit that cannot delete while an owned child row is
	// still present: the shared engine restarts the phase instead of advancing.
	rewind bool
}

type purgeJob struct {
	id, account, profile             string
	accountLifetime, profileLifetime string
	cursor, state, epoch             string
	attempt, due                     int64
}

// PurgeProfileStep performs at most one durable turn of the earliest due purge
// job. It reports whether that turn did work; false means no job is due. Completion
// is never a boolean: a job is finished only when its row is gone in the final
// commit.
func (s *Service) PurgeProfileStep(ctx context.Context) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Physical cleanup runs on the background maintenance class with FULL
	// durability: the authority decision is already committed, so it must not
	// compete with user writes, but a committed turn must survive a power cut.
	ctx = dbwork.WithDurableUser(ctx)
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassMaintenance)
	if err != nil {
		return false, err
	}
	tx := gated.Tx()
	job, err := purgeNextJobTx(ctx, tx)
	if err != nil {
		gated.Rollback()
		return false, err
	}
	if job == nil {
		gated.Rollback()
		return false, nil
	}
	plan := profilePurgeUnits(s, job)
	stored, err := purgeDecodeStoredCursor(job.cursor)
	if err != nil {
		gated.Rollback()
		return false, errors.Join(err, s.markProfilePurgeBlocked(job, err))
	}
	cursor, err := phasedpurge.DecodeCursor(stored, plan)
	if err != nil {
		// A stored cursor that does not name a real unit or a real typed key is a
		// source defect, not a reason to start the purge over silently.
		gated.Rollback()
		return false, errors.Join(err, s.markProfilePurgeBlocked(job, err))
	}
	owner := phasedpurge.Owner{
		ReadBound: purgeOwnerReadBound,
		Before: func(bctx context.Context, btx *sql.Tx) error {
			if err := purgeTargetTx(bctx, btx, job); err != nil {
				return err
			}
			if job.state == "blocked" {
				_, err := btx.ExecContext(bctx, `UPDATE security_purge_jobs SET state='running' WHERE job_id=? AND profile_incarnation=?`, job.id, job.profileLifetime)
				return err
			}
			return nil
		},
		Save: func(sctx context.Context, stx *sql.Tx, c phasedpurge.Cursor) error {
			return purgeSaveCursorTx(sctx, stx, job, c)
		},
		Finalize: func(fctx context.Context, ftx *sql.Tx) error {
			return s.purgeFinalizeTx(fctx, ftx, job)
		},
	}
	result, err := phasedpurge.Turn(ctx, tx, plan, cursor, owner)
	if err != nil {
		gated.Rollback()
		return false, errors.Join(err, s.markProfilePurgeBlocked(job, err))
	}
	if err = gated.Commit(); err != nil {
		return false, err
	}
	return result.Worked, nil
}

// profilePurgeUnits binds the compiled registry to this turn's exact job. The
// closures carry the owner predicates; the engine supplies the transaction.
func profilePurgeUnits(s *Service, job *purgeJob) []phasedpurge.Unit {
	out := make([]phasedpurge.Unit, len(profilePurgeRegistry))
	for i := range profilePurgeRegistry {
		st := &profilePurgeRegistry[i]
		out[i] = phasedpurge.Unit{
			Phase:     st.phase,
			Name:      st.name,
			KeyKinds:  st.keyKinds,
			ReadBound: st.readBound,
			Run: func(ctx context.Context, tx *sql.Tx, key []phasedpurge.Key) (phasedpurge.Outcome, error) {
				return s.purgeStepTx(ctx, tx, job, st, key)
			},
			Empty: func(ctx context.Context, tx *sql.Tx) (bool, error) {
				var found bool
				var err error
				switch {
				case st.tokenDrain:
					found, err = purgeTokenDrainExistsTx(ctx, tx, job)
				case st.avatarObject:
					found, err = purgeAvatarObjectExistsTx(ctx, tx, job)
				case st.preparationRoot:
					var id string
					err = tx.QueryRowContext(ctx, purgePreparationActorSQL, job.account, job.profile).Scan(&id)
					found = err == nil
					if errors.Is(err, sql.ErrNoRows) {
						err = nil
					}
				default:
					found, err = purgeCandidateExistsTx(ctx, tx, job, st)
				}
				return !found, err
			},
		}
	}
	return out
}

// purgeNextJobTx returns the earliest due job using three bounded probes, each of
// which reads one candidate through the (state,next_attempt_ms,job_id) index. This
// process is the database's only writer, so a job left running by a predecessor is
// simply resumed.
func purgeNextJobTx(ctx context.Context, tx *sql.Tx) (*purgeJob, error) {
	now := time.Now().UnixMilli()
	probes := []struct {
		query string
		args  []any
	}{
		{`SELECT job_id,account_id,profile_id,account_incarnation,profile_incarnation,cursor_key,attempt,state,process_epoch,next_attempt_ms
FROM security_purge_jobs WHERE state='pending' AND next_attempt_ms<=?
ORDER BY next_attempt_ms,job_id LIMIT 1`, []any{now}},
		{`SELECT job_id,account_id,profile_id,account_incarnation,profile_incarnation,cursor_key,attempt,state,process_epoch,next_attempt_ms
FROM security_purge_jobs WHERE state='blocked' AND next_attempt_ms<=?
ORDER BY next_attempt_ms,job_id LIMIT 1`, []any{now}},
		{`SELECT job_id,account_id,profile_id,account_incarnation,profile_incarnation,cursor_key,attempt,state,process_epoch,next_attempt_ms
FROM security_purge_jobs WHERE state='running'
ORDER BY next_attempt_ms,job_id LIMIT 1`, nil},
	}
	var best *purgeJob
	for _, probe := range probes {
		var j purgeJob
		err := tx.QueryRowContext(ctx, probe.query, probe.args...).Scan(&j.id, &j.account, &j.profile, &j.accountLifetime, &j.profileLifetime, &j.cursor, &j.attempt, &j.state, &j.epoch, &j.due)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if best == nil || j.due < best.due || j.due == best.due && j.id < best.id {
			candidate := j
			best = &candidate
		}
	}
	return best, nil
}

// purgeTargetTx proves this job still names the exact retired root it was created
// for. A re-created profile id has a different lifetime, and an account
// incarnation change supersedes the job; neither may inherit the old purge.
func purgeTargetTx(ctx context.Context, tx *sql.Tx, j *purgeJob) error {
	var account, lifetime, membership string
	var deleted bool
	err := tx.QueryRowContext(ctx, `SELECT p.account_id,p.lifetime,p.deleted,m.incarnation
FROM direct_profiles p JOIN direct_memberships m ON m.account_id=p.account_id
WHERE p.id=?`, j.profile).Scan(&account, &lifetime, &deleted, &membership)
	if err != nil {
		return fmt.Errorf("%w: retired root is not readable", errPurgeTargetChanged)
	}
	if account != j.account || lifetime != j.profileLifetime || membership != j.accountLifetime || !deleted {
		return errPurgeTargetChanged
	}
	return nil
}

// purgeSaveCursorTx writes the engine's cursor and phase progress in the same
// transaction as the effect. Save is not called for the finalizing turn.
func purgeSaveCursorTx(ctx context.Context, tx *sql.Tx, j *purgeJob, c phasedpurge.Cursor) error {
	raw, err := purgeEncodeStoredCursor(c)
	if err != nil {
		return err
	}
	phase := purgeVerifyPhase
	if c.Unit < len(profilePurgeRegistry) {
		phase = profilePurgeRegistry[c.Unit].phase
	} else if c.Verify >= len(profilePurgeRegistry) {
		phase = purgeFinalizePhase
	}
	_, err = tx.ExecContext(ctx, `UPDATE security_purge_jobs SET phase=?,cursor_key=?,state='pending',process_epoch=?,attempt=0,next_attempt_ms=0
WHERE job_id=? AND profile_incarnation=?`, phase, raw, purgeProcessEpoch, j.id, j.profileLifetime)
	return err
}

// purgeStepTx is one compiled unit for the shared engine. Destructive units seek
// the lowest remaining indexed head and never persist a key: removing or scrubbing
// the row removes it from the pending predicate, so the seek stays monotone.
func (s *Service) purgeStepTx(ctx context.Context, tx *sql.Tx, j *purgeJob, st *purgeStep, key []phasedpurge.Key) (phasedpurge.Outcome, error) {
	if st.action == purgeRevokeFamily {
		return purgeRevokeFamilyTx(ctx, tx, j)
	}
	if st.tokenDrain {
		return purgeTokenDrainTx(ctx, tx, j)
	}
	if st.avatarObject {
		return purgeAvatarObjectTx(ctx, tx, j)
	}
	if st.preparationRoot {
		return purgePreparationRootTx(ctx, tx, j)
	}
	if st.table == "profile_avatars" && st.action == purgeDelete {
		// The avatar link is the mapper to the rendition bytes. Refuse (rewind)
		// while a unique object still exists so custody is never lost; shared
		// digests are not its bytes and do not block.
		unique, err := purgeAvatarObjectExistsTx(ctx, tx, j)
		if err != nil {
			return phasedpurge.Outcome{}, err
		}
		if unique {
			return phasedpurge.Outcome{Rewind: true}, nil
		}
	}
	args := j.purgeArgs(st)
	keyset := ""
	if st.scan && len(key) > 0 {
		values, err := phasedpurge.SQLKeys(key)
		if err != nil {
			return phasedpurge.Outcome{}, fmt.Errorf("profile purge unit %s cursor: %w", st.name, err)
		}
		keyset = " AND " + purgeTuple("p", st.keys) + " > " + purgePlaceholders(len(values))
		args = append(args, values...)
	}
	rows, err := tx.QueryContext(ctx, purgeStepSQL(st, keyset), args...)
	if err != nil {
		return phasedpurge.Outcome{}, fmt.Errorf("profile purge unit %s: %w", st.name, err)
	}
	width := len(purgeSplit(st.keys))
	values := make([]any, width)
	ptrs := make([]any, width)
	for i := range values {
		ptrs[i] = &values[i]
	}
	count := 0
	for rows.Next() {
		if err = rows.Scan(ptrs...); err != nil {
			rows.Close()
			return phasedpurge.Outcome{}, fmt.Errorf("profile purge unit %s: %w", st.name, err)
		}
		count++
	}
	err = rows.Err()
	// Release the statement before any probe runs on the same connection.
	rows.Close()
	if err != nil {
		return phasedpurge.Outcome{}, fmt.Errorf("profile purge unit %s: %w", st.name, err)
	}
	if count > 0 {
		if st.scan {
			next, keyErr := phasedpurge.Keys(values...)
			if keyErr != nil {
				return phasedpurge.Outcome{}, fmt.Errorf("profile purge unit %s cursor: %w", st.name, keyErr)
			}
			return phasedpurge.Outcome{Changed: true, Next: next}, nil
		}
		return phasedpurge.Outcome{Changed: true}, nil
	}
	if len(st.guards) > 0 {
		// Distinguish "no parent row is left at all" from "the next parent row is
		// still waiting on one of its owned children". Only the second case rewinds.
		found, probeErr := purgeCandidateExistsTx(ctx, tx, j, st)
		if probeErr != nil {
			return phasedpurge.Outcome{}, probeErr
		}
		if found {
			return phasedpurge.Outcome{Rewind: true}, nil
		}
	}
	return phasedpurge.Outcome{Drained: true}, nil
}

// purgeRevokeFamilyTx closes one family of the retired profile per turn. It calls
// RevokeFamilyTx, the generic exported revocation (revoked=1 + reason/at); it does
// NOT call the integration OnFamilyRevokedTx hook, so no hook may be claimed to
// fire here. Denial is already in force from the retirement's deleted-root fence
// and the same-writer reader/session closures, and this plan erases the playback
// and queue runtime rows itself in the session component.
func purgeRevokeFamilyTx(ctx context.Context, tx *sql.Tx, j *purgeJob) (phasedpurge.Outcome, error) {
	var id string
	err := tx.QueryRowContext(ctx, purgeRevokeFamilySQL, j.account, j.profile).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return phasedpurge.Outcome{Drained: true}, nil
	}
	if err != nil {
		return phasedpurge.Outcome{}, err
	}
	// The baseline revocation trigger updates every legacy session in the
	// family. Those rows must already have been drained by earlier components;
	// a one-family LIMIT alone cannot bound that trigger fanout.
	var sessions bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_sessions WHERE account_id=? AND profile_id=?)`, j.account, j.profile).Scan(&sessions); err != nil {
		return phasedpurge.Outcome{}, err
	}
	if sessions {
		return phasedpurge.Outcome{}, fmt.Errorf("%w: playback sessions precede family revocation", phasedpurge.ErrNoProgress)
	}
	if err = RevokeFamilyTx(ctx, tx, id, RevokedAdminRevoke); err != nil {
		return phasedpurge.Outcome{}, fmt.Errorf("profile purge family revoke: %w", err)
	}
	return phasedpurge.Outcome{Changed: true}, nil
}

// purgeCandidateExistsTx is the unguarded exact-owner probe. The shared engine uses
// it for both the rewind decision and the final no-private-child proof, so the
// proof asks precisely the unit's owner predicate and never the guarded delete.
func purgeCandidateExistsTx(ctx context.Context, tx *sql.Tx, j *purgeJob, st *purgeStep) (bool, error) {
	args := j.purgeArgs(st)
	query := `SELECT EXISTS(SELECT 1 FROM ` + st.table + ` AS p`
	if pred := purgePredicate(st); pred != "" {
		query += ` WHERE ` + pred
	}
	query += `)`
	var found bool
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&found); err != nil {
		return false, fmt.Errorf("profile purge probe %s: %w", st.name, err)
	}
	return found, nil
}

// purgeFinalizeTx is the last commit. The sanitized retired root is checked once
// more (its display name is kept only for the owner's removed-profile history) and
// the job row is deleted in the same transaction, so no identifying cleanup
// metadata is left behind and no false completion can be reported for a target
// that changed.
func (s *Service) purgeFinalizeTx(ctx context.Context, tx *sql.Tx, j *purgeJob) error {
	if err := purgeTargetTx(ctx, tx, j); err != nil {
		return err
	}
	var account, lifetime, art string
	var deleted, pinCleared, librariesCleared, position int
	err := tx.QueryRowContext(ctx, `SELECT account_id,lifetime,deleted,pin_hash IS NULL,art,allowed_libraries IS NULL,position
FROM direct_profiles WHERE id=?`, j.profile).Scan(&account, &lifetime, &deleted, &pinCleared, &art, &librariesCleared, &position)
	if err != nil {
		return err
	}
	if account != j.account || lifetime != j.profileLifetime || deleted != 1 || pinCleared != 1 ||
		librariesCleared != 1 || position != 0 || art != "slate" {
		return fmt.Errorf("%w: retired root is not sanitized", errPurgeTargetChanged)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM security_purge_jobs WHERE job_id=? AND profile_incarnation=?`, j.id, j.profileLifetime); err != nil {
		return err
	}
	return nil
}

// purgeStoredCursor keeps the owner's plan digest next to the shared engine's
// cursor so a cursor from a different compiled plan is refused instead of being
// interpreted against this one.
type purgeStoredCursor struct {
	Digest string `json:"d"`
	Cursor string `json:"c"`
}

func purgeDecodeStoredCursor(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	var stored purgeStoredCursor
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return "", errPurgeCursorDigest
	}
	if stored.Digest != purgePlanDigest {
		return "", errPurgeCursorDigest
	}
	return stored.Cursor, nil
}

func purgeEncodeStoredCursor(c phasedpurge.Cursor) (string, error) {
	inner, err := phasedpurge.EncodeCursor(c)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(purgeStoredCursor{Digest: purgePlanDigest, Cursor: inner})
	if err != nil || len(raw) > phasedpurge.CursorLimit {
		return "", errPurgeCursorDigest
	}
	return string(raw), nil
}

// purgeHardFailure separates a source-contract defect from a transient storage
// error. A hard failure is parked at the safe-integer sentinel so it cannot spin
// through an endless backoff and cannot look like progress.
func purgeHardFailure(err error) bool {
	return errors.Is(err, phasedpurge.ErrCursor) || errors.Is(err, phasedpurge.ErrPlan) ||
		errors.Is(err, phasedpurge.ErrEffects) || errors.Is(err, phasedpurge.ErrNoProgress) ||
		errors.Is(err, errPurgeCursorDigest)
}

// markProfilePurgeBlocked records a durable, visible blocked state after the turn
// has been rolled back. It never advances the cursor and never deletes the job; its
// own failure is returned so the caller reports both causes.
func (s *Service) markProfilePurgeBlocked(j *purgeJob, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	attempt := j.attempt + 1
	next := time.Now().UnixMilli() + purgeBackoff(attempt)
	hard := purgeHardFailure(cause)
	if hard {
		next = purgeHardBlockNextMS
	}
	ctx = dbwork.WithDurableUser(ctx)
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassMaintenance)
	if err != nil {
		return fmt.Errorf("profile cleanup could not record its blocked state: %w", err)
	}
	defer gated.Rollback()
	_, err = gated.Tx().ExecContext(ctx, `UPDATE security_purge_jobs SET state='blocked',attempt=?,next_attempt_ms=?,process_epoch=?
WHERE job_id=? AND profile_incarnation=?`, attempt, next, purgeProcessEpoch, j.id, j.profileLifetime)
	if err == nil {
		err = gated.Commit()
	}
	if err != nil {
		return fmt.Errorf("profile cleanup could not record its blocked state: %w", err)
	}
	if hard {
		log.Printf("profile cleanup hard-blocked at attempt %d: %v", attempt, cause)
	} else {
		log.Printf("profile cleanup blocked at attempt %d: %v", attempt, cause)
	}
	return nil
}

func purgeBackoff(attempt int64) int64 {
	if attempt < 1 {
		attempt = 1
	}
	shift := attempt
	if shift > 6 {
		shift = 6
	}
	wait := int64(250) << uint(shift)
	if wait > 60_000 {
		wait = 60_000
	}
	return wait
}

// purgeArgs returns the scope values in SQL placeholder order: the unit's own
// predicate first, then the parent chain's root predicate.
func (j *purgeJob) purgeArgs(st *purgeStep) []any {
	var args []any
	if st.scope != scopeNone {
		args = append(args, j.scopeArgs(st.scope)...)
	}
	if len(st.of) > 0 {
		args = append(args, j.scopeArgs(st.hopScope)...)
	}
	return args
}

func (j *purgeJob) scopeArgs(scope purgeScope) []any {
	personal := persistence.PersonalOwnerKey("local", j.account, j.profile)
	viewer, _ := json.Marshal([3]string{"local", j.account, j.profile})
	viewerKey := string(viewer)
	switch scope {
	case scopePersonal, scopeOwnerKey, scopeCursor:
		return []any{personal}
	case scopeTuple, scopeOwned:
		return []any{"local", j.account, j.profile}
	case scopeTuple2, scopeDeviceLast:
		return []any{j.account, j.profile}
	case scopeViewerKey, scopeActor:
		return []any{viewerKey}
	case scopeConsole:
		return []any{viewerKey, "profile:" + viewerKey, "device:" + viewerKey + ":web", "device:" + viewerKey + ":mobile", "device:" + viewerKey + ":television", "report:" + viewerKey}
	case scopeAudiences:
		return []any{"personal:" + personal, "profile:local:" + j.account + ":" + j.profile, "report:" + viewerKey}
	case scopeProfileID, scopeRecipient:
		return []any{j.profile}
	case scopeP251, scopeRoot:
		return []any{j.account, j.profile, j.profileLifetime}
	case scopeRetained:
		return []any{personal, j.profile}
	case scopePreparedActor:
		return []any{"local", j.account, j.profile}
	case scopeRawActor:
		return []any{"local", j.account, j.profile}
	}
	return nil
}

func purgeScopeSQL(scope purgeScope) string {
	switch scope {
	case scopePersonal:
		return "p.profile_id=?"
	case scopeTuple:
		return "p.authority=? AND p.account_id=? AND p.profile_id=?"
	case scopeTuple2:
		return "p.account_id=? AND p.profile_id=?"
	case scopeViewerKey:
		return "p.profile_key=?"
	case scopeOwnerKey:
		return "p.owner_key=?"
	case scopeOwned:
		return "p.owner_authority=? AND p.owner_account=? AND p.owner_profile=?"
	case scopeActor:
		return "p.actor=?"
	case scopeConsole:
		return "p.scope IN(?,?,?,?,?,?)"
	case scopeRoot:
		return "p.account_id=? AND p.profile_id=? AND p.profile_incarnation=?"
	case scopePreparedActor:
		return "json_extract(p.principal_json,'$.authority')=? AND json_extract(p.principal_json,'$.accountId')=? AND json_extract(p.principal_json,'$.profileId')=?"
	case scopeRetained:
		// retainStateTx unions raw personal rows with reading owner keys, so a
		// retained row may name the retired profile in either representation.
		// Both are local rows written only by the local catalog retention path;
		// hosted erasure never writes this table, so a raw-id match names the
		// retiring local profile, not a hosted one.
		return "p.profile_id IN(?,?)"
	case scopeAudiences:
		return "p.audience IN(?,?,?)"
	case scopeProfileID:
		return "p.profile_id=?"
	case scopeDeviceLast:
		return "p.account_id=? AND p.last_profile_id=?"
	case scopeRecipient:
		return "p.recipient_profile_id=?"
	case scopeCursor:
		return "p.profile_cursor=?"
	case scopeP251:
		// One receipt owner: the local account's scope row, the exact profile id
		// and the exact retired incarnation. A raw profile id alone could collide
		// with another account or a hosted scope.
		return "p.scope_id IN(SELECT a.id FROM user_mutation_accounts a WHERE a.authority='local' AND a.account_id=?) AND p.profile_id=? AND p.profile_incarnation=?"
	case scopeRawActor:
		return "p.actor_authority=? AND p.actor_account_id=? AND p.actor_profile_id=?"
	}
	return ""
}

// purgePredicate composes the raw candidate predicate. It deliberately excludes
// child guards: a candidate is chosen first, bounded by the limit, and only that
// candidate is checked against its owned children. This keeps a turn's reads
// constant even when many parent rows are still waiting on their children.
func purgePredicate(st *purgeStep) string {
	var preds []string
	if st.scope != scopeNone {
		preds = append(preds, purgeScopeSQL(st.scope))
	}
	if len(st.of) > 0 {
		preds = append(preds, purgeTuple("p", st.of[0].fk)+" IN ("+purgeHopSQL(st, 0)+")")
	}
	if st.extra != "" {
		preds = append(preds, st.extra)
	}
	return strings.Join(preds, " AND ")
}

func purgeGuarded(st *purgeStep) bool { return len(st.guards) > 0 }

// purgeHopSQL builds one level of the compiled parent chain. h0 is the immediate
// parent and the last hop is the scope-rooted table.
func purgeHopSQL(st *purgeStep, level int) string {
	alias := "h" + strconv.Itoa(level)
	h := st.of[level]
	columns := purgeColumns(alias, h.keys)
	var pred string
	if level == len(st.of)-1 {
		pred = strings.ReplaceAll(purgeScopeSQL(st.hopScope), "p.", alias+".")
		if st.hopExtra != "" {
			pred += " AND " + st.hopExtra
		}
	} else {
		// hop[level+1].fk names the referencing columns in this hop's table.
		pred = purgeTuple(alias, st.of[level+1].fk) + " IN (" + purgeHopSQL(st, level+1) + ")"
	}
	return "SELECT " + columns + " FROM " + h.table + " AS " + alias + " WHERE " + pred +
		" LIMIT " + strconv.Itoa(purgeParentProbe)
}

// purgeGuardSQL blocks a parent delete while an owned child row still exists. The
// child table is its own alias, so the correlated columns cannot be confused.
func purgeGuardSQL(g purgeGuard, candidateAlias string) string {
	alias := "g_" + g.table
	var match []string
	keys := purgeSplit(g.keys)
	for i, fk := range purgeSplit(g.fk) {
		if i >= len(keys) {
			break
		}
		match = append(match, alias+"."+fk+"="+candidateAlias+"."+keys[i])
	}
	return "NOT EXISTS(SELECT 1 FROM " + g.table + " AS " + alias + " WHERE " + strings.Join(match, " AND ") + ")"
}

// purgeCandidateSQL is the bounded raw candidate selection. MATERIALIZED keeps the
// limit from being pulled into the mutation, so exactly one row is read before any
// child is probed.
func purgeCandidateSQL(st *purgeStep, keyset string) string {
	pred := purgePredicate(st)
	if keyset != "" {
		pred += keyset
	}
	return "SELECT p.* FROM " + st.table + " AS p WHERE " + pred +
		" LIMIT 1"
}

// purgeStepSQL is the one mutation a unit may perform: a single-row delete or scrub
// at the lowest remaining indexed head, with every owned child guarded.
func purgeStepSQL(st *purgeStep, keyset string) string {
	bare := purgeTupleBare(st.keys)
	columns := strings.Join(purgeSplit(st.keys), ",")
	head := "DELETE FROM " + st.table
	if st.action == purgeScrub {
		head = "UPDATE " + st.table + " SET " + st.set
	}
	if !purgeGuarded(st) {
		return head + " WHERE " + bare + " IN (SELECT " + columns + " FROM (" + purgeCandidateSQL(st, keyset) + ") AS c) RETURNING " + columns
	}
	var guarded []string
	for _, g := range st.guards {
		guarded = append(guarded, purgeGuardSQL(g, "c"))
	}
	return "WITH candidate AS MATERIALIZED (" + purgeCandidateSQL(st, keyset) + ") " +
		head + " WHERE " + bare + " IN (SELECT " + purgeColumns("c", st.keys) + " FROM candidate AS c WHERE " + strings.Join(guarded, " AND ") + ") RETURNING " + columns
}

func purgeSplit(keys string) []string {
	if keys == "" {
		return nil
	}
	return strings.Split(keys, ",")
}

// purgeTokenDrainTx keeps the token mapper until its indexed private children drain.
func purgeTokenDrainTx(ctx context.Context, tx *sql.Tx, j *purgeJob) (phasedpurge.Outcome, error) {
	var token string
	err := tx.QueryRowContext(ctx, purgeTokenProbeSQL, j.account, j.profile).Scan(&token)
	if errors.Is(err, sql.ErrNoRows) {
		return phasedpurge.Outcome{Drained: true}, nil
	}
	if err != nil {
		return phasedpurge.Outcome{}, fmt.Errorf("profile purge token probe: %w", err)
	}
	for _, query := range purgeTokenChildSQL() {
		result, execErr := tx.ExecContext(ctx, query, token)
		if execErr != nil {
			return phasedpurge.Outcome{}, fmt.Errorf("profile purge token child: %w", execErr)
		}
		if n, _ := result.RowsAffected(); n > 0 {
			return phasedpurge.Outcome{Changed: true}, nil
		}
	}
	result, err := tx.ExecContext(ctx, purgeTokenDrainSQL, token, token, token, token)
	if err != nil {
		return phasedpurge.Outcome{}, fmt.Errorf("profile purge token delete: %w", err)
	}
	if n, _ := result.RowsAffected(); n > 0 {
		return phasedpurge.Outcome{Changed: true}, nil
	}
	// A renewal or another owned child still holds the token; restart the phase so
	// its drain runs first.
	return phasedpurge.Outcome{Rewind: true}, nil
}

// purgeTokenChildSQL is the compiled child-first order of the token drain.
func purgeTokenChildSQL() []string {
	return []string{
		`DELETE FROM console_receipts WHERE (scope,key) IN(SELECT scope,key FROM console_receipts WHERE scope='session:'||? ORDER BY scope,key LIMIT 1)`,
		`DELETE FROM console_documents WHERE scope=(SELECT scope FROM console_documents WHERE scope='session:'||? ORDER BY scope LIMIT 1)`,
		`DELETE FROM playback_route_failures WHERE (session_hash,asset_id,engine,route) IN(SELECT session_hash,asset_id,engine,route FROM playback_route_failures WHERE session_hash=? ORDER BY session_hash,asset_id,engine,route LIMIT 1)`,
	}
}

// purgeTokenDrainSQL is the token delete, allowed only with all three child
// probes empty.
const purgeTokenDrainSQL = `DELETE FROM authorization_family_tokens WHERE token_hash=?
 AND NOT EXISTS(SELECT 1 FROM console_receipts WHERE scope='session:'||?)
 AND NOT EXISTS(SELECT 1 FROM console_documents WHERE scope='session:'||?)
 AND NOT EXISTS(SELECT 1 FROM playback_route_failures WHERE session_hash=?)`

// purgeTokenProbeSQL picks one token of at most sixteen owner families.
const purgeTokenProbeSQL = `WITH heads AS MATERIALIZED (
 SELECT f.id FROM authorization_session_families f
 WHERE f.authority='local' AND f.account_id=? AND f.profile_id=? AND f.revoked=1
 ORDER BY f.id LIMIT 16), chosen AS MATERIALIZED (
 SELECT h.id FROM heads h WHERE EXISTS(SELECT 1 FROM authorization_family_tokens t WHERE t.family_id=h.id)
 LIMIT 1)
 SELECT t.token_hash FROM authorization_family_tokens t WHERE t.family_id=(SELECT id FROM chosen)
 ORDER BY t.generation LIMIT 1`

func purgeTokenDrainExistsTx(ctx context.Context, tx *sql.Tx, j *purgeJob) (bool, error) {
	var found bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(`+purgeTokenProbeSQL+`)`, j.account, j.profile).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("profile purge token proof: %w", err)
	}
	return found, nil
}

// purgeAvatarObjectTx removes the retiring link's own unshared rendition objects
// before the link. Shared digests (another profile's avatar) stay.
func purgeAvatarObjectTx(ctx context.Context, tx *sql.Tx, j *purgeJob) (phasedpurge.Outcome, error) {
	var digest string
	err := tx.QueryRowContext(ctx, `SELECT digest FROM profile_avatars WHERE profile_id=?`, j.profile).Scan(&digest)
	if errors.Is(err, sql.ErrNoRows) {
		return phasedpurge.Outcome{Drained: true}, nil
	}
	if err != nil {
		return phasedpurge.Outcome{}, fmt.Errorf("profile purge avatar digest: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM profile_avatar_objects WHERE (digest,rendition) IN(SELECT digest,rendition FROM profile_avatar_objects WHERE digest=? AND NOT EXISTS(SELECT 1 FROM profile_avatars a WHERE a.digest=? AND a.profile_id<>?) ORDER BY digest,rendition LIMIT 1)`, digest, digest, j.profile)
	if err != nil {
		return phasedpurge.Outcome{}, fmt.Errorf("profile purge avatar object: %w", err)
	}
	if n, _ := result.RowsAffected(); n > 0 {
		return phasedpurge.Outcome{Changed: true}, nil
	}
	return phasedpurge.Outcome{Drained: true}, nil
}

func purgeAvatarObjectExistsTx(ctx context.Context, tx *sql.Tx, j *purgeJob) (bool, error) {
	var found bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM profile_avatar_objects o WHERE o.digest=(SELECT a.digest FROM profile_avatars a WHERE a.profile_id=?) AND NOT EXISTS(SELECT 1 FROM profile_avatars b WHERE b.digest=o.digest AND b.profile_id<>?))`, j.profile, j.profile).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("profile purge avatar proof: %w", err)
	}
	return found, nil
}

func purgeColumns(alias, keys string) string {
	cols := purgeSplit(keys)
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = alias + "." + c
	}
	return strings.Join(out, ",")
}

// purgeTuple renders a row value usable on the left of IN or a comparison.
func purgeTuple(alias, keys string) string {
	cols := purgeSplit(keys)
	if len(cols) == 1 {
		return alias + "." + cols[0]
	}
	return "(" + purgeColumns(alias, keys) + ")"
}

// purgeTupleBare is purgeTuple against the DELETE/UPDATE target itself, where an
// alias is not allowed.
func purgePlaceholders(n int) string {
	if n <= 0 {
		return "?"
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func purgeTupleBare(keys string) string {
	cols := purgeSplit(keys)
	if len(cols) == 1 {
		return cols[0]
	}
	return "(" + strings.Join(cols, ",") + ")"
}

const purgePreparationActorSQL = `SELECT a.operation_id FROM playback_start_preparation_actor a WHERE a.authority='local' AND a.account_id=? AND a.profile_id=? LIMIT 1`

var purgePreparationChildren = []string{"playback_start_preparation_results", "playback_start_preparation_source", "playback_start_preparation_queue", "playback_start_preparation_lifecycle"}

func purgePreparationRootTx(ctx context.Context, tx *sql.Tx, j *purgeJob) (phasedpurge.Outcome, error) {
	var id string
	if err := tx.QueryRowContext(ctx, purgePreparationActorSQL, j.account, j.profile).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return phasedpurge.Outcome{Drained: true}, nil
		}
		return phasedpurge.Outcome{}, err
	}
	for _, table := range purgePreparationChildren {
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM "+table+" WHERE operation_id=?)", id).Scan(&exists); err != nil {
			return phasedpurge.Outcome{}, err
		}
		if exists {
			return phasedpurge.Outcome{Rewind: true}, nil
		}
	}
	// The actor remains the exact owner mapper until both deletes can commit.
	// NO ACTION foreign keys refuse a root with any private child still attached.
	if _, err := tx.ExecContext(ctx, `DELETE FROM playback_start_preparation_actor WHERE operation_id=?`, id); err != nil {
		return phasedpurge.Outcome{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM playback_start_preparations WHERE id=?`, id); err != nil {
		return phasedpurge.Outcome{}, err
	}
	return phasedpurge.Outcome{Changed: true}, nil
}
