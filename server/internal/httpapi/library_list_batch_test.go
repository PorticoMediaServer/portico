package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestLibraryListBatchHasOneAcquisitionAndFlatStatements(t *testing.T) {
	read := func(n int) dbwork.Cost {
		t.Helper()
		d, session := completedLocalFoundationFixture(t)
		c := catalogtest.New(t, d.DB)
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("list-%d", i)
			handle := c.Library(id, id, "movie", "/"+id)
			c.Entity(compactcatalog.Entity{Library: handle, Kind: compactcatalog.Movie, Key: id, Title: id}, nil)
		}
		ctx, cost := dbwork.Measure(context.Background())
		r := httptest.NewRequest("GET", "/v1/libraries", nil).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer "+session.AccessToken)
		w := httptest.NewRecorder()
		d.libraryList(w, r)
		if w.Code != 200 {
			t.Fatalf("libraries %d: %d %s", n, w.Code, w.Body.String())
		}
		var response struct {
			Items []json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Items == nil || len(response.Items) != n {
			t.Fatalf("expected %d library items in an array: %s", n, w.Body.String())
		}
		return cost()
	}
	_ = read(0)
	two, eleven := read(2), read(11)
	if two.Statements != eleven.Statements || eleven.Statements > 20 || two.Transactions != 1 || eleven.Transactions != 1 || two.Acquisitions != 1 || eleven.Acquisitions != 1 {
		t.Fatalf("request is not flat/single snapshot: two=%+v eleven=%+v", two, eleven)
	}
	t.Logf("two=%+v eleven=%+v", two, eleven)
}

func TestLibraryListBatchRechecksAuthorityOnlyAfterAChange(t *testing.T) {
	for _, revoke := range []bool{true, false} {
		t.Run(fmt.Sprint("revoke=", revoke), func(t *testing.T) {
			d, session := completedLocalFoundationFixture(t)
			c := catalogtest.New(t, d.DB)
			c.Library("private-library", "Private library", "movie", "/private")
			captured, err := d.Identity.Authenticate(session.AccessToken)
			if err != nil {
				t.Fatal(err)
			}
			if !revoke {
				_, err = d.Identity.CreateDirectMember(context.Background(), session.AccessToken, identity.DirectMemberCreate{Username: "other-member", Password: "Other-password1!", Name: "Other", AllowedLibraries: []string{}})
				if err != nil {
					t.Fatal(err)
				}
			}
			ctx, cost := dbwork.Measure(context.Background())
			r := httptest.NewRequest("GET", "/v1/libraries", nil).WithContext(ctx)
			r.Header.Set("Authorization", "Bearer "+session.AccessToken)
			w := httptest.NewRecorder()
			d.libraryListAfterRead(w, r, func() {
				if revoke {
					if err := d.Identity.Logout(captured); err != nil {
						t.Fatal(err)
					}
				} else {
					result, err := dbwork.ExecWrite(context.Background(), d.DB, dbwork.ClassSecurityFence, `UPDATE direct_memberships SET revision=revision+1 WHERE account_id<>?`, captured.AccountID)
					if err != nil {
						t.Fatal(err)
					}
					n, _ := result.RowsAffected()
					if n == 0 {
						t.Fatal("unrelated authority fixture did not change")
					}
				}
			})
			if revoke {
				if w.Code < 400 || strings.Contains(w.Body.String(), "private-library") || strings.Contains(w.Body.String(), `"items"`) {
					t.Fatalf("revoked snapshot escaped: %d %s", w.Code, w.Body.String())
				}
			} else if w.Code != 200 || !strings.Contains(w.Body.String(), "private-library") {
				t.Fatalf("unrelated authority change denied this viewer: %d %s", w.Code, w.Body.String())
			}
			if cost().Acquisitions != 2 {
				t.Fatalf("changed authority did not take one bounded fresh snapshot: %+v", cost())
			}
		})
	}
}
