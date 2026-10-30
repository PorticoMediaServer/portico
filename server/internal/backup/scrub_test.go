package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/testauth"
)

// A backup holds no sign-ins: the copy is scrubbed as it is written, while the
// running server's own sessions carry on.
func TestABackupHoldsNoSessions(t *testing.T) {
	s, state, db := testService(t)
	if _, err := db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',X'00','profile',1)`); err != nil {
		t.Fatal(err)
	}
	testauth.InsertSession(t, db, "token-hash-one", "account", "profile", "local", "owner", "2099-01-01T00:00:00Z")
	testauth.InsertSession(t, db, "token-hash-two", "account", "profile", "local", "owner", "2099-01-01T00:00:00Z")
	s.SetCopyScrubber(identity.ScrubBackupCopy)
	id, err := s.Create(context.Background(), KindManual)
	if err != nil {
		t.Fatal(err)
	}
	copyDB := openReadOnly(t, storagepaths.Path(state, BackupsDir, id, DatabaseName))
	var live, ended, accounts int
	if err = copyDB.QueryRow(`SELECT (SELECT count(*) FROM authorization_session_families WHERE revoked=0),(SELECT count(*) FROM authorization_session_families WHERE revoked_reason='restored'),(SELECT count(*) FROM accounts)`).Scan(&live, &ended, &accounts); err != nil {
		t.Fatal(err)
	}
	if live != 0 || ended != 2 || accounts != 1 {
		t.Fatalf("the backup holds %d live sessions (%d ended, %d accounts)", live, ended, accounts)
	}
	if err = db.QueryRow(`SELECT count(*) FROM authorization_session_families WHERE revoked=0`).Scan(&live); err != nil || live != 2 {
		t.Fatalf("taking a backup changed the running server's sessions: %d live, %v", live, err)
	}
	// The backup is still a good backup: it lists, and it can be staged for a restore.
	list, err := s.List()
	if err != nil || len(list) != 1 || list[0].ID != id {
		t.Fatalf("list: %+v %v", list, err)
	}
	if _, err = s.Stage(context.Background(), Source{BackupID: id}); err != nil {
		t.Fatalf("a scrubbed backup could not be staged for a restore: %v", err)
	}
}

// A copy that could not be scrubbed never becomes a backup.
func TestABackupWhoseCopyCannotBeScrubbedIsNotKept(t *testing.T) {
	s, state, _ := testService(t)
	refusal := errors.New("cannot scrub")
	var scrubbed string
	s.SetCopyScrubber(func(_ context.Context, copy *dbwork.Candidate) error {
		scrubbed = copy.Path()
		return refusal
	})
	if _, err := s.Create(context.Background(), KindManual); !errors.Is(err, refusal) {
		t.Fatalf("create: %v", err)
	}
	if filepath.Base(scrubbed) != filepath.Base(DatabaseName) || !strings.Contains(scrubbed, filepath.Join("Temp", "backup-snapshot-")) {
		t.Fatalf("the scrubber was given %q, not the copy being written", scrubbed)
	}
	if list, err := s.List(); err != nil || len(list) != 0 {
		t.Fatalf("an unscrubbed copy was kept as a backup: %+v %v", list, err)
	}
	entries, _ := os.ReadDir(storagepaths.Path(state, BackupsDir))
	if len(entries) != 0 {
		t.Fatalf("an unscrubbed copy was left on disk: %v", entries)
	}
}
