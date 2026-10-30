package remotesources

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type DAVInput struct {
	OperationID        string `json:"operationId"`
	ExpectedGeneration int64  `json:"expectedGeneration,omitempty"`
	Name               string `json:"name"`
	Root               string `json:"root"`
	Username           string `json:"username"`
	Password           string `json:"password"`
	KeepConnection     bool   `json:"keepConnection,omitempty"`
	KeepPassword       bool   `json:"keepPassword,omitempty"`
	InsecureLocal      bool   `json:"insecureLocal"`
}
type Receipt struct {
	OperationID  string              `json:"operationId"`
	Accepted     bool                `json:"accepted"`
	Removed      bool                `json:"removed"`
	Source       Source              `json:"source"`
	ReadSettings *SourceReadSettings `json:"readSettings,omitempty"`
}

var opPattern = regexp.MustCompile(`^([0-9]{13})-[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func checkOperation(op string) error {
	m := opPattern.FindStringSubmatch(op)
	if m == nil {
		return mounts.ErrInvalid
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	t := time.UnixMilli(n)
	if time.Since(t) > 10*time.Minute || time.Until(t) > time.Minute {
		return mounts.ErrCommandExpired
	}
	return nil
}
func (s *Service) receipt(ctx context.Context, actor, op, fingerprint string) (Receipt, error) {
	var out Receipt
	var a, h, raw string
	err := s.db.QueryRowContext(ctx, `SELECT actor,fingerprint,response FROM remote_source_operations WHERE operation_id=?`, op).Scan(&a, &h, &raw)
	if err != nil {
		return out, err
	}
	if a != actor || fingerprint != "" && !hmac.Equal([]byte(h), []byte(fingerprint)) {
		return out, mounts.ErrCommandConflict
	}
	err = json.Unmarshal([]byte(raw), &out)
	return out, err
}
func (s *Service) Receipt(ctx context.Context, actor, op string) (Receipt, error) {
	receipt, err := s.receipt(ctx, actor, op, "")
	if err == nil && receipt.ReadSettings != nil {
		err = s.finishReadSettings(ctx, receipt.Source.ID, receipt.ReadSettings.Revision)
	}
	return receipt, err
}
func (s *Service) authorize(ctx context.Context, authorize func(*sql.Tx) error) error {
	if authorize == nil {
		return identity.ErrUnauthorized
	}
	gated, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	return authorize(tx)
}
func (s *Service) ConfigureDAV(ctx context.Context, actor, id string, in DAVInput, authorize func(*sql.Tx) error) (Receipt, error) {
	s.commands.Lock()
	defer s.commands.Unlock()
	var zero Receipt
	if err := s.authorize(ctx, authorize); err != nil {
		return zero, err
	}
	raw, _ := json.Marshal(struct {
		Actor, ID string
		Input     DAVInput
	}{actor, id, in})
	fingerprint := s.mounts.PrivateDigest(raw)
	clear(raw)
	if r, err := s.receipt(ctx, actor, in.OperationID, fingerprint); err == nil {
		return r, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return zero, err
	}
	if err := checkOperation(in.OperationID); err != nil {
		return zero, err
	}
	if !validDAVFields(in.Name, in.Root, in.Username, in.Password) {
		return zero, storage.ErrRemoteConfig
	}
	if id == "" && in.ExpectedGeneration != 0 || id != "" && in.ExpectedGeneration < 1 {
		return zero, mounts.ErrCommandConflict
	}
	source := Source{ID: id, Kind: "webdav", Name: strings.TrimSpace(in.Name), Generation: 1, State: "ready", RangeSupport: "unknown", InsecureLocal: in.InsecureLocal}
	if id != "" {
		old, err := s.get(ctx, id)
		if err != nil {
			return zero, err
		}
		if old.Kind != "webdav" || old.Generation != in.ExpectedGeneration {
			return zero, mounts.ErrCommandConflict
		}
		source.RootPath = old.RootPath
		source.Generation = old.Generation + 1
		if in.KeepPassword || in.KeepConnection {
			if in.KeepPassword && in.Password != "" {
				return zero, storage.ErrRemoteConfig
			}
			var sealed []byte
			if s.db.QueryRowContext(ctx, `SELECT config FROM remote_sources WHERE id=?`, id).Scan(&sealed) != nil {
				return zero, storage.ErrRemoteConfig
			}
			raw, err := s.mounts.Open(sealed)
			if err != nil {
				return zero, err
			}
			var oldConfig remotemedia.DAVConfig
			err = json.Unmarshal(raw, &oldConfig)
			clear(raw)
			if err != nil {
				return zero, storage.ErrRemoteConfig
			}
			if in.KeepConnection {
				if in.Root != "" || in.Username != "" {
					return zero, storage.ErrRemoteConfig
				}
				in.Root, in.Username = oldConfig.Root, oldConfig.Username
			}
			// Retained credentials cannot silently migrate to a different origin/user.
			if in.KeepPassword {
				if safeOrigin(oldConfig.Root) != safeOrigin(in.Root) || oldConfig.Username != in.Username {
					return zero, storage.ErrRemoteConfig
				}
				in.Password = oldConfig.Password
			}
		}
	} else {
		if in.KeepPassword || in.KeepConnection {
			return zero, storage.ErrRemoteConfig
		}
		source.ID = identity.Token()
		source.RootPath = filepath.Join(s.private, source.ID)
	}
	// Candidate network validation happens without a database write transaction.
	config, err := remotemedia.ValidateSourceDAVConfig(ctx, remotemedia.DAVConfig{Root: in.Root, Username: in.Username, Password: in.Password, InsecureLocal: in.InsecureLocal}, s.resolver)
	if err != nil {
		return zero, mapError(err)
	}
	support, setupGuard, err := s.testDAVSetup(ctx, id, source, config, authorize)
	if err != nil {
		return zero, mapError(err)
	}
	source.RangeSupport = string(support)
	source.Origin = safeOrigin(config.Root)
	source.CredentialPresent = config.Username != "" || config.Password != ""
	raw, _ = json.Marshal(config)
	sealed, err := s.mounts.Seal(raw)
	clear(raw)
	if err != nil {
		return zero, err
	}
	if id == "" {
		if err = os.Mkdir(source.RootPath, 0700); err != nil {
			return zero, err
		}
	}
	committed := false
	defer func() {
		if !committed && id == "" {
			_ = os.Remove(source.RootPath)
		}
	}()
	gated2, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return zero, err
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	if err = setupGuard(tx); err != nil {
		return zero, err
	}
	if id == "" {
		_, err = tx.Exec(`INSERT INTO remote_sources(id,kind,name,root,generation,config,state,range_support,origin,insecure,credential_present,webdav_leaf_count) VALUES(?,'webdav',?,?,1,?,'ready',?,?,?,?,1)`, source.ID, source.Name, source.RootPath, sealed, source.RangeSupport, source.Origin, source.InsecureLocal, source.CredentialPresent)
	} else {
		var result sql.Result
		result, err = tx.Exec(`UPDATE remote_sources SET name=?,generation=generation+1,config=?,state='ready',error_code='',range_support=?,origin=?,insecure=?,credential_present=?,webdav_leaf_count=1 WHERE id=? AND generation=? AND removed=0`, source.Name, sealed, source.RangeSupport, source.Origin, source.InsecureLocal, source.CredentialPresent, id, in.ExpectedGeneration)
		if err == nil {
			n, _ := result.RowsAffected()
			if n != 1 {
				err = mounts.ErrCommandConflict
			}
		}
	}
	if err != nil {
		return zero, err
	}
	receipt := Receipt{OperationID: in.OperationID, Accepted: true, Source: source}
	response, _ := json.Marshal(receipt)
	if err = expireSourceOperationsTx(tx); err != nil {
		return zero, err
	}
	var count int
	if err = tx.QueryRow(`SELECT count(*) FROM remote_source_operations WHERE created_at>=?`, sourceOperationExpiry()).Scan(&count); err != nil {
		return zero, err
	}
	if count >= 4096 {
		return zero, mounts.ErrCommandCapacity
	}
	if _, err = tx.Exec(`INSERT INTO remote_source_operations VALUES(?,?,?,?,?)`, in.OperationID, actor, fingerprint, string(response), time.Now().Unix()); err != nil {
		return zero, err
	}
	if err = gated2.Commit(); err != nil {
		return zero, err
	}
	committed = true
	s.invalidate(source.ID)
	_ = s.clearSourceCache(context.Background(), source.ID)
	_ = s.refreshBindings(ctx)
	return receipt, nil
}
func (s *Service) RemoveDAV(ctx context.Context, actor, id, op string, generation int64, authorize func(*sql.Tx) error) (Receipt, error) {
	s.commands.Lock()
	defer s.commands.Unlock()
	var zero Receipt
	if err := s.authorize(ctx, authorize); err != nil {
		return zero, err
	}
	raw, _ := json.Marshal([]any{actor, id, op, generation, "remove"})
	hash := s.mounts.PrivateDigest(raw)
	if r, e := s.receipt(ctx, actor, op, hash); e == nil {
		return r, nil
	} else if !errors.Is(e, sql.ErrNoRows) {
		return zero, e
	}
	if err := checkOperation(op); err != nil {
		return zero, err
	}
	source, err := s.get(ctx, id)
	if err != nil {
		return zero, err
	}
	if source.Kind != "webdav" || source.Generation != generation {
		return zero, mounts.ErrCommandConflict
	}
	if s.PlaybackActive(id) {
		return zero, storage.ErrBusy
	}
	gated3, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return zero, err
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	if err = authorize(tx); err != nil {
		return zero, err
	}
	result, err := tx.Exec(`UPDATE remote_sources SET removed=1,state='removed',config=NULL,generation=generation+1 WHERE id=? AND generation=? AND removed=0`, id, generation)
	if err != nil {
		return zero, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return zero, mounts.ErrCommandConflict
	}
	// Root tombstones remain so an empty virtual folder can never become proof of
	// deletion. Explicit source removal marks only its associated bytes
	// unavailable; that walk is queued here, with the tombstone, and done in
	// bounded slices by the background loop (markRemovedSourceAssets).
	if _, err = tx.Exec(`INSERT INTO remote_source_removals(source_id,root) VALUES(?,?) ON CONFLICT(source_id) DO UPDATE SET root=excluded.root,after_path=''`, id, source.RootPath); err != nil {
		return zero, err
	}
	source.State = "removed"
	source.Generation++
	receipt := Receipt{OperationID: op, Accepted: true, Removed: true, Source: source}
	response, _ := json.Marshal(receipt)
	if err = expireSourceOperationsTx(tx); err != nil {
		return zero, err
	}
	var count int
	if err = tx.QueryRow(`SELECT count(*) FROM remote_source_operations WHERE created_at>=?`, sourceOperationExpiry()).Scan(&count); err != nil {
		return zero, err
	}
	if count >= 4096 {
		return zero, mounts.ErrCommandCapacity
	}
	if _, err = tx.Exec(`INSERT INTO remote_source_operations VALUES(?,?,?,?,?)`, op, actor, hash, string(response), time.Now().Unix()); err != nil {
		return zero, err
	}
	if err = gated3.Commit(); err != nil {
		return zero, err
	}
	s.invalidate(id)
	_ = s.clearSourceCache(context.Background(), id)
	_ = s.refreshBindings(ctx)
	return receipt, nil
}

// sourceOperationExpiry is when a source command receipt stops replaying.
func sourceOperationExpiry() int64 { return time.Now().Add(-30 * 24 * time.Hour).Unix() }

// expireSourceOperationsTx retires a bounded slice of expired receipts inside
// a command's own transaction, so a backlog never lengthens one command's
// write; only unexpired receipts count against the capacity.
func expireSourceOperationsTx(tx *sql.Tx) error {
	_, err := tx.Exec(`DELETE FROM remote_source_operations WHERE rowid IN(SELECT rowid FROM remote_source_operations WHERE created_at<? LIMIT 32)`, sourceOperationExpiry())
	return err
}
