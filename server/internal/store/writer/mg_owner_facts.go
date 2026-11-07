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
	"unicode/utf8"
)

// Native expected lifetime facts only, never a principal/capability factory.
// Parent owns opaque current authority, rating/source restrictions, original
// public user-mutation receipt/lifetime (cross-1a HELD), events and COMMIT.
type mgRecordingOwner struct {
	account, profile, memberIncarnation, profileLifetime string
	accountEpoch, memberRevision, profileRevision        int64
}

func (o mgRecordingOwner) key() string {
	h := sha256.New()
	for _, v := range []string{"local", o.account, o.profile} {
		_, _ = fmt.Fprintf(h, "%d:%s", len(v), v)
	}
	return hex.EncodeToString(h.Sum(nil))
}
func (o mgRecordingOwner) valid() bool {
	return mgRecordingText(o.account, true, 256) && mgRecordingText(o.profile, true, 256) && mgRecordingHex(o.memberIncarnation, 32) && mgRecordingHex(o.profileLifetime, 32) && o.accountEpoch > 0 && o.memberRevision > 0 && o.profileRevision > 0
}
func mgRecordingText(s string, required bool, max int) bool {
	return (!required || s != "") && len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func mgRecordingHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func mgRecordingCheckOwner(ctx context.Context, t *commandTurn, o mgRecordingOwner) error {
	if !o.valid() {
		return ErrInvalid
	}
	if t == nil || t.transaction == nil {
		return ErrInvalid
	}
	step, ok := t.steps[mgRecordingIntentOwner]
	if !ok {
		return ErrInvalid
	}
	row, err := t.queryRow(ctx, step.statementStep, o.profile, o.account)
	if err != nil {
		return err
	}
	var epoch, memberRevision, disabled, profileRevision, deleted int64
	var incarnation, lifetime string
	err = row.Scan(&epoch, &incarnation, &memberRevision, &disabled, &lifetime, &profileRevision, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDisclosureDenied
	}
	if err != nil {
		return err
	}
	if epoch != o.accountEpoch || incarnation != o.memberIncarnation || memberRevision != o.memberRevision || disabled != 0 || lifetime != o.profileLifetime || profileRevision != o.profileRevision || deleted != 0 {
		return ErrDisclosureDenied
	}
	return nil
}

const mgRecordingIntentOwner stmt.ID = "mg.recording.intent.owner"

func mgOwnerDefinition() stmt.Definition {
	d := stmt.Definition{ID: mgRecordingIntentOwner, Owner: "writer.mg.owner", Kind: stmt.Read,
		SQL:       `SELECT a.epoch,m.incarnation,m.revision,m.disabled,p.lifetime,p.revision,p.deleted FROM accounts AS a INDEXED BY sqlite_autoindex_accounts_1 CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_1 ON p.id=? AND p.account_id=a.id WHERE a.id=?`,
		Params:    []stmt.Param{{Name: "profile", Type: stmt.Text}, {Name: "account", Type: stmt.Text}},
		CallSites: []string{"writer/mg_owner_facts.go"},
		Cohorts:   []stmt.Cohort{{Name: "missing_native_key", Bindings: []any{"missing", "missing"}}},
		Plan:      stmt.Plan{Hot: true}}
	for _, v := range []struct{ table, column string }{{"accounts", "id"}, {"direct_memberships", "account_id"}, {"direct_profiles", "id"}} {
		name := "sqlite_autoindex_" + v.table + "_1"
		d.Plan.Indexes = append(d.Plan.Indexes, name)
		d.Plan.AutoIndexes = append(d.Plan.AutoIndexes, stmt.AutoIndexBinding{Table: v.table, Name: name, Origin: "pk", Columns: []stmt.IndexColumn{{Name: v.column, Collation: "BINARY"}}})
	}
	return d
}
func mgOwnerMissing(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	return err
}
