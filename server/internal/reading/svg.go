package reading

import (
	"encoding/xml"
	"strings"
)

const svgNamespace = "http://www.w3.org/2000/svg"
const xlinkNamespace = "http://www.w3.org/1999/xlink"
const xmlNamespace = "http://www.w3.org/XML/1998/namespace"

func svgAttributeNames(names string) map[string]bool {
	out := map[string]bool{}
	for _, name := range strings.Fields(names) {
		out[name] = true
	}
	return out
}

// Each admitted element has reviewed geometry and semantic attributes. The
// filter-image primitive, scripting, animation and foreign HTML are excluded.
var svgElements = func() map[string]map[string]bool {
	specs := map[string]string{
		"svg":  "x y width height viewBox preserveAspectRatio version transform role aria-label aria-labelledby aria-describedby",
		"g":    "transform role aria-label aria-labelledby aria-describedby",
		"defs": "", "title": "", "desc": "",
		"symbol":   "x y width height viewBox preserveAspectRatio refX refY",
		"use":      "x y width height transform href",
		"path":     "d pathLength transform",
		"rect":     "x y width height rx ry pathLength transform",
		"circle":   "cx cy r pathLength transform",
		"ellipse":  "cx cy rx ry pathLength transform",
		"line":     "x1 y1 x2 y2 pathLength transform",
		"polyline": "points pathLength transform", "polygon": "points pathLength transform",
		"text":           "x y dx dy rotate textLength lengthAdjust transform aria-label",
		"tspan":          "x y dx dy rotate textLength lengthAdjust",
		"textPath":       "href startOffset method spacing side textLength lengthAdjust",
		"image":          "x y width height preserveAspectRatio transform href role aria-label aria-labelledby aria-describedby",
		"clipPath":       "clipPathUnits transform",
		"mask":           "x y width height maskUnits maskContentUnits",
		"pattern":        "x y width height patternUnits patternContentUnits patternTransform viewBox preserveAspectRatio href",
		"marker":         "markerUnits refX refY markerWidth markerHeight orient viewBox preserveAspectRatio",
		"linearGradient": "x1 y1 x2 y2 gradientUnits gradientTransform spreadMethod href",
		"radialGradient": "cx cy r fx fy fr gradientUnits gradientTransform spreadMethod href",
		"stop":           "offset",
		"filter":         "x y width height filterUnits primitiveUnits filterRes href",
		"feBlend":        "in in2 mode", "feColorMatrix": "in type values",
		"feComponentTransfer": "in", "feComposite": "in in2 operator k1 k2 k3 k4",
		"feConvolveMatrix":  "in order kernelMatrix divisor bias targetX targetY edgeMode kernelUnitLength preserveAlpha",
		"feDiffuseLighting": "in surfaceScale diffuseConstant kernelUnitLength",
		"feDisplacementMap": "in in2 scale xChannelSelector yChannelSelector",
		"feDistantLight":    "azimuth elevation", "feDropShadow": "in dx dy stdDeviation",
		"feFlood": "", "feGaussianBlur": "in stdDeviation edgeMode",
		"feMerge": "", "feMergeNode": "in", "feMorphology": "in operator radius",
		"feOffset": "in dx dy", "fePointLight": "x y z",
		"feSpecularLighting": "in surfaceScale specularConstant specularExponent kernelUnitLength",
		"feSpotLight":        "x y z pointsAtX pointsAtY pointsAtZ specularExponent limitingConeAngle",
		"feTile":             "in", "feTurbulence": "baseFrequency numOctaves seed stitchTiles type",
		"view": "viewBox preserveAspectRatio", "style": "type media",
	}
	out := map[string]map[string]bool{}
	for tag, names := range specs {
		if strings.HasPrefix(tag, "fe") && tag != "feMergeNode" && tag != "feDistantLight" && tag != "fePointLight" && tag != "feSpotLight" {
			names += " x y width height result"
		}
		out[tag] = svgAttributeNames(names)
	}
	for _, tag := range []string{"feFuncA", "feFuncB", "feFuncG", "feFuncR"} {
		out[tag] = svgAttributeNames("type tableValues slope intercept amplitude exponent offset")
	}
	return out
}()

// These passive presentation properties can be inherited by SVG descendants.
// Validate their values with the existing CSS reference and escape rules.
var svgPresentation = svgAttributeNames("alignment-baseline baseline-shift clip-path clip-rule color color-interpolation color-interpolation-filters direction display dominant-baseline fill fill-opacity fill-rule flood-color flood-opacity font-family font-size font-size-adjust font-stretch font-style font-variant font-weight image-rendering letter-spacing lighting-color marker-start marker-mid marker-end mask filter opacity overflow paint-order pointer-events shape-rendering stop-color stop-opacity stroke stroke-width stroke-opacity stroke-linecap stroke-linejoin stroke-miterlimit stroke-dasharray stroke-dashoffset text-anchor text-decoration text-rendering unicode-bidi vector-effect visibility word-spacing writing-mode")

func validateSVG(base string, b []byte, assets map[string]string) error {
	return validateMarkup(base, b, assets, true)
}

func validateSVGAttribute(base, tag string, a xml.Attr, assets map[string]string) error {
	attributes, ok := svgElements[tag]
	if !ok {
		return ErrInvalid
	}
	name, value := a.Name.Local, strings.TrimSpace(a.Value)
	// Namespace declarations are structural, and may bind only the namespaces
	// understood here. No arbitrary namespaced SVG attribute is admitted.
	if a.Name.Space == "xmlns" {
		if value == svgNamespace || value == xlinkNamespace {
			return nil
		}
		return ErrInvalid
	}
	if a.Name.Space == "" && name == "xmlns" {
		if value == svgNamespace {
			return nil
		}
		return ErrInvalid
	}
	if a.Name.Space == xmlNamespace && name == "lang" {
		return nil
	}
	if a.Name.Space != "" && (a.Name.Space != xlinkNamespace || name != "href") {
		return ErrInvalid
	}
	if name == "href" {
		if !attributes["href"] {
			return ErrInvalid
		}
		if tag == "image" {
			if value == "" || strings.ContainsAny(value, "?#") {
				return ErrInvalid
			}
			target, err := resolveReference(base, value)
			if err != nil {
				return err
			}
			switch assets[target] {
			case "image/jpeg", "image/png", "image/gif", "image/webp":
				return nil
			default:
				return ErrInvalid
			}
		}
		if !strings.HasPrefix(value, "#") || len(value) == 1 || strings.ContainsAny(value[1:], "#?\\\t\r\n ") {
			return ErrInvalid
		}
		_, err := resolveReference(base, value)
		return err
	}
	if name == "id" || name == "class" || name == "style" {
		// The shared markup validator checks style attributes and style elements.
		return nil
	}
	if svgPresentation[name] {
		return validateCSSWithSVG(base, []byte(name+":"+a.Value), assets, true)
	}
	if attributes[name] {
		if tag == "style" && name == "type" && value != "" && !strings.EqualFold(value, "text/css") {
			return ErrInvalid
		}
		return nil
	}
	return ErrInvalid
}
