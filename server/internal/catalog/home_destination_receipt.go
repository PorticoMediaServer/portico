package catalog

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type HomeDestinationReceiptRequest struct {
	Kind          string   `json:"kind"`
	DestinationID string   `json:"destinationId"`
	JobIDs        []string `json:"jobIds"`
	Cursor        string   `json:"cursor,omitempty"`
}
type HomeDestinationReceipt struct {
	ServerID       string    `json:"serverId"`
	ViewerFence    string    `json:"viewerFence"`
	Kind           string    `json:"kind"`
	DestinationID  string    `json:"destinationId"`
	JobIDs         []string  `json:"jobIds"`
	Jobs           []BulkJob `json:"jobs"`
	Added          int64     `json:"added"`
	AlreadyPresent int64     `json:"alreadyPresent"`
	Failed         int64     `json:"failed"`
	Unknown        int64     `json:"unknown"`
	// Complete also permits genuine item failures. Uncaptured or unapplied
	// members never acquire an invented success or an invented unknown count.
	Complete         bool           `json:"complete"`
	Pending          bool           `json:"pending"`
	UnresolvedJobIDs []string       `json:"unresolvedJobIds"`
	Failures         []JobFailure   `json:"failures"`
	FailureEntries   []ContentEntry `json:"failureEntries"`
	NextCursor       string         `json:"nextCursor"`
}

// HomeDestinationReceiptTx aggregates only this current actor's genuine jobs
// for one live authorized destination. It returns no successful member IDs.
func (s *Service) HomeDestinationReceiptTx(ctx context.Context, tx *sql.Tx, server string, p identity.Principal, v Viewer, in HomeDestinationReceiptRequest, access BulkAccess) (HomeDestinationReceipt, error) {
	out := HomeDestinationReceipt{ServerID: server, ViewerFence: v.Fence, Kind: in.Kind, DestinationID: in.DestinationID, JobIDs: append([]string{}, in.JobIDs...), Jobs: []BulkJob{}, UnresolvedJobIDs: []string{}, Failures: []JobFailure{}, FailureEntries: []ContentEntry{}}
	if tx == nil || access == nil || (in.Kind != "playlist" && in.Kind != "collection") || in.DestinationID == "" || len(in.DestinationID) > 256 || len(in.JobIDs) < 1 || len(in.JobIDs) > 200 {
		return out, jobInvalid("receipt requires one destination and 1 to 200 distinct jobs")
	}
	seen := map[string]bool{}
	for _, id := range in.JobIDs {
		if id == "" || len(id) > 256 || seen[id] {
			return out, jobInvalid("receipt requires distinct job ids")
		}
		seen[id] = true
	}
	clone := *s.WithContext(ctx)
	clone.readTx = tx
	s = &clone
	visible, bound, err := access(ctx, tx, p, "item.id")
	if err != nil {
		return out, err
	}
	command := in.Kind + "-add"
	if err = s.authorizeBulkCommand(ctx, tx, p, command, JobPersonalArgs{PlaylistID: in.DestinationID, CollectionID: in.DestinationID}, false); err != nil {
		return out, err
	}
	sorted := append([]string{}, in.JobIDs...)
	sort.Strings(sorted)
	encoded, _ := json.Marshal(sorted)
	// Validate all jobs, including their original principal, destination and
	// accepted visibility fence, before reading any outcome or failure.
	revisions := []any{}
	for _, id := range in.JobIDs {
		var rawPrincipal, rawScope, kind, destination string
		var revision int64
		err = tx.QueryRowContext(ctx, `SELECT j.principal,j.selection_scope,d.kind,d.target_id,j.revision FROM personal_jobs j JOIN personal_job_destinations d ON d.job_id=j.id WHERE j.id=? AND j.profile_id=? AND j.command=?`, id, identity.PersonalKey(p.Viewer), command).Scan(&rawPrincipal, &rawScope, &kind, &destination, &revision)
		if err != nil {
			return out, err
		}
		var original identity.Principal
		var accepted Viewer
		if err = json.Unmarshal([]byte(rawPrincipal), &original); err != nil {
			return out, err
		}
		if err = json.Unmarshal([]byte(rawScope), &accepted); err != nil {
			return out, err
		}
		if original.Authority != p.Authority || original.AccountID != p.AccountID || original.ProfileID != p.ProfileID || kind != command || destination != in.DestinationID {
			return out, identity.ErrUnauthorized
		}
		if accepted.Fence != v.Fence || accepted.Profile != v.Profile {
			return out, ErrStaleContinuation
		}
		job, err := scanBulk(tx.QueryRowContext(ctx, `SELECT `+bulkColumns+` FROM personal_jobs WHERE id=?`, id))
		if err != nil {
			return out, err
		}
		if job.State == "building" {
			job.State = "queued"
		}
		out.Jobs = append(out.Jobs, job)
		revisions = append(revisions, []any{id, revision})
		if !bulkTerminal(job.State) {
			out.Pending = true
		} else if !job.TotalKnown || job.Done+job.Failed != job.Total || job.ErrorCode != "" {
			out.UnresolvedJobIDs = append(out.UnresolvedJobIDs, id)
		}
	}
	out.Complete = !out.Pending && len(out.UnresolvedJobIDs) == 0
	if in.Kind == "collection" {
		denied, err := s.readingCollectionReceiptDenied(ctx, tx, p, in.JobIDs)
		if err != nil {
			return out, err
		}
		visible += ` AND NOT EXISTS(SELECT 1 FROM json_each(?) denied WHERE pid_blob(denied.value)=item.public_id)`
		bound = append(bound, idsJSON(denied))
	}
	current, err := s.homeRevision(v.Libraries, v.Profile)
	if err != nil {
		return out, err
	}
	scope := cursorScope{Profile: identity.PersonalKey(p.Viewer), View: "home-destination-receipt:" + in.Kind, Viewer: v.Fence, Category: operationHash([]any{in.DestinationID, sorted, revisions}), Limit: 100}
	after := []byte{}
	if in.Cursor != "" {
		cursor, err := s.decodeRevisionCursor(in.Cursor, scope, current)
		if err != nil {
			return out, err
		}
		after, err = hex.DecodeString(cursor.ID)
		if err != nil || len(after) != 16 {
			return out, ErrCursor
		}
	}
	// An added outcome wins across all overlapping selectors and retry jobs.
	// An item failure is unresolved only when no genuine success covers it.
	cte := `WITH chosen AS (SELECT value AS id FROM json_each(?)),
 members AS (SELECT outcome.item_public_id,max(outcome.outcome='added') AS added,max(outcome.outcome='already_present') AS present,max(outcome.outcome='failed') AS failed FROM personal_job_membership_outcomes outcome JOIN chosen ON chosen.id=outcome.job_id GROUP BY outcome.item_public_id),
 failed AS (SELECT failure.item_public_id,failure.code,row_number() OVER(PARTITION BY failure.item_public_id ORDER BY job.created_ms DESC,job.id DESC) AS rank FROM personal_job_membership_outcomes failure JOIN chosen ON chosen.id=failure.job_id JOIN personal_jobs job ON job.id=failure.job_id WHERE failure.outcome='failed'),
 outcomes AS (SELECT members.item_public_id,CASE WHEN members.added=1 THEN 'added' WHEN members.present=1 THEN 'already_present' WHEN members.failed=1 THEN 'failed' ELSE 'unknown' END AS outcome,COALESCE(failed.code,'') AS code FROM members LEFT JOIN failed ON failed.item_public_id=members.item_public_id AND failed.rank=1),
 permitted AS (SELECT outcomes.item_public_id,CASE WHEN item.id IS NOT NULL AND item.retired=0 AND (` + visible + recordingsClause("item.id", v.EffectiveRestrictions()) + `) THEN outcomes.outcome ELSE 'unknown' END AS outcome,outcomes.code FROM outcomes LEFT JOIN catalog_entities item ON item.public_id=outcomes.item_public_id) `
	args := append([]any{string(encoded)}, bound...)
	err = tx.QueryRowContext(ctx, cte+`SELECT COALESCE(sum(outcome='added'),0),COALESCE(sum(outcome='already_present'),0),COALESCE(sum(outcome='failed'),0),COALESCE(sum(outcome='unknown'),0) FROM permitted`, args...).Scan(&out.Added, &out.AlreadyPresent, &out.Failed, &out.Unknown)
	if err != nil {
		return out, err
	}
	if out.Unknown > 0 {
		out.Complete = false
	}
	rows, err := tx.QueryContext(ctx, cte+`SELECT item_public_id,pid(item_public_id),code FROM permitted WHERE outcome='failed' AND item_public_id>? ORDER BY item_public_id LIMIT 101`, append(args, after)...)
	if err != nil {
		return out, err
	}
	var last []byte
	for rows.Next() {
		var key []byte
		var failure JobFailure
		if err = rows.Scan(&key, &failure.ItemID, &failure.Code); err != nil {
			break
		}
		if len(out.Failures) == 100 {
			out.NextCursor, err = s.encodeRevisionCursor(cursorValue{Scope: scope, ID: hex.EncodeToString(last), Expires: time.Now().Add(30 * time.Minute).Unix()}, current)
			break
		}
		out.Failures = append(out.Failures, failure)
		last = key
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	ids := []string{}
	for _, failure := range out.Failures {
		ids = append(ids, failure.ItemID)
	}
	reading, err := s.homeReadingPage(v, ids, nil)
	if err != nil {
		return out, err
	}
	ordinary := []string{}
	for _, id := range ids {
		if _, ok := reading[id]; !ok {
			ordinary = append(ordinary, id)
		}
	}
	projected, err := s.homeEntries(v.Profile, ordinary)
	if err != nil {
		return out, err
	}
	byID := reading
	for _, entry := range projected {
		byID[entry.ID] = entry
	}
	for _, id := range ids {
		entry, ok := byID[id]
		if !ok {
			return out, ErrStaleContinuation
		}
		out.FailureEntries = append(out.FailureEntries, entry)
	}
	return out, err
}
