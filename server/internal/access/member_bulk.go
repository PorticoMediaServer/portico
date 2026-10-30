package access

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/streamlimits"
)

type MemberBulkTarget struct {
	AccountID              string `json:"accountId"`
	ExpectedRevision       int64  `json:"expectedRevision"`
	ExpectedLimitsRevision int64  `json:"expectedLimitsRevision"`
}
type MemberAccessPatch struct {
	AllowedLibraries *[]string          `json:"allowedLibraries,omitempty"`
	MaxStreams       *int               `json:"maxStreams,omitempty"`
	MaxStreamsMode   *streamlimits.Mode `json:"maxStreamsMode,omitempty"`
	AllowRemote      *bool              `json:"allowRemote,omitempty"`
	MaxContentRating *string            `json:"maxContentRating,omitempty"`
	AccessTokens     *bool              `json:"accessTokens,omitempty"`
	MaxProfiles      *int               `json:"maxProfiles,omitempty"`
}
type MemberBulkRequest struct {
	OperationID string             `json:"operationId"`
	Targets     []MemberBulkTarget `json:"targets"`
	Action      string             `json:"action"`
	Access      *MemberAccessPatch `json:"access,omitempty"`
}
type MemberBulkResult struct {
	AccountIDs []string `json:"accountIds"`
	Action     string   `json:"action"`
}

// BulkMembers validates the whole selection before changing any row. The live
// actor tier, every target revision, all limits and erasures share one writer.
func (s *Store) BulkMembers(ctx context.Context, auth Authorize, issuer *identity.Service, actor identity.Principal, q MemberBulkRequest) (out MemberBulkResult, err error) {
	if issuer == nil || !validOperationID(q.OperationID) || len(q.Targets) < 1 || len(q.Targets) > 200 {
		return out, invalid("targets", "operationId")
	}
	if q.Action != "disable" && q.Action != "enable" && q.Action != "remove" && q.Action != "access" {
		return out, invalid("action")
	}
	if (q.Action == "access") != (q.Access != nil) {
		return out, invalid("access")
	}
	seen := map[string]bool{}
	for _, t := range q.Targets {
		if !validID(t.AccountID) || seen[t.AccountID] || t.ExpectedRevision < 1 || q.Action == "access" && t.ExpectedLimitsRevision < 1 {
			return out, invalid("targets")
		}
		seen[t.AccountID] = true
	}
	err = s.transaction(ctx, auth, func(tx *sql.Tx) error {
		body, want, e := receipt(tx, "member-bulk:"+actor.AccountID, q.OperationID, q)
		if e != nil {
			return e
		}
		if body != "" {
			return json.Unmarshal([]byte(body), &out)
		}
		live, e := Role(tx, actor.AccountID)
		if e != nil {
			return e
		}
		docs := make([]LimitsDocument, len(q.Targets))
		for i, t := range q.Targets {
			var role string
			var rev int64
			if e = tx.QueryRowContext(ctx, `SELECT role,revision FROM direct_memberships WHERE account_id=?`, t.AccountID).Scan(&role, &rev); e != nil {
				return e
			}
			if t.AccountID == actor.AccountID || !identity.ManagesTier(live, role) || !identity.ManagesTier(actor.Role, role) {
				return identity.ErrForbidden
			}
			if rev != t.ExpectedRevision {
				return &ConflictError{rev}
			}
			if q.Action == "enable" {
				var waiting bool
				if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_move_waiting WHERE account_id=? AND state<>'linked')`, t.AccountID).Scan(&waiting); e != nil {
					return e
				}
				if waiting {
					return invalid("targets")
				}
			}
			if q.Access != nil {
				doc, e := readLimits(tx, t.AccountID)
				if e != nil {
					return e
				}
				if doc.Revision != t.ExpectedLimitsRevision {
					return &ConflictError{doc.Revision}
				}
				p := q.Access
				if p.AllowedLibraries != nil && !bulkLibraries(*p.AllowedLibraries) {
					return invalid("access.allowedLibraries")
				}
				if p.MaxStreamsMode != nil {
					doc.Limits.MaxStreamsMode = *p.MaxStreamsMode
					if *p.MaxStreamsMode != streamlimits.Limit {
						doc.Limits.MaxStreams = 0
					}
				}
				if p.MaxStreams != nil {
					doc.Limits.MaxStreams = *p.MaxStreams
				}
				if p.AllowRemote != nil {
					doc.Limits.AllowRemote = *p.AllowRemote
				}
				if p.MaxContentRating != nil {
					doc.Limits.MaxContentRating = *p.MaxContentRating
				}
				if p.AccessTokens != nil {
					doc.Limits.AccessTokens = *p.AccessTokens
				}
				if p.MaxProfiles != nil {
					doc.Limits.MaxProfiles = *p.MaxProfiles
				}
				if e = normalizeLimits(&doc.Limits); e != nil {
					return e
				}
				docs[i] = doc
			}
		}
		out = MemberBulkResult{Action: q.Action, AccountIDs: []string{}}
		for i, t := range q.Targets {
			switch q.Action {
			case "disable", "enable":
				if _, e = tx.ExecContext(ctx, `UPDATE direct_memberships SET disabled=?,revision=revision+1 WHERE account_id=?`, q.Action == "disable", t.AccountID); e != nil {
					return e
				}
				if q.Action == "disable" {
					if e = issuer.RevokeMemberSessionsTx(ctx, tx, t.AccountID); e != nil {
						return e
					}
				}
			case "remove":
				if e = issuer.EraseMemberTx(ctx, tx, t.AccountID); e != nil {
					return e
				}
			case "access":
				d := docs[i]
				raw, e := limitsBody(d.Limits)
				if e != nil {
					return e
				}
				if e = streamlimits.WriteTx(ctx, tx, t.AccountID, d.Limits.MaxStreamsMode, d.Limits.MaxStreams); e != nil {
					return e
				}
				if _, e = tx.ExecContext(ctx, `INSERT INTO access_limits(account_id,revision,updated_ms,body) VALUES(?,?,?,?) ON CONFLICT(account_id) DO UPDATE SET revision=excluded.revision,updated_ms=excluded.updated_ms,body=excluded.body`, t.AccountID, d.Revision+1, s.now(), string(raw)); e != nil {
					return e
				}
				if _, e = tx.ExecContext(ctx, `UPDATE direct_memberships SET access_tokens=?,max_profiles=?,revision=revision+1 WHERE account_id=?`, d.Limits.AccessTokens, d.Limits.MaxProfiles, t.AccountID); e != nil {
					return e
				}
				if q.Access.AllowedLibraries != nil {
					raw, _ := json.Marshal(*q.Access.AllowedLibraries)
					if _, e = tx.ExecContext(ctx, `UPDATE direct_memberships SET allowed_libraries=? WHERE account_id=?`, string(raw), t.AccountID); e != nil {
						return e
					}
				}
			}
			out.AccountIDs = append(out.AccountIDs, t.AccountID)
		}
		return saveReceipt(tx, "member-bulk:"+actor.AccountID, q.OperationID, want, out, s.now())
	})
	return
}

func bulkLibraries(ids []string) bool {
	if len(ids) > 1000 {
		return false
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !validID(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
