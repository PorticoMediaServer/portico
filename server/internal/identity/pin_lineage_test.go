package identity

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func familyLineageForTest(t *testing.T, s *Service, family string) string {
	t.Helper()
	var lineage string
	if err := s.db.QueryRow(`SELECT lineage_id FROM identity_family_pin_lineages WHERE family_id=?`, family).Scan(&lineage); err != nil {
		t.Fatalf("family %s has no PIN lineage: %v", family, err)
	}
	return lineage
}

func pinDebtForTest(t *testing.T, s *Service, profile, lineage string) int {
	t.Helper()
	var attempts int
	if err := s.db.QueryRow(`SELECT COALESCE((SELECT failed_attempts FROM direct_profile_pin_attempts WHERE profile_id=? AND lineage_id=?),0)`, profile, lineage).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	return attempts
}

func clearPINDelayForTest(t *testing.T, s *Service, profile string) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE direct_profile_pin_attempts SET locked_until_ms=0 WHERE profile_id=?`, profile); err != nil {
		t.Fatal(err)
	}
}

// holdPINDelayForTest keeps a recorded delay running however slow the host is.
func holdPINDelayForTest(t *testing.T, s *Service, profile string) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE direct_profile_pin_attempts SET locked_until_ms=4102444800000 WHERE profile_id=?`, profile); err != nil {
		t.Fatal(err)
	}
}

func collectQuickForTest(t *testing.T, s *Service, bearer, requestID string) Envelope {
	t.Helper()
	code, id := approveQuickForTest(t, s, bearer, requestID)
	if _, err := s.db.Exec(`UPDATE quick_connect_v1 SET next_poll=0 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	grant, err := s.PollQuick(t.Context(), code.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

// A device paired through Quick Connect, and one paired from that device, keep
// the approving sign-in's PIN debt: pairing never buys a fresh guess budget.
func TestQuickPairingContinuesTheApproversPINLineage(t *testing.T) {
	s, login, child := profileWithPIN(t)
	origin := familyLineageForTest(t, s, login.Session.SessionFamilyID)
	if _, err := s.SelectDirectProfile(t.Context(), login.AccountToken, child.ID, DirectSelection{PIN: "0000"}); !errors.Is(err, ErrProfilePIN) {
		t.Fatal(err)
	}
	paired := collectQuickForTest(t, s, login.Session.AccessToken, "33333333-3333-4333-8333-333333333333")
	if paired.DeviceID == login.DeviceID {
		t.Fatal("the paired television reused the approving device")
	}
	if got := familyLineageForTest(t, s, paired.SessionFamilyID); got != origin {
		t.Fatalf("paired device started lineage %s, not the approver's %s", got, origin)
	}
	// The approver's delay is still running: the paired device is refused too.
	holdPINDelayForTest(t, s, child.ID)
	if _, err := s.SelectDirectProfile(t.Context(), paired.AccessToken, child.ID, DirectSelection{PIN: "4821"}); !errors.Is(err, ErrProfileLocked) {
		t.Fatalf("paired device escaped the approver's delay: %v", err)
	}
	clearPINDelayForTest(t, s, child.ID)
	if _, err := s.SelectDirectProfile(t.Context(), paired.AccessToken, child.ID, DirectSelection{PIN: "1111"}); !errors.Is(err, ErrProfilePIN) {
		t.Fatal(err)
	}
	if debt := pinDebtForTest(t, s, child.ID, origin); debt != 2 {
		t.Fatalf("paired device's attempt was not added to the shared debt: %d", debt)
	}
	grandchild := collectQuickForTest(t, s, paired.AccessToken, "44444444-4444-4444-8444-444444444444")
	if got := familyLineageForTest(t, s, grandchild.SessionFamilyID); got != origin {
		t.Fatalf("a device paired from a paired device started lineage %s", got)
	}
	clearPINDelayForTest(t, s, child.ID)
	if _, err := s.SelectDirectProfile(t.Context(), grandchild.AccessToken, child.ID, DirectSelection{PIN: "2222"}); !errors.Is(err, ErrProfilePIN) {
		t.Fatal(err)
	}
	if debt := pinDebtForTest(t, s, child.ID, origin); debt != 3 {
		t.Fatalf("grandchild's attempt was not added to the shared debt: %d", debt)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT count(*) FROM direct_profile_pin_attempts WHERE profile_id=?`, child.ID).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("PIN debt split across %d rows: %v", rows, err)
	}
	// The profile chosen on the paired device continues the same chain.
	clearPINDelayForTest(t, s, child.ID)
	selected, err := s.SelectDirectProfile(t.Context(), paired.AccessToken, child.ID, DirectSelection{PIN: "4821"})
	if err != nil {
		t.Fatal(err)
	}
	if got := familyLineageForTest(t, s, selected.Session.SessionFamilyID); got != origin {
		t.Fatalf("a profile chosen on a paired device started lineage %s", got)
	}
}

// A paired Cast screen and an access token continue their maker's lineage; one
// with no live parent session gets none.
func TestCastScreensAndAccessTokensContinueTheLineage(t *testing.T) {
	ctx := context.Background()
	s, login, _ := profileWithPIN(t)
	origin := familyLineageForTest(t, s, login.Session.SessionFamilyID)
	paired := collectQuickForTest(t, s, login.Session.AccessToken, "abababab-abab-4bab-8bab-abababababab")
	maker, err := s.Authenticate(paired.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	var token IssuedCredential
	var screen Envelope
	err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		var e error
		if token, e = s.IssueAPICredentialTx(ctx, tx, maker, CredentialKindToken, Token(), "script", time.Time{}); e != nil {
			return e
		}
		screen, e = s.IssueCastTargetTx(ctx, tx, "local", maker.AccountID, maker.ProfileID, maker.Epoch, time.Time{}, paired.SessionFamilyID)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := familyLineageForTest(t, s, token.FamilyID); got != origin {
		t.Fatalf("access token started lineage %s", got)
	}
	if got := familyLineageForTest(t, s, screen.SessionFamilyID); got != origin {
		t.Fatalf("Cast screen started lineage %s", got)
	}
	sessionless := maker
	sessionless.Hash = ""
	err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		_, e := s.IssueAPICredentialTx(ctx, tx, sessionless, CredentialKindToken, Token(), "script", time.Time{})
		return e
	})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a credential with no maker session got a lineage of its own: %v", err)
	}
	err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		_, e := s.IssueCastTargetTx(ctx, tx, "local", maker.AccountID, maker.ProfileID, maker.Epoch, time.Time{}, "missing-family")
		return e
	})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a Cast screen with no pairing session was issued: %v", err)
	}
}

// Choosing a profile and refreshing a session keep the family's lineage; a
// second password sign-in on the same installation reuses that device's own
// lineage, and one on another installation starts its own.
func TestProfileSelectionRefreshAndSignInKeepTheirLineage(t *testing.T) {
	ctx := context.Background()
	s, _, child := profileWithPIN(t)
	signIn := func(installation string) DirectSignIn {
		t.Helper()
		issuing, err := WithIssuingDevice(ctx, DeviceRegistration{InstallationID: installation, Name: "Phone", Platform: "ios", App: "portico", AppVersion: "1.0"}, "")
		if err != nil {
			t.Fatal(err)
		}
		login, err := s.DirectLogin(issuing, "member", "Testing2!")
		if err != nil || login.Session == nil {
			t.Fatalf("sign-in: %v", err)
		}
		return login
	}
	first := signIn("installation-one-0000000000000000")
	origin := familyLineageForTest(t, s, first.Session.SessionFamilyID)
	selected, err := s.SelectDirectProfile(ctx, first.AccountToken, child.ID, DirectSelection{PIN: "4821"})
	if err != nil {
		t.Fatal(err)
	}
	if got := familyLineageForTest(t, s, selected.Session.SessionFamilyID); got != origin {
		t.Fatalf("profile selection started lineage %s", got)
	}
	renewed, err := s.RefreshSession(ctx, selected.Session.RefreshToken, selected.Session.InstallationID, "refresh-request-00000001")
	if err != nil {
		t.Fatal(err)
	}
	if renewed.SessionFamilyID != selected.Session.SessionFamilyID || familyLineageForTest(t, s, renewed.SessionFamilyID) != origin {
		t.Fatal("refresh changed the family's lineage")
	}
	again := signIn("installation-one-0000000000000000")
	if again.DeviceID != first.DeviceID || familyLineageForTest(t, s, again.Session.SessionFamilyID) != origin {
		t.Fatal("a second sign-in on the same device did not reuse its lineage")
	}
	other := signIn("installation-two-0000000000000000")
	if familyLineageForTest(t, s, other.Session.SessionFamilyID) == origin {
		t.Fatal("an independently signed-in device shares another device's lineage")
	}
}

// Forgetting the device a chain began on leaves its descendants' debt, and a
// family whose lineage is gone gets no PIN budget at all.
func TestPINDebtOutlivesTheOriginDevice(t *testing.T) {
	s, login, child := profileWithPIN(t)
	origin := familyLineageForTest(t, s, login.Session.SessionFamilyID)
	paired := collectQuickForTest(t, s, login.Session.AccessToken, "55555555-5555-4555-8555-555555555555")
	if _, err := s.SelectDirectProfile(t.Context(), paired.AccessToken, child.ID, DirectSelection{PIN: "0000"}); !errors.Is(err, ErrProfilePIN) {
		t.Fatal(err)
	}
	if err := s.ForgetDevice(t.Context(), paired.AccessToken, login.DeviceID); err != nil {
		t.Fatal(err)
	}
	if debt := pinDebtForTest(t, s, child.ID, origin); debt != 1 {
		t.Fatalf("forgetting the origin device cleared its chain's debt: %d", debt)
	}
	holdPINDelayForTest(t, s, child.ID)
	if _, err := s.SelectDirectProfile(t.Context(), paired.AccessToken, child.ID, DirectSelection{PIN: "4821"}); !errors.Is(err, ErrProfileLocked) {
		t.Fatalf("debt stopped applying after the origin device went: %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM identity_family_pin_lineages WHERE family_id=?`, paired.SessionFamilyID); err != nil {
		t.Fatal(err)
	}
	clearPINDelayForTest(t, s, child.ID)
	if _, err := s.SelectDirectProfile(t.Context(), paired.AccessToken, child.ID, DirectSelection{PIN: "4821"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a family without a lineage was given a PIN budget: %v", err)
	}
}

// Replaying an approval or a lost collection mints nothing new; an approval
// that carries no lineage is refused rather than starting one.
func TestQuickReplayAndLineagelessApprovals(t *testing.T) {
	s, login, _ := profileWithPIN(t)
	p, err := s.Authenticate(login.Session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	code, id := approveQuickForTest(t, s, login.Session.AccessToken, "66666666-6666-4666-8666-666666666666")
	if err = s.DecideQuick(t.Context(), p, id, code.UserCode, "approve"); err != nil {
		t.Fatalf("approval replay: %v", err)
	}
	if _, err = s.db.Exec(`UPDATE quick_connect_v1 SET next_poll=0 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	first, err := s.PollQuick(t.Context(), code.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	var lineages int
	if err = s.db.QueryRow(`SELECT count(*) FROM identity_pin_lineages`).Scan(&lineages); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE quick_connect_v1 SET next_poll=0 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	again, err := s.PollQuick(t.Context(), code.DeviceCode)
	if err != nil || again.SessionFamilyID != first.SessionFamilyID {
		t.Fatalf("lost-answer recovery issued another session: %v", err)
	}
	var after int
	if err = s.db.QueryRow(`SELECT count(*) FROM identity_pin_lineages`).Scan(&after); err != nil || after != lineages {
		t.Fatalf("collection replay made a lineage: %d → %d, %v", lineages, after, err)
	}

	other, otherID := approveQuickForTest(t, s, login.Session.AccessToken, "77777777-7777-4777-8777-777777777777")
	var pinRevision int64
	if err = s.db.QueryRow(`SELECT pin_revision FROM direct_profiles WHERE id=?`, p.ProfileID).Scan(&pinRevision); err != nil {
		t.Fatal(err)
	}
	stripped, err := s.sealSetup(quickApproval{AccountID: p.AccountID, ProfileID: p.ProfileID, Epoch: p.Epoch, PINRevision: pinRevision}, "quick-approval:"+otherID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE quick_connect_v1 SET grant_cipher=?,next_poll=0 WHERE id=?`, stripped, otherID); err != nil {
		t.Fatal(err)
	}
	_, err = s.PollQuick(t.Context(), other.DeviceCode)
	var protocol *SetupProtocolError
	if !errors.As(err, &protocol) || protocol.Code != "access_denied" {
		t.Fatalf("an approval without a lineage minted a session: %v", err)
	}
}

// A lineage cannot be borrowed across accounts, and a family's lineage is fixed.
func TestPINLineageIsBoundToItsAccountAndFixed(t *testing.T) {
	s, owner, _, member := memberWithSession(t)
	ownerLineage := familyLineageForTest(t, s, owner.Session.SessionFamilyID)
	if _, err := s.db.Exec(`UPDATE identity_family_pin_lineages SET lineage_id=? WHERE family_id=?`, ownerLineage, member.Session.SessionFamilyID); err == nil {
		t.Fatal("a family's lineage was replaced")
	}
	if _, err := s.db.Exec(`DELETE FROM identity_family_pin_lineages WHERE family_id=?`, member.Session.SessionFamilyID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO identity_family_pin_lineages(family_id,lineage_id) VALUES(?,?)`, member.Session.SessionFamilyID, ownerLineage); err == nil {
		t.Fatal("a family adopted another account's lineage")
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = bindPINLineageTx(withPINLineage(t.Context(), ownerLineage), tx, member.Session.Viewer.AccountID, member.DeviceID, member.Session.SessionFamilyID); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("issuance inherited another account's lineage: %v", err)
	}
	if err = bindPINLineageTx(withPINLineage(t.Context(), ""), tx, member.Session.Viewer.AccountID, member.DeviceID, member.Session.SessionFamilyID); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("an empty inherited lineage was accepted: %v", err)
	}
}

// Wrong PINs racing from sibling devices all land on one debt row, and each
// one either counts or is refused by the delay; none is lost.
func TestSiblingDevicesShareOneDebtUnderConcurrency(t *testing.T) {
	s, login, child := profileWithPIN(t)
	origin := familyLineageForTest(t, s, login.Session.SessionFamilyID)
	siblings := []string{
		collectQuickForTest(t, s, login.Session.AccessToken, "88888888-8888-4888-8888-888888888888").AccessToken,
		collectQuickForTest(t, s, login.Session.AccessToken, "99999999-9999-4999-8999-999999999999").AccessToken,
		login.AccountToken,
	}
	var wrong sync.WaitGroup
	var mu sync.Mutex
	counted, refused := 0, 0
	for round := 0; round < 3; round++ {
		clearPINDelayForTest(t, s, child.ID)
		for _, bearer := range siblings {
			wrong.Add(1)
			go func(bearer string) {
				defer wrong.Done()
				_, err := s.SelectDirectProfile(context.Background(), bearer, child.ID, DirectSelection{PIN: "0000"})
				mu.Lock()
				defer mu.Unlock()
				switch {
				case errors.Is(err, ErrProfilePIN):
					counted++
				case errors.Is(err, ErrProfileLocked):
					refused++
				default:
					t.Errorf("unexpected result: %v", err)
				}
			}(bearer)
		}
		wrong.Wait()
	}
	if debt := pinDebtForTest(t, s, child.ID, origin); debt != counted || counted == 0 || counted+refused != 9 {
		t.Fatalf("debt %d, counted %d, refused %d", debt, counted, refused)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT count(*) FROM direct_profile_pin_attempts WHERE profile_id=?`, child.ID).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("sibling debt split across %d rows: %v", rows, err)
	}
}

// Session-history pruning drops lapsed debt and lineages nothing continues,
// and keeps every lineage still in use or still owed.
func TestPruningKeepsLiveLineagesAndDebt(t *testing.T) {
	s, login, child := profileWithPIN(t)
	origin := familyLineageForTest(t, s, login.Session.SessionFamilyID)
	if _, err := s.SelectDirectProfile(t.Context(), login.AccountToken, child.ID, DirectSelection{PIN: "0000"}); !errors.Is(err, ErrProfilePIN) {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	if _, err := s.db.Exec(`UPDATE identity_pin_lineages SET created_at=?`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO identity_pin_lineages(id,account_id,origin_device_id,created_at) SELECT 'pln_unused',account_id,'gone-device',? FROM identity_pin_lineages WHERE id=?`, old, origin); err != nil {
		t.Fatal(err)
	}
	// No session continues this one any more, but it still owes.
	if _, err := s.db.Exec(`INSERT INTO identity_pin_lineages(id,account_id,origin_device_id,created_at) SELECT 'pln_owing',account_id,'other-gone-device',? FROM identity_pin_lineages WHERE id=?`, old, origin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO direct_profile_pin_attempts(profile_id,lineage_id,failed_attempts,last_failed_ms,locked_until_ms) VALUES(?,'pln_owing',3,?,0)`, child.ID, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PruneSessionHistory(t.Context()); err != nil {
		t.Fatal(err)
	}
	var unused, live int
	if err := s.db.QueryRow(`SELECT count(*) FROM identity_pin_lineages WHERE id='pln_unused'`).Scan(&unused); err != nil || unused != 0 {
		t.Fatalf("unused lineage kept: %d %v", unused, err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM identity_pin_lineages WHERE id=?`, origin).Scan(&live); err != nil || live != 1 {
		t.Fatalf("live lineage pruned: %d %v", live, err)
	}
	if debt := pinDebtForTest(t, s, child.ID, origin); debt != 1 {
		t.Fatalf("current debt pruned: %d", debt)
	}
	if debt := pinDebtForTest(t, s, child.ID, "pln_owing"); debt != 3 {
		t.Fatalf("a lineage that still owes was pruned with its debt: %d", debt)
	}
	if _, err := s.db.Exec(`UPDATE direct_profile_pin_attempts SET last_failed_ms=?`, time.Now().Add(-25*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PruneSessionHistory(t.Context()); err != nil {
		t.Fatal(err)
	}
	if debt := pinDebtForTest(t, s, child.ID, origin); debt != 0 {
		t.Fatalf("lapsed debt kept: %d", debt)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM identity_pin_lineages WHERE id=?`, origin).Scan(&live); err != nil || live != 1 {
		t.Fatalf("a lineage still continued by a session was pruned: %d %v", live, err)
	}
}
