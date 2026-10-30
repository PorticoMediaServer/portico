# Measured fidelity

These development tools read an external reference and the genuine controlled
web screen. They never ship reference markup or assets. Reference inputs stay
outside this repository; the loopback-only reference server is read-only and
rejects traversal, external symlinks, directory listings and non-browser files.
Only the harness JSON and plain differences are retained. No image bytes,
source URLs, asset filenames, SVG paths, screenshots or DOM/HTML source are
included. Images and icons are slots: kind, load state, bounds, fit and styles.

## Local contract, version 1

`measure-screen.mjs` runs in the document after fonts and root images settle.
The canonical `scope: screen` measurement includes the declared viewport/DPR,
full root dimensions, visible hierarchy with stable child-index paths, direct
own text, classes/roles/ARIA, boxes, computed styles, input state and strings.
ARIA ID references resolve to those paths, not runtime IDs. Coordinates keep
their actual numeric precision; only negative zero is normalized. Style URLs
are replaced with an image-slot marker. SVG contents are not traversed. Font
face family/weight/status is retained without font sources. Same-input output
is extracted twice and must be deterministic.

Adapters map semantic part names to reference/built selectors. They add no DOM
attributes, markup or style to either screen. An existing `data-frame` anchor
is preferred when present; adapter anchors come next, then deterministic
role/text/DOM order within the nearest already matched semantic ancestor.
Different wrapper depths do not change that scope; multiple candidates inside
the same scope still fail as ambiguous. Repeated lists anchor their container and match children by
role, normalized descendant text and order. A list cardinality fault is reported
once; descendant missing/extra/string cascades are suppressed. Own text is used
as fallback only on leaves. Missing, extra and ambiguous elements are failures; order
selection during ambiguity is diagnostic, not a waiver. The complete declared
screen must match the authored logical viewport; an element crop cannot pass.
Reference extraction uses DPR1. Actual DOM CSS-pixel and RN dp/pt measurements
retain their real density (including 2/3) without rescaling coordinates or fonts;
see the shared measurement interface.

Geometry tolerance is **2 CSS px per x/y/width/height**, opacity **0.02**.
Font family/size/weight and normalized text are exact. Rendered Range text-run
rectangles use the same 2px tolerance. Raw textAlign and lineHeight remain
unscored diagnostics; identical rendered text must not fail for different CSS
layout recipes. Spacing/decoration and paint/radius/border/shadow/filter/transform/fit
are compared. Backgrounds are ancestor-composited RGBA, and effective opacity
is the product of ancestor opacities. Physically
identical `none` and identity matrices are equivalent, including their unused
transform origin. Colour uses sRGB D65 to CIELAB and **CIEDE2000 <= 2** with
alpha checked separately at 0.02. The implementation is checked against
[published verification pairs](https://hajim.rochester.edu/ece/sites/gsharma/ciede2000/).
Thresholds are code constants, never widened from a failed result.

Implementation layout properties (class/tag/role/ARIA, flow, flex, margin,
padding, gaps, stacking numbers) remain explicit unscored diagnostics: their
resulting rendered boxes are scored. Artwork content, illustrative object
position and internal SVG fill/path shapes are not compared. All unscored
properties and special native-input content mappings are included in results.
Native value glyph bounds are never replaced with the input host rectangle.
The native-caret illustration ruling excludes x/width/blink. Auth4 verifies its
actual focused native control, collapsed insertion point and caret colour
following text (ruling27); this is native DOM eligibility, not a pixel or blink
timing claim. It never substitutes a drawn caret or offsets native text.

Every frame string is mapped to exact existing catalogue values. Adapters may
select the actual existing key to resolve ambiguity. Public fixture values
need named, exact state/data slots; a mismatch is a failure, not an exemption.
Missing or ambiguous frame keys and built text from neither frame nor catalogue
are reported. Password values are never emitted; only redaction and length.

## First adapter

`auth4.mjs` describes the existing DEV accounts entry at 1440x900/DPR1. It
mounts the genuine LocalServerSignIn/AuthStage and public remembered facts.
The real password control is clicked and receives ten public fixture
characters to match the drawn masked state. Actions remain the existing inert
DEV recorder. The isolated browser blocks every `/v1/` request and all origins
except explicitly supplied read-only inputs, the local DEV host and public
font origins. It never reads cookies or storage. Production source cannot
import this tooling; the original DEV entry remains fail-closed in production.

Run through the existing general browser runner, with declared Node and owned
TMPDIR. Start the external read-only reference server privately and pass only
its loopback URL to `run-web.mjs --reference-url ... --output /external/screen.log`.
It writes the complete JSON outside public source and prints only a short result.
Collect that private output and format its `differences` as plain text. Calibration
compares the frame to itself (zero differences), then perturbs exactly one
measured font-size or caption-opacity fact (one difference each). Raw text metrics
are explicitly unscored diagnostics. A measured
FAIL is a useful result. No application styling is changed by this command.

Native and desktop producers use the same [measurement interface](NATIVE-CONTRACT.md)
and `compare-files.mjs` CLI. Native dump ownership stays with its producer;
logical geometry, actual pixel ratio, unavailable facts and explicit drawn
focus states are handled without a second comparator.

## Controlled state adapters and pending copy baseline

`run-web.mjs --adapter /external/state.json` accepts a declarative adapter using
existing genuine DEV entries. Optional `stateSteps` use real click/insertText
browser events on unique visible public-fixture selectors; arbitrary evaluation,
DOM/style rewrites and direct focus calls are not state preparation. Explicit
`expectations` check actual focused input type/length and, where applicable,
native-caret eligibility. Unrelated Kit states have no implicit auth4 password
requirement. An optional readySelector waits for the genuine mounted specimen;
readiness errors stay unmeasured. It never creates an alternate renderer.

The pending player Info string baseline includes the existing PlaybackInfo copy
slots inFile/playingHere/routes/sections/fields/routeName/originalValue and the
canonical projection's reason/loss/network/screen/output/shown, DV conversion
and unit patterns. Map approved frame text through the sole strings extractor
when that area is reached. Missing catalogue bindings remain explicit; no copied
test words, new fallback copy or parallel extractor. The Info domain owner binds
the resulting published catalogue keys. This queue entry is not a mapping or a
screen measurement. Relative Browse date and Live Series rules remain excluded.

Matching icon slots establish bounds/style only. The comparator emits an explicit
`icon.identity/contents` unscored diagnostic; no SVG paths, sprite references or
artwork bytes are collected. Do not use a slot match to claim the panel close
glyph is i-close or to justify a closeIcon renderer change. That identity remains
unmeasurable under the current content-exclusion policy.

Font readiness follows CSS within-family weight matching: an authored request
for 900 can select a loaded 800 face when no 900 face exists. The requested
computed weight remains 900 and is still compared exactly. An existing failed
matching face is not masked by a different loaded weight; this is readiness
selection, never a font/style substitution or relaxed comparison threshold.

Existing `/dev/kit?family=...&case=...&capture=true` specimens are allowed only
when the actual gallery registry contains that exact case and its logical
viewport matches the adapter. Missing/unknown cases cannot fall back to a pass.
Capture mode loads only the selected genuine family before module imports;
registry clients use `loadGalleryFamilies(familyId)` for the same isolation.
The normal interactive gallery keeps the no-argument all-family behavior.
A settled-motion adapter awaits real finite animations without canceling or
rewriting them. Portals require a whole BODY root and a readySelector covering
the actual open surface, not a crop of the gallery section.
Contact-sheet adapters may declare `referenceWindowViewport` large enough for
the existing authored screen to reach its natural size. The shared reference
extractor rejects a scaled root; it does not rewrite DOM/CSS or rescale values.

Dynamic catalogue copy uses explicit named messageSlots (existing key + public
fixture values) formatted by the canonical defaultI18n API, not another message
formatter. preferredKeys select existing used keys for repeated static wording;
named dataValues declare approved literal fixture facts. All matching stays
exact, and formatted-message occurrence counts still detect missing/extra copy.
