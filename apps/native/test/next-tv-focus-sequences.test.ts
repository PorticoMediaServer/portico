import test from 'node:test';
import assert from 'node:assert/strict';
import {tvFocusKit} from './helpers/tv-focus-kit.mjs';
import {tvScrollEnabled} from '../src-next/kit/layout/tv-scroll.ts';

// The walk findings W-9, W-12, W-14 and W-17 as scripted D-pad sequences through the fake native focus
// engine, using the real focus kit. Each pins the fix that closed it on main and shows the failure it
// closed, so a later change to the same seam is checked here instead of on a device walk.
// Device checks these depend on: D1 (Android covering keeps focus), D7 (tvOS covering moves it), D10.

const card = {w: 300, h: 170};

/** The TV shell's two layers as NativeShell and assembly.renderRoutes build them: the base page under a
 * CoveredLayer (autoFocus) and the route pages over it in their own CoveredLayer (autoFocus), covered while
 * no route is open (NativeShell.tsx:405, assembly.tsx:521). */
async function shell(platform: 'android' | 'tvos' = 'android') {
  const kit = await tvFocusKit({platform}), {e, engine, focus, Stop, React} = kit, n = engine.native;
  const routeLayer: {current: any} = React.createRef();
  const registry = focus.createFocusRegistry();
  const Scene = ({route, claim}: {route?: 'artist'; claim?: boolean}) => {
    // The route-open claim (NativeShell.tsx:216-230): the shell asks for focus in the route layer.
    React.useEffect(() => {if (route && claim) focus.requestNativeFocus(routeLayer.current);}, [route, claim]);
    return e(focus.FocusProvider, {registry}, e(n.View, {style: {flex: 1}},
      e(focus.CoveredLayer, {covered: !!route, autoFocus: true, testID: 'native-shell-base', style: {position: 'absolute', inset: 0}},
        e(focus.FocusRegion, {id: 'library.toolbar', orientation: 'horizontal', edges: {down: 'library.grid'}, style: {flexDirection: 'row', gap: 20}},
          e(Stop, {id: 'switch-library', w: 360, h: 60}), e(Stop, {id: 'jump-to', w: 200, h: 60})),
        e(focus.FocusRegion, {id: 'library.grid', orientation: 'horizontal', edges: {up: 'library.toolbar'}, style: {flexDirection: 'row', gap: 24, marginTop: 40}},
          e(Stop, {id: 'artist-card', ...card}), e(Stop, {id: 'album-card', ...card}), e(Stop, {id: 'book-card', ...card}))),
      e(focus.CoveredLayer, {covered: !route, autoFocus: true, testID: 'native-shell-routes', hostRef: routeLayer, style: {position: 'absolute', inset: 0}},
        route ? e(n.View, {style: {position: 'absolute', left: 0, top: 500, width: 1920, height: 580}},
          e(focus.FocusRegion, {id: 'artist.actions', orientation: 'horizontal', style: {flexDirection: 'row', gap: 20}},
            e(Stop, {id: 'artist-play', w: 240, h: 80}), e(Stop, {id: 'artist-more', w: 240, h: 80}))) : null)));
  };
  return {...kit, Scene, registry};
}

test('W-9: a route opened over the page takes focus into itself; nothing on the covered page can be reached, drawn or pressed', async () => {
  const s = await shell(), {engine, e, Scene} = s;
  await engine.render(e(Scene, {}));
  await engine.focus('artist-card');
  await engine.render(e(Scene, {route: 'artist', claim: true}));
  assert.equal(engine.focused, 'artist-play', 'the claim lands on the route\'s first action');
  for (const key of ['up', 'left', 'up', 'down', 'left'] as const) {
    await engine.press(key);
    assert.match(engine.focused ?? '', /^artist-/, `after ${key} focus is still on the route`);
  }
  await engine.press('select');
  assert.deepEqual(s.presses, ['artist-play'], 'OK reaches the control that draws the ring, never the covered library switcher');
  for (const id of ['switch-library', 'jump-to', 'artist-card']) assert.equal(engine.focusable(id), false, `${id} is covered`);
});

test('W-9 (2eec0520): without the claim, Android strands focus on the covered card (D1): no ring, no OK, and no D-pad way out', async () => {
  const s = await shell(), {engine, e, Scene} = s;
  await engine.render(e(Scene, {}));
  await engine.focus('artist-card');
  await engine.render(e(Scene, {route: 'artist'}));
  assert.equal(engine.focused, 'artist-card', 'covering cannot move focus that is already there');
  assert.equal(s.ring('artist-card'), false, 'the Covered context hides its ring (#340)');
  await engine.press('select');
  assert.deepEqual(s.presses, [], 'and makes it inert');
  for (const key of ['right', 'down', 'left'] as const) {
    await engine.press(key);
    assert.equal(engine.focused, 'artist-card', `${key}: covered siblings are no candidates, and the full-screen route layer is not in any direction (FocusFinder.isCandidate)`);
  }
});

test('W-9 on tvOS: covering moves focus out of the page by itself (D7), and the claim agrees', async () => {
  const s = await shell('tvos'), {engine, e, Scene} = s;
  await engine.render(e(Scene, {}));
  await engine.focus('artist-card');
  await engine.render(e(Scene, {route: 'artist', claim: true}));
  assert.equal(engine.focused, 'artist-play');
});

test('W-9/W-12: the empty route layer is covered, so it can never hold focus over the page', async () => {
  const s = await shell(), {engine, e, Scene} = s;
  await engine.render(e(Scene, {route: 'artist', claim: true}));
  assert.equal(engine.focused, 'artist-play');
  await engine.render(e(Scene, {}));
  assert.notEqual(engine.focused, 'native-shell-routes', 'the closed route layer never keeps focus with nothing visible');
  assert.equal(engine.focusable('native-shell-routes'), false);
});

test('W-12: Back returns to the title it was opened from even when the windowed grid re-used that slot for another title', async () => {
  const kit = await tvFocusKit(), {e, engine, focus, Stop} = kit, n = engine.native;
  const registry = focus.createFocusRegistry();
  // A natively windowed grid: focus ids name recycled slots; the title in each slot is its entity (#333).
  const Grid = ({titles, covered}: {titles: string[]; covered: boolean}) => e(focus.FocusProvider, {registry},
    e(focus.CoveredLayer, {covered, autoFocus: true, style: {position: 'absolute', inset: 0}},
      e(focus.FocusRegion, {id: 'library.grid', orientation: 'horizontal', style: {flexDirection: 'row', gap: 24}},
        titles.map((title, slot) => e(focus.FocusItem, {key: `slot-${slot}`, focusId: `slot-${slot}`, entityId: title}, e(Stop, {id: `slot-${slot}`, focusId: `slot-${slot}`, ...card}))))));
  await engine.render(e(Grid, {titles: ['Alien', 'Arrival', 'Blade Runner', 'Brazil'], covered: false}));
  await engine.focus('slot-2');
  const opener = registry.capture()!;
  assert.equal(opener.entityId, 'Blade Runner');
  const release = registry.retain(opener);
  // Blade Runner opens; while its page covers the grid, the window re-projects: Blade Runner is now in slot 0.
  await engine.render(e(Grid, {titles: ['Blade Runner', 'Brazil', 'Casablanca', 'Dune'], covered: true}));
  await engine.render(e(Grid, {titles: ['Blade Runner', 'Brazil', 'Casablanca', 'Dune'], covered: false}));
  assert.equal(registry.focus(opener), true);
  await engine.settle();
  assert.equal(engine.focused, 'slot-0', 'restore follows the title, not the slot it used to be in');
  release();
  // While the title is not mounted, nothing is focused instead of the wrong title (the caller waits).
  await engine.render(e(Grid, {titles: ['Casablanca', 'Dune', 'Eraserhead', 'Fargo'], covered: false}));
  assert.equal(registry.focus(opener), false);
});

/** TV Settings: the search field above the page column, the page gate at x 519 and the page's rows
 * (SettingsScreen.tsx:314-347). A row control nested in its own region (a choice matrix) has no Left edge. */
async function settings({gate = true, matrixTop = 200}: {gate?: boolean; matrixTop?: number} = {}) {
  const kit = await tvFocusKit(), {e, engine, focus, Stop} = kit, n = engine.native;
  const Scene = () => e(focus.FocusProvider, {}, e(n.View, {style: {flex: 1}},
    gate ? e(focus.FocusGate, {regionId: 'settings.pages', testID: 'settings-page-gate', style: {position: 'absolute', left: 519, top: 0, bottom: 0, width: 2}}) : null,
    // Left column: the 80 px title, the search field, then the page column (SettingsScreen.tsx:315-347).
    e(n.View, {style: {position: 'absolute', left: 0, top: 80, width: 480}},
      e(focus.FocusRegion, {id: 'settings.search', edges: {down: 'settings.pages'}}, e(Stop, {id: 'settings-search', w: 480, h: 70})),
      e(focus.FocusRegion, {id: 'settings.pages', orientation: 'vertical', entry: 'page-profile', order: ['page-profile', 'page-playback'], edges: {right: 'settings.rows', up: 'settings.search'}, style: {marginTop: 40, gap: 10}},
        e(Stop, {id: 'page-profile', w: 480, h: 70}), e(Stop, {id: 'page-playback', w: 480, h: 70}))),
    e(n.View, {style: {position: 'absolute', left: 560, top: matrixTop, width: 1200}},
      e(focus.FocusRegion, {id: 'settings.rows', orientation: 'vertical', edges: {left: 'settings.pages'}, style: {gap: 20}},
        e(focus.FocusRegion, {id: 'settings.rows.matrix', orientation: 'horizontal', style: {flexDirection: 'row', gap: 20}},
          e(Stop, {id: 'row-skip-intro', w: 300, h: 70}), e(Stop, {id: 'row-skip-credits', w: 300, h: 70})),
        e(Stop, {id: 'row-autoplay', w: 1200, h: 70})))));
  await engine.render(e(Scene));
  return {...kit, Scene};
}

test('W-14: Left from any Settings row returns to the page it belongs to, never to search', async () => {
  const {engine} = await settings();
  await engine.focus('page-playback');
  await engine.press('right');
  assert.match(engine.focused ?? '', /^row-/, 'W-14 reopened (#339): Right from the page column enters its rows');
  await engine.focus('row-skip-intro');
  await engine.press('left');
  assert.equal(engine.focused, 'page-playback', 'the gate hands Left to the column\'s remembered page (#318)');
  await engine.focus('row-autoplay');
  await engine.press('left');
  assert.equal(engine.focused, 'page-playback');
});

test('W-14: without the page gate, Left from a nested row control lands on the search field (the walk finding)', async () => {
  const {engine} = await settings({gate: false, matrixTop: 120});
  await engine.focus('page-profile');
  await engine.focus('row-skip-intro');
  await engine.press('left');
  assert.equal(engine.focused, 'settings-search');
});

/** A title page: Play, a row that is still loading (skeleton slots only) and the next row's cards. */
async function titlePage(rule: boolean) {
  const kit = await tvFocusKit(), {e, engine, focus, Stop} = kit, n = engine.native;
  const Row = ({id, items}: {id: string; items: string[]}) => e(n.ScrollView, {testID: id, horizontal: true, scrollEnabled: rule ? tvScrollEnabled(true, items.length > 0) : undefined, style: {height: 400}},
    items.length ? e(focus.FocusRegion, {id, orientation: 'horizontal', style: {flexDirection: 'row', gap: 24}}, items.map(item => e(Stop, {key: item, id: item, ...card})))
      : [0, 1, 2, 3].map(slot => e(n.View, {key: slot, style: {...card, width: card.w, height: card.h}})));
  await engine.render(e(focus.FocusProvider, {}, e(n.View, {style: {flex: 1}},
    e(focus.FocusRegion, {id: 'title.actions', orientation: 'horizontal', style: {flexDirection: 'row', gap: 20, height: 120}}, e(Stop, {id: 'title-play', w: 240, h: 80}), e(Stop, {id: 'title-more', w: 240, h: 80})),
    e(Row, {id: 'title.loading', items: []}), e(Row, {id: 'title.related', items: ['related-1', 'related-2']}))));
  return kit;
}

test('W-17: Down from Play skips a row with nothing focusable in it and lands on the next real card', async () => {
  const {engine, ring} = await titlePage(true);
  await engine.focus('title-play');
  await engine.press('down');
  assert.equal(engine.focused, 'related-1');
  assert.equal(ring('related-1'), true);
});

test('W-17: without the scroller rule, focus lands on the empty row itself and nothing on screen is drawn focused', async () => {
  const {engine, ring} = await titlePage(false);
  await engine.focus('title-play');
  await engine.press('down');
  assert.equal(engine.focused, 'title.loading');
  assert.equal(ring('title-play'), false);
  await engine.press('right');
  assert.equal(engine.focused, 'title.loading', 'the arrows go nowhere');
});

test('W-14 hazard (D11): a nested row control level with the search field still reaches search through the gate', async () => {
  // FocusFinder prefers a candidate in the source's beam for Left/Right; the 2 px full-height gate is in
  // every row's beam, but so is the search field when the row sits at its height, and the field is nearer
  // by weighted distance. A direct row child keeps its Left edge to the column and is safe.
  const {engine} = await settings({matrixTop: 90});
  await engine.focus('page-profile');
  await engine.focus('row-skip-intro');
  await engine.press('left');
  assert.equal(engine.focused, 'settings-search', 'geometry decides; step 4 replaces the gate with inherited region edges');
  await engine.focus('row-autoplay');
  await engine.press('left');
  assert.equal(engine.focused, 'page-profile', 'the region edge (nextFocusLeft) is not geometric');
});
