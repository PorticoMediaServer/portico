# Shared measurement interface, version 1

The canonical implementation lives here in the public server repository. Native
and desktop producers consume this interface; they do not copy the comparator,
schema validator, colour math or tolerances into their repositories. Import
`compareMeasurements(reference, actual, adapter)` from `compare-files.mjs`, or:

```sh
node tools/fidelity/compare-files.mjs \
  --reference /private/frame.json --actual /private/native.json \
  --adapter /private/state.json --output /private/result.json
```

Output parents must exist outside public source. The CLI writes once and prints
only verdict/count. Private reference data and actual frame comparisons stay on
our machines, never public Actions. Public synthetic tests have no design data.

`--adapter` and `compareMeasurements` take the flat comparison recipe. For the
public frontdoor state descriptor use `frontdoorStateAdapters[platform].comparison`,
not its `{anchors,comparison}` envelope. The envelope is rejected rather than
silently ignoring its content slots. Public semantic ID bindings stay in `anchors`;
they are a separate producer input. Comparison files are named `*-comparison.json`.

Explicit anchors and their semantic regions are reserved before unanchored
fallback matching. A unique unanchored reference canvas pairs with the unique
actual tree root even when native paint/bounds are unavailable; absence is kept
as coverage, never filled with defaults. Its measured geometry remains scored.
Fallback children cannot consume an anchor or another anchored region's contents.
Unknown `image.loaded` is also coverage. Its meaning is successful decode
completion for the currently bound image, not retained/intermediate drawable
presence; it establishes neither artwork identity nor visibility. An unavailable
producer observation uses the explicit property `image.loaded`, never `true`.

The common top-level fields are `version:1`, `scope:'screen'`, `fullViewport`,
`root:{x,y,width,height}`, `viewport:{width,height,deviceScaleFactor}`, `elements`,
`strings`, `anchorIssues`, `repeatedLists`. The root is the whole selected screen,
at zero origin, equal to the declared logical viewport; screen measurements must
not crop a component.

An explicitly selected DOM Kit control may instead declare `scope:'component'`.
The same extractor and comparator retain the true window viewport/DPR and measured
subtree, translate only its measured origin, and record
`componentPresentation:{windowBounds,sourceRoot}`. No scaling or restyling occurs.
The actual subtree must be fully visible. References carry
`measurement.sourceRole:'reference-component'`; they cannot be actual output.
Component results never establish full-screen acceptance. The normal two-logical-unit
geometry rule applies; native measurements remain whole-screen only.
Native adds `measurement:{renderer:'react-native',platform:'android'|'ios'|'tvos',
coordinateUnit:'dp'|'pt'}`. Keep actual physical deviceScaleFactor unchanged.
DOM declares `measurement:{renderer:'dom',coordinateUnit:'css-px'}` and keeps
its actual positive deviceScaleFactor, including Retina DPR2/3. DOM rectangles
come unchanged from getBoundingClientRect/Range and the viewport from
innerWidth/innerHeight: these APIs already report CSS logical pixels. RN window
and layout APIs report Android dp or Apple pt; use those actual logical values.
`sameLogicalExtent(a,b,measurement)` accepts exact equality, plus an exact
float32 projection in either direction for declared RN logical extents only
(`a === Math.fround(b)` or its inverse). This handles float32 host sizes versus
float64 window dimensions without changing serialized values or applying an
epsilon. DOM equality and the 2-logical-unit element tolerance are unchanged.
Use that same relation when deriving native `fullViewport`; an already emitted
false flag remains a validation failure and is never silently replaced.

### Physically observed native window geometry

A producer with a checked native observer may choose
`measurement.geometrySource:'native-window'` for a NEW capture. This is a source
policy for the complete capture, not a preference for whichever API returns zero.
Root, each host and each text run must come from the same admitted, current,
attached native window/scene. The producer must check actual root/fixture/host
attachment and capture retirement before publishing the coherent native UI-thread
batch. An API name or a JSON declaration alone does not prove that admission.
Apple remains unavailable until its actual admission barrier exists.

Normalize native physical pixels once with the observed view density, or retain
actual native points. Keep truthful DPR/fontScale. Then use
`nativeWindowRectToRoot(hostWindowRect, rootWindowRect)` for each box and text run.
This subtracts only the observed physical window root; it performs no scaling,
rounding, inset subtraction, frame alignment or ShadowTree correction. Native
animation transforms may differ from Fabric/ShadowTree coordinates. Do not assume
a constant root offset, require those two sources to agree, or mix their boxes.

The root qualifies as full viewport only when
`nativeWindowCoversViewport(rootWindowRect, viewport, measurement)` returns true:
its actual native window origin is strictly zero and its actual size matches the
observed viewport using the existing exact/float32 extent relation. The canonical
root's relative zero origin is not this proof. Old false files stay false.

Retain curated raw provenance (all rectangles are already in declared dp/pt):

```js
diagnostics: {
  fontScale: actualFontScale, // optional; remains unscored
  nativeCoordinates: {
    basis: 'attached-native-window',
    windowAPI: actualPublicNativeAPIIdentifier,
    rootWindow: actualNativeRootWindowRect,
    publicRoot: {api: 'bounding-client-rect', rect: actualFabricRootRect}, // optional
    publicRootReadback: {api: 'measure-in-window', rect: actualPublicReadback} // optional
  }
}
// On each actual host, retaining existing flattenedStyle diagnostics if present:
node.diagnostics.nativeWindowRect = actualNativeHostWindowRect;
node.diagnostics.publicMeasurement = {api: actualPublicAPI, rect: actualPublicRect}; // optional
node.diagnostics.nativeWindowTextRects = actualNativeWindowRuns; // only when observed
```

API identifiers contain only bounded identifier characters, never URLs, paths,
props, assets or authentication data. Public measurement APIs are exactly
`bounding-client-rect` or `measure-in-window`. Public readbacks are diagnostics,
never scoring facts or fallbacks. The validator rederives every supplied box/run
from its raw native window pair without a new tolerance. Unavailable native host
bounds use `box:null` plus a declared `box` unavailable fact; they cannot fall
back to public layout or fabricated zero rectangles. Missing runs remain
unavailable, never host rectangles; input glyph runs are forbidden. These gaps
keep the verdict withheld. All font/paint/visibility admission rules below remain
unchanged, including unknown font family and unsupported background/shadow facts.

Comparison reports include `assessment`: coverage has separate declared facts,
emitted unavailable measurements, missing observed font/paint properties, invalid
input, calibration and state failures. `findings` counts measured, missing,
extra, ambiguous, cardinality and missing-content-slot findings separately.
Absent observed values are coverage gaps, never requested-style defaults.
`productFindingsReady` stays false and `measuredVerdict` is `WITHHELD` until
coverage and matching prerequisites are complete. Raw `verdict`, differences
and their count remain the complete harness result, not a product-defect count.
A complete controlled specimen is still not a full application/flow verdict.

The public frontdoor map also binds `<primary-semantic-id>/label` to
`primary-action-label`. The existing comparison content slot measures its
actual text/font/run facts separately from the primary focus host's paint/box.
The reference-only module must never be imported into an application.

Native comparison uses `style.resolvedFontFamily`. Native reports the active
shaped face's verified typographic family (OpenType name16, else name1).
A DOM computed `fontFamily` is a requested CSS fallback chain,
so it cannot stand in for a native resolved face. DOM-to-DOM comparison keeps
its existing exact computed-family rule. `extractReference({resolvedFonts:true})`
reads active rendered families through Chrome's platform-font observer and
bounded metadata from already loaded font resources, without changing the DOM
or emitting font files/URLs. Legacy platform family names are retained only in
unscored `style.platformFontFamily`; a style word in that name does not establish
the rendered weight. There is no suffix stripping or guessed alias mapping.
Custom faces must match actual loaded font metadata. Where a web resource has
only a legacy style-linked name1, it also requires a unique observed loaded
FontFace registration associated with that active platform face. The registered
family is observed from Chrome's loaded-font event, never selected from the
element's requested family chain. Missing or ambiguous association stays
unavailable; no hard-coded family aliases are used. Mixed/unknown families and inputs
remain unavailable. Native supplies the actual shaped run's typeface identity;
requested/flattened styles and TextView base defaults are not observations.

Observed sizes/radii/shadows use existing CSS-style scalar strings in the
declared logical coordinate unit (dp/pt), not physical pixel values. Convert a
physical getter once using the actual view density; never divide out fontScale.
Font size/weight/color must describe actual shaped-run paint. Colors use actual
RGBA channels. Effective opacity multiplies observed View ancestry through the
attached Decor root, excluding the OS/window compositor, as DOM ancestry excludes
the browser-window compositor. A missing ancestor is not assumed opaque/alpha1.
Effective background uses observed ancestor paint; unknown drawables/composition
remain unavailable. Border radii and shadow fields require actual resolved
native observations; elevation alone does not determine a CSS box shadow.
Observed lineHeight remains diagnostic and never substitutes for window text-run
rectangles or inferred glyph positions from host bounds.
Do not divide either producer's logical rectangles by density a second time.
Only genuinely physical measurements may be converted by their producer using
its measured physical-to-logical API relationship; never infer that relationship
from a frame or use a per-frame scale/offset. If the source units are unknown,
report unavailable instead of selecting a conversion.

The reference is extracted at its authored viewport/DPR1. Actual root and viewport
must equal those logical dimensions without normalization. The legacy policy
name boxPixels means two logical units, unchanged. Exact font facts and Range/
actual native line bounds are compared in these same units, preserving fractional
values. Actual font scaling must be measured, not reset or inferred from declared
style. DPR metadata never rescales fontSize or tolerances. Wrong logical viewport
is a failure; unsupported unit declarations are invalid. Pre-metadata v1 DOM
measurements retain their DPR1-only restriction. No screenshot scaling, synthetic
DPI, offsets, zoom, display-setting changes or fabricated layout defaults.

Each visible native element supplies stable `id`, `parent` (null at root),
semantic `frame` anchor when mapped, `tag`, `role`, `leaf`, `ownText`,
`normalizedText`, `box:{x,y,width,height}`, actual boolean `focused`, flattened
`style`, `effectiveOpacity`, and ancestor-composited `effectiveBackgroundColor`.
Tag/class differences are diagnostic. Stable paths or semantic IDs are suitable;
runtime object IDs are not canonical identities. Anchors must be unique. Focus
is the actual mounted native focus, not React selection or a requested target.

`ownText` belongs only to this text leaf. `normalizedText` combines visible
descendant text in order, with whitespace normalized; do not read arbitrary
props. Native `textRects` and `descendantTextRects` are actual rendered line/run
rectangles in root-relative logical units, corresponding to DOM Range rects.
Map actual native text-layout callbacks through measured Text origins. Never
substitute the Text host rectangle for line rectangles. Native input glyph
rectangles remain explicitly unscored, just as on web. Input `value` is null for
secure controls; expose only redaction and masked length. Never serialize auth
props, app stores, hidden text, arbitrary memory or user file names.

Style fontFamily is the measured/resolved family, fontSize is a canonical
`'<logical-size>px'` string and fontWeight a numeric string. Use actual font
facts rather than platform fallback guesses. Colours use computed `rgb(...)` or
`rgba(...)`; opacity is numeric 0..1. Keep all actual flattened styles for
diagnostics. Raw lineHeight/textAlign are unscored; rendered runs are scored.
Curated, unresolved input styles belong only in the optional node field
`diagnostics:{flattenedStyle:{fontSize:24,opacity:0.7}}`. This field never
substitutes for `style`, effective paint or resolved fonts. Its scalar property
allowlist is exported as `diagnosticStyleProperties` from `contract.mjs`; omit
unsupported/animated objects, transforms, image references and arbitrary keys.
Only finite numbers or strings without URL payloads are allowed. The comparator
retains this field explicitly as unscored diagnostics. Producers must curate it
before emission, not serialize raw props/styles and rely on later rejection.
Do not use unavailable paint/font defaults. Declare missing facts as
`unavailable:[{element:'stable-id',property:'textRects',reason:'...'}]`; the result
is a measurement failure, not zero differences. FontFamily/fontSize/fontWeight/
color and native text rectangles must either be measured or declared unavailable.
Unobserved `focused` or `effectiveOpacity` may be null only with that exact
unavailable declaration; do not substitute false or one. Such missing facts
produce measurement failures without speculative focus/style differences.

Images/icons are slots only: kind, bounds, fit, radius, load state. No artwork
bytes, URLs, filenames, SVG paths or asset names. `strings` uses the same visible
occurrences as web: `{id,owner,frame,kind:'text',text,rects,style}`; accessible
labels and placeholders have their corresponding kinds. Public fixture data
slots and existing catalogue mappings stay explicit in the state adapter.

State adapters reuse `referenceOnly`, `actualContentOnly`, `textHandledBySlots`
and `contentSlots` from the web comparator. Native controls may use nativeInput
content slots, with glyph bounds explicitly unscored. For a frame that draws
focus, pass `focus:{frame:'primary-action'}`. Exactly one actual node must be
focused and it must be that unique anchor. Omit focus only when the reference
state does not specify it. Do not introduce an initial-focus change to measure it.
Repeated groups use the existing repeatedLists and element.repeat metadata from
`measure-screen.mjs`; one container cardinality failure suppresses child cascades.

`calibrateFrame(reference)` is the sole calibration implementation: same frame
zero differences, one measured font-size perturbation one difference, one
effective caption-opacity perturbation one difference. This changes observed
facts in a clone, never the real screen DOM/layout. The offline CLI includes
these 0/1/1 results. Missing calibration text cannot silently pass. Geometry is
2 logical units, effective opacity .02, CIEDE2000 <=2/alpha .02, unchanged from
the checked web implementation. A harness pass is not media/hardware/system proof.

## Complete authored screens inside a contact sheet

The reference-only helper `authoredReferenceScreen(measured, viewport)` selects a
verified complete authored screen at its exact natural logical size. Element/run
coordinates are already relative to that root and are not changed. It records
the original browser viewport/window bounds in referencePresentation, preserves
truthful fullViewport (which may be false), and labels sourceRole reference-frame.
This is an origin convention for the full authored window, not an actual-app
crop or a per-frame coordinate correction. Scaled/wrong-sized roots are rejected.
The actual side cannot use this helper's reference-only sourceRole.

`extract-reference.mjs --url PRIVATE --adapter PRIVATE.json --output PRIVATE.log`
reads the immutable reference with fonts ready/two deterministic snapshots. The
external adapter supplies selector, actual windowViewport, authored viewport and
semantic anchors/repeats. It changes no DOM/styles and emits no asset URLs/paths.
Only our machines may run it. Pass its JSON to the same offline comparator.
This is needed when an authored phone is one complete column of a contact sheet;
never select just its actions/content and call that a whole screen.

Actual platform font scaling, when available, belongs in the optional top-level
`diagnostics:{fontScale:<PixelRatio.getFontScale()>}`. It is finite and positive,
explicitly unscored, absent when unavailable, and never copied into viewport or
used to multiply/divide geometry, font sizes, runs or tolerances. Observed fonts
and text runs retain their actual rendered facts independently of this metadata.

### Theme-resolved native declarations

The r46 source policy additionally accepts the current complete evaluated
`StyleSheet.flatten` result as DECLARED paint evidence. Runtime window geometry,
text runs, focus, fixture/currentness and privacy guards are unchanged. No new
native observer/carrier is required. A new capture may carry:

```js
node.diagnostics.flattenedStyle = curatedScalarFinalStyle;
node.diagnostics.declaredStyle = {
  source: 'theme-resolved-stylesheet', complete: true
};
```

Only a current registration explicitly marked `declaredStyleComplete:true` may
emit this marker. Partial, stale, unsafe/getter-truncated or absent registrations
must omit it. No source/asset/URL/raw props are allowed. Descriptor-safe
`shadowOffset` and `textShadowOffset` are scalarized as `shadowOffsetX/Y` and
`textShadowOffsetX/Y`; objects are not serialized. The canonical normalizer
`normalizeNativeDeclaredStyle(flattened, {complete:true})` returns `{style,
properties}`. Producers do not copy its colour/shadow/compositing math.

`prepareNativeDeclarations(screen)` builds a comparison-only clone from NEW
explicit evidence. Legacy flattened diagnostics remain unscored. Raw captures,
geometry, runs, focus and loaded-image facts are never changed. The clone clears
only unavailable entries supplied by accepted declarations. Own-view omitted
background/opacity/radius/border/shadow defaults are allowed only for complete
registrations. Missing ancestry is never an implicit transparent/opacity1 view.
`normalizeNativeDeclaredAncestry` requires the complete current parent chain to
the screen root for opacity; backgrounds composite that same chain in the same
scope as DOM parentElements, excluding the OS/window compositor. An opaque own
background can resolve its background independently of unknown ancestors.

Font family stays declared `style.fontFamily`; it is never written into
`resolvedFontFamily` or treated as proof of a loaded/shaped resource. Existing
observed resolved identity takes precedence when present; declared spelling is
otherwise compared and explicitly qualified as declared, with no alias suffix
stripping or requested weight defaults. Loaded identity is retained where
actually available. Declaration omission never overwrites observed weight800
with400 or invents inherited text colour/family.

Different renderer layers may use an explicit flat `paintSlots` recipe, e.g.
`[{reference:'primary-action',actual:'primary-action-face'}]`, with that face also
in `actualContentOnly`. Only paint comes from the mapped actual child; action
geometry/focus/transform stay on the action host. Missing/ambiguous paint slots
fail closed. Transparent action hosts are not evidence that an opaque child face
is absent. This is a semantic mapping, not geometry inference or a new renderer.

Assessment retains coverage separately and exposes `actionableFindingsReady`
and `actionableVerdict` for known measured/missing/extra facts when input,
calibration, state and matching are valid. Unknown unrelated facts do not hide
those differences; they still prevent complete-screen PASS. Diagnostic font
aliases, OS chrome scope and renderer-specific SVG paint need their stated
qualification before a product fix list.
