package remotesources

import (
	"context"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func observationOwner(t *testing.T, f observationFixture) (*identity.Service, identity.Principal, string) {
	t.Helper()
	identities, err := identity.New(f.s.db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',X'00','profile',1)`); err != nil {
		t.Fatal(err)
	}
	envelope, err := identities.Issue("account", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := identities.Authenticate(envelope.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	return identities, owner, envelope.SessionFamilyID
}

func TestAbsentReadProfileUsesListAndNeverOnce(t *testing.T) {
	f := newObservationFixture(t)
	gated, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer gated.Rollback()
	p, err := SourceReadProfileTx(context.Background(), gated.Tx(), "remote")
	if err != nil || p.Saved || p.Revision != 0 || p.ScanDepth != "file_list_only" || p.WholeFileReads || p.Limits != (ReadLimits{Connections: 2, RequestsPerSecond: 2}) {
		t.Fatal(p, err)
	}
	if _, err = SourceReadProfileTx(context.Background(), gated.Tx(), "unknown"); err == nil {
		t.Fatal("unknown source treated as a missing profile")
	}
}

func TestReadProfileOwnerCASAndLibraryRevocation(t *testing.T) {
	ctx := context.Background()
	f := newObservationFixture(t)
	// The checked identity fixture's public Issue/Authenticate path supplies a
	// real current-token family. No role-only or injected allow callback.
	identities, owner, family := observationOwner(t, f)
	apply := func(p SourceReadProfile, revision int64, actor identity.Principal) (SourceReadProfileResult, error) {
		gated, e := dbwork.Begin(ctx, f.s.db, dbwork.ClassBackgroundMedia)
		if e != nil {
			t.Fatal(e)
		}
		defer gated.Rollback()
		out, e := f.s.ApplySourceReadProfileTx(ctx, gated.Tx(), identities, actor, SourceReadProfileChange{SourceID: "remote", Generation: "1", ExpectedRevision: revision, Profile: p})
		if e == nil {
			e = gated.Commit()
		}
		return out, e
	}
	complete, _ := SourceReadPreset("everything")
	forged := owner
	forged.Epoch++
	if _, err := apply(complete, 0, forged); err == nil {
		t.Fatal("forged current-token claims accepted")
	}
	first, err := apply(complete, 0, owner)
	if err != nil || !first.Profile.Saved || first.Profile.Revision != 1 || len(first.AffectedLibraries) != 1 || first.AffectedLibraries[0] != f.library {
		t.Fatal(first, err)
	}
	policy, err := catalog.New(f.s.db).ScanPolicy(ctx, f.library)
	if err != nil || policy.Revision != 2 {
		t.Fatal("existing policy fence was not advanced", policy, err)
	}
	if _, err = apply(complete, 0, owner); !errors.Is(err, storage.ErrRemoteChanged) {
		t.Fatal("stale create accepted", err)
	}
	same, err := apply(complete, 1, owner)
	if err != nil || same.Profile.Revision != 1 || len(same.AffectedLibraries) != 0 {
		t.Fatal("identical profile revoked again", same, err)
	}
	list, _ := SourceReadPreset("list")
	second, err := apply(list, 1, owner)
	if err != nil || second.Profile.Revision != 2 {
		t.Fatal(second, err)
	}
	third, err := apply(complete, 2, owner)
	if err != nil || third.Profile.Revision != 3 {
		t.Fatal("A-to-B-to-A revived old revision", third, err)
	}
	if _, err = f.s.db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=?`, family); err != nil {
		t.Fatal(err)
	}
	if _, err = apply(list, 3, owner); err == nil {
		t.Fatal("revoked owner wrote source policy")
	}
}

func TestReadProfileRefusesMissingOwnerAndUnknownCeilings(t *testing.T) {
	f := newObservationFixture(t)
	gated, err := dbwork.Begin(context.Background(), f.s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	defer gated.Rollback()
	complete, _ := SourceReadPreset("everything")
	_, err = f.s.ApplySourceReadProfileTx(context.Background(), gated.Tx(), nil, identity.Principal{}, SourceReadProfileChange{SourceID: "remote", Generation: "1", Profile: complete})
	if !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatal("missing real owner authority accepted", err)
	}
	if _, err = SourceReadPreset("custom"); err == nil {
		t.Fatal("unknown/custom selection silently filled with defaults")
	}
	for _, p := range []SourceReadProfile{
		{ScanDepth: "unknown", Limits: ReadLimits{Connections: 2, RequestsPerSecond: 2}},
		{ScanDepth: "complete"},
		{ScanDepth: "complete", Limits: ReadLimits{Connections: 2, RequestsPerSecond: 2, BytesPerSecond: -1}},
	} {
		if validSourceReadProfile(p) {
			t.Fatal("unknown authority accepted", p)
		}
	}
}
