package read

import (
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"testing"
	"testing/synctest"
	"time"
)

func TestRBSavedViewPrincipalScope(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newBrowseFixture(t)
		for _, mode := range []string{"closed-purpose", "default-zero-library", "principal-positive-library", "missing-private-work", "foreign-private-work"} {
			_, err := f.snapshots.Read(t.Context(), foregroundRequest(), time.Second, func(scope *governor.ReadScope) error {
				q := func(name string, args ...any) ([][]any, error) { return scope.Query(readStatementID(name), args...) }
				in := f.input
				if mode == "closed-purpose" {
					fence, e := readSavedViewPrincipal(q, in)
					if e != nil {
						return e
					}
					if fence.library != "" || fence.policy != rbSavedViewPrincipalPolicy || fence.policyWitness != "rb-saved-view-principal-v1" || fence.profile != "p" {
						t.Fatal("not the actual closed principal", fence)
					}
					return nil
				}
				if mode == "default-zero-library" {
					in.Library = 0
				} else {
					in.policy = readPolicyBinding{recipe: rbSavedViewPrincipalPolicy, state: savedViewPrincipalPurpose}
					if mode == "foreign-private-work" {
						in.Library = 0
						in.policy.state = &savedViewPrincipalWork{purpose: 1}
					}
					if mode == "missing-private-work" {
						in.Library = 0
						in.policy.state = nil
					}
				}
				_, e := readBrowseFence(q, in)
				if !errors.Is(e, ErrInput) {
					t.Fatal("scope widened", mode, e)
				}
				return nil
			})
			if err != nil {
				t.Fatal(mode, err)
			}
		}
	})
}
