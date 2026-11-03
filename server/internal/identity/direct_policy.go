package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// PolicyReader permits checks in an existing transaction: no nested connection.
type PolicyReader interface{ QueryRow(string, ...any) *sql.Row }

// DirectAccess resolves account membership intersected with profile restrictions.
// A primary profile is NOT by itself a server owner. Empty member libraries deny
// all media; nil profile libraries inherit, never expand, account authority.
func DirectAccess(q PolicyReader, p Principal) (all bool, libraries []string, revision, profileRevision int64, err error) {
	if p.Authority != "local" || p.Role == "account" {
		err = ErrUnauthorized
		return
	}
	var epoch, disabled, deleted int
	var role, raw string
	var restricted sql.NullString
	err = q.QueryRow(`SELECT a.epoch,m.role,m.disabled,m.allowed_libraries,m.revision,p.allowed_libraries,p.revision,p.deleted FROM accounts a JOIN direct_memberships m ON m.account_id=a.id JOIN direct_profiles p ON p.account_id=a.id WHERE a.id=? AND p.id=?`, p.AccountID, p.ProfileID).Scan(&epoch, &role, &disabled, &raw, &revision, &restricted, &profileRevision, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrUnauthorized
	}
	if err != nil {
		return
	}
	if disabled != 0 || deleted != 0 || p.Epoch > 0 && epoch != p.Epoch {
		err = ErrUnauthorized
		return
	}
	if json.Unmarshal([]byte(raw), &libraries) != nil {
		err = ErrUnauthorized
		return
	}
	// Administrative is the single tier predicate (access_tier.go): the admin
	// tier sees every library, like the owner, rather than an allow list.
	all = Administrative(role)
	if restricted.Valid {
		var narrowed []string
		if json.Unmarshal([]byte(restricted.String), &narrowed) != nil {
			err = ErrUnauthorized
			return
		}
		if !all {
			allowed := map[string]bool{}
			for _, id := range libraries {
				allowed[id] = true
			}
			var intersection = []string{}
			for _, id := range narrowed {
				if allowed[id] {
					intersection = append(intersection, id)
				}
			}
			narrowed = intersection
		}
		all = false
		libraries = narrowed
	}
	if libraries == nil {
		libraries = []string{}
	}
	return
}

// DirectAccessTx is DirectAccess in tx. A request asks it once per library it
// filters, so inside a request's read snapshot the answer is proved once and
// reused (dbwork.RememberForRequest); anywhere else it is proved each time.
func DirectAccessTx(ctx context.Context, tx *sql.Tx, p Principal) (all bool, libraries []string, err error) {
	type grant struct {
		all       bool
		libraries []string
	}
	key := fmt.Sprintf("identity.direct-access\x00%+v\x00%d", p.Viewer, p.Epoch)
	g, err := dbwork.RememberForRequest(ctx, tx, key, func(g grant) grant {
		g.libraries = slices.Clone(g.libraries)
		return g
	}, func() (grant, error) {
		all, libraries, _, _, err := DirectAccess(txPolicyReader{ctx, tx}, p)
		return grant{all, libraries}, err
	})
	return g.all, g.libraries, err
}

type txPolicyReader struct {
	ctx context.Context
	tx  *sql.Tx
}

func (r txPolicyReader) QueryRow(query string, args ...any) *sql.Row {
	return r.tx.QueryRowContext(r.ctx, query, args...)
}

func DirectAllowed(q PolicyReader, p Principal, library string) error {
	all, ids, _, _, e := DirectAccess(q, p)
	if e != nil {
		return e
	}
	return DirectLibraryAllowed(all, ids, library)
}

// DirectLibraryAllowed is DirectAllowed's decision for one library, given what
// DirectAccess answered for the principal.
func DirectLibraryAllowed(all bool, ids []string, library string) error {
	if library == "" || all {
		return nil
	}
	for _, id := range ids {
		if id == library {
			return nil
		}
	}
	// A library the profile is not given is hidden, not a failed sign-in.
	return ErrNotVisible
}
func (s *Service) checkDirectPrincipalTx(q PolicyReader, p Principal) error {
	var waiting bool
	var epoch, primary, deleted, disabled int
	var role, accountPrimary string
	e := q.QueryRow(`SELECT a.epoch,p.is_primary,p.deleted,m.disabled,m.role,a.profile_id,EXISTS(SELECT 1 FROM identity_move_waiting w WHERE w.account_id=a.id AND w.state='waiting') FROM accounts a JOIN direct_memberships m ON m.account_id=a.id JOIN direct_profiles p ON p.account_id=a.id WHERE a.id=? AND p.id=?`, p.AccountID, p.ProfileID).Scan(&epoch, &primary, &deleted, &disabled, &role, &accountPrimary, &waiting)
	if errors.Is(e, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	if e != nil {
		return e
	}
	if waiting {
		return ErrMoveWaiting
	}
	if primary == 0 {
		role = "member"
	}
	// The primary flag and account credential pointer are one invariant. An old
	// session must not retain primary/owner authority after either side changes.
	if epoch != p.Epoch || deleted != 0 || disabled != 0 || (p.Role != role && p.Role != TierMember && !(p.Role == "account" && primary == 1)) || primary != 0 && accountPrimary != p.ProfileID {
		return ErrUnauthorized
	}
	return nil
}
