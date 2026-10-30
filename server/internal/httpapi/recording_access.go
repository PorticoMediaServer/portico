package httpapi

import (
	"context"
	"database/sql"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/recordingaccess"
)

func (d Dependencies) recordingPolicy() recordingaccess.Policy {
	policy := recordingaccess.Policy{Schema: d.directSchema}
	if d.Hosted != nil {
		policy.Cached = d.Hosted
	}
	return policy
}

// allowedLibrary answers whether this principal may see this library. It takes
// the request's context so the lane's budget reaches it: an authorisation check
// that cannot be cancelled holds a pooled connection past the deadline of the
// request that wanted it.
func (d Dependencies) allowedLibrary(ctx context.Context, p identity.Principal, library string) error {
	strict := strictPrincipal(dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	key := ""
	if !strict {
		key = accessKey(p, library)
		if d.access.allowed(key, time.Now()) {
			return nil
		}
	}
	generation := dbwork.AuthorityGeneration()
	err := d.recordingPolicy().Allowed(ctx, d.DB, p, library)
	if err == nil && key != "" {
		d.access.grant(key, time.Now(), generation)
	}
	return err
}

// allowedLibrarySet is allowedLibrary for a page of rows. Filtering a listing by
// calling allowedLibrary per row opened a read transaction per row; this opens
// one for the whole page, and the decision for each library is the same one
// allowedLibrary would have made.
func (d Dependencies) allowedLibrarySet(ctx context.Context, p identity.Principal, libraries []string) (map[string]bool, error) {
	// A page whose every library is already granted needs no transaction at all.
	strict := strictPrincipal(dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	now := time.Now()
	out := map[string]bool{}
	pending := make([]string, 0, len(libraries))
	for _, library := range libraries {
		if _, decided := out[library]; decided {
			continue
		}
		if !strict && d.access.allowed(accessKey(p, library), now) {
			out[library] = true
			continue
		}
		pending = append(pending, library)
	}
	if len(pending) == 0 {
		return out, nil
	}
	generation := dbwork.AuthorityGeneration()
	resolved, err := d.recordingPolicy().AllowedSet(ctx, d.DB, p, pending)
	if err != nil {
		return nil, err
	}
	for library, allowed := range resolved {
		out[library] = allowed
		if allowed && !strict {
			d.access.grant(accessKey(p, library), now, generation)
		}
	}
	return out, nil
}

func (d Dependencies) allowedLibraryTx(ctx context.Context, p identity.Principal, library string, tx *sql.Tx) error {
	// Avoid a typed-nil cached interface in isolated Local-only compositions.
	return d.recordingPolicy().AllowedTxContext(ctx, p, library, tx)
}
func (d Dependencies) allowedLibraryLegacyTx(p identity.Principal, library string, tx *sql.Tx) error {
	return d.allowedLibraryTx(context.Background(), p, library, tx)
}
