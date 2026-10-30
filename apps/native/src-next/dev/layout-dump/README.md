# DEV native measurements

This producer uses the single public server `tools/fidelity/NATIVE-CONTRACT.md`.
The validator, comparator, colour math, calibration and tolerances remain there;
they are not copied into this application. Private references and adapters stay
outside public source and app packages.

Complete, current theme-resolved `StyleSheet.flatten` declarations are accepted
paint evidence under the shared declared-style policy. The owning primitive sets
`HostFacts.declaredStyleComplete: true` only for its final evaluated style.
The capture keeps curated scalar input in `diagnostics.flattenedStyle` and marks
it with `diagnostics.declaredStyle: {source: 'theme-resolved-stylesheet', complete: true}`.
The shared `prepareNativeDeclarations` comparison helper projects that evidence
on a clone; this producer does not copy its normalization or compositing math.
Partial, stale, unregistered or unsafe declarations never invent omitted defaults.
Native window geometry, actual text runs and focus remain observed facts.

The App owner installs **one** `installNativeLayoutDump(options)` in a DEV-only
branch, giving it a live whole-screen host getter, actual logical window width
and height, actual device pixel ratio, platform, and an explicit public semantic
ID → frame-anchor map. The returned owner supplies `capture()` and `dispose()`.
Optional `diagnostics: () => ({fontScale: PixelRatio.getFontScale()})` reads
fresh platform font scaling for each capture. Only a finite positive value is
emitted in top-level `diagnostics.fontScale`; unknown/invalid values are omitted.
It is unscored metadata, never part of viewport or geometry/font arithmetic.
`captureNativeLayoutDump()` calls that owner; the collector itself installs no
global command endpoint or payload logging. Production entry calls are inert.
App/Kit imports must additionally be inside `__DEV__` branches so Metro can strip
the instrumentation from production bundles.

Targeted existing native primitives use `registerNativeHost(host, facts)` from
the same entry. It returns a registration lease, or `undefined` outside DEV.
Do not register null refs: unregister the previous lease when a host changes or
unmounts. Preserve the primitive's existing ref and native callbacks.

The lease provides:

- `update({kind, semanticId?, role?, flattenedStyle?})`: already-flattened,
  curated scalar input style is **diagnostic only**. Asset references, objects,
  URLs, arbitrary props and getters are excluded.
- `recordTextLayout(event.nativeEvent.lines)`: actual native Text line events;
  input registrations reject these without reading them.
- `observeTextRectangles(reader)`: an optional current native window-space run
  observer. It supplies actual native run boxes in the same logical dp/pt units.
  This recording API does not replace the capture's admitted native batch.
  The native-window capture subtracts only its observed native window root; it
  never infers glyph transforms from a Text host box, shell scale or frame geometry.
  Without a paired native observation, `textRects` is unavailable. Do not implement this
  observer by returning `measureInWindow()` or `getBoundingClientRect()` boxes.
- `recordResolvedStyle(facts)`: **only** actual observed/resolved native font
  and paint facts. Never pass `StyleSheet.flatten(...)` here.
- `recordNativeFocus(boolean)`: call only from the actual native focus/blur
  callback for this live host. Requested/selected focus and registry memory are
  not evidence. A stale lease or blur cannot clear a newer host's focus.
- `recordMaskLength(number)`: only an actually observed rendered mask count;
  never obtain it by reading a password/input value.
- `invalidateTextLayout()`, `invalidateResolvedStyle()`, `unregister()`:
  retire observations when their content/paint or host lifetime changes.

The dump walks the public mounted host tree. Input and image internals are
opaque; raw props, Fiber, auth stores, filenames, image URLs and asset bytes are
never inspected. Unobserved paint prevents text export. Missing focus/opacity
are null with explicit unavailability, never false/one guesses. Native Text
layout supplies text/line facts; a current native window-run observer supplies
run boxes. Missing either fact is unavailable rather than a host-box fallback.
Logical boxes/fonts are not multiplied or divided by device density. An offset
or mismatched physically observed whole-screen root fails `fullViewport`; no frame-specific offset,
zoom or display setting repairs it.
RN extent consistency follows the shared `sameLogicalExtent` relation: exact
equality or an exact float32 projection in either direction. Origins remain
exactly zero; recorded host/window numbers are never rounded or replaced.

These APIs alone establish no mounted-screen score. Actual App/Kit hooks,
compatible DEV execution and the shared offline comparator are still required.

`createAndroidNativeTreeObserver()` constructs the optional `observe` install
option only in DEV Android when the checked `PorticoLayoutObservation` module
exists. The existing public App install supplies it; no second root, global,
registry or route is introduced. The collector discovers the public host tree
once, selects exactly one registered `accounts-welcome` or `tv-front-door`
fixture View, and binds native requests to that View and the full-screen root.
Missing/duplicate scope prevents the native call. Retirement aborts publication;
it does not claim to interrupt a native UI turn already queued.

Native replies are validated against captured root/fixture/host tags. Unknown
fields, raw error messages, requested styles, URLs and asset references are not
forwarded. Real false/zero facts remain distinct from unavailable observations.
Failed scope captures carry only an allowlisted adapter boundary: before native,
after native, after decode, or native scope rejection while JS is still current.
The boundary does not expose native errors or identify which native guard failed;
all currentness and publication checks remain in force.
If the checked native rejection also supplies `userInfo.nativeObservationStage`,
only the agreed operation name (with optional `-exception`) survives in the
sanitized failure. Unknown values and getters are ignored, and stale JS scope
prevents reading that field at all. No native error text or view identity escapes.
For a pre/post-observation scope rejection only, an optional
`nativeObservationScopeCheck` retains the agreed name of the first false existing
predicate. Exceptions and all other stages cannot carry it; no predicate values,
view IDs, tree details or native error text are exported.
Native paint does not fall back to diagnostic styles or stale registered paint.
Optional native text styles are accepted only as bounded observed paint facts:
normal/italic, signed logical letter spacing, the four decoration combinations,
and an actually proved absent text shadow. Text transform remains unavailable.
Image presence and scale-type mechanisms remain internal; a retained/intermediate
drawable is not current-request decode completion and does not supply canonical
image loaded/fit fields or new diagnostic fields.
These optional reply facts are independent: an older carrier or one unsupported
paint mechanism leaves that fact unavailable without granting a substitute value.
Unassociated family labels remain unavailable; they never become a requested CSS
family chain or an observed typographic identity.
The checked canonical identity field is `style.resolvedFontFamily`, supplied only
from the active shaped resource's verified name16 (otherwise name1). Legacy
observed platform labels belong to unscored `style.platformFontFamily`. Native
system labels and diagnostic/requested family chains never fill the identity.

New captures use `measurement.geometrySource: 'native-window'` for every box and
run. The single admitted batch supplies `diagnostics.nativeCoordinates` and each
node's raw `nativeWindowRect` / observed `nativeWindowTextRects`. Only that batch's
physical window root is subtracted; missing host bounds emit `box: null` with an
explicit gap. A missing native observer, fixture scope or root bounds refuses the
capture. Apple cannot supply this source until its native admission exists.

Fabric `getBoundingClientRect` and `measureInWindow` read ShadowTree layout and
remain unscored `publicMeasurement` diagnostics. They neither prove attachment
nor need to agree with native animation geometry. No public-layout fallback,
inset adjustment, frame offset or zero-returning API preference is used. Old
captures and their false `fullViewport` flags remain unchanged.

The existing DEV App root uses `collapsable: false` to retain a physical container.
A materialized Fabric `FormsView` alone can still have flattened children mounted
as native siblings. The scoped native observer must verify actual root/fixture
containment and current attachment before publishing; the prop is not that proof.

The public synthetic `test/check-native-window-contract.mjs` check imports the
single validator from an exact checked server archive supplied as
`PORTICO_RUNNER_SECONDARY_SOURCE`. Run it with declared Node 24 and
`--experimental-transform-types`. It verifies physical/Fabric disagreement,
strict native-box rederivation, declared missing bounds and input redaction;
it does not read private references or establish native runtime acceptance.
