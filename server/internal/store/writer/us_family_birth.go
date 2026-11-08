package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Frozen target comparisons, never issuer authority. The parent owns independent
// creation permission, fresh original classification, quotas, token/protected
// credential publication, immutable result, events and physical FULL terminal.
// Ordinary births require an existing registered device/installation and an
// origin lineage. Credential births bind the prepared credential, require its
// native source marker and create a fresh API device (no installation grant).
// Both require all four current fence rows first. A protected profile is born
// PIN-gated; US-11 must decide the credential PIN policy before activation.
// This helper cannot confirm a PIN or mint issuer/creation authority.
type usFamilyBirthInput struct {
	family, server, account, profile, member, lifetime                string
	device, installationID, installationIncarnation, lineage, horizon string
	epoch, authorityGeneration                                        int64
	incarnation                                                       [16]byte
}
type usFamilyBirthPrepared struct {
	input            usFamilyBirthInput
	installationHash string
	horizon          time.Time
	credential       bool
}
type usFamilyBirthEffect struct{ controlBytes, pinGate int64 }

func (usFamilyBirthInput) Format(s fmt.State, _ rune) { formatUSPreparation(s, "usFamilyBirthInput") }
func (usFamilyBirthPrepared) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usFamilyBirthPrepared")
}

func prepareUSOrdinaryFamilyBirth(in usFamilyBirthInput) (*usFamilyBirthPrepared, error) {
	return prepareUSFamilyBirth(in, false)
}
func prepareUSCredentialFamilyBirth(in usFamilyBirthInput, c *usCredentialIssuePrepared) (*usFamilyBirthPrepared, error) {
	if !usFamilyBirthCredentialMatches(in, c) {
		return nil, ErrInvalid
	}
	return prepareUSFamilyBirth(in, true)
}

// Preparation and execution both bind the exact merged credential preparation.
// The native source marker below additionally binds its immutable source fields.
func usFamilyBirthCredentialMatches(in usFamilyBirthInput, c *usCredentialIssuePrepared) bool {
	return c != nil && validUSCredentialTarget(c.target) && !c.target.disabled && !c.target.deleted && !c.target.mustChange && usIdentifier(c.id) && (c.kind == "key" || c.kind == "token" || c.kind == "app_password") && usCredentialDigest(c.secretHash) && c.family == in.family && c.device == in.device && c.target.account == in.account && c.target.memberIncarnation == in.member && c.target.profile == in.profile && c.target.profileLifetime == in.lifetime && c.target.epoch == in.epoch
}
func prepareUSFamilyBirth(in usFamilyBirthInput, credential bool) (*usFamilyBirthPrepared, error) {
	for _, v := range []string{in.family, in.server, in.account, in.profile, in.device, in.lineage} {
		if !usIdentifier(v) {
			return nil, ErrInvalid
		}
	}
	if !usHex(in.member, 32) || !usHex(in.lifetime, 32) || in.epoch <= 0 || in.authorityGeneration <= 0 || in.incarnation == ([16]byte{}) || len(in.horizon) != 20 {
		return nil, ErrInvalid
	}
	if credential {
		if in.installationID != "" || in.installationIncarnation != "" {
			return nil, ErrInvalid
		}
	} else if !usIdentifier(in.installationID) || len(in.installationID) < 32 || !usHex(in.installationIncarnation, 32) {
		return nil, ErrInvalid
	}
	horizon, err := time.Parse(time.RFC3339, in.horizon)
	if err != nil || horizon.UTC().Format(time.RFC3339) != in.horizon {
		return nil, ErrInvalid
	}
	// Strings are bounded above before cloning/decoding outside writer custody.
	fields := []*string{&in.family, &in.server, &in.account, &in.profile, &in.member, &in.lifetime, &in.device, &in.installationID, &in.installationIncarnation, &in.lineage, &in.horizon}
	for _, p := range fields {
		*p = strings.Clone(*p)
	}
	h := sha256.Sum256([]byte(in.installationID))
	return &usFamilyBirthPrepared{input: in, installationHash: hex.EncodeToString(h[:]), horizon: horizon, credential: credential}, nil
}

func applyUSOrdinaryFamilyBirth(ctx context.Context, turn *commandTurn, p *usFamilyBirthPrepared) (usFamilyBirthEffect, error) {
	return applyUSFamilyBirth(ctx, turn, p, false, nil)
}
func applyUSCredentialFamilyBirth(ctx context.Context, turn *commandTurn, p *usFamilyBirthPrepared, c *usCredentialIssuePrepared) (usFamilyBirthEffect, error) {
	return applyUSFamilyBirth(ctx, turn, p, true, c)
}

func applyUSFamilyBirth(ctx context.Context, turn *commandTurn, p *usFamilyBirthPrepared, credential bool, c *usCredentialIssuePrepared) (out usFamilyBirthEffect, err error) {
	defer func() {
		out, err = finishCommand(ctx, commandLifetime(turn), out, err)
		// A parent accidentally ignoring an error still cannot COMMIT a partial
		// device/family/witness (or an earlier credential-marker) prefix.
		if err != nil && turn != nil && turn.transaction != nil {
			tx := turn.transaction
			if tx.begun && !tx.done && tx.cancel != nil {
				tx.cancel()
			}
		}
	}()
	if turn == nil || turn.transaction == nil || p == nil || p.credential != credential || (credential && !usFamilyBirthCredentialMatches(p.input, c)) || (!credential && c != nil) {
		return out, ErrInvalid
	}
	tx := turn.transaction
	// 510 remains unregistered until the ordinary sign-in receipt/authority recipe
	// is adopted. These private effects do not admit it or widen US-01's stamp.
	purpose := CommandID(510)
	insert := usFamilyBirthOrdinary
	if credential {
		purpose = usAPICredentialIssue
		insert = usFamilyBirthCredential
	}
	if tx.descriptor.ID != purpose || tx.descriptor.Durability != Full || !tx.begun || tx.done || !tx.policyVerified || tx.settings.Synchronous != 2 || tx.writer == nil || tx.writer.now == nil {
		return out, ErrInvalid
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	now := tx.writer.now()
	deadline, ok := tx.ctx.Deadline()
	if !ok || !p.horizon.After(now) || !p.horizon.After(deadline) {
		return out, ErrDisclosureDenied
	}
	if err = tx.observeTime(now); err != nil {
		return out, err
	}
	i := p.input
	// Only bounded role + integer PIN state leave native current-source points.
	row, e := wc008DraftQuery(ctx, turn, usFamilyBirthPrincipal, i.account, i.epoch, i.member, i.profile, i.lifetime)
	if e != nil {
		return out, e
	}
	var role string
	var gate int64
	if e = row.Scan(&role, &gate); e != nil {
		return out, usFamilyBirthReadError(e)
	}
	if (role != "owner" && role != "admin" && role != "member") || (gate != 0 && gate != 1) {
		return out, ErrDisclosureDenied
	}

	points := []struct {
		id      stmt.ID
		args    []any
		present bool
	}{
		{usFamilyBirthServer, []any{i.server}, true},
		{usFamilyBirthState, []any{i.authorityGeneration}, true},
		{usFamilyBirthNotWaiting, []any{i.account}, true},
	}
	if credential {
		points = append(points, struct {
			id      stmt.ID
			args    []any
			present bool
		}{usFamilyBirthLineage, []any{i.lineage, i.account}, true},
			struct {
				id      stmt.ID
				args    []any
				present bool
			}{usFamilyBirthCredentialMarker, []any{c.id, c.kind, c.target.account, c.target.profile, c.family, c.device, c.secretHash, c.name, c.scopes, c.hint, c.expiresMS}, true})
	} else {
		points = append(points, struct {
			id      stmt.ID
			args    []any
			present bool
		}{usFamilyBirthDevice, []any{i.device, i.account, i.installationID}, true},
			struct {
				id      stmt.ID
				args    []any
				present bool
			}{usFamilyBirthInstallation, []any{p.installationHash, i.installationIncarnation}, true},
			struct {
				id      stmt.ID
				args    []any
				present bool
			}{usFamilyBirthOriginLineage, []any{i.lineage, i.account, i.device}, true},
			struct {
				id      stmt.ID
				args    []any
				present bool
			}{usFamilyBirthCredentialAbsent, []any{i.family}, false})
	}
	for _, point := range points {
		row, e = wc008DraftQuery(ctx, turn, point.id, point.args...)
		if e != nil {
			return out, e
		}
		var yes int64
		e = row.Scan(&yes)
		if !point.present && errors.Is(e, sql.ErrNoRows) {
			continue
		}
		if e != nil {
			return out, usFamilyBirthReadError(e)
		}
		if !point.present || yes != 1 {
			return out, ErrDisclosureDenied
		}
	}
	scopes := [4]string{"device", "profile_pin", "profile_remember", "profile_lineage"}
	var ids, generations [4]int64
	for n, scope := range scopes {
		profile, lifetime, device, lineage := i.profile, i.lifetime, "", ""
		if scope == "device" {
			profile, lifetime, device = "", "", i.device
		}
		if scope == "profile_lineage" {
			lineage = i.lineage
		}
		row, e = wc008DraftQuery(ctx, turn, usFamilyBirthFence, scope, i.account, i.member, profile, lifetime, device, lineage)
		if e != nil {
			return out, e
		}
		if e = row.Scan(&ids[n], &generations[n]); e != nil {
			return out, usFamilyBirthReadError(e)
		}
		if ids[n] <= 0 || generations[n] <= 0 || generations[n] > usMaxRevision {
			return out, ErrDisclosureDenied
		}
	}
	if credential {
		stamp := now.UTC().Truncate(time.Second).Format(time.RFC3339)
		row, e = wc008DraftQuery(ctx, turn, usFamilyBirthAPIDevice, c.device, c.target.account, "api-credential-"+c.id, c.name, "api-"+c.kind, stamp, stamp)
		if e != nil {
			return out, e
		}
		var retired int64
		if e = row.Scan(&retired); e != nil {
			return out, e
		}
		if retired != 0 {
			return out, ErrInvalid
		}
	}
	row, e = wc008DraftQuery(ctx, turn, insert, i.family, i.server, i.account, i.profile, role, i.epoch, i.horizon, gate, i.device, i.lineage, i.authorityGeneration, i.member, i.lifetime, i.incarnation[:])
	if e != nil {
		return out, e
	}
	if e = row.Scan(&out.controlBytes, &out.pinGate); e != nil {
		return out, e
	}
	if out.controlBytes <= 0 || out.pinGate != gate {
		return out, ErrInvalid
	}
	for n, scope := range scopes {
		row, e = wc008DraftQuery(ctx, turn, usFamilyBirthWitness, i.family, scope, ids[n], generations[n])
		if e != nil {
			return out, e
		}
		var id, generation int64
		if e = row.Scan(&id, &generation); e != nil {
			return out, e
		}
		if id != ids[n] || generation != generations[n] {
			return out, ErrInvalid
		}
	}
	// Nothing here is Applied or a public permission. Any failure requires the
	// parent to roll back the entire source/receipt/event prefix.
	return out, turn.checkLifetimes(ctx)
}
func usFamilyBirthReadError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDisclosureDenied
	}
	return err
}
