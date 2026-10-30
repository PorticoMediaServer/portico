package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// swapDiscProbe derives one independent observation from the committed fixture
// probe. A distinct title duration makes each accepted tree identifiable in the
// canonical rows without inventing structural facts.
func swapDiscProbe(t *testing.T, probe disc.Probe, addMs int64) disc.Probe {
	t.Helper()
	raw, err := json.Marshal(probe)
	if err != nil {
		t.Fatal(err)
	}
	var out disc.Probe
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Titles) == 0 {
		t.Fatal("disc fixture has no titles")
	}
	out.Titles[0].DurationMs += addMs
	return out
}

// TestInfoDiscCrossedCustodySwapKeepsPinsWithCurrentObjects crosses two accepted
// disc observations between two physical inventory objects, then publishes one
// fresh real probe for each object through the ordinary job/policy fence. Each
// asset still pins its previous object at that moment, so the finite worker must
// release the lost OLD pin before either side can copy the other's tree. Without
// that release both phase-2 inputs wait on one another forever; the bounded turn
// count below turns that deadlock into a failure instead of a sleep.
func TestInfoDiscCrossedCustodySwapKeepsPinsWithCurrentObjects(t *testing.T) {
	s, db, source, job, objectA, policy, fixtureProbe := discPublicationFixture(t)
	ctx := context.Background()
	c := catalogtest.New(t, db)

	var assetA int64
	var tokenA, revisionA, pathA string
	var sizeA, modifiedA int64
	if err := db.QueryRow(`SELECT a.id,a.token,o.revision,a.path,o.size,o.modified_ns
 FROM inventory_objects o JOIN catalog_assets a ON a.token=o.asset_id WHERE o.id=?`, objectA).
		Scan(&assetA, &tokenA, &revisionA, &pathA, &sizeA, &modifiedA); err != nil {
		t.Fatal(err)
	}

	// The second asset/object pair shares the fixture's one actual observation.
	// Both inventory rows therefore carry the exact same size/modified_ns their
	// assets carry, so the crossed asset_id swap preserves the producer's
	// a.size=o.size AND a.modified_ns=o.modified_ns admission unchanged.
	tokenB := identity.Token()
	var assetB int64
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		assetB, err = compactcatalog.CreateAssetTx(ctx, tx, tokenB, compactcatalog.Asset{
			Path: filepath.Join(filepath.Dir(pathA), "VIDEO_TS_B"), Size: sizeA, ModifiedNS: modifiedA, Container: "disc",
		}, false)
		return err
	})
	objectB := identity.Token()
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path)
 SELECT ?,source_id,?,root_incarnation,'VIDEO_TS_B',object_identity,revision,evidence_json,size,modified_ns,seen_job,directory_path
 FROM inventory_objects WHERE id=?`, objectB, tokenB, objectA)
		return err
	})
	var revisionB string
	if err := db.QueryRow(`SELECT revision FROM inventory_objects WHERE id=?`, objectB).Scan(&revisionB); err != nil {
		t.Fatal(err)
	}

	library := c.Handle(source.LibraryID)
	itemA := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: "swap-disc-a", Title: "Swap disc A"}, nil)
	itemB := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: "swap-disc-b", Title: "Swap disc B"}, nil)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.LinkAssetTx(ctx, tx, itemA.ID, assetA, compactcatalog.Link{})
	})
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.LinkAssetTx(ctx, tx, itemB.ID, assetB, compactcatalog.Link{})
	})

	probeA0 := swapDiscProbe(t, fixtureProbe, 0)
	probeB0 := swapDiscProbe(t, fixtureProbe, 61_000)
	acceptInitial := func(object, revision string, probe disc.Probe) {
		t.Helper()
		receipt, err := acceptDiscProbeForTest(t, s, ctx, job, object, revision, policy, probe)
		if err != nil {
			t.Fatalf("initial disc producer for object %s: %v", object, err)
		}
		if receipt.State != compactcatalog.InfoInputAcceptedPending && receipt.State != compactcatalog.InfoInputApplied {
			t.Fatalf("initial disc receipt for object %s: %+v", object, receipt)
		}
	}
	acceptInitial(objectA, revisionA, probeA0)
	c.Drain()
	acceptInitial(objectB, revisionB, probeB0)
	c.Drain()

	readObject := func(object, revision string) disc.Probe {
		t.Helper()
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		got, err := ReadInventoryDiscProbeTx(ctx, tx, object, revision)
		if err != nil {
			t.Fatalf("read disc observation %s: %v", object, err)
		}
		return got
	}
	pin := func(asset int64) (string, string) {
		t.Helper()
		var object, revision sql.NullString
		if err := db.QueryRow(`SELECT info_owner_object,info_owner_revision FROM catalog_assets WHERE id=?`, asset).Scan(&object, &revision); err != nil {
			t.Fatal(err)
		}
		if !object.Valid || !revision.Valid {
			t.Fatalf("asset %d has no accepted pin", asset)
		}
		return object.String, revision.String
	}
	facts := func(object string) (string, string) {
		t.Helper()
		var token, fingerprint string
		if err := db.QueryRow(`SELECT asset_id,fingerprint FROM inventory_disc_facts WHERE object_id=?`, object).Scan(&token, &fingerprint); err != nil {
			t.Fatalf("accepted facts for object %s: %v", object, err)
		}
		return token, fingerprint
	}

	// Two independently accepted initial observations: each asset pins exactly
	// its own current object and carries that object's complete tree.
	if object, revision := pin(assetA); object != objectA || revision != revisionA {
		t.Fatalf("initial A pin %s/%s, want %s/%s", object, revision, objectA, revisionA)
	}
	if object, revision := pin(assetB); object != objectB || revision != revisionB {
		t.Fatalf("initial B pin %s/%s, want %s/%s", object, revision, objectB, revisionB)
	}
	if token, _ := facts(objectA); token != tokenA {
		t.Fatalf("initial A facts token %s, want %s", token, tokenA)
	}
	if token, _ := facts(objectB); token != tokenB {
		t.Fatalf("initial B facts token %s, want %s", token, tokenB)
	}
	if got := readObject(objectA, revisionA); !reflect.DeepEqual(got, probeA0) {
		t.Fatal("initial A observation changed")
	}
	if got := readObject(objectB, revisionB); !reflect.DeepEqual(got, probeB0) {
		t.Fatal("initial B observation changed")
	}
	fpA0 := func() string { _, fp := facts(objectA); return fp }()
	fpB0 := func() string { _, fp := facts(objectB); return fp }()

	// Cross the two current objects. Only the owner token moves; every stat and
	// revision stays byte-for-byte what the producer already accepted.
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE inventory_objects SET asset_id=CASE id WHEN ? THEN ? WHEN ? THEN ? END WHERE id IN(?,?)`,
			objectA, tokenB, objectB, tokenA, objectA, objectB)
		return err
	})
	var statMismatch int
	if err := db.QueryRow(`SELECT count(*) FROM inventory_objects o JOIN catalog_assets a ON a.token=o.asset_id
 WHERE o.id IN(?,?) AND (a.size<>o.size OR a.modified_ns<>o.modified_ns)`, objectA, objectB).Scan(&statMismatch); err != nil || statMismatch != 0 {
		t.Fatalf("crossed swap changed an exact stat: %d %v", statMismatch, err)
	}

	// Each current object now gets an independent real probe through the same
	// job, revision and policy fence. Both inputs are staged before any worker
	// turn so the crossed pair cannot be replaced by an availability-only choice.
	probeForA := swapDiscProbe(t, fixtureProbe, 122_000)
	probeForB := swapDiscProbe(t, fixtureProbe, 183_000)
	if reflect.DeepEqual(probeForA, probeA0) || reflect.DeepEqual(probeForB, probeB0) {
		t.Fatal("crossed observations are indistinguishable from their old trees")
	}
	type crossedInput struct {
		object, revision string
		probe            disc.Probe
		fingerprint      string
		accepted         bool
	}
	inputs := []*crossedInput{
		{object: objectA, revision: revisionA, probe: probeForA},
		{object: objectB, revision: revisionB, probe: probeForB},
	}
	accept := func(w *crossedInput) (bool, error) {
		receipt, err := s.AcceptInventoryDiscProbe(ctx, job, w.object, w.revision, policy, w.probe)
		if err != nil {
			return false, err
		}
		if receipt.State != compactcatalog.InfoInputAcceptedPending {
			return false, fmt.Errorf("crossed disc receipt for %s: %+v", w.object, receipt)
		}
		// Keep the accepted header's exact fingerprint as the expected tree
		// provenance before the worker can retire it.
		if err := db.QueryRow(`SELECT lower(hex(fingerprint)) FROM catalog_info_changes WHERE id=?`, receipt.ChangeID).Scan(&w.fingerprint); err != nil {
			return false, err
		}
		w.accepted = true
		return true, nil
	}
	for _, w := range inputs {
		if _, err := accept(w); err != nil &&
			!errors.Is(err, compactcatalog.ErrInfoInputDeferred) && !errors.Is(err, compactcatalog.ErrInfoInputBusy) {
			t.Fatalf("crossed disc producer for object %s: %v", w.object, err)
		}
	}
	worker := compactcatalog.NewWorker(db)
	accepted := 0
	for round := 0; round < 1000; round++ {
		accepted = 0
		for _, w := range inputs {
			if w.accepted {
				accepted++
				continue
			}
			_, err := accept(w)
			switch {
			case err == nil:
				accepted++
			case errors.Is(err, compactcatalog.ErrInfoInputDeferred), errors.Is(err, compactcatalog.ErrInfoInputBusy):
				// The real A5 directory pass the worker drives has not captured
				// the swapped owner yet; retry after its next bounded turn.
			default:
				t.Fatalf("crossed disc producer for object %s: %v", w.object, err)
			}
		}
		if accepted == len(inputs) {
			break
		}
		if _, err := worker.Step(ctx, 8); err != nil {
			t.Fatalf("compact worker while accepting crossed inputs: %v", err)
		}
	}
	if accepted != len(inputs) {
		rows, e := db.Query(`SELECT source_id,token,revision,pass_revision,phase,after_key,inner_key,flags FROM catalog_upstream_sources WHERE domain=41 AND source_id IN(?,?)`, assetA, assetB)
		if e != nil {
			t.Fatal(e)
		}
		for rows.Next() {
			var id, revision, pass, phase, flags int64
			var token, key, inner string
			if e = rows.Scan(&id, &token, &revision, &pass, &phase, &key, &inner, &flags); e != nil {
				t.Fatal(e)
			}
			t.Log("source", id, token, revision, pass, phase, key, inner, flags)
		}
		rows.Close()
		rows, e = db.Query(`SELECT asset_id,slot,phase,expected_epoch,family_mask,COALESCE(target_owner_object,''),copy_aux FROM catalog_info_changes WHERE asset_id IN(?,?)`, assetA, assetB)
		if e != nil {
			t.Fatal(e)
		}
		for rows.Next() {
			var asset, slot, phase, epoch, mask, aux int64
			var owner string
			if e = rows.Scan(&asset, &slot, &phase, &epoch, &mask, &owner, &aux); e != nil {
				t.Fatal(e)
			}
			t.Log("change", asset, slot, phase, epoch, mask, owner, aux)
		}
		rows.Close()
		t.Fatal("crossed disc inputs did not accept within the bounded producer turns")
	}
	fpForA, fpForB := inputs[0].fingerprint, inputs[1].fingerprint

	readOK := func(object, revision string) (disc.Probe, bool) {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return disc.Probe{}, false
		}
		defer tx.Rollback()
		got, err := ReadInventoryDiscProbeTx(ctx, tx, object, revision)
		if err != nil {
			return disc.Probe{}, false
		}
		return got, true
	}
	settled := func() bool {
		var pending int64
		if err := db.QueryRow(`SELECT count(*) FROM catalog_info_changes WHERE asset_id IN(?,?)`, assetA, assetB).Scan(&pending); err != nil || pending != 0 {
			return false
		}
		var object, revision sql.NullString
		if err := db.QueryRow(`SELECT info_owner_object,info_owner_revision FROM catalog_assets WHERE id=?`, assetA).Scan(&object, &revision); err != nil || !object.Valid || !revision.Valid || object.String != objectB || revision.String != revisionB {
			return false
		}
		if err := db.QueryRow(`SELECT info_owner_object,info_owner_revision FROM catalog_assets WHERE id=?`, assetB).Scan(&object, &revision); err != nil || !object.Valid || !revision.Valid || object.String != objectA || revision.String != revisionA {
			return false
		}
		var token, fingerprint string
		if err := db.QueryRow(`SELECT asset_id,fingerprint FROM inventory_disc_facts WHERE object_id=?`, objectA).Scan(&token, &fingerprint); err != nil || token != tokenB || fingerprint != fpForA {
			return false
		}
		if err := db.QueryRow(`SELECT asset_id,fingerprint FROM inventory_disc_facts WHERE object_id=?`, objectB).Scan(&token, &fingerprint); err != nil || token != tokenA || fingerprint != fpForB {
			return false
		}
		if got, ok := readOK(objectA, revisionA); !ok || !reflect.DeepEqual(got, probeForA) {
			return false
		}
		if got, ok := readOK(objectB, revisionB); !ok || !reflect.DeepEqual(got, probeForB) {
			return false
		}
		for _, item := range []int64{itemA.ID, itemB.ID} {
			head, err := compactcatalog.InfoHeadTx(ctx, db, item)
			if err != nil || !head.Ready() {
				return false
			}
		}
		return true
	}

	// A finite worker interleaving, no sleeps and no timeout: the crossed inputs
	// either settle or the turn budget is exhausted.
	const maxTurns = 1200
	drained := false
	turn := 0
	for ; turn < maxTurns; turn++ {
		n, err := worker.Step(ctx, 16)
		if err != nil {
			t.Fatalf("compact worker on crossed custody: %v", err)
		}
		if n == 0 && settled() {
			drained = true
			break
		}
	}
	if !drained {
		var pending, phase int64
		_ = db.QueryRow(`SELECT count(*) FROM catalog_info_changes WHERE asset_id IN(?,?)`, assetA, assetB).Scan(&pending)
		_ = db.QueryRow(`SELECT COALESCE(min(phase),-1) FROM catalog_info_changes WHERE asset_id IN(?,?)`, assetA, assetB).Scan(&phase)
		t.Fatalf("crossed custody did not settle in %d bounded worker turns: %d pending input(s), lowest phase %d", maxTurns, pending, phase)
	}

	// Each asset's accepted pin and tree follow its current object.
	if object, revision := pin(assetA); object != objectB || revision != revisionB {
		t.Fatalf("crossed A pin %s/%s, want current object %s/%s", object, revision, objectB, revisionB)
	}
	if object, revision := pin(assetB); object != objectA || revision != revisionA {
		t.Fatalf("crossed B pin %s/%s, want current object %s/%s", object, revision, objectA, revisionA)
	}
	if token, fingerprint := facts(objectA); token != tokenB || fingerprint != fpForA {
		t.Fatalf("object A holds foreign old tree: token %s fingerprint %s, want %s/%s", token, fingerprint, tokenB, fpForA)
	}
	if token, fingerprint := facts(objectB); token != tokenA || fingerprint != fpForB {
		t.Fatalf("object B holds foreign old tree: token %s fingerprint %s, want %s/%s", token, fingerprint, tokenA, fpForB)
	}
	if fpForA == fpA0 || fpForB == fpB0 {
		t.Fatal("crossed provenance equals the prior tree")
	}
	if got := readObject(objectA, revisionA); !reflect.DeepEqual(got, probeForA) {
		t.Fatal("object A did not receive the observation accepted for its current asset")
	}
	if got := readObject(objectB, revisionB); !reflect.DeepEqual(got, probeForB) {
		t.Fatal("object B did not receive the observation accepted for its current asset")
	}
	for _, item := range []int64{itemA.ID, itemB.ID} {
		head, err := compactcatalog.InfoHeadTx(ctx, db, item)
		if err != nil {
			t.Fatal(err)
		}
		if !head.Ready() {
			t.Fatalf("item %d informational head is not ready: %+v", item, head)
		}
	}
	var stuck int64
	if err := db.QueryRow(`SELECT count(*) FROM catalog_info_changes WHERE asset_id IN(?,?)`, assetA, assetB).Scan(&stuck); err != nil || stuck != 0 {
		t.Fatalf("crossed custody left %d stuck input(s)", stuck)
	}
}
