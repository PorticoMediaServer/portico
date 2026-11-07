package writer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"time"
)

// Only trusted measurement composition can create this one-use startup permit.
// Ordinary Accept/Resolve/ACK cannot request birth or initialize a missing owner.
// It establishes actual native rows on the same exclusive opening, before the
// driver escapes. This is not a product sign-in or a credential API.
type wcProgressExistingCatalog struct {
	libraryID, subjectID int64
	libraryKey           string
	publicID             [16]byte
}

type wcProgressBirth struct {
	existingCatalog *wcProgressExistingCatalog
	driver          *WCProgressDriver
	expiresAt       string
	used            bool
}

func wcProgressBirthDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	blob := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: "wc.fixture.existing_catalog", Owner: "store/writer WC trusted fixture birth", CallSites: []string{"store/writer/wc_progress_fixture.go"}, Kind: stmt.Read, SQL: `SELECT s.kind FROM catalog_subjects AS s INNER JOIN catalog_libraries AS l ON l.id=s.library_id WHERE s.id=? AND s.public_id=? AND l.id=? AND l.library_id=?`, Params: []stmt.Param{integer("subject"), blob("public_id"), integer("library"), text("library_key")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"s", "l"}}},
		{ID: "wc.fixture.account", Owner: "store/writer WC trusted fixture birth", CallSites: []string{"store/writer/wc_progress_fixture.go"}, Kind: stmt.Write, SQL: `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES(?,?,?,?)`, Params: []stmt.Param{text("account"), text("username"), blob("unused_password"), text("profile")}, Plan: stmt.Plan{Hot: true}},
		{ID: "wc.fixture.member", Owner: "store/writer WC trusted fixture birth", CallSites: []string{"store/writer/wc_progress_fixture.go"}, Kind: stmt.Write, SQL: `INSERT INTO direct_memberships(account_id,incarnation,role,allowed_libraries) VALUES(?,?,?,?)`, Params: []stmt.Param{text("account"), text("birth"), text("role"), text("libraries")}, Plan: stmt.Plan{Hot: true}},
		{ID: "wc.fixture.profile", Owner: "store/writer WC trusted fixture birth", CallSites: []string{"store/writer/wc_progress_fixture.go"}, Kind: stmt.Write, SQL: `INSERT INTO direct_profiles(id,account_id,lifetime,name,is_primary) VALUES(?,?,?,?,?)`, Params: []stmt.Param{text("profile"), text("account"), text("birth"), text("name"), integer("primary")}, Plan: stmt.Plan{Hot: true}},
		{ID: "wc.fixture.family", Owner: "store/writer WC trusted fixture birth", CallSites: []string{"store/writer/wc_progress_fixture.go"}, Kind: stmt.Write, SQL: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,command_member_incarnation,command_profile_lifetime,command_incarnation) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{text("family"), text("server"), text("account"), text("profile"), text("authority"), text("role"), integer("epoch"), text("horizon"), integer("token_generation"), text("member_birth"), text("profile_birth"), blob("command_incarnation")}, Plan: stmt.Plan{Hot: true}},
		{ID: "wc.fixture.token", Owner: "store/writer WC trusted fixture birth", CallSites: []string{"store/writer/wc_progress_fixture.go"}, Kind: stmt.Write, SQL: `INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES(?,?,?,?)`, Params: []stmt.Param{text("hash"), text("family"), integer("generation"), text("expiry")}, Plan: stmt.Plan{Hot: true}},
		{ID: "wc.fixture.library", Owner: "store/writer WC trusted fixture birth", CallSites: []string{"store/writer/wc_progress_fixture.go"}, Kind: stmt.Write, SQL: `INSERT INTO catalog_libraries(id,library_id,root) VALUES(?,?,?)`, Params: []stmt.Param{integer("id"), text("public_key"), text("root")}, Plan: stmt.Plan{Hot: true}},
		{ID: "wc.fixture.kind", Owner: "store/writer WC trusted fixture birth", CallSites: []string{"store/writer/wc_progress_fixture.go"}, Kind: stmt.Write, SQL: `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(?,?,?,?,?,?,?)`, Params: []stmt.Param{integer("id"), text("name"), integer("playable"), integer("container"), integer("searchable"), integer("browsable"), integer("listening")}, Plan: stmt.Plan{Hot: true}},
		{ID: "wc.fixture.subject", Owner: "store/writer WC trusted fixture birth", CallSites: []string{"store/writer/wc_progress_fixture.go"}, Kind: stmt.Write, SQL: `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,?,?)`, Params: []stmt.Param{integer("id"), blob("public_id"), integer("library"), integer("kind")}, Plan: stmt.Plan{Hot: true}},
		{ID: "wc.fixture.activity", Owner: "store/writer WC trusted fixture birth", CallSites: []string{"store/writer/wc_progress_fixture.go"}, Kind: stmt.Write, SQL: `INSERT INTO personal_activity_revisions(profile_id,revision,activity_generation,policy_authority,policy_account_id,policy_profile_id,policy_member_incarnation,policy_profile_lifetime,history_policy_generation) VALUES(?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{text("profile_key"), integer("revision"), integer("activity"), text("authority"), text("account"), text("profile"), text("member_birth"), text("profile_birth"), integer("policy")}, Plan: stmt.Plan{Hot: true}},
		{ID: "wc.fixture.personal", Owner: "store/writer WC trusted fixture birth", CallSites: []string{"store/writer/wc_progress_fixture.go"}, Kind: stmt.Write, SQL: `INSERT INTO personal_items(profile_id,item_id,item_public_id,library_id,activity_generation) VALUES(?,?,?,?,?)`, Params: []stmt.Param{text("profile_key"), integer("subject"), blob("public_id"), integer("library"), integer("activity")}, Plan: stmt.Plan{Hot: true}},
		{ID: "wc.fixture.snapshot", Owner: "store/writer WC trusted fixture birth", CallSites: []string{"store/writer/wc_progress_fixture.go"}, Kind: stmt.Write, SQL: `INSERT INTO personal_snapshots(profile_id,item_id,revision,state,created_at) VALUES(?,?,?,?,?)`, Params: []stmt.Param{text("profile_key"), integer("subject"), integer("revision"), text("state"), text("created")}, Plan: stmt.Plan{Hot: true}},
		{ID: "wc.fixture.viewer", Owner: "store/writer WC trusted fixture birth", CallSites: []string{"store/writer/wc_progress_fixture.go"}, Kind: stmt.Write, SQL: `INSERT INTO viewer_revisions(profile_id,library_id,revision,observation_revision) VALUES(?,?,?,?)`, Params: []stmt.Param{text("profile_key"), text("library"), integer("revision"), integer("observation")}, Plan: stmt.Plan{Hot: true}},
		{ID: "wc.fixture.claim", Owner: "store/writer WC trusted fixture birth", CallSites: []string{"store/writer/wc_progress_fixture.go"}, Kind: stmt.Write, SQL: `INSERT INTO playback_personal_claims(playback_id,profile_key,item_id,accepted_ordinal,command_authority,command_account_id,command_profile_id,command_member_incarnation,command_profile_lifetime,command_library_id,command_incarnation,source_occurrence_generation) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{text("playback"), text("profile_key"), integer("subject"), integer("ordinal"), text("authority"), text("account"), text("profile"), text("member_birth"), text("profile_birth"), integer("library"), blob("incarnation"), integer("occurrence")}, Plan: stmt.Plan{Hot: true}},
	}
	for i := range defs {
		args := []any{}
		for _, p := range defs[i].Params {
			switch p.Type {
			case stmt.Text:
				args = append(args, "fixture")
			case stmt.Blob:
				args = append(args, []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
			default:
				args = append(args, int64(1))
			}
		}
		defs[i].Cohorts = []stmt.Cohort{{Name: "trusted_birth_values", Bindings: args}}
	}
	return defs
}

func initializeWCProgressFixture(ctx context.Context, d *WCProgressDriver, existingCatalog ...wcProgressExistingCatalog) (err error) {
	defer func() { _, err = finishCommand(ctx, ctx, struct{}{}, err) }()
	if len(existingCatalog) > 1 {
		return ErrInvalid
	}
	if err = d.current(); err != nil {
		return err
	}
	// This private helper is called ONLY by the separately reviewed test fixture.
	// Permit and actual birth witnesses are constructed before exposing d.
	p := &wcProgressBirth{driver: d, expiresAt: d.scope.horizon}
	if len(existingCatalog) == 1 {
		captured := existingCatalog[0]
		p.existingCatalog = &captured
	}
	q := ProgressReceiptRequest{PlaybackID: d.scope.playbackID, Kind: "capture_report", Receipt: ReceiptReference{Identity: CommandIdentity{Incarnation: d.scope.incarnation, Sequence: 1}}}
	_, err = d.writer.runWCProgress(ctx, &wcProgressInvocation{disclosure: d.Disclosure(), request: q, birth: p}, false)
	if err == nil {
		// The fixture owns one continuously playing source beginning only AFTER
		// native birth. No interval is inferred from position or a report counter.
		now := d.writer.effective.executor.now()
		d.observed = now
		d.intervalFrom = now.UnixMilli()
	}
	return err
}
func applyWCProgressFixtureBirth(ctx context.Context, t *commandTurn, v *wcProgressInvocation) error {
	p := v.birth
	if p == nil || p.used || p.driver != v.disclosure.driver || v.input != nil || v.snapshot || v.retained != nil {
		return ErrInvalid
	}
	p.used = true
	s := p.driver.scope
	now, err := t.commandTimeMS(ctx, ProgressAccept, 0)
	if err != nil {
		return err
	}
	at := time.UnixMilli(now).UTC().Format(time.RFC3339Nano)
	var familyInc, password [16]byte
	if _, err = rand.Read(familyInc[:]); err != nil {
		return err
	}
	if _, err = rand.Read(password[:]); err != nil {
		return err
	}

	if p.existingCatalog != nil {
		c := p.existingCatalog
		if c.libraryID != s.libraryID || c.subjectID != s.subjectID || c.libraryKey != s.libraryKey || c.publicID != s.subjectPublicID {
			return ErrConflict
		}
		kind, e := wc008DraftInteger(ctx, t, "wc.fixture.existing_catalog", c.subjectID, c.publicID[:], c.libraryID, c.libraryKey)
		if e != nil || kind != 1 || p.driver.original[9].Integer != kind {
			return errors.Join(ErrConflict, e)
		}
	}
	rows := []struct {
		id   stmt.ID
		args []any
	}{
		{"wc.fixture.account", []any{s.accountID, s.accountID, password[:], s.profileID}},
		{"wc.fixture.member", []any{s.accountID, s.memberBirth, s.membershipRole, s.memberLibraries}},
		{"wc.fixture.profile", []any{s.profileID, s.accountID, s.profileBirth, "Fixture", int64(1)}},
		{"wc.fixture.family", []any{s.familyID, s.serverID, s.accountID, s.profileID, "local", s.role, s.epoch, p.expiresAt, s.tokenGeneration, s.memberBirth, s.profileBirth, familyInc[:]}},
		{"wc.fixture.token", []any{s.tokenHash, s.familyID, s.tokenGeneration, p.expiresAt}},
		{"wc.fixture.library", []any{s.libraryID, s.libraryKey, "/wc-fixture"}},
		{"wc.fixture.kind", []any{int64(1), "movie", int64(1), int64(0), int64(1), int64(1), int64(0)}},
		{"wc.fixture.subject", []any{s.subjectID, s.subjectPublicID[:], s.libraryID, int64(1)}},
		{"wc.fixture.activity", []any{s.profileKey, int64(1), int64(1), "local", s.accountID, s.profileID, s.memberBirth, s.profileBirth, int64(1)}},
		{"wc.fixture.personal", []any{s.profileKey, s.subjectID, s.subjectPublicID[:], s.libraryID, int64(1)}},
		{"wc.fixture.snapshot", []any{s.profileKey, s.subjectID, int64(1), `{"revision":1,"watched":false,"favorite":false,"watchlisted":false,"progressSeconds":0,"lastPlayedAt":"","status":"current","conflicts":[],"rating":null}`, at}},
		{"wc.fixture.viewer", []any{s.profileKey, s.libraryKey, int64(1), int64(1)}},
		{"wc.fixture.claim", []any{s.playbackID, s.profileKey, s.subjectID, int64(1), "local", s.accountID, s.profileID, s.memberBirth, s.profileBirth, s.libraryID, s.incarnation[:], int64(1)}},
	}
	for _, row := range rows {
		if p.existingCatalog != nil {
			switch row.id {
			case "wc.fixture.library", "wc.fixture.kind", "wc.fixture.subject":
				continue
			}
		}
		if err = wc008DraftChanged(ctx, t, row.id, row.args...); err != nil {
			return err
		}
	}
	return nil
}
func wcRandomBirth() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
