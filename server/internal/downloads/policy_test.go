package downloads

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestDownloadPolicySeparatesLocalAndHostedProfileIDs(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('local-account','owner',x'00','same-profile',1);
		INSERT INTO profile_restrictions(profile_id,allow_downloads) VALUES('same-profile',0)`)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	local := identity.Viewer{Authority: "local", AccountID: "local-account", ProfileID: "same-profile"}
	hosted := identity.Viewer{Authority: "hosted", AccountID: "hosted-account", ProfileID: "same-profile"}
	if allowed, err := ProfileAllowsDownloads(tx, local); err != nil || allowed {
		t.Fatalf("local download restriction: %v %v", allowed, err)
	}
	if allowed, err := ProfileAllowsDownloads(tx, hosted); !errors.Is(err, identity.ErrUnauthorized) || allowed {
		t.Fatalf("retired authority accepted: %v %v", allowed, err)
	}
	if _, err := tx.Exec(`UPDATE profile_restrictions SET allow_downloads=1 WHERE profile_id='same-profile'; INSERT INTO restrictions(profile_id,revision,revoked,allowed_libraries) VALUES('same-profile',1,1,'[]')`); err != nil {
		t.Fatal(err)
	}
	if allowed, err := ProfileAllowsDownloads(tx, local); err != nil || !allowed {
		t.Fatalf("local viewer borrowed Hosted revocation: %v %v", allowed, err)
	}
	if disabled, err := AccountDisabled(tx, local); err != nil || disabled {
		t.Fatalf("local account borrowed Hosted revocation: %v %v", disabled, err)
	}
	if disabled, err := AccountDisabled(tx, hosted); !errors.Is(err, identity.ErrUnauthorized) || disabled {
		t.Fatalf("retired authority accepted: %v %v", disabled, err)
	}
}
