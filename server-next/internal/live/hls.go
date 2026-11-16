package live

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Most IPTV channels are HLS: a playlist naming short MPEG-TS pieces, renewed as the channel
// goes on, rather than one stream. The input follows the playlist and writes the pieces in
// order, as one stream, so everything after the connection (sharing, rewind, recording, the
// player) is the same as for a source that sends MPEG-TS.

const (
	hlsPlaylistLimit = 1 << 20
	hlsFetchFor      = 30 * time.Second // the most one playlist or piece may take
	hlsStartBack     = 3                // a channel starts this many pieces behind the newest, as players do
)

type hlsPlaylist struct {
	variant    *url.URL // a master playlist's variant of the highest bandwidth
	pieces     []hlsPiece
	target     time.Duration
	ended      bool
	unreadable string // why the pieces cannot be read as MPEG-TS
}

type hlsPiece struct {
	sequence int64
	address  *url.URL
}

// followHLS follows a playlist the source answered with (its first answer is body, from
// address) until the channel ends, the playlist stops moving, or reading it fails.
func (in *Input) followHLS(ctx context.Context, address *url.URL, body io.Reader, w *segmentWriter) error {
	text, err := io.ReadAll(io.LimitReader(body, hlsPlaylistLimit))
	if err != nil {
		return err
	}
	list := parseHLS(text, address)
	if list.variant != nil {
		if text, address, err = in.hlsPlaylist(ctx, list.variant); err != nil {
			return err
		}
		if list = parseHLS(text, address); list.variant != nil {
			return errors.New("the source's playlist names only other playlists")
		}
	}
	var quiet time.Time // since when the playlist has brought nothing new
	for {
		if list.unreadable != "" {
			return fmt.Errorf("the source sends %s, which this server cannot read", list.unreadable)
		}
		// The first time, and when the source has started its numbering again, begin a few
		// pieces behind the newest.
		if n := len(list.pieces); n > 0 && (!in.hlsStarted || list.pieces[n-1].sequence < in.hlsLast) {
			in.hlsLast, in.hlsStarted = list.pieces[max(n-hlsStartBack, 0)].sequence-1, true
		}
		moved := false
		for _, piece := range list.pieces {
			if piece.sequence <= in.hlsLast {
				continue
			}
			// A piece that cannot be fetched is a moment lost, not the channel; but a playlist
			// whose pieces never come is a channel that stopped.
			if in.copyPiece(ctx, piece.address, w) {
				moved = true
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			in.hlsLast = piece.sequence
		}
		if list.ended {
			return io.EOF
		}
		now := in.t.now()
		if moved || quiet.IsZero() {
			quiet = now
		} else if now.Sub(quiet) >= 3*list.target {
			return errors.New("the source's playlist stopped moving")
		}
		// As players do: wait a piece's length after a change, half of it when nothing changed.
		wait := list.target
		if !moved {
			wait /= 2
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		if text, address, err = in.hlsPlaylist(ctx, address); err != nil {
			return err
		}
		if list = parseHLS(text, address); list.variant != nil {
			return errors.New("the source's playlist names only other playlists")
		}
	}
}

// hlsPlaylist fetches a playlist and answers it with its address after any redirect.
func (in *Input) hlsPlaylist(ctx context.Context, address *url.URL) ([]byte, *url.URL, error) {
	ctx, cancel := context.WithTimeout(ctx, hlsFetchFor)
	defer cancel()
	resp, err := in.get(ctx, address.String())
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	text, err := io.ReadAll(io.LimitReader(resp.Body, hlsPlaylistLimit))
	return text, resp.Request.URL, err
}

// copyPiece writes one piece into the input and says whether it brought anything. A piece
// holds whole packets: what is left of one that broke off is dropped, so the next piece
// starts on a packet.
func (in *Input) copyPiece(ctx context.Context, address *url.URL, w *segmentWriter) bool {
	defer func() { w.held = w.held[:0] }()
	ctx, cancel := context.WithTimeout(ctx, hlsFetchFor)
	defer cancel()
	resp, err := in.get(ctx, address.String())
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	n, _ := io.Copy(w, resp.Body)
	return n > 0
}

// parseHLS reads a master or media playlist; addresses in it are relative to base.
func parseHLS(text []byte, base *url.URL) hlsPlaylist {
	list := hlsPlaylist{target: 10 * time.Second}
	var sequence int64
	variant, best := false, int64(-1)
	var bandwidth int64
	for _, line := range strings.Split(string(text), "\n") {
		line = strings.TrimSpace(line)
		tag, value, _ := strings.Cut(line, ":")
		switch {
		case line == "":
		case tag == "#EXT-X-STREAM-INF":
			variant, bandwidth = true, 0
			for _, attribute := range strings.Split(value, ",") {
				if n, ok := strings.CutPrefix(attribute, "BANDWIDTH="); ok {
					bandwidth, _ = strconv.ParseInt(n, 10, 64)
				}
			}
		case tag == "#EXT-X-MEDIA-SEQUENCE":
			sequence, _ = strconv.ParseInt(value, 10, 64)
		case tag == "#EXT-X-TARGETDURATION":
			if seconds, err := strconv.ParseFloat(value, 64); err == nil && seconds > 0 {
				list.target = time.Duration(seconds * float64(time.Second))
			}
		case tag == "#EXT-X-ENDLIST":
			list.ended = true
		case tag == "#EXT-X-KEY" && !strings.Contains(value, "METHOD=NONE"):
			list.unreadable = "encrypted HLS"
		case tag == "#EXT-X-MAP":
			list.unreadable = "HLS in MP4 pieces"
		case strings.HasPrefix(line, "#"):
		case variant:
			variant = false
			if address, err := base.Parse(line); err == nil && bandwidth > best {
				list.variant, best = address, bandwidth
			}
		default:
			if address, err := base.Parse(line); err == nil {
				list.pieces = append(list.pieces, hlsPiece{sequence: sequence, address: address})
			}
			sequence++
		}
	}
	return list
}
