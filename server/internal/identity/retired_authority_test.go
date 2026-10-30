package identity

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetiredSessionAuthorityIsAlwaysRefused(t *testing.T) {
	s, db, _ := familyFixture(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx := context.Background()
	for _, horizon := range []time.Time{{}, time.Now().Add(time.Hour)} {
		if _, err = s.IssueTx(ctx, tx, "account", "profile", "hosted", "owner", 1, horizon); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("retired authority issued", err)
		}
	}
	p := Principal{Viewer: Viewer{Authority: "hosted", AccountID: "account", ProfileID: "profile", Role: "owner", ServerID: s.ID()}}
	if _, err = s.ViewerRestrictions(ctx, p); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("retired authority got open content restrictions", err)
	}
	if err = s.WithProfileRestrictions(ctx, p, func(ProfileRestrictions) error { t.Fatal("retired authority reached feature policy"); return nil }); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	if _, err = RestrictionsForViewerTx(ctx, tx, p.Viewer); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("retired transaction got open restrictions", err)
	}
}

func TestSecondaryProfileCannotManageAccountDevices(t *testing.T) {
	ctx := context.Background()
	s, _, login := directFixture(t)
	child, err := s.CreateDirectProfile(ctx, login.AccountToken, "Child", "")
	if err != nil {
		t.Fatal(err)
	}
	selected, err := s.SelectDirectProfile(ctx, login.AccountToken, childProfile(t, child).ID, DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	token := selected.Session.AccessToken
	if _, err = s.Devices(ctx, token, ""); !errors.Is(err, ErrForbidden) {
		t.Fatal("secondary profile saw account devices", err)
	}
	if _, err = s.EditDevice(ctx, token, login.DeviceID, DeviceEdit{Name: func() *string { v := "TV"; return &v }()}); !errors.Is(err, ErrForbidden) {
		t.Fatal("secondary profile edited account device", err)
	}
	if _, err = s.RememberBrowserAccount(ctx, token, Token(), false); !errors.Is(err, ErrForbidden) {
		t.Fatal("secondary profile changed remembered account", err)
	}
	if err = s.ForgetBrowserAccount(ctx, token, Token(), login.Account.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("secondary profile forgot account", err)
	}
}
