package workload

import (
	"io"
	"os"
	"reflect"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// WALSnapshot is advisory raw telemetry, never an admission publication, reset
// classification or continuous WAL-bound proof. All metadata reads are inside
// the gate-token bracket. Equal tokens do not make the reads atomic: Publish
// can update event metadata without advancing that token.
type WALSnapshot struct {
	DatabasePath                       string
	TokenBeforeAt, TokenAfterAt        time.Time // immediately after the respective getter returns
	TokenBefore, TokenAfter            *uint64
	State                              *governor.WALState
	ResetCount                         *uint64
	LastRestart                        *governor.WALRestartObservation
	ReadStarted, ReadReturned          time.Time
	ReadNS                             time.Duration
	MetadataStarted, MetadataReturned  time.Time
	MetadataNS                         time.Duration
	FileBefore, FileAfter              *WALFileObservation
	TokensEqual, SameFile, HeaderEqual *bool
	StateError                         string
}

// WALFileIdentity is an observed OS device/inode pair when available. It is
// not W's file/opening capability. QL still needs the actual database/owner join.
type WALFileIdentity struct{ Device, Inode uint64 }

// WALFileObservation preserves a bounded raw header, including a partial read.
// A present zero byte length or empty header is observed, not a missing value.
// File reads may race the writer; comparisons describe only these two probes.
type WALFileObservation struct {
	Started, Returned                             time.Time
	ElapsedNS                                     time.Duration
	Bytes                                         *int64
	Identity                                      *WALFileIdentity
	IdentityUnavailable                           string
	Header                                        []byte
	OpenError, StatError, HeaderError, CloseError string
	info                                          os.FileInfo // transient comparison only; no OS handle escapes
}

type walGateObserver interface {
	Observation() uint64
	ResetCount() uint64
	LastRestartObservation() governor.WALRestartObservation
}

func observeWAL(path string, gate walGateObserver, read func(string) (governor.WALState, error), now func() time.Time) (*WALSnapshot, error) {
	result := &WALSnapshot{DatabasePath: path}
	before := gate.Observation()
	result.TokenBefore = &before // generation zero is a genuine observation
	result.TokenBeforeAt = now()
	result.FileBefore = observeWALFile(path+"-wal", now)
	result.ReadStarted = now()
	state, err := read(path) // GV remains the sole WAL/SHM parser
	result.ReadReturned = now()
	result.ReadNS = result.ReadReturned.Sub(result.ReadStarted)
	if err != nil {
		result.StateError = err.Error()
	} else {
		result.State = &state
	}
	result.FileAfter = observeWALFile(path+"-wal", now)
	result.MetadataStarted = now()
	resets, restart := gate.ResetCount(), gate.LastRestartObservation()
	result.ResetCount, result.LastRestart = &resets, &restart
	result.MetadataReturned = now()
	result.MetadataNS = result.MetadataReturned.Sub(result.MetadataStarted)
	after := gate.Observation()
	result.TokenAfter = &after
	result.TokenAfterAt = now()
	equal := before == after
	result.TokensEqual = &equal
	if result.FileBefore.info != nil && result.FileAfter.info != nil {
		same := os.SameFile(result.FileBefore.info, result.FileAfter.info)
		result.SameFile = &same
	}
	if result.FileBefore.HeaderError == "" && result.FileAfter.HeaderError == "" && result.FileBefore.Header != nil && result.FileAfter.Header != nil {
		same := string(result.FileBefore.Header) == string(result.FileAfter.Header)
		result.HeaderEqual = &same
	}
	result.FileBefore.info, result.FileAfter.info = nil, nil
	return result, err
}

func observeWALFile(path string, now func() time.Time) *WALFileObservation {
	out := &WALFileObservation{Started: now()}
	defer func() { out.Returned = now(); out.ElapsedNS = out.Returned.Sub(out.Started) }()
	file, err := os.Open(path)
	if err != nil {
		out.OpenError = err.Error()
		return out
	}
	info, err := file.Stat()
	if err != nil {
		out.StatError = err.Error()
	} else {
		out.info = info
		size := info.Size()
		out.Bytes = &size
		out.Identity = walFileIdentity(info)
		if out.Identity == nil {
			out.IdentityUnavailable = "OS file device/inode diagnostic unavailable"
		}
	}
	header := make([]byte, 32)
	n, readErr := io.ReadFull(file, header)
	out.Header = header[:n]
	if readErr != nil {
		out.HeaderError = readErr.Error()
	}
	if err := file.Close(); err != nil {
		out.CloseError = err.Error()
	}
	return out
}

// Read only the conventional OS stat diagnostic. No driver reflection, handle,
// filename opening shortcut or authority is introduced on unsupported systems.
func walFileIdentity(info os.FileInfo) *WALFileIdentity {
	value := reflect.ValueOf(info.Sys())
	if value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
		return nil
	}
	stat := value.Elem()
	device, inode := stat.FieldByName("Dev"), stat.FieldByName("Ino")
	if !device.IsValid() || !inode.IsValid() || !device.CanUint() || !inode.CanUint() {
		return nil
	}
	return &WALFileIdentity{Device: device.Uint(), Inode: inode.Uint()}
}
