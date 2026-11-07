package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Draft effect components only. W must establish CURRENT opaque disclosure
// before receipt access, resolve history before fresh CAS, and save typed result,
// actual debt and required events in this same turn. None of these values is an
// authority capability, Applied result, commit permission or physical-exit proof.
const (
	MPStartPrepBegin  CommandID = 700
	MPStartPrepClaim  CommandID = 701
	MPStartPrepSeal   CommandID = 702
	MPStartPrepAccept CommandID = 703
	MPStartPrepRetire CommandID = 704
)

type mpPrepActor struct {
	server, authority, account, profile, device, family, grant, sessionHash string
	epoch, tokenGeneration, horizonMS                                       int64
}

func (a mpPrepActor) valid(now int64) bool {
	return mpPrepTexts(a.server, a.authority, a.account, a.profile, a.device, a.family, a.grant) && lowerHex(a.sessionHash, 64) && a.epoch >= 0 && a.tokenGeneration > 0 && a.horizonMS > now
}
func (a mpPrepActor) args() []any {
	return []any{a.server, a.authority, a.account, a.profile, a.device, a.family, a.grant, a.sessionHash, a.epoch, a.tokenGeneration, a.horizonMS}
}

// Complete selected inventory/configuration witness. The dispatcher must supply
// current effective policy/authority witnesses, never reconstruct them from a
// saved preparation. Native object/source/asset CAS is additionally checked here.
type mpPrepSource struct {
	library, source, asset, object, revision, root, incarnation, remote, configuration, policy, authority string
	libraryGeneration, configurationGeneration, profileRevision, size, modifiedNS                         int64
}

func (s mpPrepSource) valid() bool {
	return mpPrepTexts(s.library, s.source, s.asset, s.object, s.revision, s.root, s.incarnation, s.configuration, s.policy, s.authority) && mpPrepText(s.remote, true) && s.libraryGeneration > 0 && s.configurationGeneration > 0 && s.profileRevision >= 0 && s.size > 0 && s.modifiedNS >= 0
}
func (s mpPrepSource) args() []any {
	return []any{s.library, s.source, s.asset, s.object, s.revision, s.root, s.incarnation, s.remote, s.configuration, s.policy, s.authority, s.libraryGeneration, s.configurationGeneration, s.profileRevision, s.size, s.modifiedNS}
}

type mpPrepQueue struct {
	id, entry, ownerKind, ownerID string
	revision, custody             int64
}

func (q mpPrepQueue) valid() bool {
	if q.id == "" {
		return q == (mpPrepQueue{})
	}
	return mpPrepTexts(q.id, q.entry, q.ownerKind, q.ownerID) && q.revision > 0 && q.custody >= 0
}
func (q mpPrepQueue) args() []any {
	return []any{q.id, q.entry, q.ownerKind, q.ownerID, q.revision, q.custody}
}

func mpPrepText(s string, empty bool) bool {
	if (!empty && s == "") || len(s) > 4096 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
func mpPrepTexts(v ...string) bool {
	for _, s := range v {
		if !mpPrepText(s, false) {
			return false
		}
	}
	return true
}
func mpPrepRevision(v int64) bool { return v > 0 && v < math.MaxInt64 }

func mpPrepTurn(t *commandTurn, kind CommandID) error {
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != kind {
		return ErrInvalid
	}
	return nil
}
func mpPrepRow(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	if t == nil {
		return commandRow{}, ErrInvalid
	}
	s, ok := t.steps[id]
	if !ok {
		return commandRow{}, ErrInvalid
	}
	return t.queryRow(ctx, s.statementStep, args...)
}
func mpPrepOne(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	r, e := mpPrepRow(ctx, t, id, args...)
	if e != nil {
		return e
	}
	var one int64
	if e = r.Scan(&one); errors.Is(e, sql.ErrNoRows) {
		return ErrConflict
	}
	if e != nil {
		return e
	}
	if one != 1 {
		return ErrResolveRequired
	}
	return nil
}

// Native relationship supplement to W's current opaque disclosure, not a
// replacement for membership/profile/item restriction and horizon checks.
func checkMPPrepActor(ctx context.Context, t *commandTurn, a mpPrepActor) error {
	return mpPrepOne(ctx, t, "mp.prep.actor_current", a.sessionHash, a.family, a.server, a.authority, a.account, a.profile, a.device, a.epoch, a.tokenGeneration)
}

func checkMPPrepSource(ctx context.Context, t *commandTurn, s mpPrepSource) error {
	if !s.valid() {
		return ErrInvalid
	}
	return mpPrepOne(ctx, t, "mp.prep.source_current", s.object, s.source, s.asset, s.revision, s.incarnation, s.size, s.modifiedNS, s.library, s.root, s.libraryGeneration, s.remote)
}
func checkMPPrepPublicItem(ctx context.Context, t *commandTurn, item string, part int64, s mpPrepSource) error {
	raw, ok := entityid.Decode(item)
	if !ok {
		return ErrInvalid
	}
	return mpPrepOne(ctx, t, "mp.prep.public_item_current", raw, s.library, s.asset, part)
}
func checkMPPrepItem(ctx context.Context, t *commandTurn, item, part int64, s mpPrepSource) error {
	if item <= 0 || part < 0 {
		return ErrInvalid
	}
	return mpPrepOne(ctx, t, "mp.prep.item_current", item, s.library, s.asset, part)
}

func checkMPPrepQueue(ctx context.Context, t *commandTurn, q mpPrepQueue, a mpPrepActor) error {
	if !q.valid() {
		return ErrInvalid
	}
	if q.id == "" {
		return nil
	}
	return mpPrepOne(ctx, t, "mp.prep.queue_current", q.id, q.ownerKind, q.ownerID, q.revision, q.custody, q.entry, a.authority, a.account, a.profile)
}

// Closed SQL catalog helper; callers below supply compiled SQL and native
// parameter types, never runtime identifiers or generic transition strings.
func mpPrepDefinition(id, sqlText, types string, kind stmt.Kind) stmt.Definition {
	p := make([]stmt.Param, len(types))
	b := make([]any, len(types))
	for i, c := range types {
		p[i].Name = "p" + strconv.Itoa(i)
		switch c {
		case 's':
			p[i].Type = stmt.Text
			b[i] = "fixture"
		case 'i':
			p[i].Type = stmt.Integer
			b[i] = int64(1)
		case 'b':
			p[i].Type = stmt.Blob
			b[i] = []byte{1}
		case 'n':
			p[i].Type = stmt.Integer
			p[i].Nullable = true
			b[i] = nil
		case 't':
			p[i].Type = stmt.Text
			p[i].Nullable = true
			b[i] = nil
		}
	}
	return stmt.Definition{ID: stmt.ID(id), SQL: sqlText, Kind: kind, Params: p, Owner: "store/writer MP draft", CallSites: []string{"store/writer/mp_start_prep_core.go", "store/writer/mp_start_prep.go", "store/writer/mp_transcode.go"}, Cohorts: []stmt.Cohort{{Name: "binding_shape_only_unqualified", Bindings: b}}, Plan: stmt.Plan{Hot: true}}
}

func mpPrepCommonDefinitions() []stmt.Definition {
	return []stmt.Definition{
		mpPrepDefinition("mp.prep.public_item_current", `SELECT 1 FROM catalog_entities AS e CROSS JOIN catalog_libraries AS c ON c.id=e.library_id CROSS JOIN catalog_assets AS a ON a.library_id=e.library_id CROSS JOIN catalog_asset_links AS l ON l.entity_id=e.id AND l.asset_id=a.id AND l.library_id=e.library_id WHERE e.public_id=? AND c.library_id=? AND a.token=? AND l.part_index=? AND e.retired=0 AND c.retired=0 AND a.available=1 AND l.available=1 AND l.info_pending_change IS NULL`, "bssi", stmt.Read),
		mpPrepDefinition("mp.prep.item_current", `SELECT 1 FROM catalog_entities AS e CROSS JOIN catalog_libraries AS c ON c.id=e.library_id CROSS JOIN catalog_assets AS a ON a.library_id=e.library_id CROSS JOIN catalog_asset_links AS l ON l.entity_id=e.id AND l.asset_id=a.id AND l.library_id=e.library_id WHERE e.id=? AND c.library_id=? AND a.token=? AND l.part_index=? AND e.retired=0 AND c.retired=0 AND a.available=1 AND l.available=1 AND l.info_pending_change IS NULL`, "issi", stmt.Read),
		mpPrepDefinition("mp.prep.actor_current", `SELECT 1 FROM authorization_family_tokens AS t CROSS JOIN authorization_session_families AS f ON f.id=t.family_id WHERE t.token_hash=? AND f.id=? AND f.server_id=? AND f.authority=? AND f.account_id=? AND f.profile_id=? AND f.device_id=? AND f.epoch=? AND t.generation=? AND t.generation=f.current_generation AND t.retired=0 AND f.revoked=0 AND f.pin_gate=0`, "sssssssii", stmt.Read),
		mpPrepDefinition("mp.prep.source_current", `SELECT 1 FROM inventory_objects AS o CROSS JOIN library_sources AS s ON s.id=o.source_id CROSS JOIN catalog_assets AS a ON a.token=o.asset_id WHERE o.id=? AND o.source_id=? AND o.asset_id=? AND o.revision=? AND o.root_incarnation=? AND o.size=? AND o.modified_ns=? AND o.retired=0 AND o.state='available' AND s.library_id=? AND s.root_identity=? AND s.generation=? AND coalesce(s.remote_source_id,'')=? AND s.incarnation=o.root_incarnation AND s.enabled=1 AND s.identity_confirmed=1 AND s.health='ready' AND a.available=1 AND a.info_publication_pending=0 AND a.size=o.size AND a.modified_ns=o.modified_ns`, "sssssiissis", stmt.Read),
		mpPrepDefinition("mp.prep.queue_current", `SELECT 1 FROM queues_v1 WHERE id=? AND owner_kind=? AND owner_id=? AND revision=? AND custody_generation=? AND current_entry_id=? AND authority=? AND account_id=? AND profile_id=?`, "sssiissss", stmt.Read),
	}
}
