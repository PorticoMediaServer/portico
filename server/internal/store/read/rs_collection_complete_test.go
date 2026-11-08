package read

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func TestRSCollectionCompleteAuthoredPages(t *testing.T) {
	f, r := newRSCollectionCompleteFixture(t, func(f *browseFixture) {
		f.exec(t, `DELETE FROM catalog_collection_members WHERE collection_id=1101`)
		f.exec(t, `INSERT INTO catalog_collection_members(collection_id,item_id,order_key) SELECT 1101,id,CASE WHEN id<=10 THEN 'a' ELSE 'b' END FROM catalog_entities WHERE id<=23`)
	})
	f.input.Limit = 7
	p := rsBeginCompleteCollection(t, f, r)
	rsCompleteAuthored(t, f, p)
	if !p.ready || !p.nonempty || len(p.nodes) != 21 || p.source == nil || p.fence == nil {
		t.Fatalf("incomplete authored reduction ready=%v nonempty=%v nodes=%d", p.ready, p.nonempty, len(p.nodes))
	}
	var got []int64
	var after *CollectionDetailCursor
	for page := 0; page < 100; page++ {
		if page > 0 {
			p = rsBeginCompleteCollection(t, f, r)
			rsCompleteAuthored(t, f, p)
		}
		d, cost, err := rsAuthoredLiveDetail(t, f, p, after)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if d.Collection.ID != 1101 || d.Collection.Title != "Collection" || d.LibraryID != "movies" || d.Revision != 1 || d.Origin != "owner" || d.ExactCount != 21 {
			t.Fatalf("collection card/count %+v", d)
		}
		if len(d.Items) > 7 || d.Offset != len(got) || d.NextOffset != d.Offset+len(d.Items) {
			t.Fatalf("page bounds %+v", d)
		}
		for _, item := range d.Items {
			if item.Kind != 1 {
				t.Fatalf("wrong member kind %+v", item)
			}
			got = append(got, item.ID)
		}
		for _, w := range cost.LookupWindows {
			if w.Ended.Sub(w.Started) > time.Second {
				t.Fatal("long snapshot")
			}
		}
		if d.Exhausted {
			if d.Next != nil {
				t.Fatal("terminal page kept a continuation")
			}
			break
		}
		if d.Next == nil {
			t.Fatal("missing continuation")
		}
		after = d.Next
	}
	want := rsAuthoredVisibleIDs(t)[:21]
	if !slices.Equal(got, want) {
		t.Fatalf("order got %v want %v", got, want)
	}
	// A continuation is bound to its exact collection, source and viewer.
	p = rsBeginCompleteCollection(t, f, r)
	rsCompleteAuthored(t, f, p)
	bad := *after
	bad.collection = 1102
	if d, _, err := rsAuthoredLiveDetail(t, f, p, &bad); !errors.Is(err, ErrStale) || !reflect.DeepEqual(d, CollectionDetail{}) {
		t.Fatalf("foreign cursor released a page %+v %v", d, err)
	}
}

func TestRSCollectionCompleteAllHiddenAndEmpty(t *testing.T) {
	f, r := newRSCollectionCompleteFixture(t, nil)
	// A restricted viewer hides every live member; the collection itself is
	// logically nonempty and therefore must not be delivered as an empty page.
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
	f.exec(t, `UPDATE catalog_entities SET admission_own_age=18 WHERE id<=201`)
	p := rsBeginCompleteCollection(t, f, r)
	rsCompleteAuthored(t, f, p)
	if !p.nonempty || len(p.nodes) != 0 {
		t.Fatalf("logical membership confused with visible membership %v %d", p.nonempty, len(p.nodes))
	}
	d, _, err := rsAuthoredDetail(t, f, p, nil)
	if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(d, CollectionDetail{}) {
		t.Fatalf("all-hidden collection escaped %+v %v", d, err)
	}
	// A genuinely empty collection is permitted and reports an exact zero.
	f.exec(t, `DELETE FROM profile_restrictions WHERE profile_id='p'`)
	f.exec(t, `DELETE FROM catalog_collection_members WHERE collection_id=1101`)
	p = rsBeginCompleteCollection(t, f, r)
	rsCompleteAuthored(t, f, p)
	d, _, err = rsAuthoredDetail(t, f, p, nil)
	if err != nil || p.nonempty || d.ExactCount != 0 || !d.Exhausted || len(d.Items) != 0 {
		t.Fatalf("empty detail %+v %v", d, err)
	}
}

func TestRSCollectionCompleteCountsOnlyFifthMember(t *testing.T) {
	f, r := newRSCollectionCompleteFixture(t, func(f *browseFixture) {
		f.exec(t, `DELETE FROM catalog_collection_members WHERE collection_id=1101`)
		f.exec(t, `INSERT INTO catalog_collection_members(collection_id,item_id,order_key) VALUES(1101,1,'a'),(1101,2,'b'),(1101,4,'c'),(1101,5,'d'),(1101,9,'e')`)
	})
	// The root's own counts-only tuple claims five accepted members while only
	// four authored positions are currently visible.
	f.exec(t, `UPDATE catalog_entities SET admission_member_count=5,admission_unrated_members=0,admission_min_known_age=5,admission_allow_age=-1,admission_known_age=-1 WHERE id=1101`)
	p := rsBeginCompleteCollection(t, f, r)
	rsCompleteAuthored(t, f, p)
	d, _, err := rsAuthoredDetail(t, f, p, nil)
	if err != nil || d.ExactCount != 4 || len(d.Items) != 4 {
		t.Fatalf("counts-only root certified membership %+v %v", d, err)
	}
	var ids []int64
	for _, item := range d.Items {
		ids = append(ids, item.ID)
	}
	if !slices.Equal(ids, []int64{1, 2, 4, 5}) {
		t.Fatal("authored order", ids)
	}
	// The fifth logical position is real but hidden; dropping it does not let the
	// stale root counter reappear as a member or an exact count.
	f.exec(t, `DELETE FROM catalog_collection_members WHERE collection_id=1101 AND item_id=9`)
	p = rsBeginCompleteCollection(t, f, r)
	rsCompleteAuthored(t, f, p)
	d, _, err = rsAuthoredDetail(t, f, p, nil)
	if err != nil || d.ExactCount != 4 || len(d.Items) != 4 {
		t.Fatalf("counts-only tuple became membership %+v %v", d, err)
	}
}

func TestRSCollectionCompleteSparseContinuationAndDrift(t *testing.T) {
	f, r := newRSCollectionCompleteFixture(t, nil)
	f.input.Limit = 2
	// Only every sixty-seventh position is currently visible; every hidden
	// position still consumes a raw position before the complete count is ready.
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id<=201 AND id%67<>0`)
	p := rsBeginCompleteCollection(t, f, r)
	rsCompleteAuthored(t, f, p)
	d, _, err := rsAuthoredDetail(t, f, p, nil)
	if err != nil || d.ExactCount != 3 || len(d.Items) != 2 || d.Next == nil {
		t.Fatalf("sparse first page %+v %v", d, err)
	}
	after := d.Next
	// A reordered membership/order source invalidates the private continuation.
	f.exec(t, `UPDATE catalog_collection_members SET order_key='0' WHERE collection_id=1101 AND item_id=67`)
	f.exec(t, `UPDATE catalog_entities SET collection_revision=collection_revision+1 WHERE id=1101`)
	p = rsBeginCompleteCollection(t, f, r)
	rsCompleteAuthored(t, f, p)
	if d, _, err := rsAuthoredLiveDetail(t, f, p, after); !errors.Is(err, ErrStale) || !reflect.DeepEqual(d, CollectionDetail{}) {
		t.Fatalf("reordered membership accepted %+v %v", d, err)
	}
}

func TestRSCollectionCompleteMemberRetirementAndSourceDrift(t *testing.T) {
	f, r := newRSCollectionCompleteFixture(t, nil)
	f.input.Limit = 2
	p := rsBeginCompleteCollection(t, f, r)
	rsCompleteAuthored(t, f, p)
	d, _, err := rsAuthoredDetail(t, f, p, nil)
	if err != nil || len(d.Items) != 2 || d.Next == nil {
		t.Fatalf("first page %+v %v", d, err)
	}
	after := d.Next
	// Retirement of an already-enumerated member invalidates the exact delivery.
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=1`)
	p = rsBeginCompleteCollection(t, f, r)
	rsCompleteAuthored(t, f, p)
	if d, _, err := rsAuthoredLiveDetail(t, f, p, after); !errors.Is(err, ErrStale) || !reflect.DeepEqual(d, CollectionDetail{}) {
		t.Fatalf("retired member accepted %+v %v", d, err)
	}
	f.exec(t, `UPDATE catalog_entities SET retired=0 WHERE id=1`)
	// A changed current source/root revision is a stale continuation.
	p = rsBeginCompleteCollection(t, f, r)
	rsCompleteAuthored(t, f, p)
	d, _, err = rsAuthoredDetail(t, f, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	after = d.Next
	f.exec(t, `UPDATE catalog_entities SET metadata_incarnation=randomblob(16) WHERE id=1101`)
	p = rsBeginCompleteCollection(t, f, r)
	rsCompleteAuthored(t, f, p)
	if d, _, err := rsAuthoredLiveDetail(t, f, p, after); !errors.Is(err, ErrStale) || !reflect.DeepEqual(d, CollectionDetail{}) {
		t.Fatalf("source drift accepted %+v %v", d, err)
	}
	// An active aggregate publication must still match the accepted tuple.
	f.exec(t, `INSERT INTO catalog_aggregate_operations(id,library_id,source_kind,source_entity_id,source_incarnation,source_revision,phase) VALUES(1,1,'entity',1101,'i','r','complete')`)
	f.exec(t, `INSERT INTO catalog_aggregate_publications(id,operation_id,parent_id,captured_revision,generation,phase,duration_key,community_rating_key,critic_rating_key,file_size_key,episode_count,latest_aired_key,admission_member_count,admission_unrated_members,admission_min_known_age,admission_allow_age,admission_known_age) VALUES(1,1,1101,1,1,'published',0,0,0,0,0,'',5,0,5,-1,-1)`)
	f.exec(t, `UPDATE catalog_entities SET aggregate_publication_id=1,admission_member_count=5,admission_unrated_members=0,admission_min_known_age=5,admission_allow_age=-1,admission_known_age=-1 WHERE id=1101`)
	p = rsBeginCompleteCollection(t, f, r)
	rsCompleteAuthored(t, f, p)
	if _, _, err := rsAuthoredDetail(t, f, p, nil); err != nil {
		t.Fatal("matching publication refused", err)
	}
	p = rsBeginCompleteCollection(t, f, r)
	rsCompleteAuthored(t, f, p)
	f.exec(t, `UPDATE catalog_aggregate_publications SET admission_member_count=6 WHERE id=1`)
	if d, _, err := rsAuthoredDetail(t, f, p, nil); !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(d, CollectionDetail{}) {
		t.Fatalf("changed publication released values %+v %v", d, err)
	}
}

func TestRSCollectionCompletePolicyRequiresOwnedCompleteWork(t *testing.T) {
	f, r := newRSCollectionCompleteFixture(t, nil)
	for _, in := range []BrowseInput{
		{Kind: 10, policy: readPolicyBinding{recipe: rsCollectionComputePolicy}},
		{Kind: 10, policy: readPolicyBinding{recipe: rsCollectionDeliveryPolicy, subject: 1101}},
		{Kind: 10, policy: readPolicyBinding{recipe: 0, state: &CollectionPreparation{}}},
	} {
		if _, err := resolveReadPolicy(in); !errors.Is(err, ErrInput) {
			t.Fatalf("default/incomplete work accepted %+v", in)
		}
	}
	p := rsBeginCompleteCollection(t, f, r)
	if _, _, err := rsAuthoredDetail(t, f, p, nil); !errors.Is(err, ErrInput) {
		t.Fatalf("pending delivery accepted: %v", err)
	}
	rsCompleteAuthored(t, f, p)
	// Private work integrity is checked again at delivery, not merely at Ready.
	p.nodes[0].id = 999
	if d, _, err := rsAuthoredDetail(t, f, p, nil); !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(d, CollectionDetail{}) {
		t.Fatalf("changed ready tree accepted %+v %v", d, err)
	}
}

func TestRSCollectionCompleteLeaseOwnership(t *testing.T) {
	f, r := newRSCollectionCompleteFixture(t, nil)
	p := rsBeginCompleteCollection(t, f, r)
	rsCompleteAuthored(t, f, p)
	if err := f.authority.CheckPreparation(p.lease); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.authority.CheckPreparation(p.lease); err == nil {
		t.Fatal("standalone close did not retire its lease")
	}
	// A borrowed child shares the enclosing lease. It computes and reduces under
	// it, but cannot deliver, hand off or retire its parent's reservation.
	parent := rsBeginCompleteCollection(t, f, r)
	child := &CollectionPreparation{borrowed: true, started: parent.started, owner: parent.owner, reader: r, authority: parent.authority, lease: parent.lease, in: CollectionInput{View: parent.in.View, Collection: 1101}, nodes: make([]rsCollectionNode, 0, 1024)}
	child.in.View.policy = readPolicyBinding{recipe: rsCollectionComputePolicy, subject: 1101, state: child}
	rsCompleteAuthored(t, f, child)
	if !child.ready || len(child.nodes) == 0 {
		t.Fatal("borrowed reduction did not complete")
	}
	if _, _, err := rsAuthoredDetail(t, f, child, nil); !errors.Is(err, ErrInput) {
		t.Fatalf("borrowed child delivered its parent's values: %v", err)
	}
	if _, _, err := child.Artwork(t.Context(), foregroundRequest(), time.Second, f.output, "poster", true); !errors.Is(err, ErrInput) {
		t.Fatalf("borrowed child delivered parent art: %v", err)
	}
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.authority.CheckPreparation(parent.lease); err != nil {
		t.Fatalf("borrowed close retired the enclosing lease: %v", err)
	}
	if err := f.authority.ReadyPreparation(parent.lease); err != nil {
		t.Fatalf("parent lease no longer ready-capable: %v", err)
	}
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRSCollectionCompleteLIVEAndArtwork(t *testing.T) {
	f, r := newRSCollectionCompleteLiveFixture(t, nil)
	p := rsBeginCompleteCollection(t, f, r)
	rsCompleteAuthoredLive(t, f, p)
	if live := f.authority.ForegroundUsage().Live; live != 0 {
		t.Fatalf("ready preparation kept consumer custody: %d", live)
	}
	// Public delivery and art require their own admitted consumer.
	d, cost, err := rsAuthoredLiveDetail(t, f, p, nil)
	if err != nil || d.ExactCount != 179 || len(d.Items) != 40 {
		t.Fatalf("public detail %+v %v", d, err)
	}
	for _, w := range cost.LookupWindows {
		if w.Ended.Sub(w.Started) > time.Second {
			t.Fatal("long snapshot")
		}
	}
	assertLiveUsage(t, f, 0, 0)
	p = rsBeginCompleteCollection(t, f, r)
	rsCompleteAuthoredLive(t, f, p)
	q, request, output := liveRequests("rs-collection-complete-art")
	var fact ArtworkFact
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		fact, _, e = p.Artwork(ctx, request, time.Second, out, "poster", true)
		return e
	})
	if err != nil || !fact.Present || fact.Owner != 1101 || fact.Role != "poster" || fact.Digest != rsArtDigest(1) {
		t.Fatalf("public collection art %+v %v", fact, err)
	}
	assertLiveUsage(t, f, 0, 0)
	// A revoked final viewer wins over already-copied ready values.
	p = rsBeginCompleteCollection(t, f, r)
	rsCompleteAuthoredLive(t, f, p)
	f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
	if d, _, err := rsAuthoredLiveDetail(t, f, p, nil); !errors.Is(err, ErrDenied) || !reflect.DeepEqual(d, CollectionDetail{}) {
		t.Fatalf("revoked ready values escaped %+v %v", d, err)
	}
	assertLiveUsage(t, f, 0, 0)
}

func TestRSCollectionCompleteAdvanceExpiryAndCancellation(t *testing.T) {
	f, r := newRSCollectionCompleteFixture(t, nil)
	p := rsBeginCompleteCollection(t, f, r)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if step, _, err := p.Advance(ctx, foregroundRequest(), time.Second, BrowseOutput{}); !errors.Is(err, ErrInput) || step.Ready {
		t.Fatalf("unadmitted advance %+v %v", step, err)
	}
	deadline := p.Deadline()
	if _, _, err := p.advance(t.Context(), foregroundRequest(), time.Second, f.output, f.input.Now); err != nil {
		t.Fatal(err)
	}
	if !p.Deadline().Equal(deadline) {
		t.Fatal("poll renewed the preparation deadline")
	}
	if err := f.authority.InterruptPreparations(governor.PublicationFootprint{{Library: 1}}); err != nil {
		t.Fatal(err)
	}
	if step, _, err := p.advance(t.Context(), foregroundRequest(), time.Second, f.output, f.input.Now); !errors.Is(err, governor.ErrPreparationStale) || step.Ready || !p.closed || p.nodes != nil {
		t.Fatalf("interrupted work returned exact values %+v %v", step, err)
	}
}
