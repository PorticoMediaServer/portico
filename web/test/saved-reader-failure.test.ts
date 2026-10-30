/** Prepared offline: actual SavedScreen/SavedLists split-reader callback wiring through
 * the published web/test componentModule + createRenderer. Controlled SavedService
 * snapshots; models callback composition, not native React paint. UNRUN. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {componentModule} from './helpers/component-harness.mjs';
import {all, createRenderer, one, textOf, tick, tree} from './helpers/interaction-harness.mjs';
import * as retryAdmission from '../src/screens/saved/retry-admission.ts';

const route = {view: 'watchlist', sort: 'added', direction: 'desc'} as const;
const artwork = {foreground: () => undefined, ambient: () => undefined, backdrop: () => undefined};
const presentation = {view: 'grid' as const, cardWidth: 160, setView() {}, backdrops: false, backdrop: undefined, reducedMotion: true};
const section = (id: string, count: number) => ({id, heading: {label: id}, totalCount: count,
  entries: count ? [{kind: 'movie' as const, id: `${id}-entry`, title: id}] : [], nextCursor: null});
const projection = (sections: any[]) => ({sections, sorts: [], filters: [], query: {sort: 'added', direction: 'desc'}, actions: []});
function snapshot(shape: 'posters' | 'squares' | undefined, phase: 'idle' | 'loading' | 'ready' | 'error', sections: any[] = []) {
  return {phase, route: shape ? {...route, shape} : {...route}, projection: phase === 'ready' ? projection(sections) : null, error: phase === 'error' ? {code: 'failed'} : null};
}
function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {resolve = res; reject = rej;});
  return {promise, resolve, reject};
}
function service(name: 'posters' | 'squares' | 'saved', shape: 'posters' | 'squares' | undefined, initial: any, calls: Record<string, number>) {
  let current = initial, next = initial, behavior: (() => Promise<void>) | undefined;
  return {getSnapshot: () => current, subscribe: () => () => {},
    select: async () => {calls[name] = (calls[name] ?? 0) + 1; if (behavior) {await behavior(); return;} current = next;},
    revalidate: async () => {}, next: async () => {}, retry: async () => {}, retryRead: async () => {},
    set(value: any) {current = value;}, plan(value: any) {next = value;}, behavior(value: () => Promise<void>) {behavior = value;}};
}

function stubGlobals(t: any) {
  const saved = {ResizeObserver: (globalThis as any).ResizeObserver, window: (globalThis as any).window, devicePixelRatio: (globalThis as any).devicePixelRatio};
  class FakeResizeObserver {observe() {} disconnect() {} unobserve() {}}
  (globalThis as any).ResizeObserver = FakeResizeObserver;
  (globalThis as any).window = {innerHeight: 900};
  (globalThis as any).devicePixelRatio = 2;
  t.after(() => Object.assign(globalThis, saved));
}

async function mount(initial: {posters: any; squares: any}) {
  const h = createRenderer(), errors: unknown[] = [], calls: Record<string, number> = {};
  const posters = service('posters', 'posters', initial.posters, calls);
  const squares = service('squares', 'squares', initial.squares, calls);
  const saved = service('saved', undefined, snapshot(undefined, 'ready', [section('saved', 0)]), calls);
  const operations = {current: true, run: async (run: (signal: AbortSignal) => Promise<void>) => run(new AbortController().signal), dispose() {}};
  let runtime: any = {scope: {authority: 'local', serverId: 's', accountId: 'a', profileId: 'p', sessionFamilyId: 'f'},
    operations, posters, squares, saved, select: async () => {},
    summary: {subscribe: () => () => {}, getSnapshot: () => ({value: null, error: null}), refresh: async () => {}},
    personal: {subscribe: () => () => {}, getSnapshot: () => ({route: null, resources: []})},
    downloads: {}, installReadyDownloads() {}, dispose() {}};
  let currentRoute: any = route;
  let effects: any = {openEntry() {}, playEntry: async () => {}, selectRoute() {}, openItem() {}, saveFile: async () => {},
    reportError(error: unknown) {errors.push(error);}};
  const app = await componentModule(new URL('../src/screens/saved/Saved.tsx', import.meta.url), {
    react: h.react,
    '@i18n': {defaultI18n: {t: (key: string) => key, has: () => false, number: (value: number) => String(value),
      bytes: (value: number) => `${value} B`, region: {uiLocale: 'en-US', formatLocale: 'en-US'}}},
    '@core/presentation/server-text.ts': {serverTextLabel: (_i18n: any, heading: any) => heading?.label ?? 'section'},
    '@core/presentation/card-shape.ts': {shapeFor: () => 'poster'},
    '@core/freshness-hub.ts': {dependsOn: {personal: () => 'personal', progress: () => 'progress', content: () => 'content', settings: () => 'settings'}},
    '../../kit/controls': {Button: 'Button', Chip: 'Chip', Tabs: 'Tabs', Switch: 'Switch'},
    '../../kit/browse': {FiltersButton: 'FiltersButton', SortMenu: 'SortMenu', ViewToggle: 'ViewToggle'},
    '../../kit/overlays': {MenuRow: 'MenuRow', Popover: 'Popover'},
    '../../kit/cards': {PlaylistMosaic: 'PlaylistMosaic', PosterCard: 'PosterCard', SquareCard: 'SquareCard', NewPlaylistCard: 'NewPlaylistCard'},
    '../../kit/artwork': {AmbientArt: 'AmbientArt', CoverMosaic: 'CoverMosaic'},
    '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/layout': {WindowedGrid: 'WindowedGrid'},
    '../../kit/states': {Skeleton: 'Skeleton', StateView: 'StateView'},
    '../../platform/freshness': {useQuietRefresh: () => {}},
    '../../shell/viewport': {useShellWidth: () => 1440},
    '../home/model': {homeCardModel: (media: any) => ({title: media.title})},
    '../browse': {BrowseEntryCard: 'BrowseEntryCard'},
    '../browse/document-selection': {CatalogueSelectionEntry: 'CatalogueSelectionEntry', useCatalogueSelectionActive: () => false},
    './Downloads': {LocalDownloads: 'LocalDownloads', ServerDownloads: 'ServerDownloads'},
    './runtime.ts': {savedReaderRoute: (value: any) => value},
    './window.ts': {savedSectionWindow: () => ({collection: {}})},
    './text.ts': {savedText: (key: string) => key},
    './Resource': {SavedHistory: 'SavedHistory', SavedResourceScreen: 'SavedResourceScreen'},
    './ResourceFields': {ResourceFields: 'ResourceFields'},
    './SavedDialog': {SavedDialog: 'SavedDialog'},
    './SavedPhoneDirectory': {SavedPhoneDirectory: 'SavedPhoneDirectory'},
    './SavedPhoneNav': {SavedPhoneNav: 'SavedPhoneNav'},
    './route.ts': {sameSavedRoute: (left: any, right: any) => JSON.stringify(left) === JSON.stringify(right), savedViewKey: () => 'view'},
    './retry-admission.ts': retryAdmission,
    './use-view-operations.ts': {useSavedViewOperations: () => operations},
    './saved.module.css': {default: {}},
    './ReadingGroupCard': {ReadingGroupCard: 'ReadingGroupCard'},
  });
  const flat = () => tree(h.render(() => app.SavedScreen({runtime, route: currentRoute, online: true, downloadsOffered: false,
    artwork, preparationArtwork: artwork, presentation, effects})));
  flat();
  return {flat, posters, squares, saved, calls, errors,
    setRuntime(value: any) {runtime = value;}, runtime: () => runtime,
    setRoute(value: any) {currentRoute = value;},
    setReportError(value: (error: unknown) => void) {effects = {...effects, reportError: value};}};
}

const compact = (f: any) => all(f.flat(), 'StateView').filter((node: any) => node.props.variant === 'compact');
const retryOf = (node: any) => node.props.primary;

test('actual SavedLists keeps the healthy poster rows beside a square reader failure', async t => {
  stubGlobals(t);
  const f = await mount({posters: snapshot('posters', 'ready', [section('posters', 1)]), squares: snapshot('squares', 'error')});
  const failures = compact(f);
  assert.equal(failures.length, 1);
  assert.equal(retryOf(failures[0]!).id, 'retry-squares');
  assert.ok(all(f.flat(), 'WindowedGrid').some((node: any) => node.props.label === 'posters'), 'the healthy posters section stays mounted');
  assert.equal(textOf(f.flat()).includes('lib.count'), false, 'a partial read never claims a combined exact total');
});

test('actual SavedLists shows the empty state only when both readers are ready and empty', async t => {
  stubGlobals(t);
  const partial = await mount({posters: snapshot('posters', 'loading'), squares: snapshot('squares', 'ready', [])});
  assert.equal(compact(partial).length, 0);
  assert.equal(all(partial.flat(), 'StateView').some((node: any) => node.props.title === 'web.saved.empty.watchlist'), false);
  partial.posters.set(snapshot('posters', 'ready', []));
  partial.flat();
  assert.ok(all(partial.flat(), 'StateView').some((node: any) => node.props.title === 'web.saved.empty.watchlist'), 'both ready and empty reaches empty');

  const inverse = await mount({posters: snapshot('posters', 'error'), squares: snapshot('squares', 'ready', [section('squares', 1)])});
  assert.equal(compact(inverse).length, 1);
  assert.equal(retryOf(compact(inverse)[0]!).id, 'retry-posters');
  // The split reader's squares half is published under the screen's own "Music and
  // audiobooks" heading, not the raw section id; its presence is the mounted witness.
  assert.ok(all(inverse.flat(), 'WindowedGrid').some((node: any) => node.props.label === 'next.saved.musicAndAudiobooks'), 'the healthy squares section stays mounted');
  assert.equal(textOf(inverse.flat()).includes('lib.count'), false);
});

test('actual SavedLists shows one scoped recovery per failed reader and coalesces retry', async t => {
  stubGlobals(t);
  const f = await mount({posters: snapshot('posters', 'error'), squares: snapshot('squares', 'error')});
  const failures = compact(f);
  assert.equal(failures.length, 2);
  assert.deepEqual(failures.map((node: any) => retryOf(node).id).sort(), ['retry-posters', 'retry-squares']);
  f.posters.plan(snapshot('posters', 'ready', [section('posters', 1)]));
  f.squares.plan(snapshot('squares', 'ready', []));
  retryOf(failures[0]!).onPress();
  retryOf(failures[1]!).onPress();
  assert.deepEqual(f.calls, {posters: 1, squares: 1}, 'duplicate clicks issue one recovery per reader');
  await Promise.resolve(); f.flat();
  assert.equal(compact(f).length, 0, 'one explicit retry recovers both shapes');
  assert.ok(all(f.flat(), 'WindowedGrid').some((node: any) => node.props.label === 'posters'));
  assert.ok(textOf(f.flat()).includes('lib.count'), 'both ready restores the exact combined total');
});

test('actual SavedLists attempts both failed readers even when the first rejects', async t => {
  stubGlobals(t);
  const f = await mount({posters: snapshot('posters', 'error'), squares: snapshot('squares', 'error')});
  const poster = deferred<void>();
  f.posters.behavior(() => poster.promise);
  const retry = retryOf(compact(f)[0]!);
  retry.onPress();
  poster.reject(new Error('poster failed'));
  await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); f.flat();
  assert.equal(f.calls.posters, 1);
  assert.equal(f.calls.squares, 1, 'the sibling reader is attempted after the first rejection');
  assert.equal(f.errors.length, 1, 'the first rejection is reported once');
});

test('an old failure retry cannot adopt a same-route runtime replacement', async t => {
  stubGlobals(t);
  const f = await mount({posters: snapshot('posters', 'error'), squares: snapshot('squares', 'error')});
  const oldRetry = retryOf(compact(f)[0]!);
  const original = f.runtime();
  f.setRuntime({...original, operations: {...original.operations}}); f.flat();
  oldRetry.onPress();
  assert.deepEqual(f.calls, {}, 'the captured old admission does not adopt the replacement runtime');
  retryOf(compact(f)[0]!).onPress();
  assert.deepEqual(f.calls, {posters: 1, squares: 1}, 'the current published admission works');
});

test('a replay across runtime replacement leaves the old callback dead and the fresh one live', async t => {
  stubGlobals(t);
  const f = await mount({posters: snapshot('posters', 'error'), squares: snapshot('squares', 'error')});
  const oldRetry = retryOf(compact(f)[0]!);
  const original = f.runtime();
  f.setRuntime({...original, operations: {...original.operations}}); f.flat();
  f.setRuntime(original); f.flat();
  oldRetry.onPress();
  assert.deepEqual(f.calls, {}, 'the old Strict/replay callback never adopts the fresh admission');
  retryOf(compact(f)[0]!).onPress();
  assert.deepEqual(f.calls, {posters: 1, squares: 1}, 'the fresh layout admission is live');
});

test('an old retry after an equal-route ticket replacement is inert and the current one works', async t => {
  stubGlobals(t);
  const f = await mount({posters: snapshot('posters', 'error'), squares: snapshot('squares', 'error')});
  const oldRetry = retryOf(compact(f)[0]!);
  f.setReportError(() => {}); f.flat();
  oldRetry.onPress();
  assert.deepEqual(f.calls, {}, 'the old ticket is dead after the same-route replacement');
  retryOf(compact(f)[0]!).onPress();
  assert.deepEqual(f.calls, {posters: 1, squares: 1});
});

test('an equal-content route object never replaces the admitted retry lifetime', async t => {
  stubGlobals(t);
  const f = await mount({posters: snapshot('posters', 'error'), squares: snapshot('squares', 'error')});
  const retry = retryOf(compact(f)[0]!);
  // A re-render hands a new route object with identical content. The routeKey is
  // the ticket's content witness; object identity must not retire the admission.
  f.setRoute({...route}); f.flat();
  retry.onPress();
  assert.deepEqual(f.calls, {posters: 1, squares: 1}, 'the original route witness still authorizes the retry');
  assert.deepEqual(f.errors, []);
});

test('changed route content retires the original retry lifetime and admits its successor', async t => {
  stubGlobals(t);
  const f = await mount({posters: snapshot('posters', 'error'), squares: snapshot('squares', 'error')});
  const oldRetry = retryOf(compact(f)[0]!);
  const next = {view: 'favorites', sort: 'added', direction: 'desc'} as const;
  const nextPosters = {...snapshot('posters', 'error'), route: {...next, shape: 'posters'}}, nextSquares = {...snapshot('squares', 'error'), route: {...next, shape: 'squares'}};
  f.setRoute(next);
  f.posters.set(nextPosters); f.posters.plan(nextPosters);
  f.squares.set(nextSquares); f.squares.plan(nextSquares);
  f.flat();
  oldRetry.onPress();
  assert.deepEqual(f.calls, {}, 'the superseded route witness is dead');
  retryOf(compact(f)[0]!).onPress();
  assert.deepEqual(f.calls, {posters: 1, squares: 1}, 'the successor content admission works');
});

test('a late rejection after route retirement neither reports nor starts the sibling', async t => {
  stubGlobals(t);
  const f = await mount({posters: snapshot('posters', 'error'), squares: snapshot('squares', 'error')});
  const pending = deferred<void>();
  f.posters.behavior(() => pending.promise);
  const oldRetry = retryOf(compact(f)[0]!);
  oldRetry.onPress();
  const next = {view: 'favorites', sort: 'added', direction: 'desc'} as const;
  f.setRoute(next);
  f.posters.set({...snapshot('posters', 'error'), route: {...next, shape: 'posters'}});
  f.squares.set({...snapshot('squares', 'error'), route: {...next, shape: 'squares'}});
  f.flat();
  pending.reject(new Error('late'));
  await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); f.flat();
  assert.equal(f.calls.posters, 1, 'no extra post-retirement request');
  assert.deepEqual(f.errors, [], 'the dead admission never reports into the successor route');
  assert.equal(f.calls.squares, 1, 'the sibling started before retirement and is not restarted');
});

test('a synchronous first-select route replacement stops the sibling select', async t => {
  stubGlobals(t);
  const f = await mount({posters: snapshot('posters', 'error'), squares: snapshot('squares', 'error')});
  const next = {view: 'favorites', sort: 'added', direction: 'desc'} as const;
  f.posters.behavior(async () => {
    f.squares.set({...snapshot('squares', 'error'), route: {...next, shape: 'squares'}});
  });
  retryOf(compact(f)[0]!).onPress();
  assert.equal(f.calls.posters, 1);
  assert.equal(f.calls.squares, undefined, 'the sibling is not started against successor-owned readers');
  assert.deepEqual(f.errors, []);
});

test('admission factory setup/cleanup/setup (helper vector, not actual-component replay) leaves the first dead and the second live', () => {
  const fake = (shape: 'posters' | 'squares' | undefined) => ({getSnapshot: () => ({route: shape ? {...route, shape} : {...route}, phase: 'error'}), select: async () => {}});
  const ticket = {runtime: {operations: {current: true}}, route: {...route}, routeKey: JSON.stringify(route),
    posters: fake('posters'), squares: fake('squares'), saved: fake(undefined), reportError() {}} as any;
  const first = retryAdmission.createSavedRetryAdmission(ticket);
  retryAdmission.retireSavedRetryAdmission(first);
  const second = retryAdmission.createSavedRetryAdmission(ticket);
  assert.equal(first.live, false, 'the first setup is dead');
  assert.equal(second.live, true, 'the Strict second setup is live');
  assert.equal(retryAdmission.savedRetryCurrent(second), true);
});


test('both independent reader retries start before the first settles', async t => {
  stubGlobals(t);
  const f = await mount({posters: snapshot('posters', 'error'), squares: snapshot('squares', 'error')});
  const pending = deferred<void>();
  f.posters.behavior(() => pending.promise);
  retryOf(compact(f)[0]!).onPress();
  assert.equal(f.calls.posters, 1);
  assert.equal(f.calls.squares, 1, 'the second reader starts while the first is still pending');
  pending.resolve();
  await Promise.resolve(); await Promise.resolve(); f.flat();
  assert.deepEqual(f.calls, {posters: 1, squares: 1});
});

test('the original pending token blocks a retry reentered by the unlock predicate', async t => {
  stubGlobals(t);
  const f = await mount({posters: snapshot('posters', 'error'), squares: snapshot('squares', 'error')});
  const retry = retryOf(compact(f)[0]!);
  const originalGet = f.posters.getSnapshot;
  let reenter = false, fired = false;
  f.posters.getSnapshot = () => {
    if (reenter && !fired) {fired = true; retry.onPress();}
    return originalGet();
  };
  f.posters.behavior(async () => {await Promise.resolve(); reenter = true;});
  retry.onPress();
  // Drain the original async work without rendering; the first getSnapshot after
  // this point must be the unlock predicate, not a render.
  await tick();
  assert.equal(fired, true, 'the unlock currentness predicate actually reentered a retry');
  assert.deepEqual(f.calls, {posters: 1, squares: 1}, 'the original pending token prevented duplicate starts');
  assert.deepEqual(f.errors, []);
  f.flat();
  assert.equal(retryOf(compact(f)[0]!).loading, false, 'the original unlock settled its own loading state');
});

test('savedRetryCurrent rechecks liveness after a replacing getter (helper vector)', () => {
  const fake = (shape: 'posters' | 'squares' | undefined) => ({getSnapshot: () => ({route: shape ? {...route, shape} : {...route}, phase: 'error'}), select: async () => {}});
  let admission: any;
  const runtime = {operations: {get current() {retryAdmission.retireSavedRetryAdmission(admission); return true;}}};
  const ticket = {runtime, route: {...route}, routeKey: JSON.stringify(route), posters: fake('posters'), squares: fake('squares'), saved: fake(undefined), reportError() {}} as any;
  admission = retryAdmission.createSavedRetryAdmission(ticket);
  assert.equal(retryAdmission.savedRetryCurrent(admission), false, 'the getter retired the original and the recheck caught it');
  assert.equal(admission.live, false);
});
