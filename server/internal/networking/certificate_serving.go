package networking

import (
	"context"
	"crypto/tls"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"time"
)

// Serving never enters the writer scheduler. The short read snapshot fences
// authority, while parsed chains and private keys are reused until material changes.
func (m *CertificateManager) servingSnapshot(ctx context.Context) (certificateState, error) {
	tx, release, e := dbwork.BeginRead(ctx, m.store.db)
	if e != nil {
		return certificateState{}, e
	}
	defer release()
	return m.servingSnapshotTx(ctx, tx)
}
func (m *CertificateManager) servingSnapshotTx(ctx context.Context, tx *sql.Tx) (certificateState, error) {
	var q certificateState
	lease, e := claimAuthority(ctx)
	if e != nil {
		return q, e
	}
	id, e := readIdentity(ctx, tx)
	if e != nil {
		return q, e
	}
	var enabled bool
	var server, incarnation string
	var generation int64
	e = tx.QueryRowContext(ctx, `SELECT enabled,server_id,local_generation,incarnation FROM networking_certificate_settings WHERE singleton=1`).Scan(&enabled, &server, &generation, &incarnation)
	if e != nil {
		return q, e
	}
	if !enabled || server != id.server || generation != id.generation || incarnation != lease.Incarnation() || !id.active.Valid || !id.installed.Valid || id.active.String != id.installed.String {
		return q, ErrStale
	}
	v, e := loadIntentTx(ctx, tx, id.installed.String)
	if e != nil {
		return q, e
	}
	if v.Stage != Installed || !v.InstallationAcknowledged {
		return q, ErrStale
	}
	if _, e = m.store.positive(ctx, tx, v.Intent); e != nil {
		return q, e
	}
	scope, e := scopeForCertificate(ctx, v.Intent, m.environment)
	if e != nil {
		return q, e
	}
	q, e = readCertificateStateTx(ctx, tx, scope)
	if e != nil {
		return q, e
	}
	if q.Environment != m.environment {
		return q, ErrStale
	}
	return q, nil
}

type servingCertificate struct {
	scope, request string
	cert           *tls.Certificate
}

func (m *CertificateManager) cachedMaterial(ctx context.Context, q certificateState, id string) (*tls.Certificate, error) {
	m.servingMu.Lock()
	defer m.servingMu.Unlock()
	cached := m.serving
	if cached != nil && cached.scope == q.Scope.ID && cached.request == id && time.Now().Before(cached.cert.Leaf.NotAfter) {
		return cached.cert, nil
	}
	tx, release, e := dbwork.BeginRead(ctx, m.store.db)
	if e != nil {
		return nil, e
	}
	defer release()
	mat, e := readCertificateMaterialTx(ctx, tx, q.Scope.ID, id)
	if e != nil {
		return nil, e
	}
	cert, e := m.validateMaterial(q.Scope, q.Namespace, mat, mat.Chain, time.Now())
	if e != nil {
		return nil, e
	}
	key, e := m.privateKey(q.Scope, mat)
	if e != nil {
		return nil, e
	}
	cert.PrivateKey = authorityTLSSigner{manager: m, scope: q.Scope, request: id, key: key}
	m.serving = &servingCertificate{q.Scope.ID, id, cert}
	return cert, nil
}
