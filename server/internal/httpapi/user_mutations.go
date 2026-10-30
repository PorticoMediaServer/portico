package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

func userMutationError(err error) *apierror.Error {
	switch {
	case errors.Is(err, usermutation.ErrInvalidKey):
		return &apierror.Error{Code: "invalid_mutation_key", Cause: err}
	case errors.Is(err, usermutation.ErrExpired):
		return &apierror.Error{Code: "receipt_expired", Cause: err}
	case errors.Is(err, usermutation.ErrRootGone):
		// A gone root's lifetime is no longer available; 410 through the
		// catalogue's existing "receipt is no longer available" code. The
		// request is refused before any domain mutation.
		return &apierror.Error{Code: "receipt_expired", Cause: err}
	case errors.Is(err, usermutation.ErrConflict):
		return &apierror.Error{Code: "idempotency_conflict", Cause: err}
	case errors.Is(err, usermutation.ErrCapacity):
		return &apierror.Error{Code: "writer_capacity", RetryAfterSeconds: 30, Cause: err}
	}
	return nil
}

func mutationRouteError(err error) error {
	if err == nil {
		return nil
	}
	var public *apierror.Error
	if errors.As(err, &public) {
		return err
	}
	if mutation := userMutationError(err); mutation != nil {
		return mutation
	}
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, identity.ErrContentRestricted) || errors.Is(err, identity.ErrNotVisible) {
		return &apierror.Error{Code: "not_found", Cause: err}
	}
	return foundationAuthError(err)
}

func (d Dependencies) userMutationBinding(ctx context.Context, p identity.Principal) (usermutation.Binding, error) {
	var family identity.FamilyState
	err := dbwork.WithReadSnapshot(ctx, d.DB, func(ctx context.Context) error {
		var e error
		family, e = d.Identity.SessionFamilyTx(ctx, dbwork.Snapshot(ctx), p)
		return e
	})
	return usermutation.Binding{Authority: p.Authority, AccountID: p.AccountID, ProfileID: p.ProfileID, FamilyID: family.ID}, err
}

// bindUserMutation captures the received semantic request before a handler can
// normalize it. It never stores credentials or accepts an actor from the body.
func (d Dependencies) bindUserMutation(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	meta, ok := d.registry.Lookup(r.Method, r.URL.EscapedPath())
	if !ok || !userMutationRoutes[meta.ID] {
		return r, true
	}
	key := r.Header.Get("Idempotency-Key")
	limit := meta.BodyLimit
	if limit <= 0 {
		limit = 65536
	}
	if limit > 65536 {
		limit = 65536
	}
	var body any
	if r.Body != nil {
		raw, e := io.ReadAll(io.LimitReader(r.Body, limit+1))
		if e != nil || int64(len(raw)) > limit {
			policyError(w, "invalid_request")
			return r, false
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		if len(bytes.TrimSpace(raw)) > 0 {
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			if e = decoder.Decode(&body); e != nil {
				policyError(w, "invalid_request")
				return r, false
			}
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				policyError(w, "invalid_request")
				return r, false
			}
		}
	}
	protocol := strings.HasPrefix(key, "p251_")
	if object, ok := body.(map[string]any); ok {
		for _, name := range []string{"operationId", "idempotencyKey", "requestId"} {
			if supplied, exists := object[name]; exists {
				reserved, _ := supplied.(string)
				if (protocol || strings.HasPrefix(reserved, "p251_")) && supplied != key {
					policyError(w, "idempotency_conflict")
					return r, false
				}
			}
		}
	}
	if !protocol {
		return r, true
	}
	p, err := d.principal(r)
	if err != nil {
		failure(w, err)
		return r, false
	}
	binding, err := d.userMutationBinding(r.Context(), p)
	if err != nil {
		failure(w, err)
		return r, false
	}
	// P2-62: the client's mutation root travels with the request, and the store
	// binds the receipt to that root's exact lifetimes. Only the identifier is
	// captured here; the root is proved against the live owner inside the
	// accepting transaction. An absent root keeps the existing rootless receipt.
	if root := strings.TrimSpace(r.Header.Get("Portico-Mutation-Root")); root != "" {
		if !usermutation.ValidRootID(root) {
			policyError(w, "invalid_request")
			return r, false
		}
		binding.RootID = root
	}
	issued, err := usermutation.IssuedMs(key, time.Now().UnixMilli())
	if err != nil {
		failure(w, err)
		return r, false
	}
	cutoff, err := usermutation.Policy(r.Context(), d.DB, binding, time.Now().UnixMilli())
	if err != nil {
		failure(w, err)
		return r, false
	}
	if issued < cutoff {
		failure(w, usermutation.ErrExpired)
		return r, false
	}
	canonical, err := json.Marshal(struct {
		Method, Path, Query string
		Body                any
	}{r.Method, r.URL.EscapedPath(), r.URL.Query().Encode(), body})
	if err != nil {
		policyError(w, "invalid_request")
		return r, false
	}
	digest := sha256.Sum256(canonical)
	envelope := usermutation.Envelope{Key: key, Operation: meta.ID, Digest: hex.EncodeToString(digest[:]), Stop: meta.ID == "stop_playback_session" || meta.ID == "stop_account_playback"}
	ctx := usermutation.WithRequest(dbwork.WithDurableUser(r.Context()), binding, envelope)
	ctx = usermutation.WithAuthority(ctx, func(ctx context.Context, tx *sql.Tx) error {
		family, err := d.Identity.SessionFamilyTx(ctx, tx, p)
		if err != nil {
			return err
		}
		if family.ID != binding.FamilyID {
			return identity.ErrUnauthorized
		}
		if meta.Access == apikit.Owner {
			return d.ownerAuthorityTx(ctx, tx, p)
		}
		return nil
	})
	w.Header().Set("Portico-Mutation-Key", key)
	return r.WithContext(ctx), true
}

// Receipt authorization follows the original operation, while absent targets
// can still acknowledge a committed intent within the original live family.
func (d Dependencies) userMutationReceiptAccess(ctx context.Context, p identity.Principal, receipt *usermutation.Receipt) error {
	tx := dbwork.Snapshot(ctx)
	if tx == nil {
		return errors.New("receipt authorization requires a snapshot")
	}
	var definitions []apikit.Definition
	if d.registry != nil {
		definitions = d.registry.Definitions()
	}
	for _, meta := range definitions {
		if meta.ID == receipt.Operation && meta.Access == apikit.Owner {
			if err := d.ownerAuthorityTx(ctx, tx, p); err != nil {
				return err
			}
			break
		}
	}
	switch receipt.Operation {
	case "stop_playback_session", "stop_account_playback":
		return v1Error(d.playbackV1().AuthorizeStopReceipt(ctx, playbackv1.Caller{Principal: p, AuthorizeItem: func(ctx context.Context, item string) error {
			return itemAdmission(d.itemAccess(ctx, p, item))
		}}, *receipt))
	case "put_saved_pins_kind_id":
		// A pin needs current read access, including viewer shares and server-
		// visible collections. Prove existence separately: a revoked share must
		// not be mistaken for the genuine absence that permits acknowledgement.
		var kind string
		err := tx.QueryRowContext(ctx, `SELECT kind FROM saved_resources WHERE id=? UNION ALL SELECT 'playlist' FROM catalog_playlists WHERE token=?`, receipt.TargetID, receipt.TargetID).Scan(&kind)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		switch kind {
		case "collection", "view":
			_, _, err = d.Catalog.WithContext(ctx).SavedResourceReceiptStatus(receipt.TargetID, resourceActor(p))
		case "playlist":
			_, err = d.Catalog.WithContext(ctx).PlaylistReceiptStatus(receipt.TargetID, resourceActor(p))
		default:
			return sql.ErrNoRows
		}
		return err
	case "put_items_personal_state_batch":
		return nil // No historical per-item identifiers are retained.
	case "put_items_id_personal_state", "put_items_itemId_reading_favorite", "put_items_itemId_reading_mark", "put_items_itemId_reading_my_list":
		var present int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM catalog_entities WHERE public_id=pid_blob(?)`, receipt.TargetID).Scan(&present)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if receipt.Operation == "put_items_id_personal_state" {
			return d.itemAccess(ctx, p, receipt.TargetID)
		}
		return d.savedReadingItemAuthorityTx(ctx, tx, p, receipt.TargetID)
	case "set_container_personal_state", "undo_container_personal_state":
		var kind, library string
		err := tx.QueryRowContext(ctx, `SELECT k.name,l.library_id FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind JOIN catalog_libraries l ON l.id=e.library_id WHERE e.public_id=pid_blob(?)`, receipt.TargetID).Scan(&kind, &library)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = d.allowedLibraryTx(ctx, p, library, tx); err != nil {
			return err
		}
		viewer, err := d.catalogViewer((&http.Request{}).WithContext(ctx), p, []string{library}, "")
		if err != nil {
			return err
		}
		return d.Catalog.WithContext(ctx).VisibleEntity(ctx, viewer, kind, receipt.TargetID)
	case "post_saved_resources", "delete_saved_resources_id", "patch_saved_resources_id", "put_saved_resources_id_entries", "reorder_saved_collection", "delete_saved_resources_id_shares", "put_saved_resources_id_shares":
		var owner, role string
		var deleted bool
		err := tx.QueryRowContext(ctx, `SELECT owner_key,deleted,COALESCE((SELECT role FROM saved_resource_shares WHERE resource_id=r.id AND authority=? AND account_id=? AND profile_id=?),'') FROM saved_resources r WHERE id=?`, p.Authority, p.AccountID, p.ProfileID, receipt.TargetID).Scan(&owner, &deleted, &role)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if owner == identity.PersonalKey(p.Viewer) {
			return nil
		}
		if !deleted && role == "editor" && (receipt.Operation == "put_saved_resources_id_entries" || receipt.Operation == "reorder_saved_collection") {
			return nil
		}
		return sql.ErrNoRows
	case "post_playlists", "delete_playlists_id", "patch_playlists_id", "post_playlists_id_entries", "delete_playlists_id_entries_entryId", "reorder_playlist_window", "delete_playlists_id_shares", "put_playlists_id_shares":
		var authority, account, profile, role string
		var deleted bool
		err := tx.QueryRowContext(ctx, `SELECT owner_authority,owner_account,owner_profile,deleted,COALESCE((SELECT role FROM playlist_shares WHERE playlist_id=p.token AND authority=? AND account_id=? AND profile_id=?),'') FROM catalog_playlists p WHERE token=?`, p.Authority, p.AccountID, p.ProfileID, receipt.TargetID).Scan(&authority, &account, &profile, &deleted, &role)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if authority == p.Authority && account == p.AccountID && profile == p.ProfileID {
			return nil
		}
		if !deleted && role == "editor" && (receipt.Operation == "post_playlists_id_entries" || receipt.Operation == "delete_playlists_id_entries_entryId" || receipt.Operation == "reorder_playlist_window") {
			return nil
		}
		return sql.ErrNoRows
	}
	return nil
}

// Resolve a replay before target-dependent HTTP admission. Lookup never reserves
// or mutates: a new intent still enters its domain's accepting transaction.
func (d Dependencies) personalMutationReplay(ctx context.Context, p identity.Principal) (out *usermutation.Receipt, missing bool, err error) {
	binding, envelope, ok := usermutation.RequestFrom(ctx)
	if !ok {
		return nil, false, nil
	}
	err = dbwork.WithReadSnapshot(ctx, d.DB, func(ctx context.Context) error {
		tx := dbwork.Snapshot(ctx)
		if err := usermutation.AuthorizeContext(ctx, tx); err != nil {
			return err
		}
		var e error
		out, e = usermutation.LookupTx(ctx, tx, binding, envelope.Key, time.Now().UnixMilli())
		if e != nil || out == nil {
			return e
		}
		var digest string
		if e = tx.QueryRowContext(ctx, `SELECT digest FROM user_mutation_receipts WHERE key=?`, envelope.Key).Scan(&digest); e != nil {
			return e
		}
		if digest != envelope.Digest || out.Operation != envelope.Operation {
			return usermutation.ErrConflict
		}
		if e = d.userMutationReceiptAccess(ctx, p, out); e != nil {
			return e
		}
		var present int
		e = tx.QueryRowContext(ctx, `SELECT 1 FROM catalog_entities WHERE public_id=pid_blob(?)`, out.TargetID).Scan(&present)
		missing = errors.Is(e, sql.ErrNoRows)
		if missing {
			return nil
		}
		return e
	})
	return
}

type userMutationPolicy struct {
	ServerTimeMs    int64 `json:"serverTimeMs"`
	RetiredBeforeMs int64 `json:"retiredBeforeMs"`
	RetentionDays   int   `json:"retentionDays"`
}
type retireUserMutations struct {
	Authority                 string `json:"authority"`
	AccountID                 string `json:"accountId"`
	ConfirmReceiptHistoryLoss bool   `json:"confirmReceiptHistoryLoss"`
}

func registerUserMutationRoutes(registry *apikit.Registry, d Dependencies) error {
	d.registry = registry
	if err := apikit.Register(registry, apikit.Route[struct{}, usermutation.Receipt]{Metadata: apikit.Metadata{Audience: apikit.Clients, ID: "get_user_mutation", Headers: []string{"Portico-Mutation-Root"}, Method: "GET", Path: "/v1/user-mutations/{key}", Summary: "Read the original sign-in's compact mutation receipt", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_permitted", "not_found", "receipt_unknown", "receipt_expired", "invalid_mutation_key", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (usermutation.Receipt, error) {
		p, err := d.principal(r)
		if err != nil {
			return usermutation.Receipt{}, mutationRouteError(err)
		}
		var receipt *usermutation.Receipt
		err = dbwork.WithReadSnapshot(ctx, d.DB, func(ctx context.Context) error {
			binding, err := d.userMutationBinding(ctx, p)
			if err != nil {
				return err
			}
			if root := strings.TrimSpace(r.Header.Get("Portico-Mutation-Root")); root != "" {
				if !usermutation.ValidRootID(root) {
					return usermutation.ErrInvalidKey
				}
				binding.RootID = root
			}
			receipt, err = usermutation.Lookup(ctx, d.DB, binding, r.PathValue("key"), time.Now().UnixMilli())
			if err != nil {
				return err
			}
			if receipt == nil {
				return &apierror.Error{Code: "receipt_unknown"}
			}
			if p.Grant == identity.GrantCastTarget && receipt.Operation != "stop_playback_session" {
				return identity.ErrForbidden
			}
			return d.userMutationReceiptAccess(ctx, p, receipt)
		})
		if err != nil {
			return usermutation.Receipt{}, mutationRouteError(err)
		}
		// The receipt and its current scope are proved above. Release that read
		// snapshot before completing the Stop's captured process/grant fences.
		if receipt.Operation == "stop_playback_session" || receipt.Operation == "stop_account_playback" {
			if err = d.playbackV1().CompleteStopReceipt(ctx, *receipt); err != nil {
				return usermutation.Receipt{}, mutationRouteError(err)
			}
		}
		apikit.SetHeader(ctx, "Cache-Control", "no-store")
		return *receipt, nil
	}}); err != nil {
		return err
	}
	if err := apikit.Register(registry, apikit.Route[struct{}, userMutationPolicy]{Metadata: apikit.Metadata{Audience: apikit.Clients, ID: "get_user_mutation_policy", Method: "GET", Path: "/v1/user-mutations/policy", Summary: "Read server time and the mutation replay cutoff", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_permitted", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (userMutationPolicy, error) {
		p, err := d.principal(r)
		if err != nil {
			return userMutationPolicy{}, mutationRouteError(err)
		}
		binding, err := d.userMutationBinding(ctx, p)
		if err != nil {
			return userMutationPolicy{}, mutationRouteError(err)
		}
		now := time.Now().UnixMilli()
		cutoff, err := usermutation.Policy(ctx, d.DB, binding, now)
		apikit.SetHeader(ctx, "Cache-Control", "no-store")
		return userMutationPolicy{now, cutoff, 30}, mutationRouteError(err)
	}}); err != nil {
		return err
	}
	return apikit.Register(registry, apikit.Route[retireUserMutations, userMutationPolicy]{Metadata: apikit.Metadata{Audience: apikit.Clients, ID: "retire_user_mutations", Method: "POST", Path: "/v1/admin/user-mutations/retire", Summary: "Retire an account's old receipt history without changing user data", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Security, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 1024, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "internal"}}, Handler: func(ctx context.Context, r *http.Request, input retireUserMutations) (userMutationPolicy, error) {
		p, err := d.owner(r)
		if err != nil {
			return userMutationPolicy{}, mutationRouteError(err)
		}
		if !input.ConfirmReceiptHistoryLoss || (input.Authority != "local" && input.Authority != "hosted") || input.AccountID == "" || len(input.AccountID) > 128 {
			return userMutationPolicy{}, &apierror.Error{Code: "invalid_request"}
		}
		now := time.Now().UnixMilli()
		err = dbwork.WithWriteTxContext(dbwork.WithDurableUser(ctx), d.DB, dbwork.ClassSecurityFence, func(ctx context.Context, tx *sql.Tx) error {
			if err := d.ownerAuthorityTx(ctx, tx, p); err != nil {
				return err
			}
			return usermutation.RetireAccount(ctx, tx, input.Authority, input.AccountID, now)
		})
		return userMutationPolicy{now, now, 30}, mutationRouteError(err)
	}})
}
