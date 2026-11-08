package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
	"time"
)

// Frozen target comparisons, never issuer authority. The parent owns independent
// creation permission, fresh original classification, quotas, token/protected
// credential publication, immutable result, events and physical FULL terminal.
// This first component requires an already registered device/installation,
// lineage and all four current retained fences; it never invents a grant or
// revives a retired device. A protected profile is born PIN-gated; this helper
// cannot confirm a PIN. No public constructor or accepting facade exists.
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
func prepareUSCredentialFamilyBirth(in usFamilyBirthInput) (*usFamilyBirthPrepared, error) {
	return prepareUSFamilyBirth(in, true)
}
func prepareUSFamilyBirth(in usFamilyBirthInput, credential bool) (*usFamilyBirthPrepared, error) {
	for _, v := range []string{in.family, in.server, in.account, in.profile, in.device, in.installationID, in.lineage} {
		if !usIdentifier(v) {
			return nil, ErrInvalid
		}
	}
	if !usHex(in.member, 32) || !usHex(in.lifetime, 32) || !usHex(in.installationIncarnation, 32) || in.epoch <= 0 || in.authorityGeneration <= 0 || in.incarnation == ([16]byte{}) || len(in.horizon) > 64 {
		return nil, ErrInvalid
	}
	horizon, err := time.Parse(time.RFC3339Nano, in.horizon)
	if err != nil {
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
	return applyUSFamilyBirth(ctx, turn, p, false)
}
func applyUSCredentialFamilyBirth(ctx context.Context, turn *commandTurn, p *usFamilyBirthPrepared) (usFamilyBirthEffect, error) {
	return applyUSFamilyBirth(ctx, turn, p, true)
}

func applyUSFamilyBirth(ctx context.Context, turn *commandTurn, p *usFamilyBirthPrepared, credential bool) (out usFamilyBirthEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || p == nil || p.credential != credential {
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
	for _, point := range []struct {
		id   stmt.ID
		args []any
	}{
		{usFamilyBirthServer, []any{i.server}},
		{usFamilyBirthState, []any{i.authorityGeneration}},
		{usFamilyBirthDevice, []any{i.device, i.account, i.installationID}},
		{usFamilyBirthInstallation, []any{p.installationHash, i.installationIncarnation}},
		{usFamilyBirthLineage, []any{i.lineage, i.account}},
		{usFamilyBirthNotWaiting, []any{i.account}},
	} {
		row, e = wc008DraftQuery(ctx, turn, point.id, point.args...)
		if e != nil {
			return out, e
		}
		var yes int64
		if e = row.Scan(&yes); e != nil {
			return out, usFamilyBirthReadError(e)
		}
		if yes != 1 {
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
