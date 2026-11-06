package writer

import (
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func TestInventoryPublicPartCaptureAndRequestVersion(t *testing.T) {
	in, identity := inventoryPublicInput(t)
	var previous RequestDigest
	for _, part := range []int64{0, 1, 2} {
		in.Files[0].Part = part
		value, err := NewInventoryAcceptCommand(identity, in)
		if err != nil {
			t.Fatal(err)
		}
		c := value.(*inventoryAcceptCommand)
		if c.request.files[0].part != part {
			t.Fatal("part omitted", part, c.request.files[0].part)
		}
		expected, err := canonicalRequestDigest(InventoryAccept, 2, identity, c.request)
		oldVersion, oldErr := canonicalRequestDigest(InventoryAccept, 1, identity, c.request)
		if err != nil || oldErr != nil || expected != c.digest || oldVersion == c.digest || c.digest == previous {
			t.Fatal("part/version missing from original commitment", part, err, oldErr)
		}
		previous = c.digest
		saved, err := InventoryAcceptReference(value)
		if err != nil {
			t.Fatal(err)
		}
		in.Files[0].Part = 99
		again, err := InventoryAcceptReference(value)
		if err != nil || again != saved || c.request.files[0].part != part {
			t.Fatal("original reference changed", again, err)
		}
	}
	in.Files[0].Part = -1
	value, err := NewInventoryAcceptCommand(identity, in)
	if value != nil || !errors.Is(err, ErrInvalid) || errors.Is(err, ErrFilenameUnrepresentable) {
		t.Fatal("negative part accepted or classified as poison", value, err)
	}
}

func TestInventoryFreshNativePartAndHistoricalReference(t *testing.T) {
	definitions := append(scanInventoryFreshDefinitions(), inventoryHistoryDefinitions()...)
	e, path, base, registry := inventoryFreshFixtureDefinitions(t, definitions)
	for _, part := range []int64{0, 1, 2} {
		t.Run(string(rune('0'+part)), func(t *testing.T) {
			r := inventoryTestClone(base.request)
			r.files[0].part = part
			c, err := newInventoryAcceptCommand(base.identity, r)
			if err != nil {
				t.Fatal(err)
			}
			saved, err := InventoryAcceptReference(c)
			if err != nil {
				t.Fatal(err)
			}
			turn, a := inventoryFreshTestTurn(t, e, path, registry)
			out, err := applyInventoryAccept(t.Context(), turn, c)
			if err != nil || out.Outcome != (ReceiptOutcome{}) || len(out.Members) != 1 {
				t.Fatal(out, err)
			}
			m := out.Members[0]
			var actual, wholeVersions int64
			if err = turn.transaction.conn.QueryRowContext(t.Context(), `SELECT part_index,(SELECT count(*) FROM catalog_asset_links WHERE entity_id=? AND part_index=0) FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, m.SubjectID, m.SubjectID, m.AssetID).Scan(&actual, &wholeVersions); err != nil || actual != part {
				t.Fatal(actual, err)
			}
			expectedVersions := int64(0)
			if part == 0 {
				expectedVersions = 1
			}
			if wholeVersions != expectedVersions {
				t.Fatal("multipart consumed whole-source version", wholeVersions)
			}
			// The saved original reference is the historical authority. Neither a
			// new V2 constructor nor today's link part reconstructs a saved receipt.
			if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE catalog_asset_links SET part_index=99 WHERE entity_id=? AND asset_id=?`, m.SubjectID, m.AssetID); err != nil {
				t.Fatal(err)
			}
			lookup, err := lookupInventoryAccept(t.Context(), turn, saved)
			if err != nil || lookup.digest != out.Digest || lookup.result.AcceptedRunRevision != out.AcceptedRunRevision || !reflect.DeepEqual(InventoryMember(lookup.result.Members[0]), m) {
				t.Fatal("history depended on current part", lookup, err)
			}
			changed := saved
			changed.Receipt.RequestDigest[0] ^= 1
			if _, err = lookupInventoryAccept(t.Context(), turn, changed); !errors.Is(err, ErrConflict) {
				t.Fatal("wrong original digest accepted", err)
			}
			if err = turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err = turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			if a.Usage() != ([4]governor.Amounts{}) {
				t.Fatal("part retained custody")
			}
			inventoryFreshAssertAbsent(t, e)
		})
	}
}

func TestInventoryFreshPartReadbackConflictRollsBack(t *testing.T) {
	e, path, base, registry := inventoryFreshFixture(t)
	r := inventoryTestClone(base.request)
	r.files[0].part = 2
	c, err := newInventoryAcceptCommand(base.identity, r)
	if err != nil {
		t.Fatal(err)
	}
	fault := &inventoryFreshDriverFault{at: scanFreshFinalLinks}
	fault.before = func(conn writerDriverConn) {
		if _, err := conn.ExecContext(t.Context(), `UPDATE catalog_asset_links SET part_index=1`, nil); err != nil {
			t.Fatal(err)
		}
	}
	inventoryFreshInstallDriver(t, e, path, fault)
	turn, a := inventoryFreshTestTurn(t, e, path, registry)
	out, err := applyInventoryAccept(t.Context(), turn, c)
	if !fault.hit || !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, InventoryResult{}) {
		t.Fatal("wrong native part accepted", out, err)
	}
	if closeErr := turn.closeRows(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if _, rollbackErr := turn.transaction.rollback(err); !errors.Is(rollbackErr, err) {
		t.Fatal(rollbackErr)
	}
	if a.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("failed part retained custody")
	}
	inventoryFreshAssertAbsent(t, e)
}

func TestInventoryPartProjectionUsesOneMovieIdentity(t *testing.T) {
	var key string
	for _, filename := range []string{"The Film (2020) CD1.mkv", "The Film (2020) CD2.mkv"} {
		projection, err := ProjectOrdinaryFilename(InventoryOrdinaryFilenameInput{IdentityRoot: []byte("/media"), AssetPath: []byte("/media/" + filename), RelativePath: []byte(filename), MetadataLanguage: "en-US"})
		if err != nil {
			t.Fatal(err)
		}
		in, identity := inventoryPublicInput(t)
		f := &in.Files[0]
		f.RelativePath, f.AssetPath = []byte(filename), []byte("/media/"+filename)
		f.SourceKey, f.Year, f.Part = projection.SourceKey, projection.Year, projection.Part
		p := projection.Metadata
		f.Title, f.SortTitle, f.SortKey, f.SortHead = p.Title, p.SortTitle, p.SortKey, p.SortHead
		f.SortKeyKeepArticles, f.SortHeadKeepArticles, f.MetadataLanguage = p.SortKeyKeepArticles, p.SortHeadKeepArticles, p.MetadataLanguage
		command, err := NewInventoryAcceptCommand(identity, in)
		if err != nil {
			t.Fatal(err)
		}
		actual := command.(*inventoryAcceptCommand).request.files[0]
		if actual.part != projection.Part || actual.part < 1 || actual.part > 2 || actual.sourceKey != projection.SourceKey {
			t.Fatal("projection lost part/identity", actual)
		}
		if key != "" && key != actual.sourceKey {
			t.Fatal("part changed movie namespace", key, actual.sourceKey)
		}
		key = actual.sourceKey
	}
}
