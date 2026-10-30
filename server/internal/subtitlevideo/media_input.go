package subtitlevideo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"sort"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// OpenMediaInput shares the existing retained, revision-checked source adapter;
// it does not create a subtitle session or interpret subtitle resource identity.
func (r *Runtime) OpenMediaInput(ctx context.Context, item, source, owner string) (subtitles.RenderInput, error) {
	// Optimization owns its authority independently; owner is not a playback session ID.
	return r.OpenSubtitleInput(ctx, item, source, "")
}

// MediaBridge keeps one acquisition's extent ledger across fresh physical
// decoder endpoints. A repeated read may not silently change within a job.
type MediaBridge struct {
	input  subtitles.RenderInput
	ledger extentLedger
}

func NewMediaBridge(input subtitles.RenderInput) *MediaBridge { return &MediaBridge{input: input} }

// HeaderPrefix observes the same first extent as the decoder bridge. Header
// completion cannot introduce a second, unfenced view of a remote acquisition.
func (m *MediaBridge) HeaderPrefix(ctx context.Context) ([]byte, error) {
	n := min(m.input.Size(), int64(1<<20))
	if n <= 0 {
		return nil, io.ErrUnexpectedEOF
	}
	b, err := m.input.ReadExtent(ctx, 0, n)
	if err != nil {
		return nil, err
	}
	if int64(len(b)) != n {
		return nil, io.ErrUnexpectedEOF
	}
	if err = m.ledger.check(0, b); err != nil {
		return nil, err
	}
	return b, m.input.Validate(ctx)
}
func (m *MediaBridge) With(ctx context.Context, run func(string, *decoder.EndpointReservation) error) error {
	b, e := openBridge(ctx, m.input, &m.ledger)
	if e != nil {
		return e
	}
	defer b.Close()
	return run(b.url, b.reservation) // run must wait for physical process retirement
}
func (m *MediaBridge) Evidence() string {
	m.ledger.mu.Lock()
	defer m.ledger.mu.Unlock()
	offsets := make([]int64, 0, len(m.ledger.digests))
	for k := range m.ledger.digests {
		offsets = append(offsets, k)
	}
	sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })
	type extent struct {
		Offset int64
		Digest [32]byte
	}
	xs := make([]extent, 0, len(offsets))
	for _, k := range offsets {
		xs = append(xs, extent{k, m.ledger.digests[k]})
	}
	b, _ := json.Marshal(struct {
		Acquisition string
		Extents     []extent
	}{m.input.Evidence(), xs})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
