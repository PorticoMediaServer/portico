// Package audiotags reads bounded metadata from retained media descriptors.
// It never follows a path, executes a decoder, or reads the audio payload.
package audiotags

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

const maxBlock = 8 << 20
const maxRead = 24 << 20
const maxValues = 512

type Picture struct {
	Type int
	MIME string
	Data []byte
}
type Lyric struct{ Language, Format, Text string }
type Chapter struct {
	Title      string
	Start, End float64
}
type Tags struct {
	Lossless  *bool
	Values    map[string][]string
	Pictures  []Picture
	Lyrics    []Lyric
	CueTracks []Chapter
	Chapters  []Chapter
	Warnings  []string
}
type reader struct {
	r          io.ReaderAt
	size, used int64
	nodes      int
	out        Tags
}

func Read(r io.ReaderAt, size int64) Tags {
	p := &reader{r: r, size: size, out: Tags{Values: map[string][]string{}}}
	h := p.at(0, 16)
	if len(h) >= 4 && string(h[:4]) == "wvpk" {
		if raw := p.at(24, 4); len(raw) == 4 {
			v := binary.LittleEndian.Uint32(raw)&8 == 0
			p.out.Lossless = &v
		}
	}
	if len(h) >= 16 && string(h[:4]) == "FRM8" && string(h[12:16]) == "DSD " {
		p.dff()
	}
	if len(h) >= 4 && string(h[:4]) == "\x1a\x45\xdf\xa3" {
		p.ebml(0, size, 0)
	}
	if len(h) >= 16 && string(h[:16]) == "\x30\x26\xb2\x75\x8e\x66\xcf\x11\xa6\xd9\x00\xaa\x00\x62\xce\x6c" {
		p.asf()
	}
	if len(h) >= 4 && string(h[:4]) == "DSD " {
		v := p.at(20, 8)
		if len(v) == 8 {
			off := binary.LittleEndian.Uint64(v)
			if off < uint64(size) {
				p.id3(int64(off))
			}
		}
	}
	if len(h) >= 10 && string(h[:3]) == "ID3" {
		p.id3(0)
	}
	if len(h) >= 4 && string(h[:4]) == "fLaC" {
		p.flac()
	}
	if len(h) >= 4 && string(h[:4]) == "OggS" {
		p.ogg()
	}
	if len(h) >= 12 && (string(h[:4]) == "RIFF" || string(h[:4]) == "FORM") {
		p.chunks(string(h[:4]) == "FORM")
	}
	if len(h) >= 8 && (string(h[4:8]) == "ftyp" || string(h[4:8]) == "moov") {
		p.atoms(0, size, 0, "")
	}
	p.ape()
	// ID3v1 is a fallback, never a replacement for richer fields.
	v := p.at(size-128, 128)
	if len(v) == 128 && string(v[:3]) == "TAG" {
		for _, f := range []struct {
			k    string
			a, b int
		}{{"title", 3, 33}, {"artist", 33, 63}, {"album", 63, 93}, {"date", 93, 97}, {"description", 97, 127}} {
			if len(p.out.Values[f.k]) == 0 {
				p.add(f.k, latin(v[f.a:f.b]))
			}
		}
		if v[125] == 0 && v[126] > 0 && len(p.out.Values["track"]) == 0 {
			p.add("track", strconv.Itoa(int(v[126])))
		}
	}
	return p.out
}
func (p *reader) warn() {
	if len(p.out.Warnings) < 16 {
		p.out.Warnings = append(p.out.Warnings, "An embedded metadata entry was skipped.")
	}
}
func (p *reader) at(off, n int64) []byte {
	if off < 0 || n < 0 || n > maxBlock || off > p.size || n > p.size-off || p.used+n > maxRead {
		return nil
	}
	p.used += n
	b := make([]byte, int(n))
	if _, e := p.r.ReadAt(b, off); e != nil && e != io.EOF {
		return nil
	}
	return b
}
func canonical(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	k = strings.ReplaceAll(k, " ", "_")
	switch k {
	case "albumartist", "album_artists", "albumartists":
		return "album_artist"
	case "narrators":
		return "narrator"
	case "authors":
		return "author"
	case "composers":
		return "composer"
	case "artists":
		return "artist"
	case "tracknumber", "track_number":
		return "track"
	case "discnumber", "disk", "disc_number":
		return "disc"
	case "tracktotal", "totaltracks":
		return "total_tracks"
	case "disctotal", "totaldiscs":
		return "total_discs"
	case "originalyear", "original_date":
		return "originaldate"
	case "year":
		return "date"
	case "releasedate", "release_date":
		return "release_date"
	case "artistsort", "artist_sort":
		return "artist_sort"
	case "albumartistsort", "album_artist_sort":
		return "album_artist_sort"
	case "albumsort":
		return "album_sort"
	case "titlesort":
		return "title_sort"
	case "comment", "comments", "synopsis":
		return "description"
	case "organization", "label":
		return "publisher"
	case "grouping", "series_title", "show", "mvnm", "calibre:series":
		return "series"
	case "series-part", "series_part", "series_index", "seriesindex", "calibre:series_index", "mvin":
		return "series_position"
	case "musicbrainz_album_id", "musicbrainz_releaseid":
		return "musicbrainz_albumid"
	case "musicbrainz_track_id", "musicbrainz_recordingid":
		return "musicbrainz_trackid"
	case "musicbrainz_artist_id":
		return "musicbrainz_artistid"
	case "musicbrainz_album_artist_id":
		return "musicbrainz_albumartistid"
	case "musicbrainz_release_group_id":
		return "musicbrainz_releasegroupid"
	case "musicbrainz_release_track_id":
		return "musicbrainz_releasetrackid"
	}
	return k
}
func (p *reader) add(k, v string) {
	k = canonical(k)
	v = strings.TrimSpace(strings.Trim(v, "\x00"))
	limit := 4096
	if strings.Contains(k, "lyrics") {
		if len(v) <= 256<<10 {
			p.lyric("und", v)
		}
		return
	}
	if k == "metadata_block_picture" {
		b, e := base64.StdEncoding.DecodeString(v)
		if e == nil && len(b) <= maxBlock {
			p.flacPicture(b)
		}
		return
	}
	if len(p.out.Values) >= maxValues || len(p.out.Values[k]) >= 64 || len(v) == 0 || len(v) > limit || !utf8.ValidString(v) || strings.IndexFunc(v, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) >= 0 {
		if len(v) > 0 {
			p.warn()
		}
		return
	}
	for _, old := range p.out.Values[k] {
		if old == v {
			return
		}
	}
	p.out.Values[k] = append(p.out.Values[k], v)
	if k == "track" || k == "disc" {
		if _, total, ok := strings.Cut(v, "/"); ok {
			if k == "track" {
				p.add("total_tracks", total)
			} else {
				p.add("total_discs", total)
			}
		}
	}
}

var timedLyric = regexp.MustCompile(`(?m)^\s*\[[0-9]{1,4}:[0-9]{2}`)

func (p *reader) lyric(lang, text string) {
	text = strings.TrimSpace(text)
	if len(p.out.Lyrics) >= 12 || len(text) == 0 || len(text) > 256<<10 || !utf8.ValidString(text) {
		return
	}
	format := "text"
	if timedLyric.MatchString(text) {
		format = "lrc"
	}
	p.out.Lyrics = append(p.out.Lyrics, Lyric{lang, format, text})
}
func latin(b []byte) string {
	var s strings.Builder
	for _, v := range bytes.TrimRight(b, "\x00 ") {
		s.WriteRune(rune(v))
	}
	return s.String()
}
func text(b []byte, enc byte) string {
	if enc == 0 {
		return latin(b)
	}
	if enc == 3 {
		if utf8.Valid(b) {
			return string(b)
		}
		return ""
	}
	little := false
	if len(b) >= 2 && (bytes.Equal(b[:2], []byte{255, 254}) || bytes.Equal(b[:2], []byte{254, 255})) {
		little = b[0] == 255
		b = b[2:]
	}
	if len(b)%2 != 0 {
		return ""
	}
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i < len(b); i += 2 {
		v := binary.BigEndian.Uint16(b[i:])
		if little {
			v = binary.LittleEndian.Uint16(b[i:])
		}
		u = append(u, v)
	}
	return string(utf16.Decode(u))
}
func splitText(b []byte, enc byte) (string, []byte) {
	step := 1
	if enc == 1 || enc == 2 {
		step = 2
	}
	for i := 0; i+step <= len(b); i += step {
		if b[i] == 0 && (step == 1 || b[i+1] == 0) {
			return text(b[:i], enc), b[i+step:]
		}
	}
	return text(b, enc), nil
}
func syncsafe(b []byte) int64 {
	if len(b) != 4 {
		return -1
	}
	var n int64
	for _, v := range b {
		if v >= 128 {
			return -1
		}
		n = n<<7 | int64(v)
	}
	return n
}
func unsync(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		out = append(out, b[i])
		if b[i] == 255 && i+1 < len(b) && b[i+1] == 0 {
			i++
		}
	}
	return out
}
func (p *reader) id3(off int64) {
	h := p.at(off, 10)
	if len(h) != 10 || string(h[:3]) != "ID3" || h[3] < 2 || h[3] > 4 {
		return
	}
	n := syncsafe(h[6:10])
	b := p.at(off+10, n)
	if len(b) == 0 {
		return
	}
	ver := h[3]
	if h[5]&128 != 0 && ver != 4 {
		b = unsync(b)
	}
	if h[5]&64 != 0 {
		if ver == 2 {
			return
		}
		if len(b) < 4 {
			return
		}
		skip := int64(binary.BigEndian.Uint32(b[:4])) + 4
		if ver == 4 {
			skip = syncsafe(b[:4])
		}
		if skip < 4 || skip > int64(len(b)) {
			return
		}
		b = b[skip:]
	}
	p.frames(b, ver, 0, h[5]&128 != 0)
}

var frameNames = map[string]string{"TT2": "title", "TIT2": "title", "TP1": "artist", "TPE1": "artist", "TP2": "album_artist", "TPE2": "album_artist", "TAL": "album", "TALB": "album", "TRK": "track", "TRCK": "track", "TPA": "disc", "TPOS": "disc", "TYE": "date", "TYER": "date", "TDRC": "date", "TDRL": "release_date", "TOR": "originaldate", "TORY": "originaldate", "TDOR": "originaldate", "TCO": "genre", "TCON": "genre", "TCM": "composer", "TCOM": "composer", "TPB": "publisher", "TPUB": "publisher", "TSOP": "artist_sort", "TSO2": "album_artist_sort", "TSOA": "album_sort", "TSOT": "title_sort", "TCMP": "compilation", "MVNM": "series", "MVIN": "series_position", "TSST": "disc_subtitle", "TSRC": "isrc"}

func (p *reader) frames(b []byte, ver byte, depth int, globalUnsync bool) {
	if depth > 4 {
		return
	}
	for count := 0; count < 4096; count++ {
		header := 10
		if ver == 2 {
			header = 6
		}
		if len(b) < header || b[0] == 0 {
			return
		}
		idlen := 4
		if ver == 2 {
			idlen = 3
		}
		id := string(b[:idlen])
		n := int64(0)
		if ver == 2 {
			n = int64(b[3])<<16 | int64(b[4])<<8 | int64(b[5])
		} else if ver == 4 {
			n = syncsafe(b[4:8])
		} else {
			n = int64(binary.BigEndian.Uint32(b[4:8]))
		}
		if n < 0 || n > int64(len(b)-header) {
			p.warn()
			return
		}
		v := b[header : int64(header)+n]
		flags := byte(0)
		if ver != 2 {
			flags = b[9]
		}
		b = b[int64(header)+n:]
		if ver == 3 && flags&192 != 0 || ver == 4 && flags&12 != 0 {
			p.warn()
			continue
		}
		if ver == 4 && (flags&2 != 0 || globalUnsync) {
			v = unsync(v)
		}
		if ver == 3 && flags&32 != 0 || ver == 4 && flags&64 != 0 {
			if len(v) == 0 {
				continue
			}
			v = v[1:]
		}
		if ver == 4 && flags&1 != 0 {
			if len(v) < 4 {
				continue
			}
			v = v[4:]
		}
		if len(v) == 0 {
			continue
		}
		enc := v[0]
		if k := frameNames[id]; k != "" {
			for _, s := range strings.Split(text(v[1:], enc), "\x00") {
				p.add(k, s)
			}
			continue
		}
		switch id {
		case "TXXX", "TXX":
			key, rest := splitText(v[1:], enc)
			for _, s := range strings.Split(text(rest, enc), "\x00") {
				p.add(key, s)
			}
		case "COMM", "COM":
			if len(v) > 4 {
				_, rest := splitText(v[4:], enc)
				p.add("description", text(rest, enc))
			}
		case "USLT", "ULT":
			if len(v) > 4 {
				_, rest := splitText(v[4:], enc)
				p.lyric(string(v[1:4]), text(rest, enc))
			}
		case "RVA2":
			p.relativeVolume(v)
		case "SYLT", "SLT":
			p.sylt(v)
		case "APIC", "PIC":
			p.id3Picture(v, id == "PIC")
		case "UFID", "UFI":
			owner, rest := splitText(v, 0)
			if owner == "http://musicbrainz.org" {
				p.add("musicbrainz_trackid", string(rest))
			}
		case "CHAP":
			_, rest := splitText(v, 0)
			if len(rest) >= 16 && len(p.out.Chapters) < 4096 {
				start := float64(binary.BigEndian.Uint32(rest)) / 1000
				end := float64(binary.BigEndian.Uint32(rest[4:])) / 1000
				title := ""
				child := &reader{out: Tags{Values: map[string][]string{}}}
				child.frames(rest[16:], ver, depth+1, false)
				if len(child.out.Values["title"]) > 0 {
					title = child.out.Values["title"][0]
				}
				p.chapter(title, start, end)
			}
		}
	}
	p.warn()
}
func (p *reader) chapter(title string, start, end float64) {
	if len(p.out.Chapters) < 4096 && !math.IsNaN(start) && !math.IsNaN(end) && start >= 0 && end > start && end <= 7*24*3600 {
		p.out.Chapters = append(p.out.Chapters, Chapter{title, start, end})
	} else {
		p.warn()
	}
}
func (p *reader) picture(kind int, mime string, b []byte) {
	if len(p.out.Pictures) < 8 && len(b) > 0 && len(b) <= 4<<20 {
		p.out.Pictures = append(p.out.Pictures, Picture{kind, mime, append([]byte(nil), b...)})
	} else {
		p.warn()
	}
}
func (p *reader) id3Picture(v []byte, old bool) {
	if len(v) < 5 {
		return
	}
	enc := v[0]
	v = v[1:]
	mime := ""
	if old {
		mime = "image/" + strings.ToLower(string(v[:3]))
		v = v[3:]
	} else {
		mime, v = splitText(v, 0)
	}
	if len(v) == 0 {
		return
	}
	kind := int(v[0])
	_, v = splitText(v[1:], enc)
	p.picture(kind, mime, v)
}
func (p *reader) sylt(v []byte) {
	if len(v) < 6 || v[4] != 2 {
		return
	}
	enc := v[0]
	lang := string(v[1:4])
	_, v = splitText(v[6:], enc)
	var s strings.Builder
	for i := 0; i < 4096 && len(v) > 0; i++ {
		line, rest := splitText(v, enc)
		if len(rest) < 4 {
			break
		}
		ms := int64(binary.BigEndian.Uint32(rest))
		if ms > 24*3600*1000 {
			break
		}
		fmtStamp := strconv.FormatInt(ms/60000, 10) + ":"
		sec := strconv.FormatInt(ms/1000%60, 10)
		if len(sec) < 2 {
			sec = "0" + sec
		}
		frac := strconv.FormatInt(ms%1000+1000, 10)[1:]
		s.WriteString("[" + fmtStamp + sec + "." + frac + "]" + line + "\n")
		v = rest[4:]
		if s.Len() > 256<<10 {
			return
		}
	}
	p.lyric(lang, s.String())
}
func (p *reader) comments(b []byte) {
	pos := 0
	read := func() []byte {
		if pos+4 > len(b) {
			return nil
		}
		n := int(binary.LittleEndian.Uint32(b[pos:]))
		pos += 4
		if n > len(b)-pos {
			return nil
		}
		v := b[pos : pos+n]
		pos += n
		return v
	}
	if read() == nil || pos+4 > len(b) {
		return
	}
	count := binary.LittleEndian.Uint32(b[pos:])
	pos += 4
	if count > maxValues {
		p.warn()
		count = maxValues
	}
	for i := uint32(0); i < count; i++ {
		v := read()
		if v == nil {
			p.warn()
			break
		}
		k, s, ok := strings.Cut(string(v), "=")
		if ok {
			p.add(k, s)
		}
	}
}
func (p *reader) flac() {
	rate := 0
	off := int64(4)
	for i := 0; i < 256; i++ {
		h := p.at(off, 4)
		if len(h) != 4 {
			return
		}
		n := int64(h[1])<<16 | int64(h[2])<<8 | int64(h[3])
		if h[0]&127 == 0 {
			b := p.at(off+4, n)
			if len(b) >= 18 {
				rate = int(binary.BigEndian.Uint64(b[10:18]) >> 44)
			}
		}
		if h[0]&127 == 5 {
			p.flacCue(p.at(off+4, n), rate)
		}
		if h[0]&127 == 4 {
			p.comments(p.at(off+4, n))
		}
		if h[0]&127 == 6 {
			p.flacPicture(p.at(off+4, n))
		}
		off += 4 + n
		if h[0]&128 != 0 {
			return
		}
	}
	p.warn()
}
func (p *reader) flacPicture(b []byte) {
	if len(b) < 4 {
		return
	}
	kind := int(binary.BigEndian.Uint32(b))
	b = b[4:]
	take := func() []byte {
		if len(b) < 4 {
			return nil
		}
		n := int(binary.BigEndian.Uint32(b))
		b = b[4:]
		if n > len(b) {
			return nil
		}
		v := b[:n]
		b = b[n:]
		return v
	}
	mime := take()
	if mime == nil || take() == nil || len(b) < 16 {
		return
	}
	b = b[16:]
	pic := take()
	if pic != nil {
		p.picture(kind, string(mime), pic)
	}
}
func (p *reader) ogg() {
	off := int64(0)
	var packet []byte
	var serial uint32
	have := false
	for pages := 0; pages < 256; pages++ {
		h := p.at(off, 27)
		if len(h) != 27 || string(h[:4]) != "OggS" {
			return
		}
		l := p.at(off+27, int64(h[26]))
		if len(l) != int(h[26]) {
			return
		}
		n := int64(0)
		for _, v := range l {
			n += int64(v)
		}
		data := p.at(off+27+int64(len(l)), n)
		if len(data) != int(n) {
			return
		}
		sid := binary.LittleEndian.Uint32(h[14:])
		if !have {
			serial = sid
			have = true
		}
		off += 27 + int64(len(l)) + n
		if sid != serial {
			continue
		}
		pos := 0
		for _, v := range l {
			if len(packet)+int(v) > maxBlock {
				return
			}
			packet = append(packet, data[pos:pos+int(v)]...)
			pos += int(v)
			if v < 255 {
				switch {
				case bytes.HasPrefix(packet, []byte("OpusTags")):
					p.comments(packet[8:])
					return
				case bytes.HasPrefix(packet, []byte{3, 'v', 'o', 'r', 'b', 'i', 's'}):
					p.comments(packet[7:])
					return
				}
				packet = nil
			}
		}
	}
	p.warn()
}
func (p *reader) ape() {
	end := p.size
	v := p.at(end-128, 3)
	if string(v) == "TAG" {
		end -= 128
	}
	f := p.at(end-32, 32)
	if len(f) != 32 || string(f[:8]) != "APETAGEX" {
		return
	}
	n := int64(binary.LittleEndian.Uint32(f[12:]))
	count := binary.LittleEndian.Uint32(f[16:])
	if n < 32 || n > maxBlock || count > maxValues {
		return
	}
	b := p.at(end-n, n-32)
	for i := uint32(0); i < count; i++ {
		if len(b) < 8 {
			return
		}
		size := int(binary.LittleEndian.Uint32(b))
		flags := binary.LittleEndian.Uint32(b[4:])
		b = b[8:]
		z := bytes.IndexByte(b, 0)
		if z < 0 || z > 256 {
			return
		}
		k := string(b[:z])
		b = b[z+1:]
		if size > len(b) {
			return
		}
		v := b[:size]
		b = b[size:]
		if flags&6 == 0 {
			for _, s := range strings.Split(string(v), "\x00") {
				p.add(k, s)
			}
		} else if strings.HasPrefix(strings.ToLower(k), "cover art") {
			_, v = splitText(v, 0)
			kind := 0
			if strings.Contains(strings.ToLower(k), "front") {
				kind = 3
			}
			p.picture(kind, "", v)
		}
	}
}
func (p *reader) chunks(big bool) {
	off := int64(12)
	for i := 0; i < 4096 && off+8 <= p.size; i++ {
		h := p.at(off, 8)
		if len(h) != 8 {
			return
		}
		n := int64(binary.LittleEndian.Uint32(h[4:]))
		if big {
			n = int64(binary.BigEndian.Uint32(h[4:]))
		}
		if n > p.size-off-8 {
			return
		}
		id := string(h[:4])
		switch id {
		case "id3 ", "ID3 ":
			p.id3(off + 8)
		case "NAME":
			p.add("title", latin(p.at(off+8, n)))
		case "AUTH":
			p.add("artist", latin(p.at(off+8, n)))
		case "ANNO":
			p.add("description", latin(p.at(off+8, n)))
		case "LIST":
			b := p.at(off+8, n)
			if len(b) < 4 || string(b[:4]) != "INFO" {
				break
			}
			b = b[4:]
			for len(b) >= 8 {
				size := int(binary.LittleEndian.Uint32(b[4:]))
				if size > len(b)-8 {
					break
				}
				key := map[string]string{"INAM": "title", "IART": "artist", "IPRD": "album", "ICRD": "date", "IGNR": "genre", "ICMT": "description", "ITRK": "track", "IPUB": "publisher"}[string(b[:4])]
				if key != "" {
					p.add(key, latin(b[8:8+size]))
				}
				skip := 8 + size + (size & 1)
				if skip > len(b) {
					break
				}
				b = b[skip:]
			}
		}
		off += 8 + n + (n & 1)
	}
}

// JSONValues keeps the unambiguous lists available to catalogue evidence.
func (t Tags) JSONValues() map[string]string {
	out := map[string]string{}
	for k, v := range t.Values {
		if len(v) == 0 {
			continue
		}
		switch k {
		case "artist":
			b, _ := json.Marshal(v)
			out["artists"] = string(b)
			out[k] = strings.Join(v, " / ")
		case "album_artist":
			b, _ := json.Marshal(v)
			out["album_artists"] = string(b)
			out[k] = strings.Join(v, " / ")
		case "author", "narrator", "composer":
			b, _ := json.Marshal(v)
			out[k+"s"] = string(b)
			out[k] = strings.Join(v, " / ")
		case "musicbrainz_artistid", "musicbrainz_albumartistid":
			b, _ := json.Marshal(v)
			out[k+"s"] = string(b)
			out[k] = v[0]
		case "genre":
			b, _ := json.Marshal(v)
			out["genres"] = string(b)
			out[k] = strings.Join(v, "; ")
		default:
			out[k] = v[0]
		}
	}
	if dates := t.Values["release_date"]; len(dates) > 0 {
		out["date"] = dates[0]
	}
	return out
}

func (p *reader) relativeVolume(v []byte) {
	id, rest := splitText(v, 0)
	scope := "track"
	if strings.Contains(strings.ToLower(id), "album") {
		scope = "album"
	}
	for len(rest) >= 4 {
		channel := rest[0]
		gain := float64(int16(binary.BigEndian.Uint16(rest[1:3]))) / 512
		bits := int(rest[3])
		n := (bits + 7) / 8
		if n > len(rest)-4 {
			return
		}
		if channel == 1 {
			p.add("replaygain_"+scope+"_gain", strconv.FormatFloat(gain, 'f', 6, 64)+" dB")
			if bits > 0 && bits <= 32 {
				var peak uint64
				for _, b := range rest[4 : 4+n] {
					peak = peak<<8 | uint64(b)
				}
				p.add("replaygain_"+scope+"_peak", strconv.FormatFloat(float64(peak)/float64(uint64(1)<<(bits-1)), 'f', 9, 64))
			}
			return
		}
		rest = rest[4+n:]
	}
}
