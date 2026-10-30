package reading

import (
	"context"
	"errors"
	"io"
	"math"
	"sync"
	"unicode"
	"unicode/utf8"

	"rsc.io/pdf"
)

// pdfReadWindow bounds one random-access read of the isolated snapshot.
const pdfReadWindow = 256 << 10

// parsePDFText runs only in the isolated helper process. It first walks the
// complete page tree itself and counts every Page leaf; the declared /Count is
// never consulted. Then it reads the page content streams of the pinned parser
// and the exact Info/Author string as bounded file evidence. It renders no
// pages, runs no OCR, executes no action and loads no external resource or host
// file. The helper's RLIMIT_AS/RLIMIT_CPU bound every allocation and CPU path
// the parser can reach, so a decoded-work explosion kills only the child.
func parsePDFText(ctx context.Context, r io.ReaderAt, size int64) (count *int64, opening, author string, pages *int64, err error) {
	if size <= 0 || size > maxCountPDFSource {
		return nil, "", "", nil, nil
	}
	reader := &pdfReader{ctx: ctx, source: r, size: size}
	defer func() {
		if recover() != nil {
			count, opening, author, pages = nil, "", "", nil
			err = reader.err
		}
	}()
	if err = ctx.Err(); err != nil {
		return nil, "", "", nil, err
	}
	document, e := pdf.NewReader(reader, size)
	if reader.err != nil {
		return nil, "", "", nil, reader.err
	}
	if e != nil {
		return nil, "", "", nil, nil
	}
	// Encrypted documents are not read even when an empty user password would
	// let the parser decrypt them: no DRM/password path is part of this rule.
	if !document.Trailer().Key("Encrypt").IsNull() {
		return nil, "", "", nil, nil
	}
	leaves, ok := pdfStructuralPages(document)
	if reader.err != nil {
		return nil, "", "", nil, reader.err
	}
	if !ok {
		return nil, "", "", nil, nil
	}
	total := int64(len(leaves))
	pages = &total
	// A structurally complete page tree is a real fact even when the text layer
	// is image-only or unsupported. Only the text count becomes unknown then.
	author = pdfInfoAuthor(document)
	count, opening, err = pdfTextLeaves(ctx, reader, leaves)
	if err != nil {
		return nil, "", author, pages, err
	}
	return count, opening, author, pages, nil
}

// pdfInfoAuthor returns the document's exact single Info dictionary author.
// The Info value must be a dictionary and Author a PDF string; a missing entry,
// name, array, stream, XMP packet or outline title publishes nothing. The value
// is never split, guessed or repaired, and a value that cannot be stored exactly
// (invalid UTF-8 or over 4096 bytes) publishes nothing instead of a shortened
// name.
func pdfInfoAuthor(document *pdf.Reader) string {
	info := document.Trailer().Key("Info")
	if info.Kind() != pdf.Dict {
		return ""
	}
	value := info.Key("Author")
	if value.Kind() != pdf.String {
		return ""
	}
	author := value.Text()
	if author == "" || len(author) > 4096 || !utf8.ValidString(author) {
		return ""
	}
	return author
}

// pdfStructuralPages validates the complete bounded page tree. Every child must
// be a Pages or Page dictionary; every Page leaf is counted, including a blank
// one. A missing/foreign node, excessive depth/nodes/pages, or a cycle makes the
// whole structural fact unknown; the declared /Count is never used as a sample.
func pdfStructuralPages(document *pdf.Reader) ([]pdf.Value, bool) {
	root := document.Trailer().Key("Root")
	if root.Kind() != pdf.Dict {
		return nil, false
	}
	top := root.Key("Pages")
	if top.Kind() != pdf.Dict {
		return nil, false
	}
	const maxDepth, maxNodes = 64, 2*maxCountPDFPages + 64
	leaves := []pdf.Value{}
	nodes := 0
	var walk func(pdf.Value, int) bool
	walk = func(node pdf.Value, depth int) bool {
		if depth > maxDepth || node.Kind() != pdf.Dict {
			return false
		}
		nodes++
		if nodes > maxNodes {
			return false
		}
		switch node.Key("Type").Name() {
		case "Page":
			if len(leaves) >= maxCountPDFPages {
				return false
			}
			leaves = append(leaves, node)
			return true
		case "Pages":
			kids := node.Key("Kids")
			if kids.Kind() != pdf.Array {
				return false
			}
			count := kids.Len()
			if count < 0 || count > maxNodes || nodes+count > maxNodes {
				return false
			}
			for index := 0; index < count; index++ {
				if !walk(kids.Index(index), depth+1) {
					return false
				}
			}
			return true
		default:
			return false
		}
	}
	if !walk(top, 0) || len(leaves) == 0 {
		return nil, false
	}
	return leaves, true
}

// pdfTextLeaves applies the word rule to the complete validated page sequence.
// Unsupported text, an image-only page or a malformed content stream leaves the
// count unknown while the structural page fact survives. A real file/context
// failure is returned so the caller retries and does not adopt a fact.
func pdfTextLeaves(ctx context.Context, reader *pdfReader, leaves []pdf.Value) (count *int64, opening string, err error) {
	defer func() {
		if recover() != nil {
			count, opening = nil, ""
			err = reader.err
		}
	}()
	var words int64
	var excerpt openingText
	inWord := false
	var previous pdf.Text
	havePrevious := false
	textBytes := 0
	for _, leaf := range leaves {
		if err = ctx.Err(); err != nil {
			return nil, "", err
		}
		page := pdf.Page{V: leaf}
		// A page with no content stream is a real blank page: it counts
		// structurally but contributes no text, so it must not make the text
		// determination fail.
		contents := leaf.Key("Contents")
		if contents.IsNull() || contents.Kind() == pdf.Array && contents.Len() == 0 {
			continue
		}
		if !pdfPageSupported(page) {
			return nil, "", nil
		}
		content := page.Content()
		if reader.err != nil {
			return nil, "", reader.err
		}
		havePrevious = false
		inWord = false
		for _, fragment := range content.Text {
			if err = ctx.Err(); err != nil {
				return nil, "", err
			}
			text := fragment.S
			if text == "" {
				continue
			}
			if len(text) > maxCountPDFRun {
				return nil, "", nil
			}
			textBytes += len(text)
			if textBytes > maxCountPDFText {
				return nil, "", nil
			}
			if havePrevious && !pdfLineBreak(previous, fragment) {
				if pdfWordGap(previous, fragment) {
					inWord = false
					excerpt.add(" ")
				}
			} else {
				inWord = false
				excerpt.add(" ")
			}
			var supported = true
			words, inWord, supported = pdfCountRun(text, words, inWord)
			if !supported {
				return nil, "", nil
			}
			excerpt.add(text)
			previous, havePrevious = fragment, true
		}
	}
	if words <= 0 {
		return nil, "", nil
	}
	return &words, excerpt.text.String(), nil
}

// pdfReader returns the file bytes the parser asks for, coalescing small random
// reads into one bounded window. It retains the first real file/context failure;
// parser panics on malformed structure are not file failures.
type pdfReader struct {
	ctx    context.Context
	source io.ReaderAt
	size   int64

	mu     sync.Mutex
	buffer []byte
	base   int64
	length int
	err    error
}

func (p *pdfReader) ReadAt(dst []byte, off int64) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	if off < 0 || off >= p.size {
		return 0, io.EOF
	}
	if err := p.ctx.Err(); err != nil {
		p.record(err)
		return 0, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(dst) <= pdfReadWindow {
		if off >= p.base && off+int64(len(dst)) <= p.base+int64(p.length) {
			n := copy(dst, p.buffer[int(off-p.base):p.length])
			if n == len(dst) {
				return n, nil
			}
			return n, io.EOF
		}
		want := int64(pdfReadWindow)
		if off+want > p.size {
			want = p.size - off
		}
		if cap(p.buffer) < int(want) {
			p.buffer = make([]byte, want)
		} else {
			p.buffer = p.buffer[:want]
		}
		n, err := p.source.ReadAt(p.buffer, off)
		if err != nil && !errors.Is(err, io.EOF) {
			p.record(err)
			return 0, err
		}
		p.base, p.length = off, n
		if n == 0 {
			return 0, io.EOF
		}
		copied := copy(dst, p.buffer[:n])
		if copied < len(dst) {
			return copied, io.EOF
		}
		return copied, nil
	}
	n, err := p.source.ReadAt(dst, off)
	if err != nil && !errors.Is(err, io.EOF) {
		p.record(err)
	}
	return n, err
}

func (p *pdfReader) record(err error) {
	if p.err == nil && !errors.Is(err, io.EOF) {
		p.err = err
	}
}

// pdfPageSupported rejects encodings this extractor cannot decode completely.
// The pinned parser implements WinAnsi/MacRoman, Differences and ToUnicode/CMap
// simple fonts; Identity-H composite fonts (and unknown encoding kinds) would
// otherwise be counted from raw glyph-code bytes. A missing font dictionary is
// likewise ambiguous. The publication stays unknown rather than guessing.
func pdfPageSupported(page pdf.Page) bool {
	for _, name := range page.Fonts() {
		font := page.Font(name)
		if font.V.IsNull() {
			return false
		}
		encoding := font.V.Key("Encoding")
		switch encoding.Kind() {
		case pdf.Name:
			switch encoding.Name() {
			case "WinAnsiEncoding", "MacRomanEncoding":
			default:
				return false
			}
		case pdf.Dict, pdf.Null:
		default:
			return false
		}
	}
	return true
}

// pdfCountRun applies the same rule 1 tokenization as EPUB text: maximal
// Unicode letter/number/combining-mark runs containing a letter or number.
// Scripts that require dictionary segmentation are unsupported, so the whole
// publication stays unknown rather than receiving a whitespace guess.
func pdfCountRun(text string, words int64, inWord bool) (int64, bool, bool) {
	for _, r := range text {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Thai, unicode.Lao, unicode.Khmer, unicode.Myanmar) {
			return words, inWord, false
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if !inWord {
				words++
				inWord = true
			}
		} else if !unicode.IsMark(r) {
			inWord = false
		}
	}
	return words, inWord, true
}

// pdfLineBreak reports a new visual line: a different baseline beyond half the
// smaller font size. Text fragments on one baseline may continue the same word.
func pdfLineBreak(previous, next pdf.Text) bool {
	tolerance := math.Max(0.5, 0.5*math.Min(math.Abs(previous.FontSize), math.Abs(next.FontSize)))
	return math.Abs(next.Y-previous.Y) > tolerance
}

// pdfWordGap reports a horizontal separation large enough to be a word space.
// Small gaps are kept inside the word so a word split across font runs is not
// counted twice; the heuristic uses only the page's real text geometry.
func pdfWordGap(previous, next pdf.Text) bool {
	advance := math.Max(previous.W, 0)
	tolerance := math.Max(0.5, 0.15*math.Max(math.Abs(previous.FontSize), math.Abs(next.FontSize)))
	return next.X > previous.X+advance+tolerance
}
