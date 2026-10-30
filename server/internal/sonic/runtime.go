package sonic

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

const (
	ModelID      = "musicnn-mtt"
	ModelVersion = "516acb2a0ff5ef73f64547898e018e793152c506-onnx1"
	ModelSHA256  = "59876d9801156711a7051b4496f45ccfeeac598cf79df02968238d6318627f8a"
	TagCount     = 50
)

var ErrUnavailable = errors.New("sonic: inference runtime unavailable")

type Identity struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

type Availability struct {
	State  string    `json:"state"`
	Reason string    `json:"reason,omitempty"`
	Model  *Identity `json:"model,omitempty"`
}

type RuntimeOptions struct {
	Helper, ModelDirectory string
	Libraries              []string
	Supervisor             *storage.Supervisor
}

type Runtime struct {
	options      RuntimeOptions
	model        string
	identity     Identity
	helperDigest string
	mu           sync.RWMutex
	ready        bool
	failed       bool
}

type Analysis struct {
	Model     Identity
	Embedding Embedding
	Tags      [TagCount]float32
	Patches   int
}

type modelManifest struct {
	Identity
	Licence, Format, SourceCommit, Preprocessing string
	Input, Embedding, Tags                       string
	Frames, Mels, Dimensions, TagCount           int
	Bytes                                        int64
}

// OpenRuntime validates installation bytes but makes no inference/CPU claim.
// Only a successful out-of-process Run marks it ready. Missing or unsupported
// installations never prevent the media server from starting.
func OpenRuntime(options RuntimeOptions) (*Runtime, error) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" || options.Supervisor == nil {
		return nil, ErrUnavailable
	}
	for _, path := range []string{options.Helper, options.ModelDirectory} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return nil, ErrUnavailable
		}
	}
	file, err := os.Open(filepath.Join(options.ModelDirectory, "model.json"))
	if err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	data, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	file.Close()
	var model modelManifest
	if err != nil || len(data) > 16<<10 || json.Unmarshal(data, &model) != nil ||
		model.ID != ModelID || model.Version != ModelVersion || model.Licence != "ISC" || model.Format != "onnx" ||
		model.SourceCommit != "516acb2a0ff5ef73f64547898e018e793152c506" || model.Preprocessing != "musicnn-logmel-1" ||
		model.Input != "log_mel" || model.Embedding != "embedding" || model.Tags != "tags" ||
		model.Frames != PatchFrames || model.Mels != MelBands || model.Dimensions != Dimensions || model.TagCount != TagCount ||
		model.Bytes != 3193128 || model.SHA256 != ModelSHA256 {
		return nil, ErrUnavailable
	}
	if digest, err := hex.DecodeString(model.SHA256); err != nil || len(digest) != sha256.Size {
		return nil, ErrUnavailable
	}
	r := &Runtime{options: options, model: filepath.Join(options.ModelDirectory, "model.onnx"), identity: model.Identity}
	r.helperDigest, err = helperDigest(options.Helper)
	if err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	opened, err := r.openModel()
	if err != nil {
		return nil, err
	}
	stat, err := opened.Stat()
	opened.Close()
	if err != nil || stat.Size() != model.Bytes {
		return nil, ErrUnavailable
	}
	return r, nil
}

func (r *Runtime) Identity() Identity { return r.identity }

func (r *Runtime) HelperDigest() string { return r.helperDigest }

func helperDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() < 1 || before.Size() > 128<<20 {
		return "", ErrUnavailable
	}
	h := sha256.New()
	if _, err = io.Copy(h, io.LimitReader(f, (128<<20)+1)); err != nil {
		return "", err
	}
	after, err := f.Stat()
	if err != nil || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		return "", ErrUnavailable
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (r *Runtime) Availability() Availability {
	if r == nil {
		return Availability{State: "unavailable", Reason: "runtime_or_model_missing"}
	}
	r.mu.RLock()
	ready, failed := r.ready, r.failed
	r.mu.RUnlock()
	model := r.identity
	if failed {
		return Availability{State: "unavailable", Reason: "inference_failed", Model: &model}
	}
	if !ready {
		return Availability{State: "unavailable", Reason: "inference_not_verified", Model: &model}
	}
	return Availability{State: "ready", Model: &model}
}

// CanAttempt is installation/admission state, not an inference success claim.
func (r *Runtime) CanAttempt() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return !r.failed
}

func (r *Runtime) openModel() (*os.File, error) {
	f, err := os.Open(r.model)
	if err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() < 1 || before.Size() > 32<<20 {
		f.Close()
		return nil, ErrUnavailable
	}
	h := sha256.New()
	_, err = io.Copy(h, io.LimitReader(f, (32<<20)+1))
	after, statErr := f.Stat()
	if err != nil || statErr != nil || before.Size() != after.Size() || before.ModTime() != after.ModTime() || hex.EncodeToString(h.Sum(nil)) != r.identity.SHA256 {
		f.Close()
		return nil, ErrUnavailable
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// Run streams numeric patches to one model process for the entire track. The
// existing durable analysis allocation is inherited by the child, so cancel
// cannot release physical ownership before actual process retirement. It does
// not claim a second background slot while its decoder holds the first one.
func (r *Runtime) Run(ctx context.Context, key string, physical *os.File, produce func(func([]float32) error) error) (result Analysis, err error) {
	if !r.CanAttempt() || produce == nil || key == "" {
		return result, ErrUnavailable
	}
	defer func() {
		// Cancellation/window pauses and a bad recording do not erase a prior
		// successful runtime check. Loader/model/protocol failures do.
		if errors.Is(err, ErrUnavailable) {
			r.mu.Lock()
			r.ready = false
			r.failed = true
			r.mu.Unlock()
		}
	}()
	if digest, err := helperDigest(r.options.Helper); err != nil || digest != r.helperDigest {
		return result, ErrUnavailable
	}
	model, err := r.openModel()
	if err != nil {
		return result, err
	}
	defer model.Close()
	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	job := mediaexec.Job{Executable: r.options.Helper, Background: true, MaxFileBytes: mediaexec.NoOutputFiles, MaxCPUSeconds: 24 * 60 * 60, Libraries: r.options.Libraries}
	if runtime.GOOS == "windows" {
		job.Args = []string{"--model", r.model}
		job.ReadPaths = []string{r.model}
	} else {
		job.Args = []string{"--model", "/dev/fd/3"}
		job.Files = []*os.File{model}
	}
	if physical != nil {
		job.Files = append(job.Files, physical)
	}
	cmd, err := mediaexec.CommandContext(ctx, job)
	if err != nil {
		return result, errors.Join(ErrUnavailable, err)
	}
	input, writer := io.Pipe()
	cmd.Stdin = input
	produced := 0
	producerDone := make(chan error, 1)
	firstPatch := make(chan struct{})
	var signalPatch sync.Once
	supervise.Go("sonic.feature-producer", func() {
		producerErr := ErrEmbedding
		defer func() { writer.CloseWithError(producerErr); producerDone <- producerErr }()
		producerErr = produce(func(patch []float32) error {
			if len(patch) != PatchFrames*MelBands || produced >= 65536 {
				return ErrEmbedding
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			data := make([]byte, len(patch)*4)
			for i, v := range patch {
				if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) || v < 0 || v > 16 {
					return ErrEmbedding
				}
				binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(v))
			}
			signalPatch.Do(func() { close(firstPatch) })
			if _, err := writer.Write(data); err != nil {
				return err
			}
			produced++
			return nil
		})
	})
	if physical != nil {
		// The decoder must own its ordinary background permit before the
		// numeric helper joins that same durable allocation. Starting the
		// helper first would occupy the last generic permit and deadlock its
		// own decoder. One validated patch proves the decoder has started;
		// the pipe then bounds it while the helper loads, without buffering a
		// recording or inventing a second admission/scheduler.
		select {
		case <-firstPatch:
		case producerErr := <-producerDone:
			input.Close()
			writer.Close()
			if producerErr != nil {
				return Analysis{}, producerErr
			}
			return Analysis{}, ErrEmbedding
		case <-ctx.Done():
			input.Close()
			writer.Close()
			<-producerDone
			return Analysis{}, ctx.Err()
		}
	}
	var sums [Dimensions + TagCount]float64
	consume := func(output io.Reader) error {
		var header [8]byte
		if _, err := io.ReadFull(output, header[:]); err != nil || string(header[:]) != "PSNC0001" {
			return ErrUnavailable
		}
		data := make([]byte, (Dimensions+TagCount)*4)
		for {
			_, err := io.ReadFull(output, data)
			if err == io.EOF {
				return nil
			}
			if err != nil || result.Patches >= 65536 {
				return ErrEmbedding
			}
			for i := range sums {
				v := math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
				if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) || i >= Dimensions && (v < 0 || v > 1) {
					return ErrEmbedding
				}
				sums[i] += float64(v)
			}
			result.Patches++
		}
	}
	retired := make(chan struct{})
	if physical != nil {
		err = r.options.Supervisor.RunOwnedLinear(ctx, "analysis:sonic:"+key, cmd, consume, nil, func() { close(retired) })
	} else {
		err = r.options.Supervisor.RunOwnedCompletion(ctx, "analysis:sonic:"+key, cmd, consume, nil, func() { close(retired) })
	}
	cancel()
	input.Close()
	writer.Close()
	producerErr := <-producerDone
	<-retired
	if parent.Err() != nil {
		return Analysis{}, parent.Err()
	}
	if producerErr != nil && !errors.Is(producerErr, io.ErrClosedPipe) && !errors.Is(producerErr, context.Canceled) {
		return Analysis{}, producerErr
	}
	if err != nil || produced == 0 || result.Patches != produced {
		return Analysis{}, errors.Join(ErrUnavailable, err)
	}
	if producerErr != nil {
		return Analysis{}, producerErr
	}
	if digest, err := helperDigest(r.options.Helper); err != nil || digest != r.helperDigest {
		return Analysis{}, ErrUnavailable
	}
	if runtime.GOOS == "windows" {
		checked, e := r.openModel()
		if e != nil {
			return Analysis{}, e
		}
		checked.Close()
	}
	var values [Dimensions]float32
	for i := range values {
		values[i] = float32(sums[i] / float64(result.Patches))
	}
	result.Embedding, err = Normalize(values[:])
	if err != nil {
		return Analysis{}, err
	}
	for i := range result.Tags {
		result.Tags[i] = float32(sums[Dimensions+i] / float64(result.Patches))
	}
	result.Model = r.identity
	r.mu.Lock()
	r.ready = true
	r.mu.Unlock()
	return result, nil
}
