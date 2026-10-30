package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type JobFieldEdit struct {
	Value     *string   `json:"value,omitempty"`
	Values    *[]string `json:"values,omitempty"`
	Locked    *bool     `json:"locked,omitempty"`
	Automatic bool      `json:"useAutomatic,omitempty"`
}
type JobListEdit struct {
	Add    []string `json:"add,omitempty"`
	Remove []string `json:"remove,omitempty"`
}
type JobItemError struct{ Code string }

func (e *JobItemError) Error() string { return e.Code }

// Hooks retain domain ownership without making catalogue import metadata or
// administration (both already depend on catalogue). SQL effects share the job
// transaction; they do not create per-item idempotency receipts.
type BulkCommandHooks struct {
	// Reading collection membership repeats the established personal Reading admission.
	ReadingCollectionAuthorize func(context.Context, *sql.Tx, identity.Principal, string) error
	Validate                   func(string, JobPersonalArgs) error
	Authorize                  func(context.Context, *sql.Tx, identity.Principal, string) error
	Capture                    func(context.Context, *sql.Tx, identity.Principal, string, string, JobPersonalArgs) (string, error)
	Apply                      func(context.Context, *sql.Tx, identity.Principal, string, string, string, JobPersonalArgs) error
}

func jobActor(p identity.Principal) ResourceActor {
	return ResourceActor{p.Authority, p.AccountID, p.ProfileID}
}
func jobOwnerCommand(command string) bool {
	return command == "refresh" || command == "metadata-edit"
}
func (s *Service) validateBulkCommand(command string, a JobPersonalArgs) error {
	raw, _ := json.Marshal(a)
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	allowed := map[string]bool{}
	switch command {
	case "personal-state":
		for _, k := range []string{"watched", "favorite", "watchlist", "rating"} {
			allowed[k] = true
		}
	case "playlist-add":
		for _, k := range []string{"playlistId", "expectedRevision", "placement"} {
			allowed[k] = true
		}
	case "collection-add":
		for _, k := range []string{"collectionId", "expectedRevision"} {
			allowed[k] = true
		}
	case "metadata-edit":
		for _, k := range []string{"fields", "lists", "genres", "lockEdited"} {
			allowed[k] = true
		}
	case "refresh":
	default:
		return jobInvalid("unsupported command")
	}
	for k := range fields {
		if !allowed[k] {
			return jobInvalid("argument does not apply to command: " + k)
		}
	}
	switch command {
	case "personal-state":
		return validJobArgs(a)
	case "playlist-add", "collection-add":
		target := a.PlaylistID
		if command == "collection-add" {
			target = a.CollectionID
		}
		if target == "" || len(target) > 256 || a.ExpectedRevision == nil || *a.ExpectedRevision < 1 || *a.ExpectedRevision > 9007199254740991 {
			return jobInvalid("destination and expectedRevision are required")
		}
		if command == "playlist-add" {
			_, _, e := bulkPlacement(a.Placement)
			return e
		}
	default:
		if s.BulkCommands.Validate == nil || s.BulkCommands.Authorize == nil {
			return jobInvalid("command is unavailable")
		}
		return s.BulkCommands.Validate(command, a)
	}
	return nil
}
func bulkPlacement(raw json.RawMessage) (mode, after string, err error) {
	if json.Unmarshal(raw, &mode) == nil {
		if mode == "end" || mode == "next" {
			return mode, "", nil
		}
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil && len(object) == 1 {
		if value, ok := object["after"]; ok && json.Unmarshal(value, &after) == nil && after != "" && len(after) <= 256 {
			return "after", after, nil
		}
	}
	return "", "", jobInvalid("placement requires end, next, or {after:entryId}")
}
func (s *Service) authorizeBulkCommand(ctx context.Context, tx *sql.Tx, p identity.Principal, command string, a JobPersonalArgs, checkRevision bool) error {
	if jobOwnerCommand(command) {
		if s.BulkCommands.Authorize == nil {
			return identity.ErrUnauthorized
		}
		return s.BulkCommands.Authorize(ctx, tx, p, command)
	}
	if command == "personal-state" {
		return nil
	}
	var role string
	var rev int64
	var deleted bool
	var e error
	if command == "playlist-add" {
		role, rev, deleted, e = playlistRole(tx, a.PlaylistID, jobActor(p))
	} else {
		var kind string
		role, kind, rev, deleted, e = savedResourceRole(tx, a.CollectionID, jobActor(p))
		if e == nil && kind != "collection" {
			return jobInvalid("destination must be a saved collection")
		}
	}
	if e != nil {
		return e
	}
	if deleted {
		return sql.ErrNoRows
	}
	if role != "owner" && role != "editor" {
		return identity.ErrUnauthorized
	}
	if checkRevision && (a.ExpectedRevision == nil || rev != *a.ExpectedRevision) {
		return ErrPersonalConflict
	}
	return nil
}
func (s *Service) createBulkDestination(tx *sql.Tx, id string, p identity.Principal, command string, a JobPersonalArgs) error {
	if command != "playlist-add" && command != "collection-add" {
		return nil
	}
	target, prefix := a.CollectionID, ""
	if command == "playlist-add" {
		target = a.PlaylistID
		mode, after, e := bulkPlacement(a.Placement)
		if e != nil {
			return e
		}
		lower, upper := "", ""
		if mode == "end" {
			e = tx.QueryRow(`SELECT e.order_key FROM catalog_playlists p JOIN catalog_playlist_entries e INDEXED BY catalog_playlist_entries_page ON e.playlist_id=p.id WHERE p.token=? ORDER BY e.order_key DESC,e.id DESC LIMIT 1`, target).Scan(&lower)
		} else if mode == "after" {
			e = tx.QueryRow(`SELECT e.order_key FROM catalog_playlists p JOIN catalog_playlist_entries e ON e.playlist_id=p.id WHERE p.token=? AND e.token=?`, target, after).Scan(&lower)
			if e != nil {
				return e
			}
		}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if mode != "end" {
			e = tx.QueryRow(`SELECT e.order_key FROM catalog_playlists p JOIN catalog_playlist_entries e INDEXED BY catalog_playlist_entries_page ON e.playlist_id=p.id WHERE p.token=? AND e.order_key>? ORDER BY e.order_key,e.id LIMIT 1`, target, lower).Scan(&upper)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
		}
		prefix = playlistBetween(lower, upper)
	}
	_, e := tx.Exec(`INSERT INTO personal_job_destinations(job_id,kind,target_id,revision,order_prefix) VALUES(?,?,?,?,?)`, id, command, target, *a.ExpectedRevision, prefix)
	return e
}
func (s *Service) checkBulkDestination(tx *sql.Tx, id string, p identity.Principal, command string) error {
	if command != "playlist-add" && command != "collection-add" {
		return nil
	}
	var target string
	var revision int64
	if e := tx.QueryRow(`SELECT target_id,revision FROM personal_job_destinations WHERE job_id=?`, id).Scan(&target, &revision); e != nil {
		return e
	}
	a := JobPersonalArgs{PlaylistID: target, CollectionID: target, ExpectedRevision: &revision}
	return s.authorizeBulkCommand(context.Background(), tx, p, command, a, true)
}
func (s *Service) advanceBulkDestination(tx *sql.Tx, id, command string) error {
	if command != "playlist-add" && command != "collection-add" {
		return nil
	}
	update := `UPDATE catalog_playlists SET revision=revision+1 WHERE token=(SELECT target_id FROM personal_job_destinations WHERE job_id=?)`
	if command == "collection-add" {
		update = `UPDATE saved_resources SET revision=revision+1 WHERE id=(SELECT target_id FROM personal_job_destinations WHERE job_id=?)`
	}
	if _, e := tx.Exec(update, id); e != nil {
		return e
	}
	_, e := tx.Exec(`UPDATE personal_job_destinations SET revision=revision+1 WHERE job_id=?`, id)
	return e
}
func (s *Service) applyBulkCommand(ctx context.Context, tx *sql.Tx, p identity.Principal, job, command, item string, a JobPersonalArgs) error {
	switch command {
	case "personal-state":
		return s.applyJobPersonal(tx, identity.PersonalKey(p.Viewer), item, a)
	case "playlist-add":
		var target, prefix, key string
		var pos int64
		var allowed bool
		if e := tx.QueryRow(`SELECT k.playable=1 AND e.kind<>11 FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE e.public_id=pid_blob(?)`, item).Scan(&allowed); e != nil {
			return e
		}
		if !allowed {
			return &JobItemError{"unsupported_item_kind"}
		}
		if e := tx.QueryRow(`SELECT target_id,order_prefix FROM personal_job_destinations WHERE job_id=?`, job).Scan(&target, &prefix); e != nil {
			return e
		}
		if e := tx.QueryRow(`SELECT sort_key FROM personal_job_details WHERE job_id=? AND item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, job, item).Scan(&key); e != nil {
			return e
		}
		if e := tx.QueryRow(`SELECT COALESCE(max(e.position),0)+1 FROM catalog_playlists p JOIN catalog_playlist_entries e ON e.playlist_id=p.id WHERE p.token=?`, target).Scan(&pos); e != nil {
			return e
		}
		mode, _, e := bulkPlacement(a.Placement)
		if e != nil {
			return e
		}
		// Home's destination picker appends membership with "end", skipping
		// existing titles. Explicit next/after placement inserts occurrences in
		// the captured order, including titles already elsewhere in the playlist.
		result, e := tx.Exec(`INSERT INTO catalog_playlist_entries(token,playlist_id,item_id,position,order_key) SELECT ?,p.id,i.id,?,? FROM catalog_playlists p,catalog_entities i WHERE p.token=? AND i.public_id=pid_blob(?) AND (?<>'end' OR NOT EXISTS(SELECT 1 FROM catalog_playlist_entries existing WHERE existing.playlist_id=p.id AND existing.item_id=i.id))`, identity.Token(), pos, prefix+key, target, item, mode)
		if e != nil {
			return e
		}
		inserted, e := result.RowsAffected()
		if e != nil {
			return e
		}
		// A membership that really landed is a change to the playlist itself; an item the
		// "end" placement skipped is not. The revision still advances with this job's chunk.
		if inserted == 1 {
			if _, e = tx.Exec(`UPDATE catalog_playlists SET updated_at=? WHERE token=?`, time.Now().UTC().Format(time.RFC3339), target); e != nil {
				return e
			}
		}
		return recordBulkMembershipOutcome(tx, job, item, inserted == 1)
	case "collection-add":
		var kind int
		if e := tx.QueryRowContext(ctx, `SELECT kind FROM catalog_entities WHERE public_id=pid_blob(?)`, item).Scan(&kind); e != nil {
			return e
		}
		if kind == 20 || kind == 21 {
			if e := s.authorizeReadingCollection(ctx, tx, p, item); e != nil {
				if readingCollectionDenied(e) {
					return &JobItemError{"not_found"}
				}
				return e
			}
		}
		var target string
		var entity int64
		if e := tx.QueryRow(`SELECT d.target_id,i.id FROM personal_job_destinations d,catalog_entities i WHERE d.job_id=? AND i.public_id=pid_blob(?)`, job, item).Scan(&target, &entity); e != nil {
			return e
		}
		var present bool
		if e := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM saved_resource_entries WHERE resource_id=? AND item_id=?)`, target, entity).Scan(&present); e != nil {
			return e
		}
		if e := appendSavedEntry(tx, target, identity.Token(), entity); e != nil {
			return e
		}
		return recordBulkMembershipOutcome(tx, job, item, !present)
	default:
		if s.BulkCommands.Apply == nil {
			return errors.New("command worker unavailable")
		}
		var expected string
		if e := tx.QueryRow(`SELECT expected FROM personal_job_details WHERE job_id=? AND item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, job, item).Scan(&expected); e != nil {
			return e
		}
		return s.BulkCommands.Apply(ctx, tx, p, command, item, expected, a)
	}
}

// This receipt shares the member savepoint and the apply/progress transaction.
// Captured membership is purged after completion; outcomes live with the job.
func recordBulkMembershipOutcome(tx *sql.Tx, job, item string, added bool) error {
	outcome := "already_present"
	if added {
		outcome = "added"
	}
	result, err := tx.Exec(`UPDATE personal_job_membership_outcomes SET outcome=?,code='' WHERE job_id=? AND item_public_id=pid_blob(?) AND outcome='pending'`, outcome, job, item)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("membership outcome did not match captured member")
	}
	return nil
}
