package catalog

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/recordingaccess"
)

const separateActorPrefix = "separate-local-v1:"

// This is an accepted command descriptor, not a personal owner identity. Both
// direct sign-in and Portico Account sign-in administer the same server-local
// membership/profile rows. No Hosted request or bearer is needed to continue.
type separateFolderActor struct {
	Folder     string `json:"f"`
	Account    string `json:"a"`
	Profile    string `json:"p"`
	Membership string `json:"m"`
	Lifetime   string `json:"l"`
	Tier       string `json:"t"`
}

type separateFolderActorKey struct{}

// WithSeparateFolderActor is supplied only after normal request authentication.
// The accepting writer captures the current local lifetime and authority again.
func WithSeparateFolderActor(ctx context.Context, p identity.Principal) context.Context {
	return context.WithValue(ctx, separateFolderActorKey{}, p)
}

func separateFolderAcceptedOwnerTx(ctx context.Context, tx *sql.Tx, folder string) (string, error) {
	p, present := ctx.Value(separateFolderActorKey{}).(identity.Principal)
	if !present {
		// Existing in-process callers retain their explicitly authorized manual
		// continuation. Only a typed accepted actor enables scheduler execution.
		return folder, nil
	}
	if p.Authority != "local" || !identity.Grants(p.Role, identity.TierAdmin) {
		return "", identity.ErrForbidden
	}
	a := separateFolderActor{Folder: folder, Account: p.AccountID, Profile: p.ProfileID, Tier: p.Role}
	if err := tx.QueryRowContext(ctx, `SELECT m.incarnation,p.lifetime FROM direct_profiles p JOIN direct_memberships m ON m.account_id=p.account_id WHERE p.account_id=? AND p.id=?`, a.Account, a.Profile).Scan(&a.Membership, &a.Lifetime); err != nil {
		return "", err
	}
	if _, err := a.authorizeTx(ctx, tx, ""); err != nil {
		return "", err
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	return separateActorPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func separateFolderDecodeActor(owner string) (separateFolderActor, bool, error) {
	var a separateFolderActor
	if !strings.HasPrefix(owner, separateActorPrefix) {
		return a, false, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(owner, separateActorPrefix))
	if err != nil || len(raw) > 2048 || json.Unmarshal(raw, &a) != nil {
		return a, true, ErrSeparateFolderCursor
	}
	canonical, err := json.Marshal(a)
	if err != nil || string(raw) != string(canonical) || a.Folder == "" || a.Account == "" || a.Profile == "" || len(a.Membership) != 32 || len(a.Lifetime) != 32 || !identity.Grants(a.Tier, identity.TierAdmin) {
		return a, true, ErrSeparateFolderCursor
	}
	return a, true, nil
}

func (a separateFolderActor) authorizeTx(ctx context.Context, tx *sql.Tx, library string) (identity.Principal, error) {
	p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: a.Account, ProfileID: a.Profile}}
	var membership, lifetime, primaryProfile string
	var disabled, deleted, primary, moving bool
	err := tx.QueryRowContext(ctx, `SELECT m.incarnation,p.lifetime,m.role,m.disabled,p.deleted,p.is_primary,a.profile_id,EXISTS(SELECT 1 FROM identity_move_waiting w WHERE w.account_id=a.id AND w.state='waiting')
 FROM accounts a JOIN direct_memberships m ON m.account_id=a.id JOIN direct_profiles p ON p.account_id=a.id
 WHERE a.id=? AND p.id=?`, a.Account, a.Profile).Scan(&membership, &lifetime, &p.Role, &disabled, &deleted, &primary, &primaryProfile, &moving)
	if errors.Is(err, sql.ErrNoRows) {
		return p, identity.ErrUnauthorized
	}
	if err != nil {
		return p, err
	}
	if membership != a.Membership || lifetime != a.Lifetime || disabled || deleted || !primary || primaryProfile != a.Profile || moving || !identity.Grants(a.Tier, identity.TierAdmin) || !identity.Grants(p.Role, identity.TierAdmin) {
		return p, identity.ErrForbidden
	}
	// As on the management route, a current owner retains the administrative
	// view; an administrator additionally needs current library visibility.
	if p.Role != identity.TierOwner {
		if err := (recordingaccess.Policy{}).AllowedTxContext(ctx, p, library, tx); err != nil {
			return p, err
		}
	}
	return p, nil
}

func separateFolderActorAuthorizeTx(ctx context.Context, tx *sql.Tx, m compactcatalog.EntityMutation) (bool, error) {
	a, typed, err := separateFolderDecodeActor(m.OwnerID)
	if err != nil || !typed {
		return typed, err
	}
	if caller, present := ctx.Value(separateFolderActorKey{}).(identity.Principal); present && (caller.Authority != "local" || caller.AccountID != a.Account || caller.ProfileID != a.Profile) {
		return true, identity.ErrForbidden
	}
	var library string
	if err := tx.QueryRowContext(ctx, `SELECT library_id FROM catalog_libraries WHERE id=? AND retired=0`, m.Library).Scan(&library); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return true, identity.ErrNotVisible
		}
		return true, err
	}
	if a.Folder != separateFolderOwner(library, m.SplitSourceID, m.SplitDirectory) {
		return true, ErrSeparateFolderCursor
	}
	_, err = a.authorizeTx(ctx, tx, library)
	return true, err
}
