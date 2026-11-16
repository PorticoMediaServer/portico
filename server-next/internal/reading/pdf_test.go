package reading

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"testing"
)

// Every PDF's cover is its first page drawn: here a page of nothing but text, which has no
// picture inside it, made by the test. A file that is not a whole PDF has no cover, and says
// so, and drawing goes on working after it.
func TestEveryPDFsCoverIsItsFirstPageDrawn(t *testing.T) {
	text := "BT /F1 48 Tf 20 150 Td (Cover) Tj ET"
	raw := classicPDF(
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Contents 4 0 R"+
			" /Resources << /Font << /F1 << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >> >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(text), text))
	cover := drawnCover(t, raw)
	// The 200 by 300 page, as large as it fits in 1000 by 1500, with the word drawn on it.
	if cover.Bounds().Dx() != 1000 || cover.Bounds().Dy() != 1500 {
		t.Fatalf("cover is %v", cover.Bounds())
	}
	dark := 0
	for y := 0; y < 1500; y += 4 {
		for x := 0; x < 1000; x += 4 {
			if r, _, _, _ := cover.At(x, y).RGBA(); r < 0x8000 {
				dark++
			}
		}
	}
	if dark < 100 {
		t.Fatalf("the text was not drawn: %d dark samples", dark)
	}
	if got, err := pdfCover(bytes.NewReader(raw[:len(raw)/2]), int64(len(raw)/2), 20<<20); got != nil || !errors.Is(err, errDamaged) {
		t.Fatalf("half a PDF: %d bytes, %v", len(got), err)
	}
	if got, err := pdfCover(bytes.NewReader(raw), int64(len(raw)), 10); got != nil || err != nil {
		t.Fatalf("a cover over the limit: %d bytes, %v", len(got), err)
	}
	drawnCover(t, raw)
}

func drawnCover(t *testing.T, raw []byte) image.Image {
	t.Helper()
	cover, err := pdfCover(bytes.NewReader(raw), int64(len(raw)), 20<<20)
	if err != nil {
		t.Fatal(err)
	}
	picture, err := jpeg.Decode(bytes.NewReader(cover))
	if err != nil {
		t.Fatal(err)
	}
	return picture
}

// classicPDF numbers the objects from 1, the first being the catalogue, and lists them in a
// cross-reference table.
func classicPDF(objects ...string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	var offsets []int
	for i, object := range objects {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return b.Bytes()
}
