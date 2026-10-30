package networking

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"time"
)

func certificateOwnerID(id identityRow, incarnation string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("portico/tls-owner/v1\x00%s\x00%s\x00%d\x00%s\x00%s", incarnation, id.server, id.generation, id.active.String, id.installed.String)))
	return hex.EncodeToString(sum[:])
}
func readCertificateConfigTx(ctx context.Context, tx *sql.Tx, id identityRow, incarnation string) (CertificateConfig, error) {
	var c CertificateConfig
	var server, root string
	var generation int64
	e := tx.QueryRowContext(ctx, `SELECT enabled,public_port,public_address,revision,server_id,local_generation,incarnation FROM networking_certificate_settings WHERE singleton=1`).Scan(&c.Enabled, &c.PublicPort, &c.PublicAddress, &c.Revision, &server, &generation, &root)
	if e != nil {
		return c, e
	}
	if server != id.server || generation != id.generation || root != incarnation {
		c.Enabled = true
		c.PublicPort = 32500
		c.PublicAddress = ""
		c.Revision++
		_, e = tx.ExecContext(ctx, `UPDATE networking_certificate_settings SET enabled=1,public_port=32500,public_address='',revision=?,server_id=?,local_generation=?,incarnation=? WHERE singleton=1`, c.Revision, id.server, id.generation, incarnation)
	}
	return c, e
}
func readCertificateStateTx(ctx context.Context, tx *sql.Tx, scope certificateScope) (certificateState, error) {
	q := certificateState{Scope: scope}
	var next, routeNext int64
	e := tx.QueryRowContext(ctx, `SELECT namespace,issuer,environment,pending_id,active_id,previous_id,state,error_code,next_attempt,attempts,route_address,route_port,route_hostname,route_url,route_next_attempt,route_error_code FROM networking_certificate_state WHERE scope_id=? AND operation_id=? AND server_id=? AND local_generation=? AND claim_generation=? AND credential_generation=? AND incarnation=?`, scope.ID, scope.Intent.OperationID, scope.Intent.ServerID, scope.Intent.LocalGeneration, scope.Intent.ClaimGeneration, scope.Intent.CredentialGeneration, scope.Incarnation).Scan(&q.Namespace, &q.Issuer, &q.Environment, &q.PendingID, &q.ActiveID, &q.PreviousID, &q.State, &q.ErrorCode, &next, &q.Attempts, &q.RouteAddress, &q.RoutePort, &q.RouteHostname, &q.RouteURL, &routeNext, &q.RouteErrorCode)
	if next > 0 {
		q.NextAttempt = time.UnixMilli(next)
	}
	if routeNext > 0 {
		q.RouteNextAttempt = time.UnixMilli(routeNext)
	}
	return q, e
}
func (m *CertificateManager) snapshotTx(ctx context.Context, tx *sql.Tx) (CertificateConfig, certificateState, string, error) {
	var q certificateState
	lease, e := claimAuthority(ctx)
	if e != nil {
		return CertificateConfig{}, q, "", e
	}
	id, e := readIdentity(ctx, tx)
	if e != nil {
		return CertificateConfig{}, q, "", e
	}
	c, e := readCertificateConfigTx(ctx, tx, id, lease.Incarnation())
	if e != nil {
		return c, q, "", e
	}
	ownerID := certificateOwnerID(id, lease.Incarnation())
	q.State = "claim_required"
	if !id.active.Valid || !id.installed.Valid || id.active.String != id.installed.String {
		return c, q, ownerID, nil
	}
	stored, e := loadIntentTx(ctx, tx, id.installed.String)
	if e != nil {
		return c, q, ownerID, e
	}
	if stored.Stage != Installed || !stored.InstallationAcknowledged {
		q.State = "claim_pending"
		return c, q, ownerID, nil
	}
	current, e := m.store.positive(ctx, tx, stored.Intent)
	if e != nil {
		if errors.Is(e, ErrStale) || errors.Is(e, ErrUnavailable) || errors.Is(e, ErrCancelled) {
			q.State = "authority_unavailable"
			return c, q, ownerID, nil
		}
		return c, q, ownerID, e
	}
	scope, e := scopeForCertificate(ctx, current.Intent, m.environment)
	if e != nil {
		return c, q, ownerID, e
	}
	_, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO networking_certificate_state(scope_id,operation_id,server_id,local_generation,claim_generation,credential_generation,incarnation) VALUES(?,?,?,?,?,?,?)`, scope.ID, current.OperationID, current.ServerID, current.LocalGeneration, current.ClaimGeneration, current.CredentialGeneration, scope.Incarnation)
	if e != nil {
		return c, q, ownerID, e
	}
	q, e = readCertificateStateTx(ctx, tx, scope)
	return c, q, scope.ID, e
}
func (m *CertificateManager) snapshot(ctx context.Context) (CertificateConfig, certificateState, string, error) {
	gated, e := m.store.tx(ctx)
	if e != nil {
		return CertificateConfig{}, certificateState{}, "", e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	c, q, id, e := m.snapshotTx(ctx, tx)
	if e != nil {
		return c, q, id, e
	}
	return c, q, id, commitClaimDatabaseTx(ctx, gated)
}
func readCertificateMaterialTx(ctx context.Context, tx *sql.Tx, scope, id string) (certificateMaterial, error) {
	var mat certificateMaterial
	var before, after, renew int64
	e := tx.QueryRowContext(ctx, `SELECT request_id,scope_id,order_id,state,key_nonce,key_cipher,csr,chain,not_before,not_after,renew_at FROM networking_certificates WHERE request_id=? AND scope_id=?`, id, scope).Scan(&mat.RequestID, &mat.ScopeID, &mat.OrderID, &mat.State, &mat.Nonce, &mat.Cipher, &mat.CSR, &mat.Chain, &before, &after, &renew)
	if before > 0 {
		mat.NotBefore = time.UnixMilli(before)
	}
	if after > 0 {
		mat.NotAfter = time.UnixMilli(after)
	}
	if renew > 0 {
		mat.RenewAt = time.UnixMilli(renew)
	}
	return mat, e
}
func (m *CertificateManager) material(ctx context.Context, scope certificateScope, id string) (certificateMaterial, error) {
	var mat certificateMaterial
	e := m.store.WithInstalledTransaction(ctx, scope.Intent, func(ctx context.Context, tx *sql.Tx) error {

		current, e := scopeForCertificate(ctx, scope.Intent, m.environment)
		if e != nil || current.ID != scope.ID || current.Incarnation != scope.Incarnation {
			return ErrStale
		}
		mat, e = readCertificateMaterialTx(ctx, tx, scope.ID, id)
		return e
	})
	return mat, e
}
func (m *CertificateManager) mutate(ctx context.Context, scope certificateScope, enabled bool, apply func(*sql.Tx, certificateState) error) error {
	// Material publication/withdrawal must be durable on this exact writer.
	// The normal pool is NORMAL, including after another security commit;
	// dbwork owns switching FULL for the fence and restoring the connection.
	ctx = dbwork.WithClass(ctx, dbwork.ClassSecurityFence)
	return m.store.WithInstalledTransaction(ctx, scope.Intent, func(ctx context.Context, tx *sql.Tx) error {
		c, q, _, e := m.snapshotTx(ctx, tx)
		if e != nil {
			return e
		}
		if q.Scope.ID != scope.ID || enabled && !c.Enabled {
			return ErrStale
		}
		var sync int
		if e = tx.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&sync); e != nil {
			return e
		}
		if sync < 2 {
			return errCertificateMaterial
		}
		return apply(tx, q)
	})
}
func (m *CertificateManager) progress(ctx context.Context, s certificateScope, state, code string, next time.Time) error {
	return m.mutate(ctx, s, false, func(tx *sql.Tx, q certificateState) error {
		// A late failure must not erase pause/cancel intent for this request.
		if q.PendingID != "" && q.State == "cancel_pending" {
			state = "cancel_pending"
		}
		_, e := tx.ExecContext(ctx, `UPDATE networking_certificate_state SET state=?,error_code=?,next_attempt=?,attempts=attempts+1 WHERE scope_id=?`, state, code, next.UnixMilli(), s.ID)
		return e
	})
}
func (m *CertificateManager) publishNamespace(ctx context.Context, s certificateScope, n certificateNamespace) error {
	return m.mutate(ctx, s, true, func(tx *sql.Tx, q certificateState) error {
		if q.Namespace != "" && q.Namespace != n.Namespace {
			return ErrStale
		}
		_, e := tx.ExecContext(ctx, `UPDATE networking_certificate_state SET namespace=?,issuer=?,environment=?,state='waiting',error_code='',next_attempt=0 WHERE scope_id=?`, n.Namespace, n.Issuer, n.Environment, s.ID)
		return e
	})
}
func (m *CertificateManager) prepareMaterial(ctx context.Context, q certificateState) error {
	mat, e := m.newMaterial(q.Scope, q.Namespace)
	if e != nil {
		return e
	}
	return m.mutate(ctx, q.Scope, true, func(tx *sql.Tx, current certificateState) error {
		if current.PendingID != "" || current.Namespace != q.Namespace {
			return ErrStale
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO networking_certificates(request_id,scope_id,key_nonce,key_cipher,csr,created_at) VALUES(?,?,?,?,?,?)`, mat.RequestID, mat.ScopeID, mat.Nonce, mat.Cipher, mat.CSR, time.Now().UnixMilli())
		if e != nil {
			return e
		}
		saved, e := readCertificateMaterialTx(ctx, tx, mat.ScopeID, mat.RequestID)
		if e != nil {
			return e
		}
		if _, e = m.privateKey(q.Scope, saved); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE networking_certificate_state SET pending_id=?,state='requesting',error_code='',next_attempt=0,attempts=0 WHERE scope_id=?`, mat.RequestID, mat.ScopeID)
		return e
	})
}
func (m *CertificateManager) receiveOrder(ctx context.Context, q certificateState, mat certificateMaterial, o certificateOrder) error {
	return m.mutate(ctx, q.Scope, false, func(tx *sql.Tx, current certificateState) error {
		if current.PendingID != mat.RequestID {
			return ErrStale
		}
		result, e := tx.ExecContext(ctx, `UPDATE networking_certificates SET order_id=?,state=? WHERE request_id=? AND scope_id=? AND (order_id='' OR order_id=?)`, o.ID, o.State, mat.RequestID, q.Scope.ID, o.ID)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil || n != 1 {
			return ErrStale
		}
		next := time.Now().Add(20 * time.Second)
		if o.State == "reconciliation_required" {
			next = time.Now().Add(5 * time.Minute)
		}
		if o.NextAttemptAt.After(next) {
			next = o.NextAttemptAt
		}
		state := o.State
		if current.State == "cancel_pending" {
			state = "cancel_pending"
			next = time.Now() // receipt arrived: exact cancellation can run now
		}
		_, e = tx.ExecContext(ctx, `UPDATE networking_certificate_state SET state=?,error_code=?,next_attempt=? WHERE scope_id=?`, state, safeCertificateCode(o.ErrorCode), next.UnixMilli(), q.Scope.ID)
		return e
	})
}
func (m *CertificateManager) install(ctx context.Context, q certificateState, mat certificateMaterial, chain []byte) error {
	cert, e := m.validateMaterial(q.Scope, q.Namespace, mat, chain, time.Now())
	if e != nil {
		return e
	}
	renew := certificateRenewAt(mat.RequestID, cert.Leaf.NotBefore, cert.Leaf.NotAfter)
	return m.mutate(ctx, q.Scope, true, func(tx *sql.Tx, current certificateState) error {
		if current.PendingID != mat.RequestID || current.State == "cancel_pending" || current.Namespace != q.Namespace || current.Environment != m.environment {
			return ErrStale
		}
		_, e := tx.ExecContext(ctx, `UPDATE networking_certificates SET chain=?,state='issued',not_before=?,not_after=?,renew_at=? WHERE request_id=? AND scope_id=? AND order_id=?`, chain, cert.Leaf.NotBefore.UnixMilli(), cert.Leaf.NotAfter.UnixMilli(), renew.UnixMilli(), mat.RequestID, q.Scope.ID, mat.OrderID)
		if e != nil {
			return e
		}
		saved, e := readCertificateMaterialTx(ctx, tx, q.Scope.ID, mat.RequestID)
		if e != nil {
			return e
		}
		if _, e = m.validateMaterial(q.Scope, q.Namespace, saved, saved.Chain, time.Now()); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE networking_certificate_state SET previous_id=CASE WHEN active_id<>'' THEN active_id ELSE previous_id END,active_id=?,pending_id='',state='ready',error_code='',next_attempt=?,attempts=0 WHERE scope_id=?`, mat.RequestID, renew.UnixMilli(), q.Scope.ID)
		if e != nil {
			return e
		}
		// Bounded private material retention: only current, previous and pending keys.
		_, e = tx.ExecContext(ctx, `DELETE FROM networking_certificates WHERE scope_id=? AND request_id NOT IN (SELECT active_id FROM networking_certificate_state WHERE scope_id=? UNION SELECT previous_id FROM networking_certificate_state WHERE scope_id=? UNION SELECT pending_id FROM networking_certificate_state WHERE scope_id=?)`, q.Scope.ID, q.Scope.ID, q.Scope.ID, q.Scope.ID)
		return e
	})
}
func (m *CertificateManager) clearPending(ctx context.Context, q certificateState, mat certificateMaterial, next time.Time) error {
	return m.mutate(ctx, q.Scope, false, func(tx *sql.Tx, current certificateState) error {
		if current.PendingID != mat.RequestID {
			return ErrStale
		}
		_, e := tx.ExecContext(ctx, `UPDATE networking_certificate_state SET pending_id='',next_attempt=?,state='waiting' WHERE scope_id=?`, next.UnixMilli(), q.Scope.ID)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `DELETE FROM networking_certificates WHERE request_id=? AND scope_id=? AND request_id<>? AND request_id<>?`, mat.RequestID, q.Scope.ID, current.ActiveID, current.PreviousID)
		return e
	})
}
func (m *CertificateManager) withServingMaterial(ctx context.Context, scope certificateScope, request string, sign func() error) error {
	tx, release, e := dbwork.BeginRead(ctx, m.store.db)
	if e != nil {
		return e
	}
	defer release()
	q, e := m.servingSnapshotTx(ctx, tx)
	if e != nil {
		return e
	}
	if q.Scope.ID != scope.ID || (q.ActiveID != request && q.PreviousID != request) {
		return ErrStale
	}
	mat, e := readCertificateMaterialTx(ctx, tx, scope.ID, request)
	if e != nil {
		return e
	}
	now := time.Now()
	if mat.State != "issued" || now.Before(mat.NotBefore) || !now.Before(mat.NotAfter) {
		return errCertificateMaterial
	}
	return sign()
}

func validCertificateAddress(address string) bool {
	if address == "" {
		return true
	}
	ip, e := netip.ParseAddr(address)
	return e == nil && ip.String() == address && ip.Zone() == "" && !ip.Is4In6() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !netip.MustParsePrefix("100.64.0.0/10").Contains(ip)
}
func (m *CertificateManager) configure(ctx context.Context, expected string, c CertificateConfig) error {
	if expected == "" || c.Revision < 1 || c.PublicPort < 1 || c.PublicPort > 65535 || !validCertificateAddress(c.PublicAddress) {
		return ErrInvalid
	}
	gated2, e := m.store.tx(ctx)
	if e != nil {
		return e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	old, q, authority, e := m.snapshotTx(ctx, tx)
	if e != nil {
		return e
	}
	if expected != authority || c.Revision != old.Revision {
		return ErrStale
	}
	// The public address is detected, never typed: an owner chooses only the port.
	c.PublicAddress = old.PublicAddress
	_, e = tx.ExecContext(ctx, `UPDATE networking_certificate_settings SET enabled=?,public_port=?,public_address=?,revision=revision+1 WHERE singleton=1`, c.Enabled, c.PublicPort, c.PublicAddress)
	if e != nil {
		return e
	}
	if q.Scope.ID != "" && (old.PublicPort != c.PublicPort || old.PublicAddress != c.PublicAddress) {
		_, e = tx.ExecContext(ctx, `UPDATE networking_certificate_state SET route_address='',route_port=0,route_hostname='',route_url='',route_next_attempt=0,route_error_code='' WHERE scope_id=?`, q.Scope.ID)
		if e != nil {
			return e
		}
	}
	if q.Scope.ID != "" && old.Enabled && !c.Enabled && q.PendingID != "" {
		if _, e = tx.ExecContext(ctx, `UPDATE networking_certificate_state SET state='cancel_pending',next_attempt=0 WHERE scope_id=?`, q.Scope.ID); e != nil {
			return e
		}
	}
	return commitClaimDatabaseTx(ctx, gated2)
}
func (m *CertificateManager) publishRoute(ctx context.Context, q certificateState, c CertificateConfig, r certificateRoute) error {
	return m.mutate(ctx, q.Scope, true, func(tx *sql.Tx, current certificateState) error {
		var revision int64
		if e := tx.QueryRowContext(ctx, `SELECT revision FROM networking_certificate_settings WHERE singleton=1`).Scan(&revision); e != nil {
			return e
		}
		if revision != c.Revision || current.Namespace != r.Namespace {
			return ErrStale
		}
		_, e := tx.ExecContext(ctx, `UPDATE networking_certificate_state SET route_address=?,route_port=?,route_hostname=?,route_url=?,route_next_attempt=0,route_error_code='' WHERE scope_id=?`, c.PublicAddress, c.PublicPort, r.Hostname, r.BaseURL, q.Scope.ID)
		return e
	})
}

// Erase only this operation's TLS secrets during known-terminal local authority
// publication. Hosted's independent public receipts continue deny-only cleanup.
func RevokeCertificatesTx(ctx context.Context, tx *sql.Tx, operation string) error {
	if tx == nil || !validID(operation) {
		return ErrInvalid
	}
	var exists int
	if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='networking_certificate_state'`).Scan(&exists); e != nil {
		return e
	}
	if exists == 0 {
		return nil
	}
	if _, e := tx.ExecContext(ctx, `DELETE FROM networking_certificates WHERE scope_id IN (SELECT scope_id FROM networking_certificate_state WHERE operation_id=?)`, operation); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, `DELETE FROM networking_certificate_state WHERE operation_id=?`, operation)
	return e
}

// observedPublicAddress records the detected WAN address. A change clears the published route
// name so the next step names the new address, which is the one Hosted call a WAN change costs.
func (m *CertificateManager) observedPublicAddress(ctx context.Context, address string) (bool, error) {
	if !validCertificateAddress(address) || address == "" {
		return false, ErrInvalid
	}
	gated, e := m.store.tx(ctx)
	if e != nil {
		return false, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	old, q, _, e := m.snapshotTx(ctx, tx)
	if e != nil {
		return false, e
	}
	if old.PublicAddress == address {
		return false, nil
	}
	if _, e = tx.ExecContext(ctx, `UPDATE networking_certificate_settings SET public_address=? WHERE singleton=1`, address); e != nil {
		return false, e
	}
	if q.Scope.ID != "" {
		if _, e = tx.ExecContext(ctx, `UPDATE networking_certificate_state SET route_address='',route_port=0,route_hostname='',route_url='',route_next_attempt=0,route_error_code='' WHERE scope_id=?`, q.Scope.ID); e != nil {
			return false, e
		}
	}
	return true, gated.Commit()
}
