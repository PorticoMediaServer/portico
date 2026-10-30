package preparedmedia

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/audiotags"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

const maxMusicMetadata = 16 << 20

// extentReader never opens a path: tags are read from the retained, fenced
// source that is also handed to the encoder. Individual extents remain bounded.
type extentReader struct {
	ctx   context.Context
	input subtitles.RenderInput
}

func (r extentReader) ReadAt(b []byte, off int64) (int, error) {
	n := 0
	for len(b) > 0 {
		if off >= r.input.Size() {
			return n, io.EOF
		}
		count := min(int64(len(b)), int64(1<<20), r.input.Size()-off)
		p, e := r.input.ReadExtent(r.ctx, off, count)
		if e != nil {
			return n, e
		}
		if int64(len(p)) != count {
			return n, io.ErrUnexpectedEOF
		}
		copy(b, p)
		n += len(p)
		off += count
		b = b[len(p):]
	}
	return n, nil
}

func (s *Service) musicTags(ctx context.Context, w work, input subtitles.RenderInput, source producerInput) (audiotags.Tags, error) {
	var r io.ReaderAt = extentReader{ctx, input}
	if source.file != nil {
		r = source.file
	}
	tags := audiotags.Read(r, input.Size())
	if len(tags.Warnings) > 0 {
		return tags, ErrUnsupported
	}
	if e := ctx.Err(); e != nil {
		return tags, e
	}
	if w.Source.Cue {
		return s.cueMusicTags(ctx, w, tags)
	}
	return tags, nil
}
func (s *Service) encodeMusic(ctx context.Context, w work, custody *attemptCustody, source producerInput, r decoder.PreparedRecipe, out io.Writer, tags audiotags.Tags) error {
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	supervise.Go("prepared.music.metadata", func() {
		e := copyMusicMetadata(out, reader, r.MusicCodec, tags)
		reader.CloseWithError(e)
		done <- e
	})
	e := s.encode(ctx, w, custody, source, r, writer)
	writer.CloseWithError(e)
	metadataErr := <-done
	if e != nil {
		return e
	}
	return metadataErr
}

func tagComments(t audiotags.Tags) ([]byte, error) {
	keys := make([]string, 0, len(t.Values))
	for k := range t.Values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	comments := []string{}
	total := 0
	for _, k := range keys {
		if k == "" || strings.ContainsAny(k, "=\x00") {
			continue
		}
		for _, v := range t.Values[k] {
			if v != "" {
				entry := strings.ToUpper(k) + "=" + v
				total += len(entry) + 4
				comments = append(comments, entry)
			}
		}
	}
	for _, l := range t.Lyrics {
		entry := "LYRICS=" + l.Text
		total += len(entry) + 4
		comments = append(comments, entry)
	}
	for _, p := range t.Pictures {
		block := flacPicture(p)
		if len(block) > 0 {
			entry := "METADATA_BLOCK_PICTURE=" + base64.StdEncoding.EncodeToString(block)
			total += len(entry) + 4
			comments = append(comments, entry)
		}
	}
	if total > 8<<20 || len(comments) > 512 {
		return nil, ErrUnsupported
	}
	var b bytes.Buffer
	putLE := func(n int) { var raw [4]byte; binary.LittleEndian.PutUint32(raw[:], uint32(n)); b.Write(raw[:]) }
	vendor := "Portico"
	putLE(len(vendor))
	b.WriteString(vendor)
	putLE(len(comments))
	for _, v := range comments {
		putLE(len(v))
		b.WriteString(v)
	}
	return b.Bytes(), nil
}
func flacPicture(p audiotags.Picture) []byte {
	if len(p.Data) == 0 || len(p.Data) > 4<<20 {
		return nil
	}
	mime := p.MIME
	if mime == "" {
		if bytes.HasPrefix(p.Data, []byte("\x89PNG")) {
			mime = "image/png"
		} else if bytes.HasPrefix(p.Data, []byte("\xff\xd8")) {
			mime = "image/jpeg"
		} else {
			mime = "application/octet-stream"
		}
	}
	var b bytes.Buffer
	put := func(n int) { var v [4]byte; binary.BigEndian.PutUint32(v[:], uint32(n)); b.Write(v[:]) }
	put(p.Type)
	put(len(mime))
	b.WriteString(mime)
	put(0)
	// Width, height, depth and palette can be unknown; the original image bytes
	// remain unchanged and carry their own dimensions.
	put(0)
	put(0)
	put(0)
	put(0)
	put(len(p.Data))
	b.Write(p.Data)
	return b.Bytes()
}
func copyMusicMetadata(out io.Writer, in io.Reader, codec string, tags audiotags.Tags) error {
	switch codec {
	case "flac":
		return copyFLACMetadata(out, in, tags)
	case "opus":
		return copyOpusMetadata(out, in, tags)
	case "aac":
		return copyMP4Metadata(out, in, tags)
	}
	return ErrUnsupported
}
func copyFLACMetadata(out io.Writer, in io.Reader, t audiotags.Tags) error {
	magic := make([]byte, 4)
	if _, e := io.ReadFull(in, magic); e != nil {
		return e
	}
	if string(magic) != "fLaC" {
		return ErrOutput
	}
	blocks := [][]byte{}
	total := 0
	for i := 0; i < 256; i++ {
		var h [4]byte
		if _, e := io.ReadFull(in, h[:]); e != nil {
			return e
		}
		n := int(h[1])<<16 | int(h[2])<<8 | int(h[3])
		total += n
		if total > maxMusicMetadata {
			return ErrOutput
		}
		payload := make([]byte, n)
		if _, e := io.ReadFull(in, payload); e != nil {
			return e
		}
		kind := h[0] & 127
		if i == 0 && (kind != 0 || n != 34) {
			return ErrOutput
		}
		if kind != 1 && kind != 4 && kind != 6 {
			blocks = append(blocks, append([]byte{kind}, payload...))
		}
		if h[0]&128 != 0 {
			break
		}
		if i == 255 {
			return ErrOutput
		}
	}
	// Vorbis comments are replaced from the source's bounded metadata, including
	// multi-valued identity tags. Pictures have native FLAC blocks, not duplicates.
	noPictures := t
	noPictures.Pictures = nil
	comments, e := tagComments(noPictures)
	if e != nil {
		return e
	}
	blocks = append(blocks, append([]byte{4}, comments...))
	for _, p := range t.Pictures {
		if v := flacPicture(p); len(v) > 0 {
			blocks = append(blocks, append([]byte{6}, v...))
		}
	}
	total = 0
	for _, b := range blocks {
		total += len(b)
	}
	if total > maxMusicMetadata {
		return ErrUnsupported
	}
	if _, e = out.Write(magic); e != nil {
		return e
	}
	for i, b := range blocks {
		n := len(b) - 1
		h := []byte{b[0], byte(n >> 16), byte(n >> 8), byte(n)}
		if i == len(blocks)-1 {
			h[0] |= 128
		}
		if _, e = out.Write(h); e != nil {
			return e
		}
		if _, e = out.Write(b[1:]); e != nil {
			return e
		}
	}
	_, e = io.Copy(out, in)
	return e
}

func mp4Atom(kind string, payload []byte) []byte {
	b := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(b, uint32(len(b)))
	copy(b[4:8], kind)
	copy(b[8:], payload)
	return b
}
func mp4Data(kind uint32, v []byte) []byte {
	b := make([]byte, 8+len(v))
	binary.BigEndian.PutUint32(b, kind)
	copy(b[8:], v)
	return mp4Atom("data", b)
}

// Native MP4 number atoms can represent one canonical uint16 index/total.
// Preserve anything else as its original freeform text instead of clamping,
// dropping totals, or inventing an index of zero.
func musicNumberValue(value string) (int, bool) {
	n, err := strconv.Atoi(value)
	return n, err == nil && n > 0 && n <= 65535 && strconv.Itoa(n) == value
}

func mp4MusicNumber(t audiotags.Tags, key string) []byte {
	values := t.Values[key]
	if len(values) != 1 {
		return nil
	}
	parts := strings.Split(values[0], "/")
	if len(parts) > 2 {
		return nil
	}
	index, ok := musicNumberValue(parts[0])
	if !ok {
		return nil
	}
	total := 0
	if len(parts) == 2 {
		total, ok = musicNumberValue(parts[1])
		if !ok {
			return nil
		}
	}
	totalKey := "total_tracks"
	if key == "disc" {
		totalKey = "total_discs"
	}
	totals := t.Values[totalKey]
	if len(totals) > 1 {
		return nil
	}
	if len(totals) == 1 {
		explicit, valid := musicNumberValue(totals[0])
		if !valid || total != 0 && explicit != total {
			return nil
		}
		total = explicit
	}
	payload := make([]byte, 8)
	binary.BigEndian.PutUint16(payload[2:], uint16(index))
	binary.BigEndian.PutUint16(payload[4:], uint16(total))
	return payload
}

func mp4MusicTags(t audiotags.Tags) ([]byte, error) {
	names := map[string]string{"title": "\xa9nam", "artist": "\xa9ART", "album_artist": "aART", "album": "\xa9alb", "date": "\xa9day", "genre": "\xa9gen", "composer": "\xa9wrt", "description": "\xa9cmt", "lyrics": "\xa9lyr", "artist_sort": "soar", "album_artist_sort": "soaa", "album_sort": "soal", "title_sort": "sonm"}
	keys := []string{}
	for k := range t.Values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ilst := []byte{}
	numbers := map[string][]byte{"track": mp4MusicNumber(t, "track"), "disc": mp4MusicNumber(t, "disc")}
	for _, k := range keys {
		values := t.Values[k]
		if len(values) == 0 {
			continue
		}
		if k == "total_tracks" && numbers["track"] != nil || k == "total_discs" && numbers["disc"] != nil {
			continue // included in its actual native index atom below
		}
		if number := numbers[k]; number != nil {
			atom := "trkn"
			if k == "disc" {
				atom = "disk"
			}
			ilst = append(ilst, mp4Atom(atom, mp4Data(0, number))...)
			continue
		}
		payload := []byte{}
		if atom := names[k]; atom != "" {
			for _, v := range values {
				payload = append(payload, mp4Data(1, []byte(v))...)
			}
			ilst = append(ilst, mp4Atom(atom, payload)...)
		} else {
			payload = append(payload, mp4Atom("mean", append(make([]byte, 4), []byte("com.apple.iTunes")...))...)
			payload = append(payload, mp4Atom("name", append(make([]byte, 4), []byte(k)...))...)
			for _, v := range values {
				payload = append(payload, mp4Data(1, []byte(v))...)
			}
			ilst = append(ilst, mp4Atom("----", payload)...)
		}
		if len(ilst) > maxMusicMetadata {
			return nil, ErrUnsupported
		}
	}
	for _, l := range t.Lyrics {
		ilst = append(ilst, mp4Atom("\xa9lyr", mp4Data(1, []byte(l.Text)))...)
	}
	pictures := []byte{}
	for _, p := range t.Pictures {
		kind := uint32(0)
		if p.MIME == "image/jpeg" || bytes.HasPrefix(p.Data, []byte("\xff\xd8")) {
			kind = 13
		}
		if p.MIME == "image/png" || bytes.HasPrefix(p.Data, []byte("\x89PNG")) {
			kind = 14
		}
		if kind == 0 {
			return nil, ErrUnsupported
		}
		pictures = append(pictures, mp4Data(kind, p.Data)...)
	}
	if len(pictures) > 0 {
		ilst = append(ilst, mp4Atom("covr", pictures)...)
	}
	if len(ilst) > maxMusicMetadata {
		return nil, ErrUnsupported
	}
	hdlr := make([]byte, 25)
	copy(hdlr[8:], "mdir")
	copy(hdlr[12:], "appl")
	meta := append(make([]byte, 4), mp4Atom("hdlr", hdlr)...)
	meta = append(meta, mp4Atom("ilst", ilst)...)
	return mp4Atom("udta", mp4Atom("meta", meta)), nil
}
func copyMP4Metadata(out io.Writer, in io.Reader, t audiotags.Tags) error {
	tags, e := mp4MusicTags(t)
	if e != nil {
		return e
	}
	for i := 0; i < 32; i++ {
		var h [8]byte
		if _, e = io.ReadFull(in, h[:]); e != nil {
			return e
		}
		n := int64(binary.BigEndian.Uint32(h[:]))
		kind := string(h[4:])
		if n < 8 || n > maxMusicMetadata {
			return ErrOutput
		}
		b := make([]byte, int(n)-8)
		if _, e = io.ReadFull(in, b); e != nil {
			return e
		}
		if kind == "moov" {
			// This builder writes empty_moov/default_base_moof. No existing media
			// offset precedes the fragments, so resizing metadata preserves addressing.
			moov := []byte{}
			for len(b) > 0 {
				if len(b) < 8 {
					return ErrOutput
				}
				size := int(binary.BigEndian.Uint32(b))
				if size < 8 || size > len(b) {
					return ErrOutput
				}
				if string(b[4:8]) != "udta" {
					moov = append(moov, b[:size]...)
				}
				b = b[size:]
			}
			moov = append(moov, tags...)
			if len(moov) > 2*maxMusicMetadata {
				return ErrUnsupported
			}
			if _, e = out.Write(mp4Atom(kind, moov)); e != nil {
				return e
			}
			_, e = io.Copy(out, in)
			return e
		}
		if kind != "ftyp" && kind != "free" && kind != "skip" {
			return ErrOutput
		}
		if _, e = out.Write(h[:]); e != nil {
			return e
		}
		if _, e = out.Write(b); e != nil {
			return e
		}
	}
	return ErrOutput
}

var oggCRCTable = func() [256]uint32 {
	var t [256]uint32
	for i := range t {
		v := uint32(i) << 24
		for j := 0; j < 8; j++ {
			if v&0x80000000 != 0 {
				v = v<<1 ^ 0x04c11db7
			} else {
				v <<= 1
			}
		}
		t[i] = v
	}
	return t
}()

func oggChecksum(b []byte) uint32 {
	var crc uint32
	for _, v := range b {
		crc = crc<<8 ^ oggCRCTable[byte(crc>>24)^v]
	}
	return crc
}
func readOggPage(in io.Reader) ([]byte, error) {
	h := make([]byte, 27)
	if _, e := io.ReadFull(in, h); e != nil {
		return nil, e
	}
	if string(h[:4]) != "OggS" || h[4] != 0 {
		return nil, ErrOutput
	}
	lace := make([]byte, int(h[26]))
	if _, e := io.ReadFull(in, lace); e != nil {
		return nil, e
	}
	n := 0
	for _, v := range lace {
		n += int(v)
	}
	body := make([]byte, n)
	if _, e := io.ReadFull(in, body); e != nil {
		return nil, e
	}
	return append(append(h, lace...), body...), nil
}
func writeOggPage(out io.Writer, page []byte, seq uint32) error {
	binary.LittleEndian.PutUint32(page[18:22], seq)
	clear(page[22:26])
	binary.LittleEndian.PutUint32(page[22:26], oggChecksum(page))
	_, e := out.Write(page)
	return e
}
func copyOpusMetadata(out io.Writer, in io.Reader, t audiotags.Tags) error {
	comments, e := tagComments(t)
	if e != nil {
		return e
	}
	packet := append([]byte("OpusTags"), comments...)
	head, e := readOggPage(in)
	if e != nil {
		return e
	}
	headLace := int(head[26])
	if !bytes.HasPrefix(head[27+headLace:], []byte("OpusHead")) || head[5] != 2 {
		return ErrOutput
	}
	serial := binary.LittleEndian.Uint32(head[14:18])
	seq := binary.LittleEndian.Uint32(head[18:22])
	if seq != 0 {
		return ErrOutput
	}
	if e = writeOggPage(out, head, seq); e != nil {
		return e
	}
	seq++
	// FFmpeg writes the identification header and comments as separate pages.
	// Consume the complete old comments packet, never buffering audio packets.
	oldBytes := 0
	inputSeq := uint32(1)
	for {
		p, err := readOggPage(in)
		if err != nil {
			return err
		}
		l := int(p[26])
		if l == 0 || binary.LittleEndian.Uint32(p[14:18]) != serial || binary.LittleEndian.Uint32(p[18:22]) != inputSeq {
			return ErrOutput
		}
		inputSeq++
		body := p[27+l:]
		if oldBytes == 0 && !bytes.HasPrefix(body, []byte("OpusTags")) {
			return ErrOutput
		}
		oldBytes += len(body)
		if oldBytes > maxMusicMetadata {
			return ErrOutput
		}
		for i, v := range p[27 : 27+l] {
			if v < 255 && i != l-1 {
				return ErrOutput
			}
		}
		if p[27+l-1] < 255 {
			break
		}
	}
	// A packet of a multiple of 255 bytes needs a final zero lacing entry.
	lace := make([]byte, 0, len(packet)/255+1)
	remaining := len(packet)
	for remaining >= 255 {
		lace = append(lace, 255)
		remaining -= 255
	}
	lace = append(lace, byte(remaining))
	offset := 0
	continued := false
	for len(lace) > 0 {
		count := min(255, len(lace))
		n := 0
		for _, v := range lace[:count] {
			n += int(v)
		}
		page := make([]byte, 27+count+n)
		copy(page, "OggS")
		if continued {
			page[5] = 1
		}
		if count < len(lace) {
			for i := 6; i < 14; i++ {
				page[i] = 255
			}
		}
		binary.LittleEndian.PutUint32(page[14:], serial)
		page[26] = byte(count)
		copy(page[27:], lace[:count])
		copy(page[27+count:], packet[offset:offset+n])
		if e = writeOggPage(out, page, seq); e != nil {
			return e
		}
		seq++
		offset += n
		lace = lace[count:]
		continued = true
	}
	for {
		p, err := readOggPage(in)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if binary.LittleEndian.Uint32(p[14:18]) != serial || binary.LittleEndian.Uint32(p[18:22]) != inputSeq {
			return fmt.Errorf("%w: discontinuous Opus pages", ErrOutput)
		}
		inputSeq++
		if e = writeOggPage(out, p, seq); e != nil {
			return e
		}
		seq++
	}
}

// Publication checks metadata after writing, from the same closed output inode.
// Missing artwork or identity tags fail the derivative instead of being claimed.
func verifyMusicMetadata(r io.ReaderAt, size int64, expected audiotags.Tags) error {
	actual := audiotags.Read(r, size)
	for key, values := range expected.Values {
		got := actual.Values[key]
		for _, v := range values {
			if slices.Contains(got, v) {
				continue
			}
			// A native MP4 atom splits a compound index/total. Both facts must
			// survive; FLAC/Opus and freeform tags may retain the exact text.
			if key == "track" || key == "disc" {
				parts := strings.Split(v, "/")
				if len(parts) == 2 {
					_, indexOK := musicNumberValue(parts[0])
					_, totalOK := musicNumberValue(parts[1])
					totalKey := "total_tracks"
					if key == "disc" {
						totalKey = "total_discs"
					}
					if indexOK && totalOK && slices.Contains(got, parts[0]) && slices.Contains(actual.Values[totalKey], parts[1]) {
						continue
					}
				}
			}
			return fmt.Errorf("%w: retained music tag missing", ErrOutput)
		}
	}
	for _, p := range expected.Pictures {
		found := false
		for _, v := range actual.Pictures {
			if bytes.Equal(p.Data, v.Data) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: retained music artwork missing", ErrOutput)
		}
	}
	for _, l := range expected.Lyrics {
		found := false
		for _, v := range actual.Lyrics {
			if v.Text == l.Text {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: retained lyrics missing", ErrOutput)
		}
	}
	return nil
}
