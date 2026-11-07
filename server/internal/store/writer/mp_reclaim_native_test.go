package writer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// Runner-only SQL preparation against exact F fragments. This deliberately
// establishes neither authority nor transaction/COMMIT/governor qualification.
func TestMPReclaimNativeFinalFPreparation(t *testing.T) {
	path := os.Getenv("PORTICO_MP_F_PACKET")
	if path == "" {
		t.Skip("F absent: set PORTICO_MP_F_PACKET to immutable final F on standard runner")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	packet := string(body)
	if !strings.Contains(packet, "mp_reclaim_result_payload BLOB") {
		t.Skip("final F fixed payload slot absent")
	}
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 128, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), filepath.Join(t.TempDir(), "reclaim.sqlite"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, root := range []string{"playback_start_preparations", "playback_sessions", "playback_v1_sessions", "prepared_media_jobs", "prepared_media_versions", "queues_v1", "playback_v1_prepared"} {
		key := "id"
		if root == "playback_v1_prepared" {
			key = "token"
		}
		if _, err = conn.ExecContext(t.Context(), "CREATE TABLE "+root+"("+key+" TEXT PRIMARY KEY)"); err != nil {
			t.Fatal(err)
		}
		for _, part := range strings.Split(packet, ";") {
			start := strings.Index(part, "ALTER TABLE "+root+" ADD COLUMN ")
			if start < 0 {
				continue
			}
			sql := strings.TrimSpace(part[start:])
			field := strings.Fields(sql)[5]
			keep := false
			for _, prefix := range []string{"mp_command_", "mp_owner_revision", "mp_receipt_", "mp_reclaim_", "mp_control_", "mp_user_root_id", "mp_owner_member_incarnation", "mp_owner_profile_lifetime", "mp_retirement_state"} {
				keep = keep || strings.HasPrefix(field, prefix)
			}
			if keep {
				if _, err = conn.ExecContext(t.Context(), sql); err != nil {
					t.Fatal(root, field, err)
				}
			}
		}
	}
	// Exact actual-root FK targets require the F composite unique indexes.
	for _, part := range strings.Split(packet, ";") {
		start := strings.Index(part, "CREATE UNIQUE INDEX mp_")
		if start >= 0 && strings.Contains(part[start:], "_command_owner ON ") {
			if _, err = conn.ExecContext(t.Context(), part[start:]); err != nil {
				t.Fatal(err)
			}
		}
	}

	for _, family := range []string{"start", "physical", "session", "job", "version", "queue", "next"} {
		marker := "CREATE TABLE mp_" + family + "_command_receipts("
		start := strings.Index(packet, marker)
		if start < 0 {
			t.Fatal(marker)
		}
		end := strings.Index(packet[start:], ";")
		if end < 0 {
			t.Fatal(marker)
		}
		if _, err = conn.ExecContext(t.Context(), packet[start:start+end]); err != nil {
			t.Fatal(family, err)
		}
	}
	defs := append(mpReceiptAckDefinitions(), mpReceiptReclaimDefinitions()...)
	for _, d := range defs {
		s, e := conn.PrepareContext(t.Context(), d.SQL)
		if e != nil {
			t.Error(d.ID, e)
			continue
		}
		s.Close()
	}
}
