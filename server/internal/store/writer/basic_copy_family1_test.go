package writer

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// basicControlChain issues consecutive Basic control commands on ONE run,
// advancing the frozen original after each saved and ACKed receipt.
type basicControlChain struct {
	t        *testing.T
	w        *Writer
	original basicOriginalScope
	identity CommandIdentity
}

func (c *basicControlChain) stage(in basicControlRequest) (BasicResult, error) {
	c.t.Helper()
	in.original, in.purpose = c.original, BasicPurposeStage
	cmd, err := newBasicStageCommand(c.identity, in)
	if err != nil {
		return BasicResult{}, err
	}
	q, _ := cmd.receiptRequest()
	disclosure := inventoryTestDisclosure(c.t, c.w, q.Scan)
	got, _, err := c.w.effective.runBasic(c.t.Context(), c.w, disclosure, basicInvocation{request: q, stage: cmd}, false)
	if err != nil {
		return got, err
	}
	if _, ack, e := c.w.effective.runBasic(c.t.Context(), c.w, disclosure, basicInvocation{request: q}, true); e != nil || ack.AcknowledgedThrough != c.identity.Sequence {
		c.t.Fatal("chain ACK", ack, e)
	}
	c.original.run.revision, c.original.run.lastSequence, c.original.run.acknowledgedSequence = got.AcceptedRunRevision, c.identity.Sequence, c.identity.Sequence
	c.identity.Sequence++
	c.identity.OperationID[1]++
	return got, nil
}

// Freeze (0->1), entity (1->2) and the family-1 copy turn run natively through
// the SAME dispatcher after a real family-1 Stage/Seal; the change ends at
// phase 2 with copy_family 2 and after_entity = the sole leaf, and the staged
// stream fact is the canonical row.
func TestBasicFreezeEntityAndFamilyOneCopyDispatch(t *testing.T) {
	w, e, authority, s, original, identity := basicControlDispatchFixture(t)
	seedBasicFirstOrdinaryOwner(t, e)
	chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
	if _, err := chain.stage(basicControlRequest{operation: basicControlStreamFact, source: s, family: 1}); err != nil {
		t.Fatal("stage family 1", err)
	}
	// Seal family 1 through the real Seal command on the same chain.
	seal, err := newBasicSealCommand(chain.identity, basicControlRequest{original: chain.original, purpose: BasicPurposeSeal, operation: basicControlStreamFact, source: s, family: 1})
	if err != nil {
		t.Fatal(err)
	}
	ql, _ := seal.receiptRequest()
	disclosure := inventoryTestDisclosure(t, w, ql.Scan)
	sealed, _, err := w.effective.runBasic(t.Context(), w, disclosure, basicInvocation{request: ql, seal: seal}, false)
	if err != nil {
		t.Fatal("seal family 1", err)
	}
	if _, _, err = w.effective.runBasic(t.Context(), w, disclosure, basicInvocation{request: ql}, true); err != nil {
		t.Fatal(err)
	}
	chain.original.run.revision, chain.original.run.lastSequence, chain.original.run.acknowledgedSequence = sealed.AcceptedRunRevision, chain.identity.Sequence, chain.identity.Sequence
	chain.identity.Sequence++
	chain.identity.OperationID[1]++
	// Pin is component-proven (pinBasicProbeShape); seed its exact post-state
	// and families 2..9 as sealed empty sets, all under mask 511.
	for _, q := range []string{
		`INSERT INTO catalog_info_shapes(id,container,video_codec,height,ref_count) VALUES(61,'mkv','h264',1080,1)`,
		`UPDATE catalog_info_changes SET family_mask=511,target_shape_id=61,copy_family=1,job_revision=2 WHERE id=51`,
		`INSERT INTO catalog_info_input_families(change_id,family,witness_ordinal,rows_expected,rows_staged,sealed) VALUES(51,2,0,0,0,1),(51,3,0,0,0,1),(51,4,0,0,0,1),(51,5,0,0,0,1),(51,6,0,0,0,1),(51,7,0,0,0,1),(51,8,0,0,0,1),(51,9,0,0,0,1)`,
	} {
		if _, err := e.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(q, err)
		}
	}
	head := basicControlRequest{source: s, changeRevision: 2, entityID: 31, shapeID: 61}
	freeze := head
	freeze.operation = basicControlFreeze
	got, err := chain.stage(freeze)
	if err != nil || got.Continuation != (BasicContinuation{NextFamily: 1}) {
		t.Fatal("freeze", got, err)
	}
	entity := head
	entity.operation, entity.changeRevision = basicControlSelectEntity, 3
	got, err = chain.stage(entity)
	if err != nil || got.Continuation != (BasicContinuation{AfterEntity: 31}) {
		t.Fatal("entity", got, err)
	}
	copy1 := head
	copy1.operation, copy1.changeRevision, copy1.family = basicControlCopyFamily, 4, 1
	got, err = chain.stage(copy1)
	if err != nil || got.Continuation != (BasicContinuation{NextFamily: 2, AfterEntity: 31}) {
		t.Fatal("copy family 1", got, err)
	}
	basicDispatchReturned(t, e, authority)
	var phase, family, after, upper, hasAfter, revision int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT phase,copy_family,after_entity,upper_entity,has_after,job_revision FROM catalog_info_changes WHERE id=51`).Scan(&phase, &family, &after, &upper, &hasAfter, &revision); err != nil ||
		phase != 2 || family != 2 || after != 31 || upper != 31 || hasAfter != 1 || revision != 5 {
		t.Fatal("copy post-state", phase, family, after, upper, hasAfter, revision, err)
	}
	var fingerprint string
	var factRevision int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT revision,fingerprint FROM asset_stream_facts WHERE asset_id='basic-asset'`).Scan(&factRevision, &fingerprint); err != nil || factRevision != s.factRevision || fingerprint != s.fingerprint {
		t.Fatal("canonical family-1 row", factRevision, fingerprint, err)
	}
	// Replaying the copy entry is refused: the cursor already moved.
	copy1.changeRevision = 4
	if _, err := chain.stage(copy1); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatal("stale copy entry was not refused natively", err)
	}
}

func TestBasicTransitionRequestsAreClosed(t *testing.T) {
	_, s, original, identity := basicControlFixture(t)
	base := basicControlRequest{original: original, source: s, changeRevision: 2, entityID: 31, shapeID: 61}
	ok := base
	ok.operation, ok.family, ok.purpose = basicControlCopyFamily, 1, BasicPurposeStage
	if _, err := newBasicStageCommand(identity, ok); err != nil {
		t.Fatal("family-1 copy request", err)
	}
	for name, r := range map[string]basicControlRequest{
		"seal_freeze": func() basicControlRequest {
			x := base
			x.operation = basicControlFreeze
			x.purpose = BasicPurposeSeal
			return x
		}(),
		"freeze_family":     func() basicControlRequest { x := base; x.operation, x.family = basicControlFreeze, 1; return x }(),
		"copy_family_zero":  func() basicControlRequest { x := base; x.operation = basicControlCopyFamily; return x }(),
		"copy_family_ten":   func() basicControlRequest { x := base; x.operation, x.family = basicControlCopyFamily, 10; return x }(),
		"copy1_with_cursor": func() basicControlRequest { x := ok; x.copy = basicCopyCursor{family: 2}; return x }(),
		"copy2_wrong_entry": func() basicControlRequest { x := ok; x.family, x.copy = 2, basicCopyCursor{family: 3}; return x }(),
		"no_revision":       func() basicControlRequest { x := ok; x.changeRevision = 0; return x }(),
		"no_entity":         func() basicControlRequest { x := ok; x.entityID = 0; return x }(),
		"stream_with_head":  {original: original, source: s, operation: basicControlStreamFact, family: 1, changeRevision: 2},
		"pin_without_probe": func() basicControlRequest {
			x := base
			x.operation, x.changeRevision, x.entityID, x.shapeID = basicControlPinShape, 0, 0, 0
			return x
		}(),
	} {
		var err error
		if r.purpose == BasicPurposeSeal {
			_, err = newBasicSealCommand(identity, r)
		} else {
			r.purpose = BasicPurposeStage
			_, err = newBasicStageCommand(identity, r)
		}
		if err == nil {
			t.Fatal("invalid transition request accepted", name)
		}
	}
}

// Freeze refuses while any of the nine families is unsealed, with no effect.
func TestBasicFreezeRefusesUnsealedFamily(t *testing.T) {
	w, e, authority, s, original, identity := basicControlDispatchFixture(t)
	seedBasicFirstOrdinaryOwner(t, e)
	for _, q := range []string{
		`INSERT INTO catalog_info_shapes(id,container,video_codec,height,ref_count) VALUES(61,'mkv','h264',1080,1)`,
		`UPDATE catalog_info_changes SET family_mask=511,target_shape_id=61,copy_family=1,job_revision=2 WHERE id=51`,
		`UPDATE catalog_info_input_families SET rows_staged=1,sealed=1 WHERE change_id=51 AND family=1`,
		`INSERT INTO catalog_info_input_families(change_id,family,witness_ordinal,rows_expected,rows_staged,sealed) VALUES(51,2,0,0,0,1),(51,3,0,0,0,1),(51,4,0,0,0,1),(51,5,0,1,1,0),(51,6,0,0,0,1),(51,7,0,0,0,1),(51,8,0,0,0,1),(51,9,0,0,0,1)`,
	} {
		if _, err := e.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(q, err)
		}
	}
	chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
	if _, err := chain.stage(basicControlRequest{operation: basicControlFreeze, source: s, changeRevision: 2, entityID: 31, shapeID: 61}); !errors.Is(err, ErrConflict) {
		t.Fatal("freeze with family 5 unsealed", err)
	}
	basicDispatchReturned(t, e, authority)
	var phase, revision int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT phase,job_revision FROM catalog_info_changes WHERE id=51`).Scan(&phase, &revision); err != nil || phase != 0 || revision != 2 {
		t.Fatal("refused freeze moved the change", phase, revision, err)
	}
}

// Freeze opens the asset's publication obligation and entity pre-fences the
// sole leaf; each is a compare-and-set whose failure rolls back the whole turn.
func TestBasicFreezeAndEntityPublicationFences(t *testing.T) {
	setup := func(t *testing.T) (*basicControlChain, *executor, *governor.Authority, basicStreamFactStage) {
		w, e, authority, s, original, identity := basicControlDispatchFixture(t)
		seedBasicFirstOrdinaryOwner(t, e)
		for _, q := range []string{
			`INSERT INTO catalog_info_shapes(id,container,video_codec,height,ref_count) VALUES(61,'mkv','h264',1080,1)`,
			`UPDATE catalog_info_changes SET family_mask=511,target_shape_id=61,copy_family=1,job_revision=2 WHERE id=51`,
			`UPDATE catalog_info_input_families SET rows_staged=1,sealed=1 WHERE change_id=51 AND family=1`,
			`INSERT INTO catalog_info_input_families(change_id,family,witness_ordinal,rows_expected,rows_staged,sealed) VALUES(51,2,0,0,0,1),(51,3,0,0,0,1),(51,4,0,0,0,1),(51,5,0,0,0,1),(51,6,0,0,0,1),(51,7,0,0,0,1),(51,8,0,0,0,1),(51,9,0,0,0,1)`,
		} {
			if _, err := e.db.ExecContext(t.Context(), q); err != nil {
				t.Fatal(q, err)
			}
		}
		return &basicControlChain{t: t, w: w, original: original, identity: identity}, e, authority, s
	}
	state := func(t *testing.T, e *executor) (phase, revision, pending int64, link sql.NullInt64) {
		t.Helper()
		if err := e.db.QueryRowContext(t.Context(), `SELECT (SELECT phase FROM catalog_info_changes WHERE id=51),(SELECT job_revision FROM catalog_info_changes WHERE id=51),(SELECT info_publication_pending FROM catalog_assets WHERE id=41),(SELECT info_pending_change FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)`).Scan(&phase, &revision, &pending, &link); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Run("set_in_order", func(t *testing.T) {
		chain, e, authority, s := setup(t)
		head := basicControlRequest{source: s, entityID: 31, shapeID: 61}
		freeze, entity := head, head
		freeze.operation, freeze.changeRevision = basicControlFreeze, 2
		entity.operation, entity.changeRevision = basicControlSelectEntity, 3
		if _, err := chain.stage(freeze); err != nil {
			t.Fatal(err)
		}
		if phase, _, pending, link := state(t, e); phase != 1 || pending != 1 || link.Valid {
			t.Fatal("after freeze", phase, pending, link)
		}
		if _, err := chain.stage(entity); err != nil {
			t.Fatal(err)
		}
		if phase, _, pending, link := state(t, e); phase != 2 || pending != 1 || !link.Valid || link.Int64 != 51 {
			t.Fatal("after entity", phase, pending, link)
		}
		basicDispatchReturned(t, e, authority)
	})
	t.Run("freeze_refuses_open_obligation", func(t *testing.T) {
		chain, e, authority, s := setup(t)
		if _, err := e.db.ExecContext(t.Context(), `UPDATE catalog_assets SET info_publication_pending=1 WHERE id=41`); err != nil {
			t.Fatal(err)
		}
		if _, err := chain.stage(basicControlRequest{operation: basicControlFreeze, source: s, changeRevision: 2, entityID: 31, shapeID: 61}); !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
		basicDispatchReturned(t, e, authority)
		if phase, revision, _, _ := state(t, e); phase != 0 || revision != 2 {
			t.Fatal("refused freeze moved the change", phase, revision)
		}
	})
	t.Run("entity_refuses_fenced_leaf", func(t *testing.T) {
		chain, e, authority, s := setup(t)
		if _, err := chain.stage(basicControlRequest{operation: basicControlFreeze, source: s, changeRevision: 2, entityID: 31, shapeID: 61}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.db.ExecContext(t.Context(), `UPDATE catalog_asset_links SET info_pending_change=51 WHERE entity_id=31 AND asset_id=41`); err != nil {
			t.Fatal(err)
		}
		if _, err := chain.stage(basicControlRequest{operation: basicControlSelectEntity, source: s, changeRevision: 3, entityID: 31, shapeID: 61}); !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
		basicDispatchReturned(t, e, authority)
		if phase, revision, pending, _ := state(t, e); phase != 1 || revision != 3 || pending != 1 {
			t.Fatal("refused entity moved the change", phase, revision, pending)
		}
	})
}

// A leaf settle would refuse never reaches a fence: freeze refuses it while the
// asset is still visible and the change is still at phase 0.
func TestBasicFreezeRefusesLeafSettleWouldRefuse(t *testing.T) {
	for name, seed := range map[string]func(*testing.T, *executor){
		"cue_link": func(t *testing.T, e *executor) {
			execBasicSeed(t, e, `UPDATE catalog_asset_links SET cue_ordinal=1 WHERE entity_id=31 AND asset_id=41`)
		},
		"ranged_start": func(t *testing.T, e *executor) {
			execBasicSeed(t, e, `UPDATE catalog_asset_links SET start_seconds=5 WHERE entity_id=31 AND asset_id=41`)
		},
		"ranged_end": func(t *testing.T, e *executor) {
			execBasicSeed(t, e, `UPDATE catalog_asset_links SET end_seconds=10 WHERE entity_id=31 AND asset_id=41`)
		},
		"retired": func(t *testing.T, e *executor) {
			execBasicSeed(t, e, `UPDATE catalog_entities SET retired=1 WHERE id=31`)
		},
		"child_entity": func(t *testing.T, e *executor) {
			seedBasicFirstOwnerEntity(t, e, 32, 0x52)
			execBasicSeed(t, e, `DELETE FROM catalog_asset_links WHERE entity_id=32`)
			execBasicSeed(t, e, `UPDATE catalog_entities SET parent_id=32 WHERE id=31`)
		},
		"aggregate_owned": func(t *testing.T, e *executor) {
			execBasicSeedUnchecked(t, e, `UPDATE catalog_entities SET aggregate_publication_id=99 WHERE id=31`)
		},
		"collection_member": func(t *testing.T, e *executor) {
			execBasicSeedUnchecked(t, e, `INSERT INTO catalog_collection_members(collection_id,item_id,order_key) VALUES(99,31,'a')`)
		},
		"unavailable_link": func(t *testing.T, e *executor) {
			execBasicSeed(t, e, `UPDATE catalog_asset_links SET available=0 WHERE entity_id=31 AND asset_id=41`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			w, e, authority, s, original, identity := basicControlDispatchFixture(t)
			seedBasicFirstOrdinaryOwner(t, e)
			execBasicSeed(t, e, `INSERT INTO catalog_info_shapes(id,container,video_codec,height,ref_count) VALUES(61,'mkv','h264',1080,1)`)
			execBasicSeed(t, e, `UPDATE catalog_info_changes SET family_mask=511,target_shape_id=61,copy_family=1,job_revision=2 WHERE id=51`)
			execBasicSeed(t, e, `UPDATE catalog_info_input_families SET rows_staged=1,sealed=1 WHERE change_id=51 AND family=1`)
			execBasicSeed(t, e, `INSERT INTO catalog_info_input_families(change_id,family,witness_ordinal,rows_expected,rows_staged,sealed) VALUES(51,2,0,0,0,1),(51,3,0,0,0,1),(51,4,0,0,0,1),(51,5,0,0,0,1),(51,6,0,0,0,1),(51,7,0,0,0,1),(51,8,0,0,0,1),(51,9,0,0,0,1)`)
			seed(t, e)
			chain := &basicControlChain{t: t, w: w, original: original, identity: identity}
			var refusal *Refusal
			if _, err := chain.stage(basicControlRequest{operation: basicControlFreeze, source: s, changeRevision: 2, entityID: 31, shapeID: 61}); !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified {
				t.Fatal("freeze was not refused", err)
			}
			basicDispatchReturned(t, e, authority)
			// Refused at phase 0: the header stays, the asset stays visible.
			var phase, revision, pending int64
			var link sql.NullInt64
			if err := e.db.QueryRowContext(t.Context(), `SELECT (SELECT phase FROM catalog_info_changes WHERE id=51),(SELECT job_revision FROM catalog_info_changes WHERE id=51),(SELECT info_publication_pending FROM catalog_assets WHERE id=41),(SELECT info_pending_change FROM catalog_asset_links WHERE entity_id=31 AND asset_id=41)`).Scan(&phase, &revision, &pending, &link); err != nil || phase != 0 || revision != 2 || pending != 0 || link.Valid {
				t.Fatal("refused freeze had an effect", phase, revision, pending, link, err)
			}
		})
	}
}

// Seeds a state whose foreign-key parent the test has no reason to build.
func execBasicSeedUnchecked(t *testing.T, e *executor, q string) {
	t.Helper()
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, statement := range []string{`PRAGMA foreign_keys=OFF`, q, `PRAGMA foreign_keys=ON`} {
		if _, err := conn.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(statement, err)
		}
	}
}

func execBasicSeed(t *testing.T, e *executor, q string) {
	t.Helper()
	if _, err := e.db.ExecContext(t.Context(), q); err != nil {
		t.Fatal(q, err)
	}
}

func TestBasicCopyFamilyRegistrationIsClosed(t *testing.T) {
	if _, ok := basicCopyFamilies[1]; !ok || len(basicCopyFamilyOrder()) == 0 || basicCopyFamilyOrder()[0] != 1 {
		t.Fatal("family 1 not registered", basicCopyFamilyOrder())
	}
	if basicControlMaxUses(BasicStage, basicCopy1Insert) != 1 || basicControlMaxUses(BasicSeal, basicCopy1Insert) != 0 {
		t.Fatal("family-1 uses leaked outside Stage")
	}
	spec := basicCopyFamilies[1]
	for name, f := range map[string]func(){
		"duplicate":  func() { registerBasicCopyFamily(1, spec) },
		"family_0":   func() { registerBasicCopyFamily(0, spec) },
		"family_10":  func() { registerBasicCopyFamily(10, spec) },
		"no_body":    func() { registerBasicCopyFamily(9, basicCopyFamilySpec{defs: spec.defs, maxUses: spec.maxUses}) },
		"no_defs":    func() { registerBasicCopyFamily(9, basicCopyFamilySpec{copy: spec.copy, maxUses: spec.maxUses}) },
		"no_maxuses": func() { registerBasicCopyFamily(9, basicCopyFamilySpec{copy: spec.copy, defs: spec.defs}) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid copy family registration accepted", name)
				}
			}()
			f()
		}()
	}
	if _, ok := basicCopyFamilies[9]; ok && len(basicCopyFamilies) == 1 {
		t.Fatal("a refused registration was installed")
	}
}
