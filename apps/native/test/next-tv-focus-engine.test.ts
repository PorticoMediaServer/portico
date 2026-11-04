import test from 'node:test';
import assert from 'node:assert/strict';
import {tvFocusEngine} from './helpers/tv-focus-engine.mjs';
import {tvFocusKit} from './helpers/tv-focus-kit.mjs';

// Calibration of the fake native focus engine against react-native-tvos 0.83.10-1. Each test pins one
// native rule the TV sequences rely on, with the library source it models. A rule that depends on
// device behaviour nobody has observed is an engine assumption with its device check (D1, D7, D10).

const row = (e: any, n: any, ids: string[], y = 0, extra: any = {}) => e(n.View, {style: {flexDirection: 'row', gap: 20, position: 'absolute', left: 0, top: y}, ...extra},
  ids.map(id => e(n.Pressable, {key: id, testID: id, style: {width: 200, height: 100}})));

test('the D-pad moves to the nearest focusable in the direction, and stops at a true end', async () => {
  const engine = await tvFocusEngine(), e = engine.React.createElement, n = engine.native;
  await engine.render(e(n.View, {style: {flex: 1}}, row(e, n, ['a', 'b', 'c']), row(e, n, ['d', 'e'], 300)));
  await engine.focus('b');
  await engine.press('right'); assert.equal(engine.focused, 'c');
  await engine.press('right'); assert.equal(engine.focused, 'c', 'no wrap at the end of a row');
  await engine.press('down'); assert.equal(engine.focused, 'e', 'down lands on the nearest card below (in beam)');
  await engine.press('left'); assert.equal(engine.focused, 'd');
  await engine.press('up'); assert.equal(engine.focused, 'a');
});

test('a disabled or unfocusable host is never a D-pad candidate', async () => {
  const engine = await tvFocusEngine(), e = engine.React.createElement, n = engine.native;
  await engine.render(e(n.View, {style: {flexDirection: 'row', gap: 20}},
    e(n.Pressable, {testID: 'a', style: {width: 200, height: 100}}), e(n.Pressable, {testID: 'b', focusable: false, style: {width: 200, height: 100}}),
    e(n.Pressable, {testID: 'c', style: {width: 200, height: 100}})));
  await engine.focus('a'); await engine.press('right');
  assert.equal(engine.focused, 'c');
});

test('nextFocus* wins over geometry (Android FocusFinder.findUserSetNextFocus; tvOS per-side UIFocusGuide)', async () => {
  for (const platform of ['android', 'tvos']) {
    const engine = await tvFocusEngine({platform}), e = engine.React.createElement, n = engine.native;
    let farTag: number | undefined;
    const Far = () => e(n.Pressable, {testID: 'far', style: {width: 200, height: 100, position: 'absolute', left: 1500, top: 800}, ref: (host: any) => {farTag = host?.tag;}});
    const Scene = ({next}: {next?: number}) => e(n.View, {style: {flex: 1}}, e(n.View, {style: {flexDirection: 'row'}},
      e(n.Pressable, {testID: 'a', nextFocusRight: next, style: {width: 200, height: 100}}), e(n.Pressable, {testID: 'b', style: {width: 200, height: 100}})), e(Far));
    await engine.render(e(Scene, {}));
    await engine.render(e(Scene, {next: farTag}));
    await engine.focus('a'); await engine.press('right');
    assert.equal(engine.focused, 'far', platform);
  }
});

test('a focus guide redirects entry to its destination, else (autoFocus) its last focused child, else its first', async () => {
  const engine = await tvFocusEngine(), e = engine.React.createElement, n = engine.native;
  let destination: any, guide: any;
  const Scene = () => e(n.View, {style: {flex: 1}},
    e(n.Pressable, {testID: 'outside', style: {width: 200, height: 100, position: 'absolute', left: 0, top: 0}}),
    e(n.TVFocusGuideView, {ref: (host: any) => {guide = host;}, autoFocus: true, style: {position: 'absolute', left: 0, top: 300, flexDirection: 'row', gap: 20}},
      e(n.Pressable, {testID: 'one', style: {width: 200, height: 100}}), e(n.Pressable, {testID: 'two', style: {width: 200, height: 100}}),
      e(n.Pressable, {testID: 'three', ref: (host: any) => {destination = host;}, style: {width: 200, height: 100}})));
  await engine.render(e(Scene));
  await engine.focus('outside'); await engine.press('down');
  assert.equal(engine.focused, 'one', 'first visit: the first focusable child');
  await engine.press('right'); await engine.press('up'); await engine.press('down');
  assert.equal(engine.focused, 'two', 'autoFocus: back to the child last focused (ReactViewGroup.kt:1461-1480)');
  guide.setDestinations([destination]); await engine.settle();
  await engine.press('up'); await engine.press('down');
  assert.equal(engine.focused, 'three', 'destinations are favoured over autoFocus memory');
});

test('Android: a guide with no focusable child holds focus itself when entered by recovery (the "nothing visibly focused" state)', async () => {
  const engine = await tvFocusEngine(), e = engine.React.createElement, n = engine.native;
  const Scene = ({card}: {card: boolean}) => e(n.View, {style: {flex: 1}},
    e(n.TVFocusGuideView, {testID: 'layer', autoFocus: true, style: {position: 'absolute', inset: 0}}, card ? e(n.Pressable, {testID: 'card', style: {width: 200, height: 100}}) : null));
  await engine.render(e(Scene, {card: true}));
  await engine.focus('card');
  await engine.render(e(Scene, {card: false}));
  assert.equal(engine.focused, 'layer', 'recoverFocus parks on the parent guide (ReactViewGroup.kt:646-731)');
});

test('Android: a horizontal scroller with nothing focusable inside is itself a focus stop until scrolling is disabled (W-17 platform rule)', async () => {
  const engine = await tvFocusEngine(), e = engine.React.createElement, n = engine.native;
  const Scene = ({enabled}: {enabled?: boolean}) => e(n.View, {style: {flex: 1}},
    e(n.Pressable, {testID: 'play', style: {width: 200, height: 100}}),
    e(n.ScrollView, {testID: 'empty-row', horizontal: true, scrollEnabled: enabled, style: {height: 400}}, e(n.View, {style: {width: 600, height: 400}})),
    e(n.Pressable, {testID: 'below', style: {width: 200, height: 100}}));
  // TV rows are about 400 px apart; with that geometry FocusFinder's weighted distance (13·major² + minor²)
  // prefers the adjacent empty scroller to the next row's card.
  await engine.render(e(Scene, {}));
  await engine.focus('play'); await engine.press('down');
  assert.equal(engine.focused, 'empty-row', 'scrollEnabled sets the scroller focusable (ReactHorizontalScrollViewManager.setScrollEnabled)');
  await engine.render(e(Scene, {enabled: false}));
  await engine.focus('play'); await engine.press('down');
  assert.equal(engine.focused, 'below');
});

test('a trap keeps the D-pad inside its container in that direction only', async () => {
  const engine = await tvFocusEngine(), e = engine.React.createElement, n = engine.native;
  await engine.render(e(n.View, {style: {flex: 1}},
    e(n.Pressable, {testID: 'page', style: {width: 200, height: 100, position: 'absolute', left: 0, top: 600}}),
    e(n.TVFocusGuideView, {trapFocusDown: true, style: {position: 'absolute', left: 0, top: 0, flexDirection: 'row'}},
      e(n.Pressable, {testID: 'menu-a', style: {width: 200, height: 100}}), e(n.Pressable, {testID: 'menu-b', style: {width: 200, height: 100}}))));
  await engine.focus('menu-a');
  await engine.press('down'); assert.equal(engine.focused, 'menu-a', 'trapFocusDown (ReactViewGroup.kt:1489-1508)');
  await engine.press('right'); assert.equal(engine.focused, 'menu-b');
});

test('covering: no new focus enters a blocked layer; on Android the focused control stays (D1), on tvOS focus moves out (D7)', async () => {
  for (const platform of ['android', 'tvos'] as const) {
    const engine = await tvFocusEngine({platform}), e = engine.React.createElement, n = engine.native;
    const Scene = ({covered}: {covered: boolean}) => e(n.View, {style: {flex: 1}},
      e(n.TVFocusGuideView, {testID: 'page', focusable: !covered, autoFocus: true, style: {position: 'absolute', inset: 0, flexDirection: 'row', gap: 20}},
        e(n.Pressable, {testID: 'card-1', style: {width: 200, height: 100}}), e(n.Pressable, {testID: 'card-2', style: {width: 200, height: 100}})),
      e(n.View, {style: {position: 'absolute', left: 0, top: 500}}, e(n.Pressable, {testID: 'route-play', style: {width: 200, height: 100}})));
    await engine.render(e(Scene, {covered: false}));
    await engine.focus('card-1');
    await engine.render(e(Scene, {covered: true}));
    if (platform === 'android') {
      assert.equal(engine.focused, 'card-1', 'D1: FOCUS_BLOCK_DESCENDANTS does not move focus already inside');
      await engine.press('right'); assert.equal(engine.focused, 'card-1', 'the covered sibling is no candidate');
      assert.equal(engine.focusable('card-2'), false);
    } else {
      assert.equal(engine.focused, 'route-play', 'D7: the focus engine leaves a non-interactive subtree');
    }
  }
});

test('tvOS: a guide created while covered survives uncovering when layout does not change, and its subtree stays unfocusable (review §4.4.1)', async () => {
  const engine = await tvFocusEngine({platform: 'tvos'}), e = engine.React.createElement, n = engine.native;
  // A plain TVFocusGuideView without autoFocus or destinations, the shape of a route's CoveredLayer before step 0.
  const Scene = ({covered}: {covered: boolean}) => e(n.View, {style: {flex: 1}},
    e(n.TVFocusGuideView, {testID: 'route', focusable: !covered, style: {position: 'absolute', inset: 0}}, e(n.Pressable, {testID: 'item', style: {width: 200, height: 100}})),
    e(n.Pressable, {testID: 'top', style: {width: 200, height: 100, position: 'absolute', left: 1000, top: 0}}));
  await engine.render(e(Scene, {covered: false}));
  assert.equal(engine.focusable('item'), true, 'never covered: no guide, interactive');
  await engine.render(e(Scene, {covered: true}));
  await engine.render(e(Scene, {covered: false}));
  assert.equal(engine.focusable('item'), false, 'handleFocusGuide only re-runs on a layout change (RCTMountingManager.mm:126-130)');
  // With autoFocus kept on, uncovering makes the guide selectable again.
  const fixed = await tvFocusEngine({platform: 'tvos'}), f = fixed.React.createElement, m = fixed.native;
  const Fixed = ({covered}: {covered: boolean}) => f(m.View, {style: {flex: 1}},
    f(m.TVFocusGuideView, {focusable: !covered, autoFocus: true, style: {position: 'absolute', inset: 0}}, f(m.Pressable, {testID: 'item', style: {width: 200, height: 100}})));
  await fixed.render(f(Fixed, {covered: true}));
  await fixed.render(f(Fixed, {covered: false}));
  assert.equal(fixed.focusable('item'), true);
});

test('hasTVPreferredFocus: Android asks once when it becomes true; tvOS asks again on a layout change after focus was requested elsewhere (review §4.4.2)', async () => {
  for (const platform of ['android', 'tvos'] as const) {
    const engine = await tvFocusEngine({platform}), e = engine.React.createElement, n = engine.native;
    let episode: any;
    const Scene = ({wide}: {wide: boolean}) => e(n.View, {style: {flex: 1}},
      e(n.Pressable, {testID: 'play', hasTVPreferredFocus: true, style: {width: wide ? 320 : 200, height: 100}}),
      e(n.Pressable, {testID: 'episode', ref: (host: any) => {episode = host;}, style: {width: 200, height: 100, position: 'absolute', left: 0, top: 400}}));
    await engine.render(e(Scene, {wide: false}));
    assert.equal(engine.focused, 'play');
    episode.requestTVFocus(); await engine.settle();
    assert.equal(engine.focused, 'episode');
    await engine.render(e(Scene, {wide: true}));
    assert.equal(engine.focused, platform === 'android' ? 'episode' : 'play', platform);
  }
});

test('D10 (Android): re-rendering an autoFocus guide clears its native last-focused memory', async () => {
  const engine = await tvFocusEngine(), e = engine.React.createElement, n = engine.native;
  const Scene = ({tick}: {tick: number}) => e(n.View, {style: {flex: 1}},
    e(n.Pressable, {testID: 'outside', style: {width: 200, height: 100}}),
    e(n.TVFocusGuideView, {autoFocus: true, style: {flexDirection: 'row', opacity: tick ? 1 : 0.99}},
      e(n.Pressable, {testID: 'one', style: {width: 200, height: 100}}), e(n.Pressable, {testID: 'two', style: {width: 200, height: 100}})));
  await engine.render(e(Scene, {tick: 0}));
  await engine.focus('two'); await engine.press('up');
  assert.equal(engine.focused, 'outside');
  await engine.render(e(Scene, {tick: 1}));
  await engine.press('down');
  assert.equal(engine.focused, 'one', 'memory reset by the repeated setAutoFocusTV');
  const steady = await tvFocusEngine({assume: {propSettersRepeat: false}}), f = steady.React.createElement, m = steady.native;
  const Steady = ({tick}: {tick: number}) => f(m.View, {style: {flex: 1}},
    f(m.Pressable, {testID: 'outside', style: {width: 200, height: 100}}),
    f(m.TVFocusGuideView, {autoFocus: true, style: {flexDirection: 'row', opacity: tick ? 1 : 0.99}},
      f(m.Pressable, {testID: 'one', style: {width: 200, height: 100}}), f(m.Pressable, {testID: 'two', style: {width: 200, height: 100}})));
  await steady.render(f(Steady, {tick: 0}));
  await steady.focus('two'); await steady.press('up');
  await steady.render(f(Steady, {tick: 1}));
  await steady.press('down');
  assert.equal(steady.focused, 'two', 'without the repeat the memory holds');
});

test('Back runs BackHandler subscriptions last-registered first, and the first true stops it', async () => {
  const engine = await tvFocusEngine(), calls: string[] = [];
  engine.native.BackHandler.addEventListener('hardwareBackPress', () => {calls.push('shell'); return true;});
  const page = engine.native.BackHandler.addEventListener('hardwareBackPress', () => {calls.push('page'); return false;});
  engine.native.BackHandler.addEventListener('hardwareBackPress', () => {calls.push('overlay'); return true;});
  await engine.press('back');
  assert.deepEqual(calls, ['overlay']);
  page.remove(); calls.length = 0;
  await engine.press('back');
  assert.deepEqual(calls, ['overlay']);
});

test('the real kit binds through the engine: FocusRegion edges and memory, and the ring follows native focus', async () => {
  const kit = await tvFocusKit(), {e, engine, focus, Stop} = kit, n = engine.native;
  await engine.render(e(focus.FocusProvider, {}, e(n.View, {style: {flex: 1}},
    e(focus.FocusRegion, {id: 'top', orientation: 'horizontal', edges: {down: 'cards'}, style: {flexDirection: 'row', gap: 20}}, e(Stop, {id: 'play'}), e(Stop, {id: 'more'})),
    e(focus.FocusRegion, {id: 'cards', orientation: 'horizontal', edges: {up: 'top'}, style: {flexDirection: 'row', gap: 20, marginTop: 100}}, e(Stop, {id: 'c1'}), e(Stop, {id: 'c2'}), e(Stop, {id: 'c3'})))));
  await engine.focus('play');
  assert.equal(kit.ring('play'), true);
  await engine.press('down'); assert.equal(engine.focused, 'c1');
  assert.equal(kit.ring('play'), false); assert.equal(kit.ring('c1'), true);
  await engine.press('right'); await engine.press('right'); assert.equal(engine.focused, 'c3');
  await engine.press('up'); assert.equal(engine.focused, 'play', 'region memory: first visit of top remembered play');
  await engine.press('down'); assert.equal(engine.focused, 'c3', 'cards remembers c3 through its guide destination');
  await engine.press('select'); assert.deepEqual(kit.presses, ['c3']);
});

// D12-D15: native Modal windows (the refused-link page, 4 Nov). Each is a device check in the PR's batch.
const windowScene = (engine: any, {open = true, visible = true, ask}: {open?: boolean; visible?: boolean; ask?: string} = {}) => {
  const e = engine.React.createElement, n = engine.native;
  return e(n.View, {style: {flex: 1}}, row(e, n, ['a', 'b', 'c']),
    e(n.Pressable, {testID: 'chrome', hasTVPreferredFocus: ask === 'chrome', style: {position: 'absolute', left: 0, top: 800, width: 200, height: 100}}),
    open ? e(n.Modal, {testID: 'page', visible, onRequestClose: () => engine.log.push({why: 'request-close'}), onDismiss: () => engine.log.push({why: 'dismissed'})},
      row(e, n, ['home', 'other'], 400)) : null);
};
test('D12: a visible Modal is its own window: it takes focus, the D-pad sees only it, and Back or Menu reach its onRequestClose', async () => {
  for (const platform of ['android', 'tvos'] as const) {
    const engine = await tvFocusEngine({platform});
    await engine.render(windowScene(engine, {open: false}));
    await engine.focus('b');
    await engine.render(windowScene(engine));
    assert.equal(engine.focused, 'home', `${platform}: the window's first focusable`);
    assert.deepEqual(engine.windows(), [{testID: 'page', dismissing: false}]);
    for (const key of ['up', 'down', 'left'] as const) {await engine.press(key); assert.equal(engine.focused, 'home', `${platform}: ${key} stays in the window`);}
    await engine.press('right'); assert.equal(engine.focused, 'other');
    assert.equal(engine.focusable('b'), false, `${platform}: nothing under it is a target`);
    let handled = 0; const off = engine.native.BackHandler.addEventListener('hardwareBackPress', () => {handled++; return true;});
    await engine.press(platform === 'android' ? 'back' : 'menu');
    if (platform === 'tvos') await engine.finishDismissals(); // D16: Menu's request comes from UIKit's completion
    assert.equal(handled, 0, `${platform}: BackHandler never sees it`);
    assert.ok(engine.log.some((step: any) => step.why === 'request-close'), `${platform}: onRequestClose`);
    off.remove();
  }
});
test('D12: the window shown last is on top even when it sits earlier in the tree; it gets the D-pad and Back or Menu', async () => {
  for (const platform of ['android', 'tvos'] as const) {
    const engine = await tvFocusEngine({platform}), e = engine.React.createElement, n = engine.native, closes: string[] = [];
    const window = (id: string) => e(n.Modal, {key: id, testID: id, onRequestClose: () => closes.push(id)}, row(e, n, [`${id}-a`, `${id}-b`], 400));
    const scene = (early: boolean) => e(n.View, {style: {flex: 1}}, early ? window('early') : null, window('late'));
    await engine.render(scene(false));
    assert.equal(engine.focused, 'late-a');
    await engine.render(scene(true));
    assert.equal(engine.focused, 'early-a', `${platform}: the new window takes focus`);
    assert.deepEqual(engine.windows().map((w: any) => w.testID), ['late', 'early'], `${platform}: bottom first, in the order shown`);
    await engine.press('right');
    assert.equal(engine.focused, 'early-b', `${platform}: the D-pad stays in the top window`);
    await engine.press(platform === 'android' ? 'back' : 'menu');
    if (platform === 'tvos') await engine.finishDismissals();
    assert.deepEqual(closes, ['early'], `${platform}: the window shown last takes the key`);
  }
});
test('D12: an explicit nextFocus naming a host in another window is ignored; the D-pad stays in the top window', async () => {
  for (const platform of ['android', 'tvos'] as const) {
    const engine = await tvFocusEngine({platform}), e = engine.React.createElement, n = engine.native;
    const scene = (open: boolean, base?: number) => e(n.View, {style: {flex: 1}}, row(e, n, ['a', 'b']),
      open ? e(n.Modal, {testID: 'page', onRequestClose() {}}, e(n.Pressable, {testID: 'home', nextFocusRight: base, style: {width: 200, height: 100}})) : null);
    await engine.render(scene(false));
    await engine.render(scene(true, engine.tag('b')));
    assert.equal(engine.focused, 'home');
    await engine.press('right');
    assert.equal(engine.focused, 'home', `${platform}: a stale cross-window edge never crosses the window`);
  }
});
test('D13 (Android): a request under a Modal becomes that window\'s focus without key focus, and is what it gets back when the Modal closes', async () => {
  const engine = await tvFocusEngine({platform: 'android'});
  await engine.render(windowScene(engine, {open: false}));
  await engine.focus('b');
  await engine.render(windowScene(engine));
  await engine.render(windowScene(engine, {ask: 'chrome'}));
  assert.equal(engine.focused, 'home', 'key focus stays in the Modal');
  assert.equal(engine.windowFocus(undefined), 'chrome', 'the app window now keeps the chrome');
  await engine.render(windowScene(engine, {open: false, ask: 'chrome'}));
  assert.equal(engine.focused, 'chrome', 'closing lands on it (the hidden-picture risk the shell\'s Covered signal prevents)');
});
test('D14 (tvOS): requests under a Modal move nothing; a dismissal keeps it on screen until UIKit finishes, then the opener, then onDismiss', async () => {
  const engine = await tvFocusEngine({platform: 'tvos'});
  await engine.render(windowScene(engine, {open: false}));
  await engine.focus('b');
  await engine.render(windowScene(engine));
  await engine.render(windowScene(engine, {ask: 'chrome'}));
  assert.equal(engine.focused, 'home', 'the request under it moved nothing');
  await engine.render(windowScene(engine, {visible: false, ask: 'chrome'}));
  assert.deepEqual(engine.windows(), [{testID: 'page', dismissing: true}], 'still on screen');
  assert.equal(engine.focused, 'home');
  await engine.press('left');
  assert.equal(engine.focused, 'home', 'nothing moves during the dismissal');
  assert.ok(!engine.log.some((step: any) => step.why === 'dismissed'));
  await engine.finishDismissals();
  assert.deepEqual(engine.windows(), []);
  assert.equal(engine.focused, 'b', 'restoresFocusAfterTransition: the view focused when it opened');
  const steps = engine.log.map((step: any) => step.why);
  assert.ok(steps.lastIndexOf('window-close') < steps.lastIndexOf('dismissed'), 'focus is back before onDismiss');
});
test('D16 (tvOS): Menu in a Modal is UIKit\'s own dismissal: focus waits, then the opener, then onDismiss and onRequestClose; a later visible={false} reports nothing', async () => {
  const engine = await tvFocusEngine({platform: 'tvos'});
  await engine.render(windowScene(engine, {open: false}));
  await engine.focus('b');
  await engine.render(windowScene(engine));
  await engine.press('menu');
  assert.deepEqual(engine.windows(), [{testID: 'page', dismissing: true}], 'dismissing at once, still on screen');
  assert.equal(engine.focused, 'home');
  assert.ok(!engine.log.some((step: any) => step.why === 'request-close' || step.why === 'dismissed'), 'nothing reaches JS yet');
  await engine.press('left');
  assert.equal(engine.focused, 'home', 'nothing moves during the dismissal');
  await engine.finishDismissals();
  assert.deepEqual(engine.windows(), [], 'gone, though the Modal is still mounted');
  assert.equal(engine.focused, 'b', 'restoresFocusAfterTransition');
  const steps = engine.log.map((step: any) => step.why);
  assert.ok(steps.lastIndexOf('window-close') < steps.indexOf('dismissed') && steps.indexOf('dismissed') < steps.indexOf('request-close'), 'focus, then onDismiss, then onRequestClose');
  await engine.render(windowScene(engine, {visible: false}));
  await engine.finishDismissals();
  assert.equal(engine.log.filter((step: any) => step.why === 'dismissed').length, 1, 'nothing left to dismiss, so no second report');
  await engine.render(windowScene(engine, {open: false}));
  assert.equal(engine.focused, 'b');
});
