package strm

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

const playlistLimit = 1 << 20

// Playlist is an HLS target. The server reads the playlist and every address in it itself;
// the decoder is given the playlist through the input bridge with each address replaced by a
// piece of the bridge (ServeHLS), so it never learns or reaches the target. A master playlist
// is read as its variant of the highest bandwidth. A playlist has no bytes of its own.
type Playlist struct {
	client  *Client
	ctx     context.Context
	cancel  context.CancelFunc
	address string // the media playlist
	mu      sync.Mutex
	pieces  []string // what each piece number stands for
	numbers map[string]int
}

// openPlaylist reads the playlist once, to know it is one and to pick a master's variant. A
// live playlist (one with no end) is refused: playback needs a title's length.
func (c *Client) openPlaylist(ctx context.Context, address string) (*Playlist, error) {
	ctx, cancel := context.WithCancel(ctx)
	p := &Playlist{client: c, ctx: ctx, cancel: cancel, address: address, numbers: map[string]int{}}
	text, base, err := p.fetch(address)
	if err == nil && bestVariant(text, base) != "" {
		p.address = bestVariant(text, base)
		if text, base, err = p.fetch(p.address); err == nil && bestVariant(text, base) != "" {
			err = ErrRange // a master naming only other masters
		}
	}
	if err == nil && !strings.Contains(text, "#EXT-X-ENDLIST") {
		err = ErrRange
	}
	if err != nil {
		cancel()
		return nil, err
	}
	return p, nil
}

func (p *Playlist) Size() int64                       { return 0 }
func (p *Playlist) ReadAt([]byte, int64) (int, error) { return 0, ErrRange }
func (p *Playlist) Close() error                      { p.cancel(); return nil }

// LimitProbe has nothing to hold: a probe reads only a playlist's first pieces, bounded by
// ffprobe's own probe size and the probe's time limit.
func (p *Playlist) LimitProbe() {}

// fetch reads a playlist and answers it with the address it came from after any redirect,
// which its relative addresses are relative to.
func (p *Playlist) fetch(address string) (string, *url.URL, error) {
	ctx, cancel := context.WithTimeout(p.ctx, ReadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return "", nil, ErrPointer
	}
	resp, err := p.client.do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Encoding") != "" {
		return "", nil, ErrRange // an answer that is not a whole playlist
	}
	text, err := io.ReadAll(io.LimitReader(resp.Body, playlistLimit+1))
	if err != nil {
		return "", nil, ErrUnavailable
	}
	if len(text) > playlistLimit {
		return "", nil, ErrLimit
	}
	text = bytes.TrimSpace(bytes.TrimPrefix(text, []byte("\xef\xbb\xbf")))
	if !bytes.HasPrefix(text, []byte("#EXTM3U")) {
		return "", nil, ErrRange // neither a playlist nor a file that can be read by range
	}
	return string(text), resp.Request.URL, nil
}

// bestVariant is a master playlist's variant of the highest bandwidth, "" for a media
// playlist. Its sound is what the variant itself carries: a separate sound rendition
// (EXT-X-MEDIA) is not followed.
func bestVariant(text string, base *url.URL) string {
	best, bandwidth, variant := "", int64(-1), int64(-1)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "#EXT-X-STREAM-INF:"); ok {
			variant = 0
			for _, attribute := range strings.Split(value, ",") {
				if n, ok := strings.CutPrefix(attribute, "BANDWIDTH="); ok {
					variant, _ = strconv.ParseInt(n, 10, 64)
				}
			}
		} else if line != "" && !strings.HasPrefix(line, "#") && variant >= 0 {
			if u, err := base.Parse(line); err == nil && variant > bandwidth {
				best, bandwidth = u.String(), variant
			}
			variant = -1
		}
	}
	return best
}

// ServeHLS answers the input bridge, which serves the playlist at /input/<token>.m3u8 and its
// pieces beneath /input/<token>/. Piece "" is the media playlist, fetched again and
// rewritten; any other piece is "<number>.ts", one address the playlist named, fetched as
// the decoder asks for it (a byte range included). Every piece is answered as plain bytes:
// FFmpeg does not read a ".ts" of no media type as a playlist, so a piece cannot send the
// decoder anywhere. The ".ts" passes FFmpeg's extension checks for MPEG-TS, MP4 and AAC pieces.
func (p *Playlist) ServeHLS(w http.ResponseWriter, r *http.Request, token, piece string) {
	if piece == "" {
		text, base, err := p.fetch(p.address)
		if err != nil || bestVariant(text, base) != "" {
			http.Error(w, "playlist unavailable", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = io.WriteString(w, p.rewrite(text, base, token))
		return
	}
	number, _ := strings.CutSuffix(piece, ".ts")
	n, err := strconv.Atoi(number)
	address := ""
	p.mu.Lock()
	if err == nil && strconv.Itoa(n) == number && number+".ts" == piece && n >= 0 && n < len(p.pieces) {
		address = p.pieces[n]
	}
	p.mu.Unlock()
	u, err := url.Parse(address)
	if err != nil || !allowedURL(u) {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer context.AfterFunc(p.ctx, cancel)()
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if byteRange := r.Header.Get("Range"); byteRange != "" {
		req.Header.Set("Range", byteRange)
	}
	resp, err := p.client.do(req)
	if err != nil {
		http.Error(w, "piece unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent || resp.Header.Get("Content-Encoding") != "" {
		http.Error(w, "piece unavailable", http.StatusBadGateway)
		return
	}
	for _, header := range []string{"Content-Length", "Content-Range"} {
		if value := resp.Header.Get(header); value != "" {
			w.Header().Set(header, value)
		}
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, &body{Reader: resp.Body, remaining: max(resp.ContentLength, 0), cancel: cancel, close: resp.Body.Close})
}

// rewrite gives the decoder the playlist with each address in it replaced by a piece. In a
// media playlist a decoder follows its pieces, EXT-X-MAP and EXT-X-KEY; a line naming any
// other address (low-latency parts and hints) is left out, so nothing in it leads elsewhere.
func (p *Playlist) rewrite(text string, base *url.URL, token string) string {
	var out strings.Builder
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			continue
		case !strings.HasPrefix(line, "#"):
			line = token + "/" + p.piece(base, line)
		case strings.Contains(line, "URI="):
			tag, _, _ := strings.Cut(line, ":")
			before, rest, quoted := strings.Cut(line, `URI="`)
			address, after, closed := strings.Cut(rest, `"`)
			if tag != "#EXT-X-MAP" && tag != "#EXT-X-KEY" || !quoted || !closed || strings.Contains(before+after, "URI=") {
				continue
			}
			line = before + `URI="` + token + "/" + p.piece(base, address) + `"` + after
		}
		out.WriteString(line + "\n")
	}
	return out.String()
}

// piece names one address by its number, the same each time the playlist is read. The name
// is relative to the playlist's address on the bridge.
func (p *Playlist) piece(base *url.URL, address string) string {
	if u, err := base.Parse(address); err == nil {
		address = u.String()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	n, ok := p.numbers[address]
	if !ok {
		n = len(p.pieces)
		p.pieces = append(p.pieces, address)
		p.numbers[address] = n
	}
	return strconv.Itoa(n) + ".ts"
}
