package storage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/progressio"
	"strings"
	"time"
)

type Snapshot struct {
	ErrorCode string `json:"errorCode,omitempty"`
	// Opaque adapter evidence; never a provider locator.
	Revision       string `json:"revision,omitempty"`
	ObjectID       string `json:"objectId,omitempty"`
	ObjectIdentity string `json:"objectIdentity,omitempty"`
	ChangeToken    string `json:"changeToken,omitempty"`
	Path           string `json:"path"`
	Name           string `json:"name"`
	Size           int64  `json:"size"`
	ModifiedNS     int64  `json:"modifiedNs"`
	Directory      bool   `json:"directory"`
	Data           string `json:"data,omitempty"`
}

// FileObjectIdentity observes an acquired descriptor without resolving its
// pathname again. It also works for inherited handles with opaque names.
func FileObjectIdentity(file *os.File) (string, error) {
	object, _, err := observedFileBinding(file)
	return object, err
}

type request struct {
	// PinnedPlayback distinguishes an accepted source-less pin from no session.
	PinnedPlayback  bool             `json:"pinnedPlayback,omitempty"`
	PlaybackBinding *PlaybackBinding `json:"playbackBinding,omitempty"`
	RootHandle      uint64           `json:"rootHandle,omitempty"`
	ManagedArtifact bool             `json:"managedArtifact,omitempty"`
	// Argv is a media tool invocation built by mediaexec in the server, reading
	// its input from descriptor 3 (inventory-command).
	Argv        []string           `json:"argv,omitempty"`
	Watch       *WatchRequest      `json:"watch,omitempty"`
	Inventory   *InventoryRequest  `json:"inventory,omitempty"`
	Inventories []InventoryRequest `json:"inventories,omitempty"`
	// InventoryBudget is the raw-name allowance an inventory-pages-budget
	// batch shares across Inventories.
	InventoryBudget int `json:"inventoryBudget,omitempty"`
	// Members are bounded paths relative to Inventory.RelativePath whose
	// identities a disc title manifest observes.
	Members                []string              `json:"members,omitempty"`
	OptionalMembers        []string              `json:"optionalMembers,omitempty"`
	Directories            []string              `json:"directories,omitempty"`
	LyricsRoot             string                `json:"lyricsRoot,omitempty"`
	LyricsLanguage         string                `json:"lyricsLanguage,omitempty"`
	LyricsProbeArgv        []string              `json:"lyricsProbeArgv,omitempty"`
	ExpectedMountIdentity  string                `json:"expectedMountIdentity,omitempty"`
	ObservedRegistrationID string                `json:"observedRegistrationId,omitempty"`
	ObservedRelativePath   string                `json:"observedRelativePath,omitempty"`
	Stem                   string                `json:"stem,omitempty"`
	Operation              string                `json:"operation"`
	Identity               string                `json:"identity,omitempty"`
	Path                   string                `json:"path"`
	MountedRoot            string                `json:"mountedRoot,omitempty"`
	Limit                  int64                 `json:"limit,omitempty"`
	Size                   int64                 `json:"size,omitempty"`
	ModifiedNS             int64                 `json:"modifiedNs,omitempty"`
	VersionScope           string                `json:"versionScope,omitempty"`
	Version                *mediasource.Evidence `json:"version,omitempty"`
}
type Client struct {
	// RecordingRoot is a canonical, private, server-managed root, never a viewer path.
	RecordingRoot string
	// RecordingLayout classifies exact private publication receipts only. A failed
	// lookup must abort the acquisition, never omit physical artifact leases.
	RecordingLayout  func(context.Context, string) (bool, error)
	Remote           RemoteSources
	SourceOperations *OperationScope
	expectedIdentity string
	Binary           string
	Supervisor       *Supervisor
	Timeout          time.Duration
	Guard            func(string) error
	// SourceGuard applies only when a library source is added or changed.
	// Existing playback, scans and inspection use their own source authority.
	SourceGuard func(string) error
	MountedRoot func(string) string
	// VersionPolicy is configured only for roots with verified filesystem/mutation
	// assumptions. Nil denies new versioned access; legacy access is unchanged.
	VersionPolicy func(string) (LocalVersionPolicy, error)
}

func New(binary string) *Client {
	return &Client{Binary: binary, Supervisor: &Supervisor{}, Timeout: 30 * time.Second}
}
func (c *Client) run(ctx context.Context, key, op, path string, visit func(Snapshot) error) error {
	return c.runWithStem(ctx, key, op, path, "", visit)
}
func (c *Client) runWithStem(ctx context.Context, key, op, path, stem string, visit func(Snapshot) error) error {
	if c.Guard != nil {
		if err := c.Guard(path); err != nil {
			return err
		}
	}
	duration := c.Timeout
	if duration <= 0 {
		duration = 30 * time.Second
	}
	ctx, progress := progressio.NewDormant(ctx, duration)
	defer progress.Close()
	managed, err := c.managedRecording(ctx, path)
	if err != nil {
		return err
	}
	req := request{Stem: stem, Operation: op, Path: path, Identity: c.expectedIdentity, ManagedArtifact: managed}
	if c.MountedRoot != nil {
		req.MountedRoot = c.MountedRoot(path)
	}
	data, _ := json.Marshal(req)
	cmd := exec.Command(c.Binary, "--portico-storage-helper")
	cmd.Stdin = bytes.NewReader(data)
	return c.Supervisor.Run(ctx, filepath.Clean(key), cmd, func(reader io.Reader) error {
		progress.Ping()
		scanner := bufio.NewScanner(progress.Reader(reader))
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			var value Snapshot
			if e := json.Unmarshal(scanner.Bytes(), &value); e != nil {
				return e
			}
			if e := visit(value); e != nil {
				return e
			}
		}
		return scanner.Err()
	})
}
func (c *Client) InspectRoot(ctx context.Context, path string) (Snapshot, error) {
	if c.IsRemote(path) {
		return c.Remote.InspectRoot(ctx, path)
	}
	var value Snapshot
	e := c.run(ctx, path, "root", path, func(v Snapshot) error { value = v; return nil })
	return value, e
}
func (c *Client) Stat(ctx context.Context, key, path string) (Snapshot, error) {
	if c.IsRemote(path) {
		return c.Remote.Stat(ctx, path)
	}
	var value Snapshot
	e := c.run(ctx, key, "stat", path, func(v Snapshot) error { value = v; return nil })
	if e == nil && value.ErrorCode != "" {
		// "There is no such file" is an answer, told apart from a file that
		// could not be looked at: a caller that reports unreadable sidecars
		// must not report every sidecar a title simply does not have.
		return Snapshot{}, inventoryCodeError(value.ErrorCode)
	}
	return value, e
}
func (c *Client) Inventory(ctx context.Context, key, path string, visit func(Snapshot) error) error {
	// Native providers require durable ListPage/CommitPage; never a FUSE fallback.
	if c.IsRemote(path) {
		return ErrRemoteCursor
	}
	return c.run(ctx, key, "inventory", path, visit)
}
func Helper(in io.Reader, out io.Writer) (result error) {
	var r request
	decoder := json.NewDecoder(io.LimitReader(in, 64<<10))
	if e := decoder.Decode(&r); e != nil {
		return e
	}
	if r.MountedRoot != "" {
		check := func() error {
			ready, e := isMount(r.MountedRoot)
			if e != nil || !ready {
				return errors.New("managed mount unavailable")
			}
			return nil
		}
		if e := check(); e != nil {
			return e
		}
		defer func() {
			if result == nil {
				result = check()
			}
		}()
	}
	encoder := json.NewEncoder(out)
	switch r.Operation {
	case "inventory-watch":
		if r.Watch == nil {
			return errors.New("missing watcher request")
		}
		return inventoryWatchHelper(*r.Watch, out)
	case "inventory-page":
		if r.Inventory == nil {
			return errors.New("missing inventory request")
		}
		return inventoryPageHelper(*r.Inventory, out)
	case "inventory-stat":
		if r.Inventory == nil {
			return errors.New("missing inventory request")
		}
		v, e := inspectInventoryObject(*r.Inventory)
		if e != nil {
			return inventoryFailure(e, out)
		}
		return encoder.Encode(v)
	case "inventory-absence":
		if r.Inventory == nil {
			return errors.New("missing inventory request")
		}
		return inventoryAbsenceHelper(*r.Inventory, out)
	case "inventory-pages", "inventory-directories":
		return inventoryBatchHelper(r.Operation, r.Inventories, out)
	case "inventory-pages-budget":
		return inventoryBudgetHelper(r.Inventories, r.InventoryBudget, out)
	case "inventory-directory":
		if r.Inventory == nil {
			return errors.New("missing inventory request")
		}
		v, e := inspectInventoryDirectory(*r.Inventory)
		if e != nil {
			return inventoryFailure(e, out)
		}
		return encoder.Encode(v)
	case "inventory-audio-cover":
		return inventoryAudioCoverHelper(r, out)
	case "inventory-audio-tags":
		return inventoryAudioTagsHelper(r, out)
	case "lyrics-local":
		return lyricLocalHelper(r, out)
	case "mount-identity":
		identity, err := physicalMountIdentity(r.Path)
		if err != nil {
			return err
		}
		return encoder.Encode(Snapshot{Data: identity})
	case "register-root":
		return registerRootHelper(r, io.MultiReader(decoder.Buffered(), in), out)
	case "playback-observed":
		return observedPlaybackHelper(r, io.MultiReader(decoder.Buffered(), in), out)
	case "playback-discover-version", "playback-validate-version":
		return playbackVersionHelper(r, out)
	case "playback-stream":
		return playbackStreamHelper(r, io.MultiReader(decoder.Buffered(), in), out)
	case "playback-validate-descriptor":
		return playbackValidateDescriptorHelper(r, out)
	case "playback-descriptor", "disc-file-descriptor", "disc-directory-descriptor":
		return playbackDescriptorHelper(r, out)
	case "root":
		if err := checkLocalRoot(r.Path); err != nil {
			return err
		}
		path, e := filepath.EvalSymlinks(r.Path)
		if e != nil {
			return e
		}
		path, e = filepath.Abs(path)
		if e != nil {
			return e
		}
		info, e := os.Stat(path)
		if e != nil {
			return e
		}
		if !info.IsDir() {
			return errors.New("not a directory")
		}
		return encoder.Encode(snapshot(path, info))
	case "inventory-audio-prefix":
		return inventoryAudioPrefixHelper(r, out)
	case "inventory-blob", "inventory-subtitle-blob":
		return inventoryBlobHelper(r, out)
	case "inventory-command":
		return inventoryCommandHelper(r)
	case "inventory-directory-command":
		return inventoryDirectoryCommandHelper(r)
	case "disc-members":
		if r.Inventory == nil {
			return errors.New("missing inventory request")
		}
		members, e := discMemberManifest(*r.Inventory, r.Members, r.OptionalMembers, r.Directories)
		if e != nil {
			return inventoryFailure(e, out)
		}
		return encoder.Encode(members)
	case "blob", "subtitle-blob":
		maximum := int64(8 << 20)
		if r.Operation == "subtitle-blob" && binarySubtitlePath(r.Path) {
			maximum = 512 << 20
		}
		if r.Limit <= 0 || r.Limit > maximum {
			return errors.New("invalid blob limit")
		}
		info, e := os.Lstat(r.Path)
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() || info.Size() > r.Limit {
			return errors.New("invalid local metadata file")
		}
		f, e := os.Open(r.Path)
		if e != nil {
			return e
		}
		defer f.Close()
		if e = lockRecordingFile(r, f); e != nil {
			return e
		}
		n, e := io.Copy(out, io.LimitReader(f, r.Limit+1))
		if n > r.Limit {
			return errors.New("local metadata exceeds limit")
		}
		return e
	case "remove-mount-root":
		if r.Identity != "" {
			actual, e := DirectoryIdentity(r.Path)
			if errors.Is(e, os.ErrNotExist) {
				return encoder.Encode(Snapshot{})
			}
			if e != nil || actual != r.Identity {
				return errors.New("allocation identity changed")
			}
		}
		mounted, err := isMount(r.Path)
		if errors.Is(err, os.ErrNotExist) {
			return encoder.Encode(Snapshot{Path: r.Path})
		}
		if err != nil {
			return err
		}
		if mounted {
			return errors.New("mount remains active")
		}
		if err = os.Remove(r.Path); err != nil {
			return err
		}
		return encoder.Encode(Snapshot{Path: r.Path})
	case "mount-status":
		mounted, err := isMount(r.Path)
		if err != nil {
			return err
		}
		return encoder.Encode(Snapshot{Path: r.Path, Directory: mounted})
	case "descriptor":
		info, e := os.Lstat(r.Path)
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() || info.Size() > 64<<10 {
			return errors.New("invalid descriptor")
		}
		file, e := os.Open(r.Path)
		if e != nil {
			return e
		}
		defer file.Close()
		if e = lockRecordingFile(r, file); e != nil {
			return e
		}
		raw, e := io.ReadAll(io.LimitReader(file, (64<<10)+1))
		if e != nil || len(raw) > 64<<10 {
			return errors.New("descriptor exceeds budget")
		}
		result := snapshot(r.Path, info)
		result.Data = string(raw)
		return encoder.Encode(result)
	case "stat":
		info, e := os.Lstat(r.Path)
		if errors.Is(e, os.ErrNotExist) {
			return encoder.Encode(Snapshot{ErrorCode: inventoryErrorCode(e)})
		}
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return errors.New("not a regular file")
		}
		return encoder.Encode(snapshot(r.Path, info))
	case "inventory", "subtitle-candidates":
		if r.Operation == "subtitle-candidates" && (r.Stem == "" || len(r.Stem) > 4096 || strings.ContainsAny(r.Stem, "/\\\x00")) {
			return errors.New("invalid subtitle association")
		}

		f, e := os.Open(r.Path)
		if e != nil {
			return e
		}
		defer f.Close()
		if e = lockRecordingFile(r, f); e != nil {
			return e
		}
		for {
			entries, e := f.ReadDir(128)
			if e != nil && e != io.EOF {
				return e
			}
			for _, entry := range entries {
				if r.Operation == "subtitle-candidates" && !subtitleCandidate(entry.Name(), entry.IsDir(), r.Stem) {
					continue
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
					continue
				}
				if err = encoder.Encode(snapshot(filepath.Join(r.Path, entry.Name()), info)); err != nil {
					return err
				}
			}
			if e == io.EOF {
				return nil
			}
		}
	default:
		return errors.New("unknown storage operation")
	}
}
func snapshot(path string, info os.FileInfo) Snapshot {
	v := Snapshot{Path: path, Name: info.Name(), Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Directory: info.IsDir()}
	v.ObjectIdentity, v.ChangeToken, _ = observedObjectBinding(info, path)
	return v
}

// observedSnapshot is snapshot for a member whose identity is the witness
// itself: an observation that cannot bind the object fails instead of returning
// an empty identity that would silently weaken the manifest.
func observedSnapshot(path string, info os.FileInfo) (Snapshot, error) {
	v := Snapshot{Path: path, Name: info.Name(), Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Directory: info.IsDir()}
	var err error
	v.ObjectIdentity, v.ChangeToken, err = observedObjectBinding(info, path)
	return v, err
}

func (c *Client) ReadDescriptor(ctx context.Context, key, path string) (Snapshot, error) {
	var out Snapshot
	e := c.run(ctx, "playback:"+key, "descriptor", path, func(v Snapshot) error { out = v; return nil })
	return out, e
}

// Mounted uses an isolated helper without the data-availability guard: it is the
// observation that drives the guard itself.
func (c *Client) Mounted(ctx context.Context, path string) (bool, error) {
	raw := *c
	raw.Guard = nil
	raw.MountedRoot = nil
	var value Snapshot
	err := raw.run(ctx, "mount-status:"+path, "mount-status", path, func(v Snapshot) error { value = v; return nil })
	return value.Directory, err
}

func (c *Client) ReadSmall(ctx context.Context, key, path string, limit int64) ([]byte, error) {
	return c.readBlob(ctx, key, path, limit, false)
}

// ReadSubtitleBinary is a distinct, bounded contract for SUP and VobSub packets.
// Metadata readers retain their smaller limits and the same rooted custody.
func (c *Client) ReadSubtitleBinary(ctx context.Context, key, path string, limit int64) ([]byte, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if (ext != ".sup" && ext != ".sub") || limit <= 0 || limit > 512<<20 {
		return nil, errors.New("invalid subtitle binary read")
	}
	return c.readBlob(ctx, key, path, limit, true)
}
func (c *Client) readBlob(ctx context.Context, key, path string, limit int64, subtitle bool) ([]byte, error) {
	if c.IsRemote(path) {
		if subtitle {
			return c.readRemoteBlob(ctx, path, limit, 512<<20)
		}
		return c.readRemoteSmall(ctx, path, limit)
	}
	if err := CheckScanRead(ctx, path); err != nil {
		return nil, err
	}
	if c.Guard != nil {
		if err := c.Guard(path); err != nil {
			return nil, err
		}
	}
	ctx, progress := progressio.NewDormant(ctx, 30*time.Second)
	defer progress.Close()
	managed, err := c.managedRecording(ctx, path)
	if err != nil {
		return nil, err
	}
	req := request{Operation: "blob", Path: path, Limit: limit, ManagedArtifact: managed}
	if subtitle {
		req.Operation = "subtitle-blob"
	}
	root, rooted, err := inventoryReadRequest(ctx, path)
	if err != nil {
		return nil, err
	}
	if rooted != nil {
		req.Operation = "inventory-blob"
		if subtitle {
			req.Operation = "inventory-subtitle-blob"
		}
		req.Inventory = &root
	}
	if c.MountedRoot != nil {
		req.MountedRoot = c.MountedRoot(path)
	}
	input, _ := json.Marshal(req)
	cmd := exec.Command(c.Binary, "--portico-storage-helper")
	cmd.Stdin = bytes.NewReader(input)
	var raw []byte
	err = c.Supervisor.Run(ctx, key, cmd, func(r io.Reader) error {
		var err error
		progress.Ping()
		raw, err = io.ReadAll(io.LimitReader(progress.Reader(r), limit+1))
		if int64(len(raw)) > limit {
			return errors.New("local metadata exceeds limit")
		}
		return err
	})
	return raw, err
}

// RemoveMountRoot is idempotent empty-directory removal behind the same bounded
// subprocess boundary as mount observation. It never recursively deletes files.
func (c *Client) RemoveMountRoot(ctx context.Context, path string) error {
	raw := *c
	raw.Guard = nil
	raw.MountedRoot = nil
	return raw.run(ctx, "mount-remove:"+path, "remove-mount-root", path, func(Snapshot) error { return nil })
}

func (c *Client) RemoveOwnedMountRoot(ctx context.Context, path, identity string) error {
	if identity == "" {
		return os.ErrInvalid
	}
	copy := *c
	copy.expectedIdentity = identity
	return copy.RemoveMountRoot(ctx, path)
}

func binarySubtitlePath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".sup" || ext == ".sub"
}

// InventorySubtitleCandidates streams bounded directory pages but emits only
// exact-stem subtitle candidates and the two conventional subtitle folders.
// Unrelated entries do not consume the caller's admitted-candidate bound.
func (c *Client) InventorySubtitleCandidates(ctx context.Context, key, path, stem string, visit func(Snapshot) error) error {
	if c.IsRemote(path) {
		return ErrRemoteCursor
	}
	return c.runWithStem(ctx, key, "subtitle-candidates", path, stem, visit)
}
func subtitleCandidate(name string, directory bool, stem string) bool {
	if directory {
		return strings.EqualFold(name, "Subs") || strings.EqualFold(name, "Subtitles")
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if !(strings.EqualFold(base, stem) || strings.HasPrefix(strings.ToLower(base), strings.ToLower(stem)+".")) {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".srt", ".vtt", ".ass", ".ssa", ".sub", ".sup", ".idx", ".smi", ".sami", ".ttml", ".dfxp":
		return true
	}
	return false
}
