package httpapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/servicelog"
)

func TestAcceptedClientReportSurvivesTiedRetention(t *testing.T) {
	for _, scope := range []string{"device", "global"} {
		t.Run(scope, func(t *testing.T) {
			f := administrationFixtureNew(t)
			fixed := time.Now().UTC()
			f.deps.Access.ClientLogs.Now = func() time.Time { return fixed }
			count := servicelog.ClientUploadsPerDevice
			if scope == "global" {
				count = 100
			}
			for n := 0; n < count; n++ {
				device := f.memberDevice
				if scope == "global" {
					device = fmt.Sprintf("another-%d", n)
				}
				// These ids sort above every server-generated URL-safe id.
				id := "~" + strings.Repeat("z", 17) + fmt.Sprintf("%04d", n)
				_, err := f.deps.DB.Exec(`INSERT INTO client_log_uploads(id,account_id,profile_id,device_id,platform,received_ms,bytes,body) VALUES(?,?,?,?,?,?,1,'x')`, id, "member", "member-profile", device, "test", fixed.UnixMilli())
				if err != nil {
					t.Fatal(err)
				}
			}
			response := f.call("POST", "/v1/diagnostics/client-logs", f.member, `{"body":"accepted report"}`)
			if response.Code != 201 {
				t.Fatal(response.Code, response.Body.String())
			}
			var receipt servicelog.ClientUpload
			if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
				t.Fatal(err)
			}
			read := f.call("GET", "/v1/admin/diagnostics/client-logs/"+receipt.ID, f.owner, "")
			if read.Code != 200 {
				t.Fatalf("accepted report immediately disappeared: %d %s", read.Code, read.Body.String())
			}
			var held int
			if err := f.deps.DB.QueryRow(`SELECT count(*) FROM client_log_uploads`).Scan(&held); err != nil {
				t.Fatal(err)
			}
			if held != count {
				t.Fatalf("retained %d reports, want %d", held, count)
			}
		})
	}
}
