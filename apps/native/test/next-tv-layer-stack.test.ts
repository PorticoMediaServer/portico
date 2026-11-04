import test from 'node:test';
import assert from 'node:assert/strict';
import {createFocusRegistry} from '../src-next/kit/focus/registry.ts';
import {createTVLayerStack, presentTVLayer, tvLayerSpecs, tvRouteLayerKeys} from '../src-next/kit/focus/layer-stack.ts';
import {tvFocusKit} from './helpers/tv-focus-kit.mjs';

// TV-FOCUS-REVIEW §5 step 2: one stack owns which full-screen TV layer is on top, what it covers, and where
// focus goes when a layer opens over another or closes. Pure behaviour first, then D-pad sequences through the
// fake native focus engine with the real CoveredLayer, registry and Focusable.

function clock() {
  const timers = new Map<number, () => void>(); let next = 0;
  return {later: (run: () => void) => {timers.set(++next, run); return next;}, cancel: (id: unknown) => {timers.delete(id as number);},
    fire: () => {for (const [id, run] of [...timers]) {timers.delete(id); run();}}, size: () => timers.size};
}
const host = (log: string[], name: string, onFocus?: () => void) => ({requestTVFocus() {log.push(name); onFocus?.();}});

test('pushing a layer keeps the covered layer\'s focus and claims focus into the new one once, then as elements attach', () => {
  const registry = createFocusRegistry(), time = clock(), log: string[] = [];
  const stack = createTVLayerStack(registry, time);
  const card = {regionId: 'grid', childId: 'blade-runner'};
  registry.configure({id: 'grid', order: ['blade-runner']}); registry.attach(card, host(log, 'card', () => registry.didFocus(card)));
  registry.didFocus(card);
  stack.sync([{key: 'page', host: () => host(log, 'page-layer')}]);
  assert.deepEqual(log, [], 'the first layer is the starting state: nothing claims');
  stack.sync([{key: 'page'}, {key: 'route:1', host: () => host(log, 'route-layer')}]);
  assert.equal(stack.top(), 'route:1');
  assert.deepEqual(stack.opener('page'), {...registry.capture(), order: ['blade-runner']});
  assert.equal(registry.isRegionActive('grid'), true, 'the opener is retained while covered');
  assert.deepEqual(log, ['route-layer']);
  const play = {regionId: 'title.actions', childId: 'play'};
  registry.configure({id: 'title.actions', order: ['play']}); registry.attach(play, host(log, 'play'));
  assert.deepEqual(log, ['route-layer', 'route-layer', 'route-layer'], 'asked again as the route\'s elements attach (configure, attach)');
  registry.didFocus(play);
  registry.configure({id: 'title.more', order: ['more']});
  assert.equal(log.length, 3, 'focus moved into the route: the claim is over');
  assert.equal(time.size(), 0, 'and its deadline is cancelled');
});

test('popping a layer returns the exact opener, waiting while it is not attached, then falls back once at the deadline', () => {
  const registry = createFocusRegistry(), time = clock(), log: string[] = [];
  const stack = createTVLayerStack(registry, time);
  const card = {regionId: 'grid', childId: 'blade-runner'}, sort = {regionId: 'toolbar', childId: 'sort'};
  registry.configure({id: 'toolbar', order: ['sort']}); registry.attach(sort, host(log, 'sort', () => registry.didFocus(sort)));
  const grid = (enabled: boolean) => registry.configure({id: 'grid', order: ['blade-runner'], neighbors: {previous: 'toolbar'}, enabled});
  grid(true); registry.attach(card, host(log, 'card', () => registry.didFocus(card)));
  registry.didFocus(card);
  const page = {key: 'page', host: () => host(log, 'page-layer')};
  stack.sync([page]); stack.sync([page, {key: 'route:1'}]);
  grid(false);
  stack.sync([page]);
  assert.deepEqual(log, [], 'the card is re-enabling: wait, do not land on the toolbar');
  grid(true);
  assert.deepEqual(log, ['card'], 'asked as soon as it is back');
  assert.equal(time.size(), 0);
  // Never comes back: one fallback at the deadline (its region's neighbour).
  stack.sync([page, {key: 'route:2'}]); grid(false); log.length = 0;
  stack.sync([page]);
  time.fire();
  assert.deepEqual(log, ['sort']);
});

test('a layer that is not on top leaving changes nothing; the top leaving returns to the next one down', () => {
  const registry = createFocusRegistry(), time = clock(), log: string[] = [];
  const stack = createTVLayerStack(registry, time);
  const card = {regionId: 'row', childId: 'card'};
  registry.configure({id: 'row', order: ['card']}); registry.attach(card, host(log, 'card', () => registry.didFocus(card)));
  registry.didFocus(card);
  stack.sync([{key: 'page'}]); stack.sync([{key: 'page'}, {key: 'player'}]); stack.sync([{key: 'page'}, {key: 'player'}, {key: 'listening'}]);
  registry.didBlur(card);
  stack.sync([{key: 'page'}, {key: 'listening'}]);
  assert.deepEqual(log, [], 'the player folding under the listening screen is not a return');
  stack.sync([{key: 'page'}]);
  assert.deepEqual(log, ['card'], 'the listening screen closing returns the page its card');
});

test('focus in an overlay when a layer opens: the covered layer is owed the overlay\'s opener, not the overlay control', () => {
  const registry = createFocusRegistry(), time = clock(), log: string[] = [];
  const stack = createTVLayerStack(registry, time);
  const programme = {regionId: 'guide', childId: 'programme-2'}, watch = {regionId: 'panel', childId: 'watch'};
  registry.configure({id: 'guide', order: ['programme-2']}); registry.attach(programme, host(log, 'programme', () => registry.didFocus(programme)));
  registry.didFocus(programme);
  registry.configure({id: 'panel', order: ['watch']}); registry.registerReturn('panel', registry.capture()); registry.attach(watch, host(log, 'watch'));
  registry.didFocus(watch);
  stack.sync([{key: 'page'}, {key: 'route:live'}]);
  stack.sync([{key: 'page'}, {key: 'route:live'}, {key: 'route:channel'}]);
  assert.equal(stack.opener('route:live')?.childId, 'programme-2');
  registry.attach(watch, null);
  stack.sync([{key: 'page'}, {key: 'route:live'}]);
  assert.deepEqual(log, ['programme']);
});

test('the shell derives one layer per route occurrence above the page, then the player and the listening screen', () => {
  const navigation = (stack: any[]) => ({subscribe: () => () => {}, getSnapshot: () => ({route: stack.at(-1).route, revision: 0, authority: {} as any}), presentationStack: () => stack});
  const home = {occurrence: '0', route: {kind: 'shell', destination: {kind: 'home'}}};
  assert.equal(tvRouteLayerKeys(navigation([home]) as any), '[]');
  assert.equal(tvRouteLayerKeys(navigation([home, {occurrence: '3', route: {kind: 'title'}}, {occurrence: '4', route: {kind: 'person'}}]) as any), '["route:3","route:4"]');
  assert.equal(tvRouteLayerKeys(navigation([{occurrence: '1', route: {kind: 'title'}}, {occurrence: '2', route: {kind: 'shell', destination: {kind: 'saved'}}}]) as any), '[]',
    'routes under a later page are not over it');
  const specs = tvLayerSpecs(['route:3'], true, true, () => null, () => null);
  assert.deepEqual(specs.map(spec => spec.key), ['page', 'route:3', 'player', 'listening']);
  assert.equal(specs[2]!.host, undefined, 'the player claims focus itself');
  assert.equal(specs[3]!.host, undefined);
  assert.equal(specs[3]!.claim!(), false, 'no listening screen provides its claim: nothing is asked');
});

test('TVF-A1: the listening screen says which control it opens on; the stack asks it when the layer goes on top, then as elements attach, then stops', async () => {
  const {provideTVLayerClaim} = await import('../src-next/kit/focus/layer-stack.ts');
  const registry = createFocusRegistry(), time = clock(), log: string[] = [];
  const stack = createTVLayerStack(registry, time);
  const card = {regionId: 'album', childId: 'play'};
  registry.configure({id: 'album', order: ['play']}); registry.attach(card, host(log, 'album-play', () => registry.didFocus(card)));
  registry.didFocus(card);
  let attached = false;
  const release = provideTVLayerClaim('listening', () => {log.push('claim'); return attached;});
  try {
    stack.sync(tvLayerSpecs([], false, false, () => host(log, 'page-layer'), () => null));
    stack.sync(tvLayerSpecs([], false, true, () => host(log, 'page-layer'), () => null));
    assert.equal(stack.top(), 'listening');
    assert.deepEqual(stack.opener('page')?.childId, 'play', 'the album keeps where focus was');
    assert.deepEqual(log, ['claim'], 'asked once as it goes on top, not the page host');
    const toggle = {regionId: 'listening.transport', childId: 'toggle'};
    registry.configure({id: 'listening.transport', order: ['toggle']});
    assert.deepEqual(log, ['claim', 'claim'], 'asked again as its views attach');
    attached = true; registry.attach(toggle, host(log, 'toggle')); registry.didFocus(toggle);
    const before = log.length;
    registry.configure({id: 'listening.queue', order: ['row']});
    assert.equal(log.length, before, 'focus is inside: the claim is over');
    assert.equal(time.size(), 0);
    stack.sync(tvLayerSpecs([], false, false, () => host(log, 'page-layer'), () => null));
    assert.deepEqual(registry.capture() && {regionId: registry.capture()!.regionId, childId: registry.capture()!.childId}, card, 'closing it returns the album\'s Play');
  } finally {release(); stack.dispose();}
  assert.equal(tvLayerSpecs([], false, true, () => null, () => null)[1]!.claim!(), false, 'a released claim is gone');
});

test('TVF-A1 (#577 review): a listening screen still moving in declines; the stack asks again when its transport region turns enabled, and then it lands', async () => {
  const {provideTVLayerClaim} = await import('../src-next/kit/focus/layer-stack.ts');
  const registry = createFocusRegistry(), time = clock(), log: string[] = [];
  const stack = createTVLayerStack(registry, time);
  const card = {regionId: 'album', childId: 'play'};
  registry.configure({id: 'album', order: ['play']}); registry.attach(card, host(log, 'album-play', () => registry.didFocus(card)));
  registry.didFocus(card);
  const toggle = {regionId: 'listening.transport', childId: 'toggle'};
  // The screen's views attach while its region is still disabled (page not ready).
  registry.configure({id: 'listening.transport', order: ['toggle'], enabled: false});
  registry.attach(toggle, host(log, 'toggle', () => registry.didFocus(toggle)));
  let ready = false;
  const release = provideTVLayerClaim('listening', () => {log.push('claim'); if (!ready) return false; registry.child(toggle)?.requestTVFocus?.(); return true;});
  try {
    stack.sync(tvLayerSpecs([], false, false, () => host(log, 'page-layer'), () => null));
    stack.sync(tvLayerSpecs([], false, true, () => host(log, 'page-layer'), () => null));
    assert.deepEqual(log, ['claim'], 'asked as it goes on top: declines while moving in');
    ready = true;
    registry.configure({id: 'listening.transport', order: ['toggle'], enabled: true});
    assert.deepEqual(log, ['claim', 'claim', 'toggle'], 'the enabled flip asks again, and it lands on Play/Pause');
    assert.equal(registry.capture()?.childId, 'toggle');
    assert.equal(time.size(), 0, 'the claim is over');
  } finally {release(); stack.dispose();}
});

/** The shell's page and route layers with the real stack doing the claims and returns. */
async function layered(platform: 'android' | 'tvos' = 'android') {
  const kit = await tvFocusKit({platform}), {e, engine, focus, Stop, React} = kit, n = engine.native;
  const registry = focus.createFocusRegistry();
  const pageHost: {current: any} = React.createRef(), routeHost: {current: any} = React.createRef();
  const stack = focus.createTVLayerStack(registry);
  // Which layer hosts the stack asked to take focus (its claim into a layer that is owed nothing of its own).
  const claims: string[] = [];
  const claimed = (name: string, ref: {current: any}) => () => ref.current && {requestTVFocus() {claims.push(name); focus.requestNativeFocus(ref.current);}};
  const pageClaim = claimed('page', pageHost), routeClaim = claimed('route', routeHost);
  const card = {w: 300, h: 170};
  // A presented state (the refused-link page) drawn outside the shell layers, as RestoredShell draws it.
  const Refused = () => {
    React.useLayoutEffect(() => presentTVLayer('content-link-unavailable', () => registry.focusRegion('content-link-unavailable')), []);
    return e(n.View, {style: {position: 'absolute', inset: 0}},
      e(focus.FocusRegion, {id: 'content-link-unavailable', orientation: 'horizontal', hold: true, style: {flexDirection: 'row', gap: 20, marginTop: 400}},
        e(Stop, {id: 'refused-home', w: 240, h: 80}), e(Stop, {id: 'refused-back', w: 240, h: 80})));
  };
  const Scene = ({routes = [], titles = ['Alien', 'Arrival', 'Blade Runner', 'Brazil'], player = false, presented = false, page = 'page', pageStart, routeStart, dock = false}: {routes?: string[]; titles?: string[]; player?: boolean;
    /** Something else uncovered first in the window's focus order (a docked control), covered only while a state is
     *  presented. When the refused page goes, the engine's native recovery (Android: the window's first focusable;
     *  tvOS: the root's preferred view, else the first in the tree) lands on it, so only the stack's claim can put
     *  focus on the page or route. An ordinary CoveredLayer, so it is selectable again in the same commit on tvOS. */ dock?: boolean;
    /** The refused-link page over everything. */ presented?: boolean;
    /** The page's own identity: a new value remounts it (a profile switch under a PIN pad or the refused page). */ page?: string;
    /** The control the page or the route says it opens on (`preferredFocus`). */ pageStart?: string; routeStart?: string}) => {
    const specs = tvLayerSpecs(routes.map(route => `route:${route}`), player, false, pageClaim, routeClaim, presented ? ['content-link-unavailable'] : []);
    const top = specs.at(-1)!.key;
    React.useLayoutEffect(() => {stack.sync(specs);});
    const route = routes.at(-1);
    return e(focus.FocusProvider, {registry}, e(n.View, {style: {flex: 1}},
      dock ? e(focus.CoveredLayer, {covered: top.startsWith('presented:'), testID: 'scene-dock', style: {position: 'absolute', left: 0, top: 0, width: 400, height: 90}},
        e(Stop, {id: 'dock-play', w: 120, h: 80})) : null,
      e(focus.CoveredLayer, {covered: top !== 'page', autoFocus: true, hostRef: pageHost, testID: 'native-shell-base', style: {position: 'absolute', inset: 0}},
        e(n.View, {key: page, style: {flex: 1}},
          e(focus.FocusRegion, {id: 'library.toolbar', orientation: 'horizontal', edges: {down: 'library.grid'}, style: {flexDirection: 'row', gap: 20}},
            e(Stop, {id: 'switch-library', w: 360, h: 60, preferredFocus: pageStart === 'switch-library'}), e(Stop, {id: 'sort', w: 200, h: 60})),
          e(focus.FocusRegion, {id: 'library.grid', orientation: 'horizontal', edges: {up: 'library.toolbar'}, style: {flexDirection: 'row', gap: 24, marginTop: 40}},
            titles.map((title, slot) => e(focus.FocusItem, {key: `slot-${slot}`, focusId: `slot-${slot}`, entityId: title}, e(Stop, {id: `slot-${slot}`, focusId: `slot-${slot}`, preferredFocus: pageStart === `slot-${slot}`, ...card})))))),
      e(focus.CoveredLayer, {covered: !top.startsWith('route:'), autoFocus: true, hostRef: routeHost, testID: 'native-shell-routes', style: {position: 'absolute', inset: 0}},
        route ? e(n.View, {key: route, style: {position: 'absolute', left: 0, top: 500, width: 1920, height: 580}},
          e(focus.FocusRegion, {id: `${route}.actions`, orientation: 'horizontal', style: {flexDirection: 'row', gap: 20}},
            e(Stop, {id: `${route}-play`, w: 240, h: 80, preferredFocus: routeStart === `${route}-play`}), e(Stop, {id: `${route}-more`, w: 240, h: 80, preferredFocus: routeStart === `${route}-more`}))) : null),
      presented ? e(Refused, {}) : null,
      player ? e(n.View, {style: {position: 'absolute', left: 0, top: 900, width: 1920, height: 180}},
        e(focus.FocusRegion, {id: 'player.controls', orientation: 'horizontal', hold: true, style: {flexDirection: 'row', gap: 20}},
          e(Stop, {id: 'player-pause', w: 120, h: 80, preferredFocus: true}), e(Stop, {id: 'player-next', w: 120, h: 80}))) : null));
  };
  /** The step that put focus where it is now: 'request' for a JS request (the stack's claim or a control's own
   *  preferred ask), 'recover*' when the fake engine's native recovery picked it. */
  const landing = () => engine.log.filter((step: any) => step.to !== undefined).at(-1)?.why;
  return {...kit, Scene, stack, claims, landing};
}

test('W-9 through the stack: a route opened from a card takes focus; nothing under it is reachable; Back returns to the card', async () => {
  const s = await layered(), {engine, e, Scene} = s;
  await engine.render(e(Scene, {}));
  await engine.focus('slot-2');
  await engine.render(e(Scene, {routes: ['title']}));
  assert.equal(engine.focused, 'title-play');
  for (const key of ['up', 'left', 'up', 'down'] as const) {await engine.press(key); assert.match(engine.focused ?? '', /^title-/);}
  await engine.press('select');
  assert.deepEqual(s.presses, ['title-play']);
  await engine.render(e(Scene, {}));
  assert.equal(engine.focused, 'slot-2', 'Back lands on the card the title was opened from');
});

test('W-12 through the stack: route over route returns to the exact element; the windowed grid return follows the title', async () => {
  const s = await layered(), {engine, e, Scene} = s;
  await engine.render(e(Scene, {}));
  await engine.focus('slot-2');
  await engine.render(e(Scene, {routes: ['title']}));
  await engine.press('right');
  assert.equal(engine.focused, 'title-more');
  await engine.render(e(Scene, {routes: ['title', 'person']}));
  assert.equal(engine.focused, 'person-play');
  await engine.render(e(Scene, {routes: ['title']}));
  assert.equal(engine.focused, 'title-more', 'Back from the person page returns to More, not the title\'s first action');
  // The grid re-projects while covered: Blade Runner now lives in slot 0.
  await engine.render(e(Scene, {routes: ['title'], titles: ['Blade Runner', 'Brazil', 'Casablanca', 'Dune']}));
  await engine.render(e(Scene, {titles: ['Blade Runner', 'Brazil', 'Casablanca', 'Dune']}));
  assert.equal(engine.focused, 'slot-0');
});

test('the player over the page: the page is covered, the player claims itself, and closing it returns the card', async () => {
  for (const platform of ['android', 'tvos'] as const) {
    const s = await layered(platform), {engine, e, Scene} = s;
    await engine.render(e(Scene, {}));
    await engine.focus('slot-1');
    await engine.render(e(Scene, {player: true}));
    assert.equal(engine.focused, 'player-pause', `${platform}: the player's own preferred control`);
    assert.equal(engine.focusable('slot-1'), false);
    await engine.render(e(Scene, {}));
    assert.equal(engine.focused, 'slot-1', platform);
  }
});

test('step 4: preferredFocus asks once on both platforms; a layout change on tvOS never takes focus back, and a page returned to by Back keeps the stack\'s restore', async () => {
  for (const platform of ['android', 'tvos'] as const) {
    const kit = await tvFocusKit({platform}), {e, engine, focus, Stop} = kit, n = engine.native;
    const registry = focus.createFocusRegistry();
    const Title = ({wide, covered}: {wide: boolean; covered: boolean}) => e(focus.FocusProvider, {registry},
      e(focus.CoveredLayer, {covered, autoFocus: true, style: {position: 'absolute', inset: 0}},
        e(focus.FocusRegion, {id: 'title.actions', orientation: 'horizontal', style: {flexDirection: 'row', gap: 20}},
          e(Stop, {id: 'play', w: wide ? 320 : 240, h: 80, preferredFocus: true}), e(Stop, {id: 'more', w: 240, h: 80}))),
      e(n.View, {style: {position: 'absolute', left: 0, top: 600}}, e(n.Pressable, {testID: 'over', style: {width: 200, height: 100}})));
    await engine.render(e(Title, {wide: false, covered: false}));
    assert.equal(engine.focused, 'play', `${platform}: the declared start`);
    await engine.press('right');
    await engine.render(e(Title, {wide: true, covered: false}));
    assert.equal(engine.focused, 'more', `${platform}: Play's label grew; focus stays on More`);
    await engine.render(e(Title, {wide: true, covered: true}));
    await engine.focus('over');
    await engine.render(e(Title, {wide: true, covered: false}));
    assert.notEqual(engine.focused, 'play', `${platform}: uncovering does not ask again`);
  }
});

test('a refused content link on TV: a presented state covers the shell, takes focus on Home, and closing it returns focus to where it was', async () => {
  const {presentTVLayer, tvPresentedLayers} = await import('../src-next/kit/focus/layer-stack.ts');
  const registry = createFocusRegistry(), time = clock(), log: string[] = [];
  const stack = createTVLayerStack(registry, time);
  const card = {regionId: 'home.continue', childId: 'film'};
  registry.configure({id: 'home.continue', order: ['film']}); registry.attach(card, host(log, 'home-card', () => registry.didFocus(card)));
  registry.didFocus(card);
  const specs = () => tvLayerSpecs([], false, false, () => host(log, 'page-layer'), () => host(log, 'route-layer'), JSON.parse(tvPresentedLayers.getSnapshot()));
  stack.sync(tvLayerSpecs([], false, false, () => host(log, 'page-layer'), () => null));
  assert.equal(tvPresentedLayers.getSnapshot(), '[]');
  const home = {regionId: 'content-link-unavailable', childId: 'home'};
  registry.configure({id: 'content-link-unavailable', order: ['home'], hold: true});
  registry.attach(home, host(log, 'home-button', () => registry.didFocus(home)));
  let notified = 0; const off = tvPresentedLayers.subscribe(() => {notified++;});
  const release = presentTVLayer('content-link-unavailable', () => registry.focusRegion('content-link-unavailable'));
  try {
    assert.equal(notified, 1); assert.deepEqual(JSON.parse(tvPresentedLayers.getSnapshot()), ['content-link-unavailable']);
    stack.sync(specs());
    assert.equal(stack.top(), 'presented:content-link-unavailable', 'over the page');
    assert.equal(specs().at(-1)!.key, 'presented:content-link-unavailable', 'above the player and listening layers too (it is last)');
    assert.equal(registry.capture()?.childId, 'home', 'focus is on Home, not the covered page');
    release();
    assert.equal(tvPresentedLayers.getSnapshot(), '[]');
    stack.sync(specs());
    assert.equal(registry.capture()?.childId, 'film', 'closing returns to where focus was');
  } finally {release(); off(); stack.dispose();}
});

// Prior art (Plezy covered_route_focus_boundary case 3, lead's TV-FOCUS-PRIOR-ART note): a screen that mounts while
// covered has no focus of its own to be given back. When the cover goes it must get its entry focus (the control it
// opens on, else its first), at once, on both platforms; never nothing, never a stale element from before it mounted.
test('mounted while covered: a route opened under the refused-link page gets its entry focus when the page closes', async () => {
  for (const platform of ['android', 'tvos'] as const) {
    for (const routeStart of [undefined, 'title-more'] as const) {
      const s = await layered(platform), {engine, e, Scene} = s, label = `${platform}, ${routeStart ?? 'no declared start'}`;
      await engine.render(e(Scene, {dock: true}));
      await engine.focus('slot-2');
      await engine.render(e(Scene, {dock: true, presented: true}));
      assert.equal(engine.focused, 'refused-home', `${label}: the refused page takes focus`);
      // A deep link navigates under it: the route mounts covered.
      await engine.render(e(Scene, {dock: true, presented: true, routes: ['title'], routeStart}));
      assert.equal(engine.focused, 'refused-home', `${label}: nothing under the page takes focus, not even the route's declared start`);
      assert.equal(engine.focusable('title-play'), false, label);
      assert.equal(engine.focusable('dock-play'), false, `${label}: the dock is covered too`);
      s.claims.length = 0;
      await engine.render(e(Scene, {dock: true, routes: ['title'], routeStart}));
      assert.equal(engine.focused, routeStart ?? 'title-play', `${label}: the route's entry focus`);
      // The engine's own recovery (the page's guide, the window's first focusable, the root's preferred view) lands
      // on the same controls, so the test names the step: the stack claimed into the route, and a request landed.
      assert.deepEqual([...new Set(s.claims)], ['route'], `${label}: the stack claims into the route it never gave focus to`);
      assert.equal(s.landing(), 'request', `${label}: focus came from a request, not the engine's recovery`);
      await engine.render(e(Scene, {dock: true}));
      assert.equal(engine.focused, 'slot-2', `${label}: Back from the route still returns to the card`);
    }
  }
});

test('mounted while covered: a page that remounts under the refused-link page opens on its own start, not the old page\'s focus', async () => {
  for (const platform of ['android', 'tvos'] as const) {
    const s = await layered(platform), {engine, e, Scene} = s;
    await engine.render(e(Scene, {dock: true, page: 'profile-a'}));
    await engine.focus('slot-2');
    await engine.render(e(Scene, {dock: true, page: 'profile-a', presented: true}));
    // The profile changes under it: a new page with other titles, which says it opens on its first card.
    const titles = ['Heat', 'Ran', 'Up', 'Zodiac'];
    await engine.render(e(Scene, {dock: true, page: 'profile-b', presented: true, titles, pageStart: 'slot-0'}));
    assert.equal(engine.focused, 'refused-home', `${platform}: still on the refused page`);
    await engine.render(e(Scene, {dock: true, page: 'profile-b', titles, pageStart: 'slot-0'}));
    assert.equal(engine.focused, 'slot-0', `${platform}: the new page's declared start, not slot-2 of the old one`);
    // The page is owed the old card, so the stack restores (not claims); the new page's own preferred ask, held while
    // covered, is what lands, and the restore yields to it instead of moving focus onto the old position.
    assert.equal(s.landing(), 'request', `${platform}: a request, not the engine's recovery`);
    assert.deepEqual(s.claims, [], `${platform}: no claim: the page was owed focus, and its own start won`);
  }
});

test('covered before it took focus: a page with no declared start under the refused-link page is never left without focus', async () => {
  for (const platform of ['android', 'tvos'] as const) {
    const s = await layered(platform), {engine, e, Scene} = s;
    // Cold start from a refused link: the shell is up but nothing on the page has taken focus yet when the link's
    // check refuses it (NativeContentLinks presents in a later commit than the shell's first), so the page is owed
    // nothing of its own.
    await engine.render(e(Scene, {dock: true}));
    const before = engine.focused;
    assert.notEqual(before?.startsWith('slot-') || before === 'switch-library' || before === 'sort', true, `${platform}: nothing on the page has focus yet (got ${before})`);
    await engine.render(e(Scene, {dock: true, presented: true}));
    assert.equal(engine.focused, 'refused-home', `${platform} (page focus before: ${before})`);
    s.claims.length = 0;
    await engine.render(e(Scene, {dock: true}));
    assert.ok(engine.focused && !engine.focused.startsWith('refused-'), `${platform}: focus is on the page (got ${engine.focused})`);
    assert.match(engine.focused ?? '', /^(switch-library|sort|slot-\d)$/, platform);
    assert.deepEqual([...new Set(s.claims)], ['page'], `${platform}: the stack claims into the page it never gave focus to`);
    assert.equal(s.landing(), 'request', `${platform}: focus came from that request, not the engine's recovery`);
  }
});
