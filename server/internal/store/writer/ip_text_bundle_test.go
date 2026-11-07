package writer

import (
	"errors"
	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
	"reflect"
	"testing"
)

func ipBundleFixture(t *testing.T) (*executor, ipTextBundleRequest) {
	w, base := ipTextFixture(t)
	ipPolicyTestExec(t, w, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at,value_present,automatic_present) VALUES('item',101,'country','','CA',0,'prior','prior',1,1),('item',101,'title','Title','Title',0,'prior','prior',1,1)`)
	s := ipTextBundleSource{OwnerID: 11, EntityID: 101, LibraryID: 7, Kind: "item", Actor: "local:administrator", OwnerRevision: 17, SubjectRevision: 23, MetadataRevision: 29, MetadataIncarnation: [16]byte{1}, Projection: ipTextProjection{Title: "Title", SortKey: "title", SortHead: "t", SortKeyKeepArticles: "title", SortHeadKeepArticles: "t"}}
	return w, ipTextBundleRequest{Identity: base.identity, Kind: IPFieldEdit, Source: s, Fields: []ipTextBundleField{
		{fieldmeta.Country, fieldmeta.TextState{Effective: fieldmeta.Text{Present: true}, Automatic: fieldmeta.Text{"CA", true}}, fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: "US"}},
		{fieldmeta.Title, fieldmeta.TextState{Effective: fieldmeta.Text{"Title", true}, Automatic: fieldmeta.Text{"Title", true}}, fieldmeta.TextEdit{Action: fieldmeta.SetText, Value: "The Voyage"}},
	}}
}
func ipBundleDo(t *testing.T, w *executor, q ipTextBundleRequest) (ipTextBundleNative, *commandTurn, error) {
	t.Helper()
	c, err := newIPTextBundleCommand(q)
	if err != nil {
		t.Fatal(err)
	}
	turn := ipPolicyTestTurn(t, w, q.Kind, ipTextBundleDefinitions())
	out, err := applyIPTextBundle(t.Context(), turn, c, 1234)
	return out, turn, err
}
func TestIPTextBundleNativeAtomicFieldsAndReplay(t *testing.T) {
	w, q := ipBundleFixture(t)
	got, turn, err := ipBundleDo(t, w, q)
	if err != nil || got.decision != receiptFresh || got.value.Revision != 18 || got.value.SubjectRevision != 24 || len(got.value.Fields) != 2 || !got.value.History.Present {
		t.Fatal(got, err)
	}
	ipPolicyTestCommit(t, turn)
	var title, country, key, keep string
	var revision, history, fields, count int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT title,country,sort_key,sort_key_keep_articles,revision,(SELECT count(*) FROM metadata_owner_history),(SELECT count(*) FROM metadata_owner_history_fields),(SELECT result_count FROM metadata_edit_receipts) FROM catalog_entities WHERE id=101`).Scan(&title, &country, &key, &keep, &revision, &history, &fields, &count); err != nil || title != "The Voyage" || country != "US" || key != "voyage" || keep != "the voyage" || revision != 24 || history != 1 || fields != 2 || count != 2 {
		t.Fatal(title, country, key, keep, revision, history, fields, count, err)
	}
	ipPolicyTestExec(t, w, `UPDATE catalog_entities SET revision=99,country='other' WHERE id=101`)
	replay, turn, err := ipBundleDo(t, w, q)
	if err != nil || replay.decision != receiptReplay || !reflect.DeepEqual(got.value, replay.value) || got.digest != replay.digest {
		t.Fatal(replay, err)
	}
	ipPolicyTestCommit(t, turn)
}
func TestIPTextBundleNativeTerminalRollback(t *testing.T) {
	w, q := ipBundleFixture(t)
	ipPolicyTestExec(t, w, `CREATE TRIGGER ip_bundle_fail BEFORE INSERT ON metadata_edit_receipt_fields WHEN NEW.ordinal=1 BEGIN SELECT RAISE(ABORT,'second result');END`)
	got, turn, err := ipBundleDo(t, w, q)
	if err == nil || !reflect.DeepEqual(got, ipTextBundleNative{}) {
		t.Fatal(got, err)
	}
	_, _ = turn.transaction.rollback(err)
	if got := ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_edit_receipts`); got != 0 {
		t.Fatal(got)
	}
	if got := ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_owner_history`); got != 0 {
		t.Fatal(got)
	}
	if got := ipPolicyTestScalar(t, w, `SELECT revision FROM catalog_entities WHERE id=101`); got != 23 {
		t.Fatal(got)
	}
	if got := ipPolicyTestScalar(t, w, `SELECT count(*) FROM metadata_owner_fields WHERE field IN('country','title') AND locked=1`); got != 0 {
		t.Fatal(got)
	}
}
func TestIPTextBundlePersonNamespaceIsolation(t *testing.T) {
	w, base := ipTextFixture(t)
	ipPolicyTestExec(t, w, `INSERT INTO catalog_people(id,token,identity_key,name,sort_name,biography,revision,artwork_incarnation) VALUES(101,'person101','provider:101','Person','person','Biography',5,zeroblob(16))`)
	ipPolicyTestExec(t, w, `INSERT INTO metadata_edit_owners(id,person_id,kind,revision,command_incarnation) VALUES(12,101,'person',1,X'02000000000000000000000000000000')`)
	ipPolicyTestExec(t, w, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at,value_present,automatic_present) VALUES('person',101,'biography','Biography','Provider biography',1,'prior','prior',1,1)`)
	q := ipTextBundleRequest{Identity: base.identity, Kind: IPFieldRestore, Source: ipTextBundleSource{OwnerID: 12, PersonID: 101, Kind: "person", Actor: "local:administrator", OwnerRevision: 1, SubjectRevision: 5}, Fields: []ipTextBundleField{{fieldmeta.Description, fieldmeta.TextState{Effective: fieldmeta.Text{"Biography", true}, Automatic: fieldmeta.Text{"Provider biography", true}, Locked: true}, fieldmeta.TextEdit{Action: fieldmeta.RestoreAutomatic}}}}
	bad := q
	bad.Source.OwnerID = 11
	rejected, turn, err := ipBundleDo(t, w, bad)
	if !errors.Is(err, ErrOwnerUnavailable) || !reflect.DeepEqual(rejected, ipTextBundleNative{}) {
		t.Fatal(rejected, err)
	}
	_, _ = turn.transaction.rollback(err)
	got, turn, err := ipBundleDo(t, w, q)
	if err != nil || got.value.EntityID != 0 || got.value.PersonID != 101 || got.value.SubjectRevision != 6 {
		t.Fatal(got, err)
	}
	ipPolicyTestCommit(t, turn)
	var biography, overview string
	var entityRevision, personRevision int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT p.biography,p.revision,e.overview,e.revision FROM catalog_people p JOIN catalog_entities e ON p.id=e.id WHERE p.id=101`).Scan(&biography, &personRevision, &overview, &entityRevision); err != nil || biography != "Provider biography" || personRevision != 6 || overview != "" || entityRevision != 23 {
		t.Fatal(biography, personRevision, overview, entityRevision, err)
	}
}
