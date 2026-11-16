package live

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Many channel logos are SVG drawings, which the picture store cannot read, so a drawing is
// made into a PNG when it is fetched. A drawing with anything that cannot be drawn faithfully
// (a picture inside it, a clip, a filter, text along a path or in pieces) is refused as
// before, and the channel shows its monogram: a wrong logo is worse than none.

const svgEdge = 512 // the longest edge a drawing is drawn at; logos are shown small

func isSVG(data []byte) bool {
	return bytes.Contains(data[:min(len(data), 1024)], []byte("<svg"))
}

// drawSVG makes an SVG drawing into a PNG.
func drawSVG(data []byte) (picture []byte, err error) {
	defer func() {
		if recover() != nil {
			picture, err = nil, errors.New("the drawing could not be read")
		}
	}()
	texts, shapes, err := svgTexts(data)
	if err != nil {
		return nil, err
	}
	icon, err := oksvg.ReadIconStream(bytes.NewReader(shapes), oksvg.StrictErrorMode)
	if err != nil {
		return nil, fmt.Errorf("the drawing could not be read: %w", err)
	}
	box := icon.ViewBox
	if box.W <= 0 || box.H <= 0 {
		return nil, errors.New("the drawing has no size")
	}
	scale := svgEdge / max(box.W, box.H)
	w, h := int(math.Ceil(box.W*scale)), int(math.Ceil(box.H*scale))
	icon.SetTarget(0, 0, float64(w), float64(h))
	canvas := image.NewRGBA(image.Rect(0, 0, w, h))
	icon.Draw(rasterx.NewDasher(w, h, rasterx.NewScannerGV(w, h, canvas, canvas.Bounds())), 1)
	for _, t := range texts {
		if err := t.draw(canvas, box.X, box.Y, scale); err != nil {
			return nil, err
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, canvas); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// svgText is one line of plain text in a drawing.
type svgText struct {
	x, y, size float64
	bold       bool
	anchor     string
	fill       color.Color
	text       string
}

// svgTextLooks are the text properties drawn; any other that changes how text looks refuses
// the drawing, unless it has its plain value.
var (
	svgTextLooks  = map[string]bool{"fill": true, "font-size": true, "font-weight": true, "text-anchor": true, "font-family": true}
	svgOtherLooks = map[string]bool{"dx": true, "dy": true, "rotate": true, "textLength": true, "letter-spacing": true,
		"word-spacing": true, "writing-mode": true, "dominant-baseline": true, "alignment-baseline": true, "baseline-shift": true,
		"opacity": true, "fill-opacity": true, "font-style": true, "stroke": true, "filter": true, "mask": true, "clip-path": true}
	svgPlain = map[string]bool{"normal": true, "1": true, "none": true, "0": true, "0px": true, "auto": true, "baseline": true, "horizontal-tb": true, "lr-tb": true, "start": true}
)

// svgTexts takes the plain text and what draws nothing (metadata, editors' own elements) out
// of a drawing, for the rest to be drawn as shapes. Text inside a transformed group, in
// pieces, styled by a style sheet or with looks it cannot draw is refused.
func svgTexts(data []byte) ([]svgText, []byte, error) {
	type element struct {
		transformed bool
		style       map[string]string // the text properties it passes down
	}
	stack := []element{{style: map[string]string{"fill": "black", "font-size": "16"}}}
	var texts []svgText
	var edits []svgEdit
	var text *svgText
	var cutFrom int64 = -1 // the start of an element being taken out
	styled := false        // the drawing has a style sheet
	cutDepth := 0
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		start := decoder.InputOffset()
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("the drawing could not be read: %w", err)
		}
		switch t := token.(type) {
		case xml.StartElement:
			parent := stack[len(stack)-1]
			current := element{transformed: parent.transformed, style: map[string]string{}}
			for k, v := range parent.style {
				current.style[k] = v
			}
			for _, attribute := range t.Attr {
				switch attribute.Name.Local {
				case "transform":
					current.transformed = true
				case "style":
					for _, declaration := range strings.Split(attribute.Value, ";") {
						if k, v, ok := strings.Cut(declaration, ":"); ok {
							current.style[strings.TrimSpace(k)] = strings.TrimSpace(v)
						}
					}
				default:
					if svgTextLooks[attribute.Name.Local] || svgOtherLooks[attribute.Name.Local] {
						current.style[attribute.Name.Local] = attribute.Value
					}
				}
			}
			stack = append(stack, current)
			switch {
			case cutFrom >= 0:
				if text != nil {
					return nil, nil, errors.New("the drawing has text in pieces")
				}
				cutDepth++
			case t.Name.Local == "rect" && (attr(t, "rx") == "") != (attr(t, "ry") == "") && bytes.HasPrefix(data[start:], []byte("<rect")):
				// One corner radius stands for both, which the shape drawing does not know.
				at, missing := start+int64(len("<rect")), "ry"
				if attr(t, "rx") == "" {
					missing = "rx"
				}
				edits = append(edits, svgEdit{from: at, to: at, insert: fmt.Sprintf(` %s="%s"`, missing, attr(t, "rx")+attr(t, "ry"))})
			case t.Name.Local == "style":
				styled = true
			case t.Name.Local == "metadata" || t.Name.Space != "" && t.Name.Space != "http://www.w3.org/2000/svg":
				cutFrom, cutDepth = start, 1
			case t.Name.Local == "text":
				if current.transformed {
					return nil, nil, errors.New("the drawing has text that is turned or moved")
				}
				for k, v := range current.style {
					if !svgTextLooks[k] && (svgOtherLooks[k] || strings.HasPrefix(k, "font") || strings.HasPrefix(k, "text")) && !svgPlain[v] {
						return nil, nil, errors.New("the drawing has text that cannot be drawn")
					}
				}
				x, xErr := number(attr(t, "x"))
				y, yErr := number(attr(t, "y"))
				size, sizeErr := number(strings.TrimSuffix(current.style["font-size"], "px"))
				fill, err := oksvg.ParseSVGColor(current.style["fill"])
				if errors.Join(xErr, yErr, sizeErr, err) != nil || size <= 0 {
					return nil, nil, errors.New("the drawing has text that cannot be drawn")
				}
				weight := current.style["font-weight"]
				heavy, _ := strconv.Atoi(weight)
				text = &svgText{x: x, y: y, size: size, fill: fill, anchor: current.style["text-anchor"],
					bold: weight == "bold" || weight == "bolder" || heavy >= 600}
				cutFrom, cutDepth = start, 1
			}
		case xml.EndElement:
			stack = stack[:len(stack)-1]
			if cutFrom >= 0 {
				if cutDepth--; cutDepth == 0 {
					edits = append(edits, svgEdit{from: cutFrom, to: decoder.InputOffset()})
					cutFrom = -1
					if text != nil {
						if text.fill != nil && strings.TrimSpace(text.text) != "" {
							texts = append(texts, *text)
						}
						text = nil
					}
				}
			}
		case xml.CharData:
			if text != nil {
				text.text += string(t)
			}
		}
	}
	if styled && len(texts) > 0 {
		return nil, nil, errors.New("the drawing has text styled by a style sheet")
	}
	shapes := make([]byte, 0, len(data))
	var from int64
	for _, edit := range edits {
		shapes = append(append(shapes, data[from:edit.from]...), edit.insert...)
		from = edit.to
	}
	return texts, append(shapes, data[from:]...), nil
}

// number reads one number; an absent one is 0.
func number(value string) (float64, error) {
	if value = strings.TrimSpace(value); value == "" {
		return 0, nil
	}
	return strconv.ParseFloat(value, 64)
}

// svgEdit replaces data[from:to] with insert.
type svgEdit struct {
	from, to int64
	insert   string
}

func attr(t xml.StartElement, name string) string {
	for _, a := range t.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// draw writes the text in Go's own sans-serif face, the nearest to the faces logos name.
func (t svgText) draw(canvas *image.RGBA, originX, originY, scale float64) error {
	file := goregular.TTF
	if t.bold {
		file = gobold.TTF
	}
	parsed, err := opentype.Parse(file)
	if err != nil {
		return err
	}
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: t.size * scale, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return err
	}
	defer face.Close()
	line := strings.Join(strings.Fields(t.text), " ")
	drawer := font.Drawer{Dst: canvas, Src: image.NewUniform(t.fill), Face: face}
	x := (t.x - originX) * scale
	switch t.anchor {
	case "middle":
		x -= float64(drawer.MeasureString(line)) / 64 / 2
	case "end":
		x -= float64(drawer.MeasureString(line)) / 64
	}
	drawer.Dot = fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6((t.y - originY) * scale * 64)}
	drawer.DrawString(line)
	return nil
}
