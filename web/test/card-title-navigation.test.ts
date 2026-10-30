import test from 'node:test';
import assert from 'node:assert/strict';
import {withSourceArtActivation, consumeSourceArtActivation} from '../src/kit/cards/source-art-activation.ts';
import {activateCard} from '../src/kit/cards/interaction.ts';
import {registerHooks} from 'node:module';
import {admittedTitleArtwork, backTitleArtwork, commitTitleArtwork, navigateTitleArtwork, originalArtworkReverseAvailable, releaseTitleArtwork, retireTitleArtworkScope, TITLE_ART_WAIT_MS, type ArtworkLocation, type TitleArtworkIntent} from '../src/platform/card-title-motion.ts';

const tick = async () => {for (let n = 0; n < 10; n++) await Promise.resolve();};
function fixture() {
  const saved = {document: globalThis.document, matchMedia: globalThis.matchMedia, getComputedStyle: globalThis.getComputedStyle};
  class Media extends EventTarget {matches = false;}
  const media = new Media();let policy: (() => void) | undefined;
  let scope = true, admitted = true, sourceLive = true, ua = false, calls = 0, transitionCalls = 0, skips = 0;
  let listener: ((event: any) => void) | undefined, oldListener: ((event: any) => void) | undefined;
  const source: ArtworkLocation = {key: 'source-entry', href: '/library/films', pathname: '/library/films'};
  let location = source, destination = {key: 'destination-entry', href: '/item/movie', pathname: '/item/movie'};
  let update!: () => Promise<void>, updatePromise: Promise<void> | undefined;
  let resolveReady!: () => void, resolveFinished!: () => void, resolveUpdated!: () => void;
  const modes: boolean[] = [], timing: any[] = [];
  const root: any = {dataset: {}, getAnimations: () => [{effect: {pseudoElement: `::view-transition-group(${art.style.viewTransitionName || target.style.viewTransitionName})`, updateTiming: (value: unknown) => timing.push(value)}}]};
  const document: any = {documentElement: root, body: {dataset: {}}, defaultView: {
    innerWidth: 1000, innerHeight: 800, matchMedia: () => media,
    MutationObserver: class {constructor(callback: () => void) {policy = callback;} observe() {}disconnect() {policy = undefined;}},
    requestAnimationFrame: (_callback: unknown) => 1, cancelAnimationFrame() {},
    getComputedStyle: (element: any) => ({visibility: element.visible === false ? 'hidden' : 'visible', display: 'block', opacity: '1', viewTransitionName: element.style.viewTransitionName || 'none'}),
  }, startViewTransition(callback: () => Promise<void>) {
    transitionCalls++;update = callback;
    return {ready: new Promise<void>(r => {resolveReady = r;}), finished: new Promise<void>(r => {resolveFinished = r;}),
      updateCallbackDone: new Promise<void>(r => {resolveUpdated = r;}), skipTransition() {skips++;}};
  }};
  const node = (): any => {const image = {isConnected: true, getAttribute: () => 'blob:original'};return ({querySelector: () => image, ownerDocument: document, isConnected: true, visible: true, style: {viewTransitionName: ''},
    getBoundingClientRect: () => ({left: 10, top: 10, width: 190, height: 285, right: 200, bottom: 295})});};
  const art = node(), target = node();
  Object.assign(globalThis, {document, matchMedia: () => media, getComputedStyle: document.defaultView.getComputedStyle});
  let onNavigate: (() => void) | undefined, onPrincipal: (() => void) | undefined;
  const intent: TitleArtworkIntent = {scope: 'original-scope', entry: Object.freeze({id: 'movie'}), itemId: 'movie', source,
    admitted: () => admitted, principalCurrent: () => {onPrincipal?.();return scope;}, sourceCurrent: () => sourceLive,
    location: () => location, browserOwns: () => ua,
    subscribe(callback) {listener = callback;oldListener = callback;return () => {listener = undefined;};},
    async navigate(manual) {calls++;modes.push(manual);onNavigate?.();listener?.({from: source, to: destination});location = destination;},
  };
  return {art, target, makeNode: node, document, root, media, source, intent, modes, timing,
    get calls() {return calls;}, get transitions() {return transitionCalls;}, get skips() {return skips;}, get listener() {return listener;},
    get location() {return location;}, set location(value) {location = value;}, set destination(value: ArtworkLocation) {destination = value;},
    set admitted(value: boolean) {admitted = value;}, set scope(value: boolean) {scope = value;}, set sourceLive(value: boolean) {sourceLive = value;}, set ua(value: boolean) {ua = value;},
    set onNavigate(value: (() => void) | undefined) {onNavigate = value;}, set onPrincipal(value: (() => void) | undefined) {onPrincipal = value;},
    async start() {updatePromise = update();await tick();},
    async capture() {await updatePromise;resolveUpdated();resolveReady();await tick();},
    async finish() {resolveFinished();await tick();},
    reduce() {root.dataset.reduceMotion = 'true';policy?.();},
    stale(event: any) {oldListener?.(event);},
    cleanup() {retireTitleArtworkScope(document, 'original-scope');Object.assign(globalThis, saved);},
  };
}
function install(f: ReturnType<typeof fixture>, claim: NonNullable<ReturnType<typeof admittedTitleArtwork>>) {
  return commitTitleArtwork(claim, f.target, () => {
    f.target.style.viewTransitionName = claim.name;
    return () => {if (f.target.style.viewTransitionName === claim.name) f.target.style.viewTransitionName = '';};
  });
}

test('physical activation exists only in the synchronous navigation branch and is consumed once', () => {
  const f = fixture();try {
    assert.equal(consumeSourceArtActivation(), undefined);
    withSourceArtActivation(f.art, () => {assert.equal(consumeSourceArtActivation()?.art, f.art);assert.equal(consumeSourceArtActivation(), undefined);}, () => true);
    assert.equal(consumeSourceArtActivation(), undefined);
  } finally {f.cleanup();}
});

test('original accepted entry waits for decoded committed target, transfers one name and runs420 out', async () => {
  const f = fixture();try {
    const navigation = navigateTitleArtwork(f.art, f.intent);const originalName = f.art.style.viewTransitionName;
    assert.match(originalName, /^card-source-art-/);assert.equal(f.calls, 0);
    await f.start();await navigation;assert.equal(f.calls, 1);assert.deepEqual(f.modes, [true]);
    assert.equal(f.art.style.viewTransitionName, '', 'old physical art relinquishes its name before target installation');
    const claim = admittedTitleArtwork(f.document, 'original-scope', 'movie', f.location)!;assert.ok(claim);
    assert.equal(f.target.style.viewTransitionName, '', 'async catalogue/decode cannot be guessed ready');
    assert.equal(install(f, claim), true);assert.equal(f.target.style.viewTransitionName, originalName);
    await f.capture();assert.equal(f.timing[0].duration, 420);assert.equal(f.timing[0].easing, 'cubic-bezier(.2, .8, .2, 1)');
    await f.finish();assert.equal(f.target.style.viewTransitionName, '');assert.equal(f.listener, undefined);
  } finally {f.cleanup();}
});

for (const kind of ['refused', 'initial-reduce', 'no-op', 'redirect', 'missing-key'] as const) test(`original ${kind} does not lend an artwork destination`, async () => {
  const f = fixture();try {
    if (kind === 'refused') f.admitted = false;
    if (kind === 'initial-reduce') f.root.dataset.reduceMotion = 'true';
    if (kind === 'no-op') f.destination = {...f.source};
    if (kind === 'redirect') f.destination = {key: 'redirect', href: '/settings', pathname: '/settings'};
    if (kind === 'missing-key') f.destination = {key: '', href: '/item/movie', pathname: '/item/movie'};
    const navigation = navigateTitleArtwork(f.art, f.intent);
    if (kind === 'refused') {await assert.rejects(navigation, {name: 'AbortError'});assert.equal(f.calls, 0);return;}
    if (kind !== 'initial-reduce') await f.start();
    await navigation;assert.equal(f.calls, 1);
    assert.equal(admittedTitleArtwork(f.document, 'original-scope', 'movie', f.location), undefined);
    assert.equal(f.art.style.viewTransitionName, '');
    if (kind === 'initial-reduce') {assert.equal(f.transitions, 0);assert.deepEqual(f.modes, [false]);}
  } finally {f.cleanup();}
});

test('same href new entry, replacement scope, late decode and replay cannot borrow the original destination', async () => {
  const f = fixture();try {
    const nav = navigateTitleArtwork(f.art, f.intent);await f.start();await nav;
    const claim = admittedTitleArtwork(f.document, 'original-scope', 'movie', f.location)!;
    assert.equal(admittedTitleArtwork(f.document, 'new-scope', 'movie', f.location), undefined);
    f.location = {...f.location, key: 'another-entry'};assert.equal(install(f, claim), false);
    f.stale({from: f.source, to: f.location});assert.equal(install(f, claim), false);
    f.target.style.viewTransitionName = 'successor-name';f.cleanup();assert.equal(f.target.style.viewTransitionName, 'successor-name');
  } finally {f.cleanup();}
});

test('reentrant principal refusal cannot dispatch or install after retiring its original visual owner', async () => {
  const f = fixture();try {
    const nav = navigateTitleArtwork(f.art, f.intent);
    f.onPrincipal = () => {f.onPrincipal = undefined;retireTitleArtworkScope(f.document, 'original-scope');};
    await f.start();await assert.rejects(nav, {name: 'AbortError'});assert.equal(f.calls, 0);assert.equal(f.art.style.viewTransitionName, '');
  } finally {f.cleanup();}
});

test('live reduction settles bounded visual work and preserves one original navigation', async () => {
  const f = fixture();try {
    const nav = navigateTitleArtwork(f.art, f.intent);f.reduce();await f.start();await nav;
    assert.equal(f.calls, 1);assert.equal(f.art.style.viewTransitionName, '');assert.equal(f.listener, undefined);assert.ok(f.skips > 0);
  } finally {f.cleanup();}
});

test('decoded wait has a finite visual cap and removes its original listeners', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});const f = fixture();try {
    const nav = navigateTitleArtwork(f.art, f.intent);await f.start();await nav;
    t.mock.timers.tick(TITLE_ART_WAIT_MS);await tick();assert.equal(f.listener, undefined);assert.ok(f.skips > 0);
    assert.equal(admittedTitleArtwork(f.document, 'original-scope', 'movie', f.location), undefined);
  } finally {f.cleanup();t.mock.timers.reset();}
});

test('reverse eligibility requires the exact original physical source, attached visible and current', async () => {
  const f = fixture();try {
    const nav = navigateTitleArtwork(f.art, f.intent);await f.start();await nav;
    const claim = admittedTitleArtwork(f.document, 'original-scope', 'movie', f.location)!;install(f, claim);await f.capture();await f.finish();
    assert.equal(originalArtworkReverseAvailable(claim), true);
    f.art.isConnected = false;assert.equal(originalArtworkReverseAvailable(claim), false);f.art.isConnected = true;
    f.sourceLive = false;assert.equal(originalArtworkReverseAvailable(claim), false);f.sourceLive = true;
    f.art.visible = false;assert.equal(originalArtworkReverseAvailable(claim), false);f.art.visible = true;
    f.art.getBoundingClientRect = () => ({left: 2000, top: 10, width: 190, height: 285, right: 2190, bottom: 295});
    assert.equal(originalArtworkReverseAvailable(claim), false);
  } finally {f.cleanup();}
});

test('reverse transfers its unique name synchronously inside the actual installed router update callback; one ordinary Back, no nested VT', async () => {
  const f = fixture();
  const saved = {window: globalThis.window, self: globalThis.self, scrollTo: globalThis.scrollTo};
  // Use the installed router's development entry (isServer remains undefined),
  // so its actual client load/commit path runs against this document fixture.
  const loader = registerHooks({resolve(specifier, context, next) {
    if (specifier === '@tanstack/router-core/isServer') return {url: new URL('../../node_modules/@tanstack/router-core/dist/esm/isServer/development.js', import.meta.url).href, shortCircuit: true};
    return next(specifier, context);
  }});
  let offHistory: (() => void) | undefined;
  try {
    const browser: any = {origin: 'https://private.invalid', location: {origin: 'https://private.invalid'}, addEventListener() {}, removeEventListener() {}};
    Object.assign(globalThis, {window: browser, self: browser, scrollTo() {}});
    const {createRouter, createRootRoute, createRoute} = await import('@tanstack/react-router');
    const {createMemoryHistory} = await import('@tanstack/history');
    const root = createRootRoute();root.addChildren(['/library/films', '/item/movie'].map(path => createRoute({getParentRoute: () => root, path})));
    const history = createMemoryHistory({initialEntries: ['/library/films']});
    let transitions = 0, inside = false;
    const captured: {inside: boolean; source: string; hero: string}[] = [];
    f.document.startViewTransition = (update: () => Promise<void>) => {
      transitions++;
      const done = Promise.resolve().then(async () => {inside = true;try {await update();captured.push({inside, source: f.art.style.viewTransitionName, hero: f.target.style.viewTransitionName});} finally {inside = false;}});
      return {updateCallbackDone: done, ready: done, finished: done, skipTransition() {}};
    };
    let claim: ReturnType<typeof admittedTitleArtwork>;
    const router = createRouter({routeTree: root, history, isServer: false, defaultViewTransition: true});
    // The renderer acknowledgement is the real framework port; commit/load/
    // event ordering remains the installed router's production implementation.
    router.startTransition = async commit => {commit();if (claim && router.state.location.pathname === '/library/films') releaseTitleArtwork(claim);return true;};
    offHistory = history.subscribe(router.load);
    await router.load();
    const read = (): ArtworkLocation => ({key: router.state.location.state.__TSR_key ?? '', href: router.state.location.href, pathname: router.state.location.pathname});
    const source = read();
    const offReady = router.subscribe('onRendered', event => {
      if (event.toLocation.pathname !== '/item/movie') return;
      claim = admittedTitleArtwork(f.document, 'original-scope', 'movie', read());assert.ok(claim);install(f, claim!);
    });
    const navigate = navigateTitleArtwork(f.art, {...f.intent, source, location: read,
      subscribe: listener => router.subscribe('onBeforeNavigate', event => listener({
        from: event.fromLocation ? {key: event.fromLocation.state.__TSR_key ?? '', href: event.fromLocation.href, pathname: event.fromLocation.pathname} : undefined,
        to: {key: event.toLocation.state.__TSR_key ?? '', href: event.toLocation.href, pathname: event.toLocation.pathname}})),
      navigate: manual => router.navigate({to: '/item/movie', ...(manual ? {viewTransition: false} : {})}),
    });
    await navigate;await tick();offReady();assert.ok(claim);
    const before = transitions;let backs = 0, offRendered: (() => void) | undefined;
    const committed = new Promise<void>(resolve => {
      backTitleArtwork(f.document, 'original-scope', () => {backs++;assert.equal(f.target.style.viewTransitionName, claim!.name);history.back();}, listener => {
        offRendered = router.subscribe('onRendered', event => {listener({key: event.toLocation.state.__TSR_key ?? '', href: event.toLocation.href, pathname: event.toLocation.pathname});assert.equal(inside, true);resolve();});
        return offRendered;
      });
    });
    await committed;await tick();
    assert.equal(backs, 1);assert.equal(transitions - before, 1, 'only the router creates the reverse transition');
    assert.deepEqual(read(), source);assert.equal(captured.at(-1)!.source, claim!.name);assert.equal(captured.at(-1)!.hero, '');
    offRendered?.();
  } finally {offHistory?.();loader.deregister();f.cleanup();Object.assign(globalThis, saved);}
});


for (const kind of ['detached', 'replaced', 'offscreen', 'wrong-entry', 'scope-change', 'late-commit', 'reduce', 'ua'] as const) test(`reverse ${kind} preserves one ordinary Back and refuses the original name transfer`, async t => {
  const f = fixture();try {
    const nav = navigateTitleArtwork(f.art, f.intent);await f.start();await nav;
    const claim = admittedTitleArtwork(f.document, 'original-scope', 'movie', f.location)!;install(f, claim);await f.capture();await f.finish();
    if (kind === 'reduce') f.root.dataset.reduceMotion = 'true';if (kind === 'ua') f.ua = true;
    let backs = 0, commit: ((to: ArtworkLocation) => void) | undefined;
    if (kind === 'late-commit') t.mock.timers.enable({apis: ['setTimeout']});
    backTitleArtwork(f.document, 'original-scope', () => {backs++;}, listener => {commit = listener;return () => {};});
    assert.equal(backs, 1);assert.equal(f.transitions, 1, 'reverse creates no manual transition');
    if (kind === 'detached') f.art.isConnected = false;
    if (kind === 'replaced') f.art.querySelector = () => ({isConnected: true, getAttribute: () => 'blob:replacement'});
    if (kind === 'offscreen') f.art.getBoundingClientRect = () => ({left: 2000, top: 10, width: 190, height: 285, right: 2190, bottom: 295});
    if (kind === 'scope-change') f.scope = false;
    if (kind === 'late-commit') t.mock.timers.tick(TITLE_ART_WAIT_MS);
    f.location = kind === 'wrong-entry' ? {...f.source, key: 'same-href-new-entry'} : f.source;
    commit?.(f.location);
    assert.equal(f.art.style.viewTransitionName, '');assert.equal(f.target.style.viewTransitionName, '');
  } finally {f.cleanup();if (kind === 'late-commit') t.mock.timers.reset();}
});

test('selection and Shift-range activation never mint or expose a physical navigation receipt', () => {
  const f = fixture();try {
    let toggles = 0, navigations = 0;
    const navigate = () => withSourceArtActivation(f.art, () => {navigations++;assert.ok(consumeSourceArtActivation());}, () => true);
    const selection = {active: true, checked: false, onToggle() {toggles++;}, onExit() {}};
    activateCard(navigate, selection, {range: false, source: 'pointer'});
    activateCard(navigate, {...selection, active: false}, {range: true, source: 'keyboard'});
    assert.equal(toggles, 2);assert.equal(navigations, 0);assert.equal(consumeSourceArtActivation(), undefined);
    activateCard(navigate, {...selection, active: false}, {range: false, source: 'pointer'});assert.equal(navigations, 1);
  } finally {f.cleanup();}
});


test('old reverse commit/cleanup cannot clear a successor unique claim on a reused hero', async () => {
  const f = fixture();try {
    const first = navigateTitleArtwork(f.art, f.intent);await f.start();await first;
    const old = admittedTitleArtwork(f.document, 'original-scope', 'movie', f.location)!;install(f, old);await f.capture();await f.finish();
    let oldCommit: ((to: ArtworkLocation) => void) | undefined;
    backTitleArtwork(f.document, 'original-scope', () => {}, listener => {oldCommit = listener;return () => {};});
    const source = {...f.location}, art = f.makeNode(), destination = {key: 'new-destination', href: '/item/movie2', pathname: '/item/movie2'};
    const second = navigateTitleArtwork(art, {...f.intent, source, itemId: 'movie2', entry: Object.freeze({id: 'movie2'}),
      async navigate() {f.listener?.({from: source, to: destination});f.location = destination;}});
    await f.start();await second;
    const next = admittedTitleArtwork(f.document, 'original-scope', 'movie2', f.location)!;assert.ok(next);assert.notEqual(next.name, old.name);
    install(f, next);oldCommit?.(f.source);releaseTitleArtwork(old);
    assert.equal(f.target.style.viewTransitionName, next.name);await f.capture();await f.finish();
  } finally {f.cleanup();}
});
