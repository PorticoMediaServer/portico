package mounts

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/authorityfiles"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var ErrCommandConflict = errors.New("storage command conflicts with its prior receipt or current control revision")
var ErrCommandExpired = errors.New("storage operation expired; refresh before creating a new intent")
var ErrCommandCapacity = errors.New("storage command receipt capacity reached; retry after retention cleanup")

type Command struct {
	Setup            *S3Setup `json:"setup,omitempty"`
	CacheBytes       *int64   `json:"cacheBytes,omitempty"`
	CacheFloor       *int64   `json:"cacheFloor,omitempty"`
	OperationID      string   `json:"operationId"`
	ExpectedRevision int64    `json:"expectedRevision,omitempty"`
	Action           string   `json:"action,omitempty"`
	Name             string   `json:"name,omitempty"`
	Executable       string   `json:"executable,omitempty"`
	Remote           string   `json:"remote,omitempty"`
	Config           string   `json:"config,omitempty"`
	// receiptKey, when set, binds this operation's receipt to the request
	// identity instead of the configuration text. It is private on purpose: a
	// wire caller cannot set it, so no decoded command can drop its body from
	// the fingerprint. Only a package method that reads the configuration
	// itself may use it.
	receiptKey string
	// Only the narrow source editor can set this. It is not a wire command or
	// a replacement for the legacy lifecycle control revision.
	sourceGeneration int64
	// UseIncluded is the requested program selection for a narrow source edit.
	// Nil means the request omitted a program choice and configure preserves the
	// saved selection; never decoded from a wire body.
	UseIncluded *bool `json:"-"`
	// Only ConfigureReadCache binds this non-wire configuration precondition;
	// its accepting callback travels through the private command call, not JSON.
	readCacheGeneration int64
	// Only ConfigureSourceProgram can opt into the P2-51 acceptance seam.
	sourceProgram *sourceProgramMutation
}
type Receipt struct {
	OperationID     string `json:"operationId"`
	MountID         string `json:"mountId"`
	ControlRevision int64  `json:"controlRevision"`
	Accepted        bool   `json:"accepted"`
	Deleted         bool   `json:"deleted"`
	Mount           *Mount `json:"mount,omitempty"`
}

var operationPattern = regexp.MustCompile(`^([0-9]{13})-([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$`)

func operationTime(id string) (time.Time, error) {
	m := operationPattern.FindStringSubmatch(id)
	if m == nil {
		return time.Time{}, ErrInvalid
	}
	n, e := strconv.ParseInt(m[1], 10, 64)
	if e != nil {
		return time.Time{}, ErrInvalid
	}
	return time.UnixMilli(n), nil
}
func freshOperation(id string) error {
	at, e := operationTime(id)
	if e != nil {
		return e
	}
	if time.Since(at) > 10*time.Minute || time.Until(at) > time.Minute {
		return ErrCommandExpired
	}
	return nil
}
func boolPointer(value bool) *bool { return &value }

func (s *Service) fingerprint(actor, id string, c Command) string {
	if c.receiptKey != "" {
		// The receipt identifies the request, not mutable configuration content.
		c.Config = ""
	}
	// The included intent is not wire data, so it is added explicitly as the
	// original tri-state: nil is omitted, and explicit false and true are
	// distinct hashes. A pre-upgrade custom retry that carried no tri-state may
	// fail closed as a conflict rather than replay under a guessed presence.
	raw, _ := json.Marshal(struct {
		Actor, ID           string
		Command             Command
		SourceGeneration    int64 `json:"sourceGeneration,omitempty"`
		Included            *bool `json:"included,omitempty"`
		ReadCacheGeneration int64 `json:"readCacheGeneration,omitempty"`
	}{actor, id, c, c.sourceGeneration, c.UseIncluded, c.readCacheGeneration})
	defer clear(raw)
	mac := hmac.New(sha256.New, s.fpKey[:])
	mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}
func (s *Service) Receipt(actor, operation string) (Receipt, error) {
	s.commands.Lock()
	defer s.commands.Unlock()
	if _, e := operationTime(operation); e != nil {
		return Receipt{}, e
	}
	r, e := s.receipt(s.db, actor, operation, "")
	if errors.Is(e, sql.ErrNoRows) && freshOperation(operation) != nil {
		return r, ErrCommandExpired
	}
	return r, e
}

type commandReader interface{ QueryRow(string, ...any) *sql.Row }

func (s *Service) receipt(q commandReader, actor, operation, fingerprint string) (Receipt, error) {
	var r Receipt
	var storedActor, storedHash, raw string
	var created int64
	e := q.QueryRow(`SELECT actor,fingerprint,response,created_at FROM mount_operations WHERE operation_id=?`, operation).Scan(&storedActor, &storedHash, &raw, &created)
	if e != nil {
		return r, e
	}
	if storedActor != actor || fingerprint != "" && !hmac.Equal([]byte(storedHash), []byte(fingerprint)) {
		return r, ErrCommandConflict
	}
	if time.Since(time.Unix(created, 0)) > 30*24*time.Hour {
		return r, ErrCommandExpired
	}
	e = json.Unmarshal([]byte(raw), &r)
	return r, e
}
func (s *Service) record(tx *sql.Tx, actor, hash string, r Receipt) error {
	raw, e := json.Marshal(r)
	if e != nil {
		return e
	}
	_, e = tx.Exec(`INSERT INTO mount_operations VALUES(?,?,?,?,?,?)`, r.OperationID, actor, hash, r.MountID, string(raw), time.Now().Unix())
	return e
}
func (s *Service) Command(ctx context.Context, actor, id string, c Command, authorize func(*sql.Tx) error) (Receipt, error) {
	return s.command(ctx, actor, id, c, authorize, nil)
}

func (s *Service) command(ctx context.Context, actor, id string, c Command, authorize func(*sql.Tx) error, readCacheAccepted func(context.Context, *sql.Tx, Mount, Mount) error) (Receipt, error) {
	s.commands.Lock()
	defer s.commands.Unlock()
	var zero Receipt
	if actor == "" || authorize == nil {
		return zero, ErrInvalid
	}
	if c.sourceProgram != nil {
		return s.sourceProgramCommand(ctx, actor, id, c, authorize)
	}
	if _, e := operationTime(c.OperationID); e != nil {
		return zero, e
	}
	hash := s.fingerprint(actor, id, c)
	// Reauthorization also precedes exact receipt replay, never accepting revoked owners.
	gated2, e := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if e != nil {
		return zero, e
	}
	tx := gated2.Tx()
	if e = authorize(tx); e != nil {
		gated2.Rollback()
		return zero, e
	}
	existing, e := s.receipt(tx, actor, c.OperationID, hash)
	if e == nil {
		gated2.Rollback()
		return existing, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		gated2.Rollback()
		return zero, e
	}
	if e = freshOperation(c.OperationID); e != nil {
		gated2.Rollback()
		return zero, e
	}
	if c.sourceGeneration != 0 {
		if c.sourceGeneration < 1 || (c.Action != "configure" && c.Action != "delete") || id == "" || c.ExpectedRevision != 0 || c.Setup != nil || c.CacheBytes != nil || c.CacheFloor != nil {
			gated2.Rollback()
			return zero, ErrInvalid
		}
		c.ExpectedRevision, e = importedSourceControlTx(ctx, tx, id, c.sourceGeneration)
		if e != nil {
			gated2.Rollback()
			return zero, e
		}
	}
	// Each command retires a bounded slice of expired receipts on the
	// retention index; only unexpired receipts count against the capacity.
	expired := time.Now().Add(-30 * 24 * time.Hour).Unix()
	if _, e = tx.Exec(`DELETE FROM mount_operations WHERE rowid IN(SELECT rowid FROM mount_operations WHERE created_at<? ORDER BY created_at LIMIT 32)`, expired); e != nil {
		gated2.Rollback()
		return zero, e
	}
	var count int
	if e = tx.QueryRow(`SELECT count(*) FROM mount_operations WHERE created_at>=?`, expired).Scan(&count); e != nil {
		gated2.Rollback()
		return zero, e
	}
	if count >= 4096 {
		gated2.Rollback()
		return zero, ErrCommandCapacity
	}
	if e = gated2.Commit(); e != nil {
		return zero, e
	}
	if c.Setup != nil {
		if c.Config != "" || c.Remote != "" {
			return zero, ErrConfigInvalid
		}
		var err error
		c.Config, c.Remote, err = managedS3(*c.Setup)
		if err != nil {
			return zero, err
		}
	}
	if c.Action == "configure" {
		return s.configure(ctx, actor, id, hash, c, authorize, readCacheAccepted)
	}
	if c.Action == "create" {
		if id != "" || c.ExpectedRevision != 0 || c.CacheBytes != nil || c.CacheFloor != nil {
			return zero, ErrInvalid
		}
		var receipt Receipt
		_, e = s.create(ctx, CreateInput{Name: c.Name, Executable: c.Executable, Remote: c.Remote, Config: c.Config, UseIncluded: c.UseIncluded != nil && *c.UseIncluded}, func(tx *sql.Tx, m Mount) error {
			if e := authorize(tx); e != nil {
				return e
			}
			receipt = Receipt{c.OperationID, m.ID, m.ControlRevision, true, false, &m}
			return s.record(tx, actor, hash, receipt)
		}, authorize)
		return receipt, e
	}
	if c.Setup != nil || c.CacheBytes != nil || c.CacheFloor != nil {
		return zero, ErrInvalid
	}
	if id == "" || c.ExpectedRevision < 1 || c.Name != "" || c.Executable != "" || c.Remote != "" || c.Config != "" {
		return zero, ErrInvalid
	}
	if c.Action != "start" && c.Action != "stop" && c.Action != "restart" && c.Action != "delete" {
		return zero, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.publish()
	m, e := s.get(id)
	if e != nil {
		return zero, e
	}
	if m.ControlRevision != c.ExpectedRevision || m.RemovalPending {
		return zero, ErrCommandConflict
	}
	if c.sourceGeneration != 0 {
		if s.GuardSourceRemoval == nil {
			return zero, ErrUnavailable
		}
		release, err := s.GuardSourceRemoval(ctx, id)
		if err != nil {
			return zero, err
		}
		if release == nil {
			return zero, ErrUnavailable
		}
		defer release()
	}
	if c.Action == "delete" && s.NativeBusy != nil && s.NativeBusy(id) {
		if c.sourceGeneration != 0 {
			return zero, ErrSourceInUse
		}
		return zero, errors.New("stop active playback using this source before removal")
	}
	if (c.Action == "start" || c.Action == "restart") && !s.mountSupported() {
		return zero, requestinput.New("Managed mounts are unavailable on this host. Native listing and playback remain available.")
	}
	if c.Action == "delete" && (m.DesiredState != "stopped" || s.active[id] != nil) {
		if c.sourceGeneration != 0 {
			return zero, ErrSourceInUse
		}
		return zero, errors.New("stop the managed mount and wait for its process to exit before removing it")
	}
	var gated3 *dbwork.Write
	gated3, e = dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if e != nil {
		return zero, e
	}
	tx = gated3.Tx()
	defer gated3.Rollback()
	if e = authorize(tx); e != nil {
		return zero, e
	}
	if c.sourceGeneration != 0 {
		control, err := importedSourceControlTx(ctx, tx, id, c.sourceGeneration)
		if err != nil {
			return zero, err
		}
		if control != c.ExpectedRevision {
			return zero, ErrCommandConflict
		}
		// This is logical removal only. The existing worker owns guarded private
		// allocation/config cleanup after physical process retirement.
		if _, e = tx.ExecContext(ctx, `UPDATE remote_sources SET removed=1,state='removed' WHERE id=? AND removed=0 AND generation=?`, id, c.sourceGeneration); e != nil {
			return zero, e
		}
	}
	desired := "running"
	if c.Action == "stop" || c.Action == "delete" {
		desired = "stopped"
	}
	restart := 0
	if c.Action == "restart" {
		restart = 1
	}
	remove := c.Action == "delete"
	result, e := tx.Exec(`UPDATE mount_controls SET revision=revision+1,restart_generation=restart_generation+?,removal_pending=? WHERE mount_id=? AND revision=? AND removal_pending=0`, restart, remove, id, c.ExpectedRevision)
	if e != nil {
		return zero, e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return zero, ErrCommandConflict
	}
	if _, e = tx.Exec(`UPDATE managed_mounts SET desired=?,error='' WHERE id=?`, desired, id); e != nil {
		return zero, e
	}
	m.ControlRevision++
	m.DesiredState = desired
	m.Error = ""
	m.RemovalPending = remove
	projectMountActions(&m)
	receipt := Receipt{c.OperationID, id, m.ControlRevision, true, remove, &m}
	if e = s.record(tx, actor, hash, receipt); e != nil {
		return zero, e
	}
	if e = gated3.Commit(); e != nil {
		return zero, e
	}
	if c.sourceGeneration != 0 && s.NativeInvalidated != nil {
		s.NativeInvalidated(id)
	}
	if c.Action != "start" {
		s.available[id] = false
		if child := s.active[id]; child != nil && !child.stopping {
			child.invalidateRuntime()
			child.stopping = true
			child.stopAt = time.Now()
			_ = child.control.Close()
		}
	}
	delete(s.retry, id)
	return receipt, nil
}
func projectMountActions(m *Mount) {
	defer func() {
		if !MountSupported() {
			actions := []string{}
			for _, action := range m.Actions {
				if action != "start" && action != "restart" {
					actions = append(actions, action)
				}
			}
			m.Actions = actions
		}
	}()
	m.Actions = []string{}
	if m.RemovalPending {
		return
	}
	if m.DesiredState == "stopped" && m.ObservedState == "stopped" {
		m.Actions = []string{"start", "delete"}
		return
	}
	m.Actions = []string{"stop"}
	if m.ObservedState != "stopping" && m.ObservedState != "quarantined" {
		m.Actions = append(m.Actions, "restart")
		if m.DesiredState == "stopped" {
			m.Actions = append(m.Actions, "start")
		}
	}
}

// Called only with ownership lock. Pending removals survive server restart and
// never recursively delete remote data; unavailable/unreaped roots remain pending.
func (s *Service) removePending(ctx context.Context, m Mount) {
	defer authorityfiles.Process.Admit()()
	if s.active[m.ID] != nil {
		return
	}
	check, cancel := context.WithTimeout(ctx, 2*time.Second)
	e := s.storage.RemoveMountRoot(check, m.MountPath)
	cancel()
	if e != nil {
		s.state(m.ID, "unavailable", "Removal is waiting for confirmed unmounted, empty storage.")
		return
	}
	if s.NativeInvalidated != nil {
		s.NativeInvalidated(m.ID)
	}
	// Only private regular configuration names are removed; never traverse a mount.
	files, e := os.ReadDir(s.private)
	if e != nil {
		return
	}
	for _, file := range files {
		name := file.Name()
		if name == m.ID+".conf" || strings.HasPrefix(name, m.ID+".g-") && strings.HasSuffix(name, ".conf") {
			if e = os.Remove(filepath.Join(s.private, name)); e != nil && !errors.Is(e, os.ErrNotExist) {
				s.state(m.ID, "unavailable", "Private configuration cleanup needs filesystem access.")
				return
			}
		}
	}
	cache := filepath.Join(s.cache, m.ID+".cache")
	// A symlink itself is removed, never followed into its target.
	if e = os.RemoveAll(cache); e != nil {
		return
	}
	if _, e = dbwork.ExecWrite(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), `DELETE FROM managed_mounts WHERE id=?`, m.ID); e != nil {
		return
	}
	delete(s.paths, m.ID)
	delete(s.available, m.ID)
}

func (s *Service) Revision() (int64, error) {
	var n int64
	e := s.db.QueryRow(`SELECT revision FROM mount_revision WHERE id=1`).Scan(&n)
	return n, e
}
