package hosted

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// validRenewal allows time extension only when canonical authority content is unchanged.
func validRenewal(previous string, current []byte) bool {
	var old, next Policy
	if json.Unmarshal([]byte(previous), &old) != nil || json.Unmarshal(current, &next) != nil {
		return false
	}
	if next.IssuedAt < old.IssuedAt || next.ExpiresAt < old.ExpiresAt {
		return false
	}
	old.IssuedAt = ""
	old.ExpiresAt = ""
	next.IssuedAt = ""
	next.ExpiresAt = ""
	a, _ := json.Marshal(old)
	b, _ := json.Marshal(next)
	return string(a) == string(b)
}

func (s *Service) Wake(ctx context.Context, signed Signed) error {
	raw, e := s.verifyEnvelope(ctx, signed)
	if e != nil {
		return identity.ErrUnauthorized
	}
	var wake struct {
		ServerID  string `json:"serverId"`
		Kind      string `json:"kind"`
		Revision  int64  `json:"revision"`
		IssuedAt  string `json:"issuedAt"`
		ExpiresAt string `json:"expiresAt"`
	}
	// "policy" is the kind older Hosted builds send; both mean "check in now".
	if json.Unmarshal(raw, &wake) != nil || wake.ServerID != s.identity.ID() || (wake.Kind != "check-in" && wake.Kind != "policy") || wake.Revision < 1 {
		return identity.ErrUnauthorized
	}
	issued, e := time.Parse(time.RFC3339, wake.IssuedAt)
	if e != nil || issued.After(time.Now().Add(time.Minute)) {
		return identity.ErrUnauthorized
	}
	expires, e := time.Parse(time.RFC3339, wake.ExpiresAt)
	if e != nil || !expires.After(time.Now()) || expires.Sub(issued) > 10*time.Minute {
		return identity.ErrUnauthorized
	}
	_, e = dbwork.ExecWrite(context.Background(), s.db, dbwork.ClassFrom(context.Background(), dbwork.ClassInteractive), `INSERT INTO configuration VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=CAST(max(CAST(value AS INTEGER),CAST(excluded.value AS INTEGER)) AS TEXT)`, wakeRevisionKey, strconv.FormatInt(wake.Revision, 10))
	if e == nil {
		s.NetworkChanged()
	}
	return e
}

// A wake asks for a check-in: Hosted has an erasure job or a new members-only
// label for this server. The revision is Hosted's wake counter; the check-in
// that follows reports it back so Hosted stops waking.
const (
	wakeRevisionKey    = "desired_policy_revision"
	checkedRevisionKey = "hosted_checked_wake_revision"
)

func (s *Service) Run(ctx context.Context) {
	if s.current != nil {
		s.runCurrent(ctx)
	}
}
