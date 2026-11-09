package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func TestMGRuleCommandBoundsAndPreparedIdentity(t *testing.T) {
	w, _, v := mgRuleCommandFixture(t)
	fake := mgPersonalFake(t, v.personal)
	id := strings.Repeat("e", 64)

	// The retained 384 KiB serialized-config ceiling is not narrowed to a generic
	// 256 KiB input hold. Exactly the ceiling is accepted and stored byte-for-byte.
	padded := mgRulePaddedPrepared(v, mgRuleMaxConfigBytes)
	config := mgRuleValuesConfig(v, nil)
	request := mgRuleSaveRequestFor(v, id, 0, config, padded)
	turn := mgRuleCommandTurn(t, w, MGSaveRule)
	out, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(1, 1), request), request)
	if err != nil || out.decision != receiptFresh || out.rule.configBytes != int64(mgRuleMaxConfigBytes) || out.rule.configDigest != mgRuleConfigDigest(padded) {
		t.Fatal(out, err)
	}
	mgRuleCommit(t, turn)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=1 WHERE root_id=?`, v.root); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := w.db.QueryRowContext(t.Context(), `SELECT config_json FROM dvr_rules WHERE id=?`, id).Scan(&stored); err != nil || len(stored) != mgRuleMaxConfigBytes || stored != padded {
		t.Fatal("stored 384 KiB config changed", len(stored), err)
	}

	// One byte beyond the retained ceiling is refused before any effect; the
	// prepared string is fresh-only, so the refusal belongs to the fresh path.
	tooLarge := mgRulePaddedPrepared(v, mgRuleMaxConfigBytes+1)
	tooLargeRequest := mgRuleSaveRequestFor(v, id, 0, config, tooLarge)
	turn = mgRuleCommandTurn(t, w, MGSaveRule)
	if got, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(2, 2), tooLargeRequest), tooLargeRequest); !errors.Is(err, ErrCap) || got != (mgRuleAcceptance{}) {
		t.Fatal("over-ceiling config admitted", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A real largest semantic config (4000 channel ids, 20 keywords each side)
	// is above the generic 256 KiB hold and still accepted as prepared output.
	w2, _, v2 := mgRuleCommandFixture(t)
	fake2 := mgPersonalFake(t, v2.personal)
	largest := mgRuleLargestConfig(v2)
	largestPrepared := mgRulePreparedWire(largest)
	if len(largestPrepared) <= 256<<10 || len(largestPrepared) > mgRuleMaxConfigBytes {
		t.Fatal("semantic largest fixture is not in the accepted range", len(largestPrepared))
	}
	large := mgRuleSaveRequestFor(v2, strings.Repeat("f", 64), 0, largest, largestPrepared)
	turn = mgRuleCommandTurn(t, w2, MGSaveRule)
	largeOut, err := acceptMGRuleSave(t.Context(), turn, fake2, v2.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(1, 1), large), large)
	if err != nil || largeOut.decision != receiptFresh || largeOut.rule.configBytes != int64(len(largestPrepared)) {
		t.Fatal("largest semantic config", len(largestPrepared), largeOut, err)
	}
	withoutConfig := largeOut.rule
	withoutConfig.configBytes = 0
	if largeOut.rule.configBytes-withoutConfig.configBytes != int64(len(largestPrepared)) {
		t.Fatal("configuration bytes not accounted exactly")
	}

	// Keyword padding changes the ORIGINAL request identity even though the
	// preparer output is identical: a retry cannot silently reuse a receipt for
	// a differently padded original request.
	paddedConfig := mgRuleValuesConfig(v, []string{" news "})
	sameConfig := mgRuleValuesConfig(v, []string{"news"})
	prepared := mgRulePreparedWire(paddedConfig)
	if mgRulePreparedWire(paddedConfig) != mgRulePreparedWire(sameConfig) {
		t.Fatal("preparer stand-in did not normalize padding")
	}
	a, err := newMGRuleSaveCommand(mgPersonalIdentity(1, 1), mgRuleSaveRequestFor(v, id, 0, paddedConfig, prepared))
	if err != nil {
		t.Fatal(err)
	}
	b, err := newMGRuleSaveCommand(mgPersonalIdentity(1, 1), mgRuleSaveRequestFor(v, id, 0, sameConfig, prepared))
	if err != nil {
		t.Fatal(err)
	}
	if a.digest == b.digest {
		t.Fatal("padding normalization changed the original request identity")
	}
	// The pathological whitespace input that exceeds the stored ceiling before
	// preparation is still hashed in full and then prepared down to "news".
	pathological := mgRuleValuesConfig(v, []string{" news" + strings.Repeat("\u2028", 70000)})
	if _, err := newMGRuleSaveCommand(mgPersonalIdentity(1, 1), mgRuleSaveRequestFor(v, id, 0, pathological, prepared)); err != nil {
		t.Fatal("pathological padding refused at the writer boundary", err)
	}

	// The stored configuration's identity must match the digested original, but
	// the adapter's prepared string is fresh-only: command construction does not
	// read it, so replay/retired lookups never depend on it. The FRESH effect
	// still refuses drift before any rule row exists.
	drifted := strings.Replace(prepared, v.source, strings.Repeat("c", 48), 1)
	if drifted == prepared {
		t.Fatal("fixture did not contain the source id")
	}
	if _, err := newMGRuleSaveCommand(mgPersonalIdentity(1, 1), mgRuleSaveRequestFor(v, id, 0, config, drifted)); err != nil {
		t.Fatal("prepared drift leaked into original request identity", err)
	}
	if _, err := newMGRuleSaveCommand(mgPersonalIdentity(1, 1), mgRuleSaveRequestFor(v, id, 0, config, `{"sourceId":`)); err != nil {
		t.Fatal("malformed prepared string leaked into original request identity", err)
	}

	// The public delete request's future selector is part of its identity.
	keep := mgRuleDeleteRequestFor(v, id, 1)
	cancel := keep
	cancel.input.future = "cancel"
	keepCommand, err := newMGRuleDeleteCommand(mgPersonalIdentity(2, 2), keep)
	if err != nil {
		t.Fatal(err)
	}
	cancelCommand, err := newMGRuleDeleteCommand(mgPersonalIdentity(2, 2), cancel)
	if err != nil {
		t.Fatal(err)
	}
	if keepCommand.digest == cancelCommand.digest {
		t.Fatal("future selector dropped from the original request identity")
	}
}

func mgRulePaddedPrepared(v mgRuleCommandValues, size int) string {
	head := `{"matchKind":"","title":"","channelId":"","name":"Rule","sourceId":"` + v.source +
		`","seriesId":"series-one","enabled":true,"episodes":"all","allowedChannels":[],"blockedChannels":[],"blockedKeywords":[],"keywords":["`
	tail := `"]}`
	return head + strings.Repeat("k", size-len(head)-len(tail)) + tail
}

func mgRuleLargestConfig(v mgRuleCommandValues) mgRuleConfig {
	c := mgRuleValuesConfig(v, nil)
	c.name = strings.Repeat("<", 160)
	c.series = strings.Repeat("&", 512)
	for i := 1; i <= 2000; i++ {
		c.allowed = append(c.allowed, fmt.Sprintf("%064x", i))
		c.blocked = append(c.blocked, fmt.Sprintf("%064x", i+2000))
	}
	for i := 0; i < 20; i++ {
		c.keywords = append(c.keywords, strings.Repeat("x", 128))
		c.blockedKeywords = append(c.blockedKeywords, strings.Repeat("y", 128))
	}
	return c
}

func TestMGRuleCommandResultCodec(t *testing.T) {
	facts := mgRuleFacts{
		id: strings.Repeat("e", 64), owner: strings.Repeat("1", 64), source: strings.Repeat("a", 48),
		series: "series-one", config: `{"sourceId":"` + strings.Repeat("a", 48) + `"}`, revision: 3, enabled: 1, updated: 100,
	}
	result := mgRuleResultOf(facts)
	id := mgPersonalIdentity(2, 5)
	var request RequestDigest
	request[0] = 7
	payload, digest, err := mgRulePayload(id, request, result)
	if err != nil || len(payload) == 0 || len(payload) > uoRootPayloadCap || digest == (ResultDigest{}) {
		t.Fatal(len(payload), digest, err)
	}
	decoded, err := decodeMGRule(id, request, payload, digest)
	if err != nil || decoded != result {
		t.Fatal(decoded, err)
	}
	// The saved delete result carries the bounded detach count; the codec keeps
	// it through a strict round trip and the digest binds it.
	detachedResult := mgRuleResult{
		id: result.id, owner: result.owner, source: result.source, series: result.series, revision: 4,
		updated: result.updated, configBytes: result.configBytes, configDigest: result.configDigest, deleted: true, detached: 7,
	}
	detachedPayload, detachedDigest, err := mgRulePayload(id, request, detachedResult)
	if err != nil {
		t.Fatal(err)
	}
	decodedDetached, err := decodeMGRule(id, request, detachedPayload, detachedDigest)
	if err != nil || decodedDetached != detachedResult {
		t.Fatal(decodedDetached, err)
	}
	// The result digest binds the original command identity and request.
	otherID := mgPersonalIdentity(2, 6)
	if _, err := decodeMGRule(otherID, request, payload, digest); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("foreign command identity decoded", err)
	}
	tampered := append([]byte{}, payload...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := decodeMGRule(id, request, tampered, digest); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("tampered payload decoded", err)
	}
	if _, err := decodeMGRule(id, request, payload[:len(payload)-1], digest); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("truncated payload decoded", err)
	}
	if _, err := decodeMGRule(id, request, payload, ResultDigest{}); err == nil {
		t.Fatal("zero result digest accepted")
	}
	// Every result guard is individually observable.
	for _, mutated := range []mgRuleResult{
		{},
		{id: result.id, owner: result.owner, source: result.source, revision: result.revision, updated: result.updated, configBytes: result.configBytes, configDigest: result.configDigest},
		{id: result.id, owner: result.owner, source: result.source, series: result.series, revision: result.revision, updated: result.updated, configBytes: result.configBytes, configDigest: [32]byte{}},
		{id: result.id, owner: result.owner, source: result.source, series: result.series, revision: result.revision, updated: result.updated, configBytes: 0, configDigest: result.configDigest},
		{id: result.id, owner: result.owner, source: result.source, series: result.series, revision: result.revision, updated: result.updated, configBytes: mgRuleMaxConfigBytes + 1, configDigest: result.configDigest},
		{id: result.id, owner: result.owner, source: result.source, series: result.series, revision: result.revision, updated: result.updated, enabled: true, deleted: true, configBytes: result.configBytes, configDigest: result.configDigest},
		{id: result.id, owner: result.owner, source: result.source, series: result.series, revision: result.revision, updated: result.updated, configBytes: result.configBytes, configDigest: result.configDigest, detached: -1},
		{id: result.id, owner: result.owner, source: result.source, series: result.series, revision: result.revision, updated: result.updated, configBytes: result.configBytes, configDigest: result.configDigest, detached: 1},
		{id: result.id, owner: result.owner, source: result.source, series: result.series, revision: result.revision, updated: result.updated, configBytes: result.configBytes, configDigest: result.configDigest, deleted: true, detached: mgRuleDetachMaxRecordings + 1},
	} {
		if err := preflightMGRuleResult(mutated); err == nil {
			t.Fatal("invalid result preflight accepted", mutated)
		}
	}
}

func TestMGRuleCommandUnknownCommitFreshReadback(t *testing.T) {
	type unknownCommitCase struct {
		name                    string
		purpose                 CommandID
		beforeRules, afterRules int64
		seed                    func(t *testing.T, w *executor, v mgRuleCommandValues)
		prepare                 func(t *testing.T, v mgRuleCommandValues) func(context.Context, *commandTurn, *usOwnerAuthorityProof) (mgRuleAcceptance, error)
		read                    func(t *testing.T, db *sql.DB, root, id string) (rules, work, receipts, sequence int64)
	}
	cases := []unknownCommitCase{
		{
			name: "save-rule", purpose: MGSaveRule, beforeRules: 0, afterRules: 1,
			prepare: func(t *testing.T, v mgRuleCommandValues) func(context.Context, *commandTurn, *usOwnerAuthorityProof) (mgRuleAcceptance, error) {
				config := mgRuleValuesConfig(v, nil)
				request := mgRuleSaveRequestFor(v, strings.Repeat("e", 64), 0, config, mgRulePreparedWire(config))
				reference := mgRuleSaveReference(t, mgPersonalIdentity(1, 1), request)
				fake := mgPersonalFake(t, v.personal)
				return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof) (mgRuleAcceptance, error) {
					return acceptMGRuleSave(ctx, turn, fake, witness, t.Context(), reference, request)
				}
			},
			read: func(t *testing.T, db *sql.DB, root, id string) (int64, int64, int64, int64) {
				var rules, work, receipts, sequence int64
				if err := db.QueryRowContext(context.Background(), `SELECT (SELECT count(*) FROM dvr_rules),(SELECT count(*) FROM dvr_rule_work),(SELECT count(*) FROM user_mutation_command_receipts),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?)`, root).Scan(&rules, &work, &receipts, &sequence); err != nil {
					t.Fatal(err)
				}
				return rules, work, receipts, sequence
			},
		},
		{
			name: "delete-rule", purpose: MGDeleteRule, beforeRules: 1, afterRules: 0,
			seed: func(t *testing.T, w *executor, v mgRuleCommandValues) {
				config := mgRuleValuesConfig(v, nil)
				if _, err := w.db.ExecContext(t.Context(), `INSERT INTO dvr_rules(id,owner_key,authority,account_id,profile_id,source_id,series_id,match_title,match_channel,revision,config_json,enabled,deleted,updated_ms) VALUES(?,?,'local',?,?,?,'series-one','','',1,?,1,0,100)`,
					strings.Repeat("e", 64), v.owner.key(), v.owner.account, v.owner.profile, v.source, mgRulePreparedWire(config)); err != nil {
					t.Fatal(err)
				}
			},
			prepare: func(t *testing.T, v mgRuleCommandValues) func(context.Context, *commandTurn, *usOwnerAuthorityProof) (mgRuleAcceptance, error) {
				request := mgRuleDeleteRequestFor(v, strings.Repeat("e", 64), 1)
				reference := mgRuleDeleteReference(t, mgPersonalIdentity(1, 1), request)
				fake := mgPersonalFake(t, v.personal)
				return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof) (mgRuleAcceptance, error) {
					return acceptMGRuleDelete(ctx, turn, fake, witness, t.Context(), reference, request)
				}
			},
			read: func(t *testing.T, db *sql.DB, root, id string) (int64, int64, int64, int64) {
				var rules, work, receipts, sequence int64
				var deleted int64
				if err := db.QueryRowContext(context.Background(), `SELECT (SELECT count(*) FROM dvr_rules),(SELECT count(*) FROM dvr_rule_work),(SELECT count(*) FROM user_mutation_command_receipts),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?),(SELECT deleted FROM dvr_rules WHERE id=?)`, root, id).Scan(&rules, &work, &receipts, &sequence, &deleted); err != nil {
					t.Fatal(err)
				}
				if deleted == 1 {
					rules = 0
				}
				return rules, work, receipts, sequence
			},
		},
	}
	for _, tc := range cases {
		for _, after := range []bool{false, true} {
			t.Run(tc.name+"/"+map[bool]string{false: "before_commit", true: "after_commit"}[after], func(t *testing.T) {
				w, path, v := mgRuleCommandFixture(t)
				id := strings.Repeat("e", 64)
				if tc.seed != nil {
					tc.seed(t, w, v)
				}
				run := tc.prepare(t, v)
				if err := w.db.Close(); err != nil {
					t.Fatal(err)
				}
				connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
				if err != nil {
					t.Fatal(err)
				}
				installFaultPool(w, &faultConnector{Connector: connector, replyError: true, afterCommit: after})

				turn := mgRuleCommandTurn(t, w, tc.purpose)
				if _, err := run(t.Context(), turn, v.proof); err != nil {
					t.Fatal("fresh command did not reach COMMIT", err)
				}
				_ = turn.closeRows()
				if _, err := turn.transaction.commit(); !errors.Is(err, ErrResolveRequired) || !errors.Is(err, errCommitReply) {
					t.Fatal("uncertain COMMIT reported success", err)
				}
				if !w.ambiguous || !w.fenced {
					t.Fatal("uncertain writer was not fenced")
				}

				db := mgPersonalReadOnly(t, path)
				rules, work, receipts, sequence := tc.read(t, db, v.root, id)
				if after {
					if rules != tc.afterRules || work != 1 || receipts != 1 || sequence != 1 {
						t.Fatal("committed frame is not whole", rules, work, receipts, sequence)
					}
				} else if rules != tc.beforeRules || work != 0 || receipts != 0 || sequence != 0 {
					t.Fatal("uncommitted frame leaked", rules, work, receipts, sequence)
				}
			})
		}
	}
}
