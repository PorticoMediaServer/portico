package identity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestAuthenticatedDeviceMetadataIsVerifiedWithoutExtraStatements(t *testing.T) {
	s, db, _ := familyFixture(t)
	ctx, err := WithIssuingDevice(context.Background(), registration(Token()), "198.51.100.42")
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.IssueWithDevice(ctx, "account", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthenticateContext(context.Background(), first.AccessToken); err != nil {
		t.Fatal(err)
	}
	before := dbwork.Reads().Statements
	p, err := s.AuthenticateContext(context.Background(), first.AccessToken)
	cost := dbwork.Reads().Statements - before
	if err != nil || p.DeviceID == "" || p.DeviceID != first.DeviceID {
		t.Fatalf("device binding was not resolved from approved identity: %v", err)
	}
	// The existing snapshot bookkeeping plus family, live-access, direct-account and
	// approval statements remain the entire workload. Count context-free policy
	// SQL too, rather than hiding it in a context-only cost measurement.
	if cost > 5 {
		t.Fatalf("device metadata added authentication reads: %d", cost)
	}
	encoded, err := json.Marshal(p)
	if err != nil || strings.Contains(string(encoded), "DeviceID") || strings.Contains(string(encoded), p.DeviceID) {
		t.Fatal("internal device accounting leaked into serialized principal")
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	current, err := s.ReauthorizeTx(context.Background(), tx, p)
	if err != nil || current != p {
		t.Fatalf("transaction reauthorization lost metadata: %v", err)
	}
	forged := p
	forged.Hash = ""
	forged.DeviceID = first.DeviceID
	if _, err = s.ReauthorizeTx(context.Background(), tx, forged); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("device metadata substituted for token authority", err)
	}
	if err = RevokeFamilyTx(context.Background(), tx, first.SessionFamilyID, RevokedAdminRevoke); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReauthorizeTx(context.Background(), tx, p); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("accounting metadata bypassed denied-device authority", err)
	}
	if _, err = s.SessionFamilyTx(context.Background(), tx, p); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("approval projection changed denied-device refusal", err)
	}
	t.Logf("verified device metadata used %d existing authentication statements", cost)
}
