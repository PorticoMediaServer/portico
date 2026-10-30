package identity

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func issueTopShelfForTest(t *testing.T, s *Service, session Envelope) (TopShelfGrant, string) {
	t.Helper()
	p, err := s.Authenticate(session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	installation := Token()
	device, err := s.RegisterDevice(t.Context(), session.AccessToken, registration(installation), "")
	if err != nil {
		t.Fatal(err)
	}
	grant, err := s.IssueTopShelfToken(t.Context(), p, installation, 0)
	if err != nil {
		t.Fatal(err)
	}
	var storedEpoch, liveEpoch, storedPIN, livePIN int
	if err := s.db.QueryRow(`SELECT t.account_epoch,a.epoch,t.pin_revision,p.pin_revision
 FROM topshelf_tokens t JOIN accounts a ON a.id=t.account_id
 JOIN direct_profiles p ON p.account_id=t.account_id AND p.id=t.profile_id
 WHERE t.token_hash=?`, Digest(grant.Token)).Scan(&storedEpoch, &liveEpoch, &storedPIN, &livePIN); err != nil {
		t.Fatal(err)
	}
	if storedEpoch != liveEpoch || storedPIN != livePIN || grant.Epoch != liveEpoch {
		t.Fatalf("grant did not capture issuance state: epoch=%d/%d PIN=%d/%d", storedEpoch, liveEpoch, storedPIN, livePIN)
	}
	return grant, device.ID
}

func TestTopShelfArtworkCapabilityBindsRealGrant(t *testing.T) {
	s, _, login := directFixture(t)
	feed, device := issueTopShelfForTest(t, s, *login.Session)
	capability, err := s.TopShelfArtworkCapability(feed.Token, "item")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := s.ResolveTopShelfArtwork(t.Context(), capability, "item")
	if err != nil || resolved.Viewer != feed.Viewer || resolved.Hash != Digest(feed.Token) || resolved.Token != "" {
		t.Fatalf("valid item capability: %v", err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(capability)
	if err != nil || len(decoded) <= sha256.Size {
		t.Fatal("capability encoding is invalid", err)
	}
	if bytes.Contains(decoded, []byte(feed.Token)) {
		t.Fatal("decoded image URL contains the reusable feed bearer")
	}
	if _, err = s.TopShelfGrant(t.Context(), capability); !errors.Is(err, ErrTopShelfToken) {
		t.Fatal("image capability became a feed credential", err)
	}
	if _, err = s.TopShelfGrant(t.Context(), Digest(feed.Token)); !errors.Is(err, ErrTopShelfToken) {
		t.Fatal("disclosed token digest became a feed credential", err)
	}
	if _, err = s.ResolveTopShelfArtwork(t.Context(), capability, "other"); !errors.Is(err, ErrTopShelfToken) {
		t.Fatal("image capability widened to another item", err)
	}
	var original topShelfArtworkGrant
	if err = json.Unmarshal(decoded[:len(decoded)-sha256.Size], &original); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"item", "expiry", "token", "version"} {
		t.Run(field+" tampering", func(t *testing.T) {
			payload := original
			item := "item"
			switch field {
			case "item":
				payload.Item, item = "other", "other"
			case "expiry":
				payload.Expires += 3600
			case "token":
				payload.TokenHash = Digest(Token())
			case "version":
				payload.Version++
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			forged := base64.RawURLEncoding.EncodeToString(append(raw, decoded[len(decoded)-sha256.Size:]...))
			if _, err = s.ResolveTopShelfArtwork(t.Context(), forged, item); !errors.Is(err, ErrTopShelfToken) {
				t.Fatal("changed capability was accepted", err)
			}
		})
	}
	// A valid MAC cannot extend an expired capability's lifetime.
	expired := original
	expired.Expires = time.Now().Add(-time.Second).Unix()
	raw, err := json.Marshal(expired)
	if err != nil {
		t.Fatal(err)
	}
	old := base64.RawURLEncoding.EncodeToString(append(raw, s.topShelfArtworkMAC(raw)...))
	if _, err = s.ResolveTopShelfArtwork(t.Context(), old, "item"); !errors.Is(err, ErrTopShelfToken) {
		t.Fatal("expired signed capability was accepted", err)
	}
	if err = s.ForgetDevice(t.Context(), login.AccountToken, device); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveTopShelfArtwork(t.Context(), capability, "item"); !errors.Is(err, ErrTopShelfToken) {
		t.Fatal("image capability survived device revocation", err)
	}
}

func TestTopShelfGrantsRetireOnBothPINChangePaths(t *testing.T) {
	for _, reset := range []bool{true, false} {
		name := "edit"
		if reset {
			name = "reset"
		}
		t.Run(name, func(t *testing.T) {
			s, login, profile := profileWithPIN(t)
			selected, err := s.SelectDirectProfile(t.Context(), login.AccountToken, profile.ID, DirectSelection{PIN: "4821"})
			if err != nil {
				t.Fatal(err)
			}
			feed, device := issueTopShelfForTest(t, s, selected.Session)
			var issuedPIN int
			if err := s.db.QueryRow(`SELECT pin_revision FROM topshelf_tokens WHERE token_hash=?`, Digest(feed.Token)).Scan(&issuedPIN); err != nil {
				t.Fatal(err)
			}
			other, _ := issueTopShelfForTest(t, s, *login.Session)
			capability, err := s.TopShelfArtworkCapability(feed.Token, "item")
			if err != nil {
				t.Fatal(err)
			}
			if reset {
				_, err = s.PINReset(t.Context(), login.AccountToken, profile.ID, "")
			} else {
				pin := "7392"
				_, err = s.EditDirectProfile(t.Context(), login.AccountToken, profile.ID, DirectProfileEdit{ExpectedRevision: profile.Revision, PIN: &pin})
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.TopShelfGrant(t.Context(), feed.Token); !errors.Is(err, ErrTopShelfToken) {
				t.Fatal("old PIN feed grant survived", err)
			}
			if _, err = s.ResolveTopShelfArtwork(t.Context(), capability, "item"); !errors.Is(err, ErrTopShelfToken) {
				t.Fatal("old PIN artwork grant survived", err)
			}
			// Restore the retired row to prove issuance binding independently of
			// the proactive deletion path, for both feeds and artwork.
			if _, err := s.db.Exec(`INSERT INTO topshelf_tokens(token_hash,device_id,authority,account_id,profile_id,expires_at,account_epoch,pin_revision) VALUES(?,?,?,?,?,?,?,?)`,
				Digest(feed.Token), device, feed.Viewer.Authority, feed.Viewer.AccountID, feed.Viewer.ProfileID, feed.ExpiresAt, feed.Epoch, issuedPIN); err != nil {
				t.Fatal(err)
			}
			if _, err = s.TopShelfGrant(t.Context(), feed.Token); !errors.Is(err, ErrTopShelfToken) {
				t.Fatal("stale PIN revision in a stored feed was accepted", err)
			}
			if _, err = s.ResolveTopShelfArtwork(t.Context(), capability, "item"); !errors.Is(err, ErrTopShelfToken) {
				t.Fatal("stale PIN revision in a stored artwork grant was accepted", err)
			}
			if _, err = s.TopShelfGrant(t.Context(), other.Token); err != nil {
				t.Fatal("unrelated profile feed was revoked", err)
			}
		})
	}
}

func TestTopShelfPasswordRevocationKeepsOrdinaryLogoutPolicy(t *testing.T) {
	s, _, login := directFixture(t)
	feed, _ := issueTopShelfForTest(t, s, *login.Session)
	if err := s.LogoutToken(t.Context(), login.Session.AccessToken); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TopShelfGrant(t.Context(), feed.Token); err != nil {
		t.Fatal("ordinary logout changed the feed lifetime", err)
	}
	fresh, err := s.DirectLogin(t.Context(), "owner", "Testing1!")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ChangeDirectPassword(t.Context(), fresh.AccountToken, "Testing1!", "Changed-password1!", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.TopShelfGrant(t.Context(), feed.Token); !errors.Is(err, ErrTopShelfToken) {
		t.Fatal("password change left the old feed valid", err)
	}
}

func TestTopShelfMemberPasswordRecoveryRevokesFeed(t *testing.T) {
	s, owner, member, login := memberWithSession(t)
	feed, _ := issueTopShelfForTest(t, s, *login.Session)
	if _, err := s.MakeMemberResetCode(t.Context(), owner.AccountToken, member.ID, MemberResetRequest{ExpectedRevision: member.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TopShelfGrant(t.Context(), feed.Token); !errors.Is(err, ErrTopShelfToken) {
		t.Fatal("password recovery left the old feed valid", err)
	}
}

func TestTopShelfMemberReenableCannotReviveFeed(t *testing.T) {
	s, owner, member, login := memberWithSession(t)
	feed, _ := issueTopShelfForTest(t, s, *login.Session)
	other, _ := issueTopShelfForTest(t, s, *owner.Session)
	capability, err := s.TopShelfArtworkCapability(feed.Token, "item")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateDirectMember(t.Context(), owner.AccountToken, member.ID, DirectMemberInput{ExpectedRevision: member.Revision, AllowedLibraries: []string{"movies"}, Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.TopShelfGrant(t.Context(), feed.Token); !errors.Is(err, ErrTopShelfToken) {
		t.Fatal("disabled account retained its feed", err)
	}
	if err = s.UpdateDirectMember(t.Context(), owner.AccountToken, member.ID, DirectMemberInput{ExpectedRevision: member.Revision + 1, AllowedLibraries: []string{"movies"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.TopShelfGrant(t.Context(), feed.Token); !errors.Is(err, ErrTopShelfToken) {
		t.Fatal("re-enabling an account revived its old feed", err)
	}
	if _, err = s.ResolveTopShelfArtwork(t.Context(), capability, "item"); !errors.Is(err, ErrTopShelfToken) {
		t.Fatal("re-enabling an account revived its old artwork capability", err)
	}
	if _, err = s.TopShelfGrant(t.Context(), other.Token); err != nil {
		t.Fatal("membership removal revoked another account's feed", err)
	}
	if _, err = s.DirectLogin(t.Context(), "member", "Testing2!"); err != nil {
		t.Fatal("re-enabled member cannot sign in again", err)
	}
}

func TestTopShelfIssuanceEpochRejectsFeedAfterSignOutEverywhere(t *testing.T) {
	s, owner, member, login := memberWithSession(t)
	feed, device := issueTopShelfForTest(t, s, *login.Session)
	other, _ := issueTopShelfForTest(t, s, *owner.Session)
	capability, err := s.TopShelfArtworkCapability(feed.Token, "item")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SignOutEverywhere(t.Context(), login.AccountToken); err != nil {
		t.Fatal(err)
	}
	var epoch, storedEpoch int
	if err := s.db.QueryRow(`SELECT a.epoch,t.account_epoch FROM accounts a JOIN topshelf_tokens t ON t.account_id=a.id WHERE a.id=? AND t.token_hash=?`, member.ID, Digest(feed.Token)).Scan(&epoch, &storedEpoch); err != nil {
		t.Fatal("epoch path must retain the stale row to exercise issuance comparison", err)
	}
	if epoch <= storedEpoch || storedEpoch != feed.Epoch {
		t.Fatalf("account epoch did not advance past issuance: %d <= %d", epoch, storedEpoch)
	}
	var present bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM identity_devices WHERE id=?)`, device).Scan(&present); err != nil || !present {
		t.Fatalf("device must remain registered for epoch comparison: %v %v", present, err)
	}
	if _, err := s.TopShelfGrant(t.Context(), feed.Token); !errors.Is(err, ErrTopShelfToken) {
		t.Fatal("feed survived its issuing account epoch", err)
	}
	if _, err := s.ResolveTopShelfArtwork(t.Context(), capability, "item"); !errors.Is(err, ErrTopShelfToken) {
		t.Fatal("artwork survived its issuing account epoch", err)
	}
	if _, err := s.TopShelfGrant(t.Context(), other.Token); err != nil {
		t.Fatal("another account's feed was revoked", err)
	}
}
