package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"time"
	"unicode/utf8"
)

const manualMetadataRequestBytes = 256 << 10

func decodeManualMetadata(w http.ResponseWriter, r *http.Request, value *catalog.ManualMetadataMutation) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, manualMetadataRequestBytes))
	if err != nil || !utf8.Valid(raw) {
		return catalog.ErrManualMetadataInput
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(value); err != nil {
		return catalog.ErrManualMetadataInput
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return catalog.ErrManualMetadataInput
	}
	return nil
}

func (d Dependencies) manualMetadataAdministrator(ctx context.Context, r *http.Request) (identity.Principal, error) {
	return d.administrator(r.WithContext(ctx))
}

// Recheck the live administrative membership and token family in the metadata transaction.
func (d Dependencies) manualMetadataAuthorize(ctx context.Context, tx *sql.Tx, p identity.Principal) error {
	_, err := d.tierAuthorityTx(ctx, tx, p, identity.TierAdmin)
	return err
}

func (d Dependencies) manualMetadataRoutes(mux *http.ServeMux) {
	for _, method := range []string{"GET", "PATCH"} {
		d.httpHandle(mux, method+" /v1/items/{id}/metadata/manual", d.manualMetadataHandler(method, d.Catalog.ManualMetadata))
	}
}

// This endpoint admits local owners and administrators. The joined predicates preserve the
// resourceAuthorize session/account rules plus the administrative tier, with cancellable SQL.
func manualMetadataAuthorize(ctx context.Context, tx *sql.Tx, p identity.Principal) error {
	if p.Authority != "local" || !identity.Administrative(p.Role) {
		return identity.ErrForbidden
	}
	var count int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM authorization_access s JOIN accounts a ON a.id=s.account_id AND a.profile_id=s.profile_id AND a.epoch=s.epoch JOIN direct_memberships m ON m.account_id=a.id AND m.role=s.role AND m.role IN ('owner','admin') AND m.disabled=0 JOIN direct_profiles p ON p.account_id=a.id AND p.id=s.profile_id AND p.deleted=0 AND p.is_primary=1 WHERE s.hash=? AND s.authority='local' AND s.role IN ('owner','admin') AND s.account_id=? AND s.profile_id=? AND s.epoch=? AND s.revoked=0 AND s.expires_at>?`, p.Hash, p.AccountID, p.ProfileID, p.Epoch, time.Now().UTC().Format(time.RFC3339)).Scan(&count)
	if err != nil {
		return err
	}
	if count != 1 {
		return identity.ErrUnauthorized
	}
	return nil
}

func (d Dependencies) manualMetadataHandler(method string, read func(context.Context, string, string, string, func(*sql.Tx) error) (catalog.ManualMetadata, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		p, err := d.manualMetadataAdministrator(ctx, r)
		if err != nil {
			failure(w, err)
			return
		}
		if r.URL.RawQuery != "" {
			failure(w, catalog.ErrManualMetadataInput)
			return
		}
		if _, valid := entityid.Decode(r.PathValue("id")); !valid {
			failure(w, sql.ErrNoRows)
			return
		}
		fence := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d", presentationIdentity(p), p.Epoch))))
		authorize := func(tx *sql.Tx) error {
			if err := d.manualMetadataAuthorize(ctx, tx, p); err != nil {
				return err
			}
			var library string
			if err := tx.QueryRowContext(ctx, `SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?) AND cl.retired=0`, r.PathValue("id")).Scan(&library); err != nil {
				return err
			}
			return d.allowedLibraryTx(ctx, p, library, tx)
		}
		var out catalog.ManualMetadata
		if method == "GET" {
			out, err = read(ctx, d.Identity.ID(), fence, r.PathValue("id"), authorize)
		} else {
			var body catalog.ManualMetadataMutation
			if err = decodeManualMetadata(w, r, &body); err != nil {
				failure(w, err)
				return
			}
			out, err = d.Catalog.SaveManualMetadata(ctx, d.Identity.ID(), fence, r.PathValue("id"), p.AccountID, body, authorize)
		}
		if errors.Is(err, catalog.ErrManualMetadataConflict) {
			write(w, 409, map[string]any{"error": map[string]any{"code": "manual_metadata_conflict", "message": publicErrorMessage("manual_metadata_conflict"), "retryable": false}})
			return
		}
		if err != nil {
			if errors.Is(err, catalog.ErrManualMetadataProjection) {
				write(w, 503, map[string]any{"error": map[string]any{"code": "metadata_edit_unavailable", "message": "Editable metadata could not be read.", "retryable": true}})
			} else {
				failure(w, err)
			}
			return
		}
		if _, err = d.manualMetadataAdministrator(ctx, r); err != nil {
			failure(w, err)
			return
		}
		encoded, encodeErr := json.Marshal(out)
		if encodeErr != nil || len(encoded) > 1<<20 {
			write(w, 503, map[string]any{"error": map[string]any{"code": "metadata_edit_unavailable", "message": "Editable metadata could not be read.", "retryable": true}})
			return
		}
		if ctx.Err() != nil {
			write(w, 503, map[string]any{"error": map[string]any{"code": "metadata_edit_interrupted", "message": "The metadata response was interrupted. Check current values before trying again.", "retryable": false}})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write(encoded)
	}
}
