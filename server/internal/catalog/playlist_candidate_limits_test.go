package catalog

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestPlaylistCandidatesSeekPastThousandProfiles(t *testing.T) {
	db, _, s := phase34PlaylistDB(t, filepath.Join(t.TempDir(), "db"))
	defer db.Close()
	a := ResourceActor{Authority: "local", AccountID: "owner", ProfileID: "primary"}
	name := "Shared"
	playlist, err := s.MutatePlaylist(a, "", "create", "", PlaylistMutation{OperationID: "create", Name: &name}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1002; i++ {
		account := fmt.Sprintf("account-%04d", i)
		_, err = tx.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES(?,?,X'00',?)`, account, account, fmt.Sprintf("profile-%04d", i))
		if err != nil {
			break
		}
	}
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	cursor := ""
	seen := map[string]bool{}
	for {
		page, err := s.PlaylistCandidates("server", "viewer", playlist.PlaylistID, a, cursor, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range page.Candidates {
			if seen[candidate.ID] {
				t.Fatal("candidate repeated", candidate.ID)
			}
			seen[candidate.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 1002 {
		t.Fatalf("only %d candidates", len(seen))
	}
}
