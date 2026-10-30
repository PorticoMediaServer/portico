# Portico component specification

`src/index.ts` and `src/components.ts` are the shared numeric specification for `web/src/kit` and `apps/native/src-next/kit`. The approved `kit.css`, page-local CSS, `DESIGN.md` and answered product questions define their values. The motion table in `motion.html#1` takes precedence over older page timings and reduced-motion rules. Renderers build the components; this package has no renderer or application data.

Geometry is in logical pixels, with explicit `web`, `phone`, `tablet` and `tv` columns where the drawings differ. TV is a 1920×1080 canvas. Tablet follows the phone layout below 1024; above that width web uses the desktop layout. Never multiply all TV radii by one factor. Never stretch cards or type to fill a wider screen.

## Type and spacing

Manrope is the only interface typeface. `typography.extraBold` names the 800 face; native font bundling is separate. `typeScale.desktop`, `.handheld` and `.tenFoot` include the lockup, long lockup, row heading, navigation, card, button, tab, chip, metadata, synopsis, rank, fallback and activity roles. `normal` line-height means the font's own metrics, exactly as the CSS rules that omit line-height. Numeric line heights are logical pixels. Tracking is in em. All roles use sentence case; labels have no uppercase transform or spaced-out uppercase tracking.

Lockups are 84 desktop, 46 phone and 120 TV, weight 800, tracking −0.045em and line-height 0.94. Desktop long lockups are 64. Row headings are 22/700/−0.015em on desktop, 19 on phone and 32 on TV. Navigation is 15/600 on desktop. Desktop card titles are 14/600 with 13-pixel secondary text. The full type data lives in `typeScale`.

`spacingGrid` includes every kit inset as well as page-local control insets; it is not restricted to multiples of four. `spacing` gives shared named roles. Page gutters are 20 on phone/tablet and 64 at desktop width 1440. Desktop gutters interpolate from 32 at 1024 to 64 at 1440 and stay 64 above it. TV content begins at 200, with 96 on the trailing edge; TV sign-in uses 120 and player chrome uses 96.

Radii are named by purpose: tags 6, thumbnails 8, card artwork and surface 10, hero buttons and live cards 12, fallback panels 14, banners 16, activity popovers 18 and phone sheets 24. Phone hero buttons use 10; TV artwork, hero buttons and chips use 14. Local facet and quick-filter pills keep their own drawn radii in `chipSpec`.

## Buttons and focus

Primary buttons use `text` fill and `ink` text; hover uses white. Accent is reserved for selection, progress and small highlights. Secondary buttons are glass: text at 14% opacity, 22% on hover, with 20-pixel web backdrop blur. Phones and Apple TV use native blur; Android TV and Fire TV use the same translucent fill without blur. A selected secondary uses an accent tint and readable text.

Hero buttons are 54 high with radius 12, horizontal padding 26 and gap 10 on desktop; phone 46/radius 10/padding 20/gap 8; TV 72/radius 14/padding 38/gap 14. `buttonSpec.hero` is separate from ordinary control sizes. The phone title action group shares its row. Round icon buttons keep the same height, and carry an accessible name. Normal press scale is 0.97. Loading preserves both label and geometry.

Keyboard focus is a 3-pixel pale focus outline, offset 3 on desktop and 2 on phone controls. Container rows use an inset ring. TV uses a 4-pixel ink gap, 4-pixel pale ring and a fixed soft blue glow (26 down, blur 64, spread −12, accent-strong at 60%). Exactly one element has TV focus. Cards scale to 1.08, buttons/chips to 1.05 and rows to 1.02; neighbours never move. Hover is separate: card art scales to 1.045; live cards and episode stills to 1.03, over 260 ms. Labels stay still. Fade a prepared shadow layer; never animate box-shadow or blur.

## Artwork, heroes and rows

Home heroes are 720 desktop and 560 phone. Desktop title heroes are 660; phone title art is 396 with its identity over the art. The Safari-contained mobile-web frames use 486 Home and 330 title art (`heroSpec.mobileWeb`), with 56-pixel web top/tab bars; the native phone tab bar remains 84. TV heroes occupy the fixed canvas. `heroSpec` defines copy widths and offsets, backdrop positions, composited masks and continuous fades. Desktop ambient copies use blur 90, saturation 1.4 and opacity 0.32; phones use blur 60 and opacity 0.45; TV opacity is 0.3. Web writes both prefixed source-in and standard intersect masks. Android TV/Fire TV ambient colour uses the approved palette or a pre-blurred image instead of an expensive live blur.

There is no logo dependency or poster beside the video hero identity. Facts are separate parts with gaps and weight contrast, with no middle-dot separator. Missing facts disappear. The resuming primary's progress line is 4 pixels, edge-to-edge, with accent fill on an ink-at-14% track. The hero rotates every 8 seconds, pauses on hover or focus and holds still under reduced motion; manually switching still fades in 160 ms. `heroSpec.rotation` holds crossfade, copy and drift values.

`cardSpec` defines posters (2:3), wide cards (16:9), squares, circles and 3:4 magazine covers. Posters are 190 desktop, 118 phone and 214 TV; wide cards are 316 desktop, 262 on current phone app/web frames and 400 TV. The original mock's 260 phone-wide width is explicitly scoped as `phoneMockWide`. Music/audiobooks use squares, artists use cover mosaics and people use initials. Rows never mix poster and square geometry.

`shelfSpec` carries heading gaps, track padding, snapping geometry, arrows, row spacing and ranks. Desktop track gaps are 16, phone 12, TV 28. Desktop row spacing is 44, phone 28. The ranked number is 168/800 with −0.08em tracking, outside the poster. `bannerSpec` defines the 300-pixel collection banner, 52/800 title, masked image, mini posters and 16-pixel corners.

`episodeSpec` has a dimmed backdrop-crop fallback, duration badge, progress and title/secondary lines. Never draw a large episode number over artwork. The small label below the title is Episode N, a date, absolute number or Special as appropriate. Mixed-show rows keep show/season/episode context. `generatedArtSpec` defines fallback panels, poster ambient copies, typographic glyphs and people circles. Decorative glyphs are forbidden in sign-in, state and server-band backgrounds.

## Controls and overlays

Chips use raised-high at rest and raised-bright on hover. Base geometry is 40/radius 10 desktop, 34/radius 9 phone and 64/radius 14 TV. Selected chips use an accent tint and accent boundary, never white. Quick filters and facet pills keep their explicit geometry. Selected segmented controls use a glass fill; the desktop browse view toggle has its distinct raised-bright selected surface. Touch hit areas extend to 44 and TV targets to 64, even where the visual is smaller.

Fields keep a readable placeholder, control boundary, label, help/error and visible focus. The separate search geometry covers the larger drawn search surface. Switches share their row's accessible name and interaction; compact desktop switches are 38×22 with a 16 thumb, phone switches 48×29 with a 23 thumb. TV focuses the row, not the switch alone.

Rows support navigation, value, toggle, action, menu, static and media variants. Settings help wraps; one surface groups related rows and draws text-aligned separators. Media rows truncate where the frames show a single line.

The overlay kinds are form, picker and acknowledge. Form dismissal means Cancel, with a discard check for changed values; acknowledge cannot dismiss. Phone sheets have a grabber and half/full detents. Opaque sheets use `raised`; the desktop blurred material is explicit. Context menus use raised-high; phone menu material is raised-bright at 96% with blur 30. Visual menu rows can be smaller than the hit target. Keep owner/destructive actions grouped, accessible names explicit and focus inside the active overlay. Side panels are distinct from centered dialogs. The motion data distinguishes standard dialog/menu entry, quick exits and slow side-panel travel, plus the settle spring for gesture release.

## Shell and activity

`navigationSpec` defines the transparent 76-pixel desktop top bar, solid-on-scroll material, icon buttons, avatar, 100/96 phone top bar, 84-pixel blurred tab bar and TV rail (260 closed, 560 expanded, 320 expanded item). The shell lane supplies routing, permissions and interaction.

`activitySpec` covers every state in desktop-activity frames 1–6. The chosen glyph is Levels (A), 22 drawing in a 40-pixel hit area. Idle has no count; busy streams show a count and accent tint; an update uses the quiet dot; open has an underline. Members never mount this control or make admin reads. The panel is 420 wide, radius 18, raised at 94%, blur 30/saturation 1.3. Progress rings show real fractions, never a looping spinner. Title and detail truncate independently, with the episode/device detail yielding first. Activity data and behavior belong to the shell lane.

## Feedback and motion

Notices preserve distinct info, success, warning and error tones. State recovery uses a white primary and glass secondary. Toast actions use glass, last 4 seconds (8 with an action) and appear above the dock/tab bar on phones. Skeletons preserve final geometry and hold still; Live dots and level meters are also still. Loading and errors use catalogue strings in the renderer.

`motion` is quick 120, standard 180, hover 260, slow 320, morph 420 and hero 900 ms. Curves are out `(.2,.8,.2,1)`, in `(.4,0,1,1)`, in-out `(.4,0,.2,1)` and linear. Native spring configurations are settle 26/260/1, player 24/200/1 and snap 30/420/1 (damping/stiffness/mass). `springTiming` creates the same 33 CSS `linear()` samples and 0.002 settlement tolerance as the approved motion page: 500, 571 and 371 ms. `springPosition` accepts release velocity; interrupted gestures start from the current position and velocity without queuing.

Reduced motion means a 160-ms crossfade, no slide, scale, spring, parallax, drift or rotation. Finger tracking remains direct. Content follows the touched surface by 60–80 ms, at most three steps. System navigation/rotation/keyboard remain native.

## CSS generation and verification

Run `node packages/design/scripts/tokens.mjs`; it writes only `web/src/kit/tokens.css`. Run the same command with `--check` to fail on missing/stale output without writing. It works from any cwd, uses no dependencies and never rewrites `web/`. The scaffold must run `--check` in its checks and import the generated CSS before kit component styles.

CSS exports palette, named and pixel spacing, radii, all component leaves (`--spec-*`), durations, curves, spring samples, targets, layers and responsive type/hero/card tokens. Dimensional lengths use px, type uses rem, tracking uses em, time uses ms and proportions/scales are unitless. `[data-platform="tv"]` selects the TV projection. `.portico-motion` or `[data-portico-motion]` opts a moving layer into the shared motion contract; reduced motion permits only opacity, suppresses automatic animation and removes its motion transform. `[data-motion-gesture]` preserves finger tracking. `[data-reduce-motion="true"]` applies the in-app preference. Renderers must use the exported reduced-motion rules for script-driven motion too.

Token/component tests preserve 4.5:1 text and 3:1 boundary/focus thresholds, pin approved geometry and motion, verify the spring samples, and prove generator determinism and read-only stale detection. Platform rendering, focus engines and real Home proofs remain the component/shell lanes' verification.
