package reading

import (
	"encoding/xml"
	"strings"
)

const svgNamespace = "http://www.w3.org/2000/svg"
const xlinkNamespace = "http://www.w3.org/1999/xlink"

// Admit passive SVG drawing and text, including ordinary publisher covers.
// Script, foreignObject, animation and embedding elements are excluded.
var svgTags = strings.Fields("svg g defs title desc symbol use path rect circle ellipse line polyline polygon text tspan textpath image clippath mask pattern marker lineargradient radialgradient stop filter feblend fecolormatrix fecomponenttransfer fecomposite feconvolvematrix fediffuselighting fedisplacementmap fedistantlight fedropshadow feflood fefunca fefuncb fefuncg fefuncr fegaussianblur feimage femerge femergenode femorphology feoffset fepointlight fespecularlighting fespotlight fetile feturbulence view style")

func validateSVG(base string, b []byte, assets map[string]string) error {
	return validateMarkup(base, b, assets, true)
}

func validateSVGAttribute(base, tag string, a xml.Attr, assets map[string]string) error {
	name := strings.ToLower(a.Name.Local)
	if a.Name.Space == "xmlns" || a.Name.Space == "" && name == "xmlns" {
		return nil
	}
	switch a.Name.Space {
	case "":
	case "http://www.w3.org/XML/1998/namespace":
		if name != "lang" && name != "space" && name != "id" {
			return ErrInvalid
		}
	case xlinkNamespace:
		if name != "href" {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	switch name {
	case "href":
		target, err := resolveReference(base, strings.TrimSpace(a.Value))
		if err != nil {
			return err
		}
		switch tag {
		case "use", "textpath":
			if target != base && assets[target] != "image/svg+xml" {
				return ErrInvalid
			}
		case "image", "feimage":
			switch assets[target] {
			case "image/jpeg", "image/png", "image/webp", "image/svg+xml":
			default:
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	case "fill", "stroke", "filter", "clip-path", "mask", "marker", "marker-start", "marker-mid", "marker-end", "cursor", "color-profile":
		return validateCSSWithSVG(base, []byte(name+":"+a.Value), assets, true)
	}
	return nil
}
