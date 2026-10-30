import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';
import type {LibrarySelection} from '../src/screens/browse/selection.tsx';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {createLibrarySelection} = await import('../src/screens/browse/selection.tsx');
const {PhoneSelectionProvider} = await import('../src/kit/selection/index.ts');

/** The shell wraps every screen it draws in the phone selection store, and the
 * bar publishes its live count and actions to it. A bar rendered outside that
 * shell is an arrangement the app never produces, so draw the real provider. */
const rendered = (adapter: Pick<LibrarySelection, 'toolbar'>) => renderToStaticMarkup(createElement(PhoneSelectionProvider, null, createElement('div', null, adapter.toolbar)));

const capabilities = {play: true, queue: true, playlist: true, myList: true, watched: true, collection: true, favorite: true, download: false, edit: true, refresh: true, delete: true};
const entry = {id: 'm1', kind: 'movie', title: 'Alpha', libraryId: 'a'};
const row = {entry, position: 0, key: 'key-0', capabilities};
const target = {mode: 'explicit' as const, context: {}, items: [{key: 'key-0', item: {kind: 'movie', id: 'm1', libraryId: 'a', capabilities}}], displayItems: [{key: 'key-0', item: {kind: 'movie', id: 'm1', libraryId: 'a', capabilities}}]};
const fake = (over: Record<string, unknown> = {}, host: Record<string, unknown> = {}) => {
  const calls: string[] = [];
  const snapshot = {
    phase: 'ready',
    rows: [row],
    resolving: false,
    playing: false,
    canPlay: true,
    selection: {active: true, context: {}, summary: {count: 1, types: [{kind: 'movie', count: 1}], library: {kind: 'single', libraryId: 'a'}, every: capabilities},
      selectedKeys: ['key-0'], version: 1, registeredWindowCount: 1, registeredItemCount: 1, lastSectionId: 'library', target},
    ...over,
  };
  const selection = {
    getSnapshot: () => snapshot,
    captureOperation: () => snapshot.selection.target ? {target: snapshot.selection.target} : null,
    isOperationCurrent: () => true,
    subscribe: () => () => {},
    toggle: (expected: unknown, range: boolean) => { calls.push(`toggle:${(expected as typeof row).key}:${range}`); return {ok: true}; },
    selectLoaded: () => { calls.push('selectLoaded'); return {ok: true}; },
    clear: () => { calls.push('clear'); },
    play: async () => { calls.push('play'); return {} as never; },
    registerWindow: async () => { calls.push('registerWindow'); return [row]; },
    captureOperation: () => ({target: snapshot.selection.target}),
    isOperationCurrent: () => true,
    start: () => () => {},
    dispose: () => {},
    ...host,
  };
  return {selection: selection as never, calls};
};

test('no bar and no checked circle before selection is active', () => {
  const {selection} = fake({selection: {active: false, summary: {count: 0, types: [], library: {kind: 'none'}, every: {}}, selectedKeys: [], version: 1, registeredWindowCount: 1, registeredItemCount: 1, lastSectionId: null, target: null}});
  const adapter = createLibrarySelection({playSelection: selection, reportError: () => {}});
  assert.equal(rendered(adapter), '<div></div>');
  const card = adapter.entry({membershipId: 'm1', entry} as never, 0);
  assert.equal(card?.active, false);
  assert.equal(card?.checked, false);
});

test('the bar counts the real selection and offers only what the adapter can perform', () => {
  const {selection, calls} = fake();
  const adapter = createLibrarySelection({playSelection: selection, reportError: () => {}});
  const html = rendered(adapter);
  assert.match(html, /1 selected/);
  assert.match(html, />Play</);
  // Whole-view Select all N is not offered until the descriptor cohort is wired here.
  assert.doesNotMatch(html, /Select all/);
  assert.doesNotMatch(html, />Queue</);
  assert.doesNotMatch(html, />Edit</);
  adapter.entry({membershipId: 'm1', entry} as never, 0)?.onToggle({range: true, source: 'pointer'});
  assert.deepEqual(calls, ['toggle:key-0:true']);
});

test('the window registers through the play adapter so ranges are real', () => {
  const {selection, calls} = fake();
  const adapter = createLibrarySelection({playSelection: selection, reportError: () => {}});
  const html = rendered(adapter);
  assert.match(html, />Done</);
  adapter.registerWindow(0, 40);
  assert.deepEqual(calls, ['registerWindow']);
});

test('a selection with nothing playable hides Play rather than pretending', () => {
  const {selection} = fake({selection: {active: true, summary: {count: 1, types: [{kind: 'movie', count: 1}], library: {kind: 'single', libraryId: 'a'},
    every: {...capabilities, play: false}}, selectedKeys: ['key-0'], version: 1, registeredWindowCount: 1, registeredItemCount: 1, lastSectionId: 'library', target: null}});
  const adapter = createLibrarySelection({playSelection: selection, reportError: () => {}});
  const html = rendered(adapter);
  assert.doesNotMatch(html, />Play</);
  assert.match(html, /1 selected/);
});


test('whole-view Select all appears only with the server descriptor and a model that can hold it', () => {
  const whole = {libraryId: 'a', pivot: 'movies', filter: null, sort: [{field: 'title', direction: 'asc'}], catalogRevision: 'r1', total: 48000,
    types: [{kind: 'movie', count: 48000, capabilities: capabilities, libraries: [{libraryId: 'a', count: 48000}]}]};
  const withWhole = fake({}, {selectWhole: () => {}});
  const adapter = createLibrarySelection({playSelection: withWhole.selection, descriptor: () => whole as never, reportError: () => {}});
  const html = rendered(adapter);
  assert.match(html, /Select all 48,000/);
  // Without a descriptor, or without the model capability, nothing is offered.
  const noDescriptor = createLibrarySelection({playSelection: withWhole.selection, reportError: () => {}});
  assert.doesNotMatch(rendered(noDescriptor), /Select all/);
  const noModel = createLibrarySelection({playSelection: fake().selection, descriptor: () => whole as never, reportError: () => {}});
  assert.doesNotMatch(rendered(noModel), /Select all/);
});

test('range eligibility paints only the actual hovered interval', () => {
  let hovered = false;
  const previewing = fake({}, {preview: () => true, rangePreviewed: () => hovered,
    setPreview: (actual: unknown, enabled: boolean) => {assert.equal(actual, row); hovered = enabled;}});
  const adapter = createLibrarySelection({playSelection: previewing.selection, reportError: () => {}});
  const card = adapter.entry({membershipId: 'm1', entry} as never, 0);
  assert.equal(card?.preview, undefined); // An eligible row alone is not a preview.
  card?.onPreview?.(true);
  assert.equal(adapter.entry({membershipId: 'm1', entry} as never, 0)?.preview, true);
  card?.onPreview?.(false);
  assert.equal(adapter.entry({membershipId: 'm1', entry} as never, 0)?.preview, undefined);
  const quiet = fake({}, {preview: () => false, rangePreviewed: () => false});
  const noPreview = createLibrarySelection({playSelection: quiet.selection, reportError: () => {}});
  assert.equal(noPreview.entry({membershipId: 'm1', entry} as never, 0)?.preview, undefined);
});


test('Watched and My List appear only with real job ports, and the job carries the real target', async () => {
  const requests: {path: string; body: unknown}[] = [];
  const actions = {
    api: {request: async (path: string, _method?: string, body?: unknown) => {
      requests.push({path, body});
      return {jobId: 'job-1', command: 'personal-state', state: 'complete', total: 1, totalKnown: true, done: 1, failed: 0};
    }},
    events: {on: () => () => {}}, requestId: async () => 'operation-1', isCurrent: () => true, reportError: () => {},
  };
  const {selection} = fake();
  const adapter = createLibrarySelection({playSelection: selection, actions: actions as never, reportError: () => {}});
  const html = rendered(adapter);
  assert.match(html, />Watched</);
  assert.match(html, />My List</);
  await adapter.personal({watched: true});
  assert.deepEqual(requests[0]?.body?.selector ?? {}, {items: {ids: ['m1']}});
  assert.equal((requests[0]?.body as {command: string}).command, 'personal-state');
  assert.deepEqual((requests[0]?.body as {args: unknown}).args, {watched: true});
  // Without the ports the bar offers no personal action at all.
  const bare = createLibrarySelection({playSelection: fake().selection, reportError: () => {}});
  assert.doesNotMatch(rendered(bare), />Watched</);
});

test('a container without the canonical selection grant never becomes a personal job', async () => {
  let requested = 0;
  const actions = {api: {request: async () => { requested++; return {}; }}, events: {on: () => () => {}}, requestId: async () => 'op', isCurrent: () => true, reportError: () => {}};
  const unavailable = {...capabilities, watched: false};
  const containerTarget = {...target, items: [{key: 'key-0', item: {kind: 'show', id: 's1', libraryId: 'a', capabilities: unavailable}}]};
  const {selection} = fake({selection: {active: true, summary: {count: 1, types: [{kind: 'show', count: 1}], library: {kind: 'single', libraryId: 'a'}, every: unavailable},
    selectedKeys: ['key-0'], version: 1, registeredWindowCount: 1, registeredItemCount: 1, lastSectionId: 'library', target: containerTarget}});
  const adapter = createLibrarySelection({playSelection: selection, actions: actions as never, reportError: () => {}});
  const html = rendered(adapter);
  assert.doesNotMatch(html, />Watched</);
  await assert.rejects(adapter.personal({watched: true}), /selection-grant-unavailable/);
  assert.equal(requested, 0);
});


test('Play is offered for explicit picks only until a whole view can be queued', () => {
  const whole = {...target, mode: 'query' as const, items: undefined, selector: {query: {libraryId: 'a', pivot: 'movies'}}, total: 48000, sourceTotal: 48000, refinement: {kind: null, excludedItems: []}, expected: {catalogRevision: 'r1'}};
  const {selection} = fake({selection: {active: true, summary: {count: 48000, types: [{kind: 'movie', count: 48000}], library: {kind: 'single', libraryId: 'a'}, every: capabilities},
    selectedKeys: [], version: 2, registeredWindowCount: 1, registeredItemCount: 1, lastSectionId: 'library', target: whole}});
  const adapter = createLibrarySelection({playSelection: selection, reportError: () => {}});
  const html = rendered(adapter);
  assert.match(html, /48,000 selected/);
  assert.doesNotMatch(html, />Play</);
});


test('Queue and the destination pickers take the explicit leaf ids in order', async () => {
  const calls: unknown[] = [];
  let queued: {items: readonly string[]; placement: string; signal: AbortSignal; isCurrent: () => boolean} | undefined;
  const enqueue = async (input: typeof queued) => { queued = input; };
  const destinations = {playlist: async (items: readonly string[], anchor: HTMLElement) => { calls.push(['playlist', items, anchor]); },
    collection: async (items: readonly string[], anchor: HTMLElement) => { calls.push(['collection', items, anchor]); }};
  const {selection} = fake();
  const actions = {signal: new AbortController().signal, isCurrent: () => true};
  const adapter = createLibrarySelection({playSelection: selection, enqueue, destinations, actions: actions as never, reportError: () => {}});
  const html = rendered(adapter);
  assert.match(html, />Add to queue</);
  assert.match(html, />Add to playlist</);
  const anchor = {} as HTMLElement;
  await adapter.queue('end');
  await adapter.addToPlaylist(anchor);
  await adapter.addToCollection(anchor);
  assert.deepEqual(queued?.items, ['m1']);
  assert.equal(queued?.placement, 'end');
  assert.ok(queued?.signal instanceof AbortSignal);
  assert.equal(typeof queued?.isCurrent, 'function');
  assert.deepEqual(calls, [['playlist', ['m1'], anchor], ['collection', ['m1'], anchor]]);
});

test('ungranted containers refuse Queue and legacy destinations stay leaf-only', async () => {
  const enqueue = async () => { throw new Error('must not run'); };
  const destinations = {playlist: async () => { throw new Error('must not run'); }};
  const ungranted = {...capabilities, queue: false};
  const containerTarget = {...target, items: [{key: 'key-0', item: {kind: 'show', id: 's1', libraryId: 'a', capabilities: ungranted}}],
    displayItems: [{key: 'key-0', item: {kind: 'show', id: 's1', libraryId: 'a', capabilities: ungranted}}]};
  const container = fake({selection: {active: true, summary: {count: 1, types: [{kind: 'show', count: 1}], library: {kind: 'single', libraryId: 'a'}, every: ungranted},
    selectedKeys: ['key-0'], version: 1, registeredWindowCount: 1, registeredItemCount: 1, lastSectionId: 'library', target: containerTarget}});
  const adapter = createLibrarySelection({playSelection: container.selection, enqueue, destinations, actions: {signal: new AbortController().signal, isCurrent: () => true} as never, reportError: () => {}});
  const html = rendered(adapter);
  assert.doesNotMatch(html, />Add to queue</);
  assert.doesNotMatch(html, />Add to playlist</);
  await assert.rejects(adapter.queue('next'), /leaf-explicit-selection-required/);
  await assert.rejects(adapter.addToPlaylist({} as HTMLElement), /leaf-explicit-selection-required/);
  const grantedTarget = {...containerTarget, items: containerTarget.items.map(pick => ({...pick, item: {...pick.item, capabilities}})),
    displayItems: containerTarget.displayItems.map(pick => ({...pick, item: {...pick.item, capabilities}}))};
  const granted = fake({selection: {active: true, summary: {count: 1, types: [{kind: 'show', count: 1}], library: {kind: 'single', libraryId: 'a'}, every: capabilities},
    selectedKeys: ['key-0'], version: 1, registeredWindowCount: 1, registeredItemCount: 1, lastSectionId: 'library', target: grantedTarget}});
  let queued: readonly string[] | undefined;
  const allowed = createLibrarySelection({playSelection: granted.selection, enqueue: async input => {queued = input.items; assert.equal(input.isCurrent(), true);},
    actions: {signal: new AbortController().signal, isCurrent: () => true} as never, reportError: () => {}});
  assert.match(rendered(allowed), />Add to queue</);
  await allowed.queue('next');
  assert.deepEqual(queued, ['s1']); // The actual granted parent, never its first episode.
});


test('a whole-view member paints checked from the model, not the explicit keys', () => {
  const whole = {...target, mode: 'query' as const, items: undefined, selector: {query: {libraryId: 'a', pivot: 'movies'}}, total: 48000, sourceTotal: 48000, refinement: {kind: null, excludedItems: []}, expected: {catalogRevision: 'r1'}};
  const {selection} = fake({selection: {active: true, summary: {count: 48000, types: [{kind: 'movie', count: 48000}], library: {kind: 'single', libraryId: 'a'}, every: capabilities},
    selectedKeys: [], version: 2, registeredWindowCount: 1, registeredItemCount: 1, lastSectionId: 'library', target: whole}},
    {selected: (row: {key: string}) => row.key === 'key-0'});
  const adapter = createLibrarySelection({playSelection: selection, reportError: () => {}});
  assert.equal(adapter.entry({membershipId: 'm1', entry} as never, 0)?.checked, true);
});


test('personal actions require a model operation lease', async () => {
  const actions = {api: {request: async () => { throw new Error('must not dispatch'); }}, events: {on: () => () => {}},
    requestId: async () => 'op', isCurrent: () => true, reportError: () => {}};
  for (const host of [{captureOperation: undefined}, {isOperationCurrent: undefined}]) {
    const adapter = createLibrarySelection({playSelection: fake({}, host).selection, actions: actions as never, reportError: () => {}});
    assert.doesNotMatch(rendered(adapter), />Watched</);
    await assert.rejects(adapter.personal({watched: true}), /selection-actions-unavailable/);
  }
});

test('clearing a captured selection while preparing a personal job prevents submission', async () => {
  let current = true, requested = 0;
  const operation = {target};
  const host = {captureOperation: () => operation, isOperationCurrent: (captured: unknown) => captured === operation && current};
  const actions = {api: {request: async () => { requested++; throw new Error('must not dispatch'); }}, events: {on: () => () => {}},
    requestId: async () => { current = false; return 'op'; }, isCurrent: () => true, reportError: () => {}};
  const adapter = createLibrarySelection({playSelection: fake({}, host).selection, actions: actions as never, reportError: () => {}});
  await assert.rejects(adapter.personal({watched: true}), /cancelled/);
  assert.equal(requested, 0);
});


test('queue requires both a model operation and the route lifetime', async () => {
  let dispatched = 0;
  const enqueue = async () => {dispatched++;};
  for (const host of [{captureOperation: undefined}, {isOperationCurrent: undefined}]) {
    const adapter = createLibrarySelection({playSelection: fake({}, host).selection, enqueue,
      actions: {signal: new AbortController().signal, isCurrent: () => true} as never, reportError: () => {}});
    assert.doesNotMatch(rendered(adapter), />Add to queue</);
    await assert.rejects(adapter.queue('end'), /selection-queue-unavailable/);
  }
  for (const actions of [undefined, {isCurrent: () => true}]) {
    const adapter = createLibrarySelection({playSelection: fake().selection, enqueue, actions: actions as never, reportError: () => {}});
    assert.doesNotMatch(rendered(adapter), />Add to queue</);
    await assert.rejects(adapter.queue('end'), /selection-queue-unavailable/);
  }
  assert.equal(dispatched, 0);
});

test('queue rejects a captured target that differs from the displayed explicit target', async () => {
  let dispatched = 0;
  const adapter = createLibrarySelection({playSelection: fake({}, {captureOperation: () => ({target: {...target}})}).selection,
    enqueue: async () => {dispatched++;}, actions: {signal: new AbortController().signal, isCurrent: () => true} as never, reportError: () => {}});
  await assert.rejects(adapter.queue('next'), /selection-target-required/);
  assert.equal(dispatched, 0);
});

test('queue carries the captured model and route fences through asynchronous preparation', async () => {
  for (const retire of ['model', 'route', 'signal', 'target']) {
    let modelCurrent = true, routeCurrent = true, displayedTarget = target, checks = 0;
    const life = new AbortController(), operation = {target};
    const original = fake({}, {captureOperation: () => operation,
      isOperationCurrent: (captured: unknown) => captured === operation && modelCurrent}).selection;
    const host = {...original as object, getSnapshot: () => {const snapshot = (original as {getSnapshot: () => {selection: Record<string, unknown>}}).getSnapshot();return {...snapshot, selection: {...snapshot.selection, target: displayedTarget}};}};
    const adapter = createLibrarySelection({playSelection: host as never,
      actions: {signal: life.signal, isCurrent: () => routeCurrent} as never, reportError: () => {},
      enqueue: async input => {
        assert.equal(input.signal, life.signal);
        assert.equal(input.isCurrent(), true);
        await Promise.resolve();
        if (retire === 'model') modelCurrent = false;
        if (retire === 'route') routeCurrent = false;
        if (retire === 'signal') life.abort();
        if (retire === 'target') displayedTarget = {...target};
        assert.equal(input.isCurrent(), false, retire);
        checks++;
      }});
    await adapter.queue('end');
    assert.equal(checks, 1);
  }
});

test('queue never enters its executor after the captured operation or route retires', async () => {
  for (const retired of ['model', 'route', 'signal']) {
    let dispatched = 0;
    const life = new AbortController();
    if (retired === 'signal') life.abort();
    const adapter = createLibrarySelection({playSelection: fake({}, {isOperationCurrent: () => retired !== 'model'}).selection,
      enqueue: async () => {dispatched++;}, actions: {signal: life.signal, isCurrent: () => retired !== 'route'} as never, reportError: () => {}});
    assert.doesNotMatch(retired === 'model' ? '' : rendered(adapter), />Add to queue</);
    await assert.rejects(adapter.queue('next'), {name: 'AbortError'});
    assert.equal(dispatched, 0);
  }
});

const queryTarget = (over: Record<string, unknown> = {}) => ({mode: 'query' as const, context: {}, queryId: 'q', selector: {query: {libraryId: 'a', pivot: 'movies'}},
  total: 48000, sourceTotal: 48000, refinement: {kind: null, excludedItems: []}, expected: {catalogRevision: 'r1'},
  summary: {count: 48000, types: [{kind: 'movie', count: 48000}], library: {kind: 'single', libraryId: 'a'}, every: capabilities}, ...over});
const wholeSnapshot = (target: unknown, over: Record<string, unknown> = {}) => ({selection: {active: true, summary: {count: 48000, types: [{kind: 'movie', count: 48000}],
  library: {kind: 'single', libraryId: 'a'}, every: capabilities}, selectedKeys: [], version: 2, registeredWindowCount: 1, registeredItemCount: 1, lastSectionId: 'library', target}, ...over});

test('an unrefined whole view sends its exact selector and catalog revision as one personal job', async () => {
  const requests: {path: string; body: {selector?: unknown; expected?: unknown; operationId?: string; args?: unknown}}[] = [];
  const actions = {
    api: {request: async (path: string, _method?: string, body?: unknown) => {
      requests.push({path, body: body as typeof requests[number]['body']});
      return {jobId: 'job-q', command: 'personal-state', state: 'complete', total: 48000, totalKnown: true, done: 48000, failed: 0};
    }},
    events: {on: () => () => {}}, requestId: async () => 'operation-1', isCurrent: () => true, reportError: () => {},
  };
  const target = queryTarget();
  const {selection} = fake(wholeSnapshot(target));
  const adapter = createLibrarySelection({playSelection: selection, actions: actions as never, reportError: () => {}});
  const html = rendered(adapter);
  assert.match(html, />Watched</);
  await adapter.personal({watched: true});
  assert.equal(requests.length, 1);
  assert.deepEqual(requests[0]?.body.selector, target.selector);
  assert.deepEqual(requests[0]?.body.expected, {catalogRevision: 'r1'});
  assert.equal(requests[0]?.body.operationId, 'operation1');
  assert.deepEqual(requests[0]?.body.args, {watched: true});
});

test('a refined whole view withholds the action and submits nothing', async () => {
  let requests = 0;
  const actions = {api: {request: async () => { requests++; return {}; }}, events: {on: () => () => {}},
    requestId: async () => 'operation-1', isCurrent: () => true, reportError: () => {}};
  for (const over of [{refinement: {kind: 'movie', excludedItems: []}}, {total: 47999, sourceTotal: 48000}]) {
    const target = queryTarget(over);
    const {selection} = fake(wholeSnapshot(target));
    const adapter = createLibrarySelection({playSelection: selection, actions: actions as never, reportError: () => {}});
    assert.doesNotMatch(rendered(adapter), />Watched</);
    await assert.rejects(adapter.personal({watched: true}), /query-refinement-required/);
  }
  // A snapshot whose every-watched grant is false is refused by the producer too.
  const denied = fake({...wholeSnapshot(queryTarget()), selection: {...(wholeSnapshot(queryTarget()).selection as object), summary: {count: 48000, types: [{kind: 'movie', count: 48000}],
    library: {kind: 'single', libraryId: 'a'}, every: {...capabilities, watched: false}}}});
  const deniedAdapter = createLibrarySelection({playSelection: denied.selection, actions: actions as never, reportError: () => {}});
  assert.doesNotMatch(rendered(deniedAdapter), />Watched</);
  await assert.rejects(deniedAdapter.personal({watched: true}), /selection-grant-unavailable/);
  assert.equal(requests, 0);
});

test('a selection change while the operation id is pending submits no personal job', async () => {
  let current = true, requests = 0, release!: () => void;
  let requested!: () => void;
  const requestedPromise = new Promise<void>(resolve => {requested = resolve;});
  const actions = {api: {request: async () => { requests++; return {}; }}, events: {on: () => () => {}},
    requestId: async () => { requested(); await new Promise<void>(resolve => {release = resolve;}); return 'operation-1'; },
    isCurrent: () => true, reportError: () => {}};
  const target = queryTarget();
  const {selection} = fake(wholeSnapshot(target), {isOperationCurrent: () => current});
  const adapter = createLibrarySelection({playSelection: selection, actions: actions as never, reportError: () => {}});
  const pending = adapter.personal({watched: true});
  await requestedPromise;
  current = false;
  release();
  await assert.rejects(pending, /cancelled/);
  assert.equal(requests, 0);
});

test('a retired route reports cancellation before any personal request', async () => {
  let requests = 0;
  const actions = {api: {request: async () => { requests++; return {}; }}, events: {on: () => () => {}},
    requestId: async () => 'operation-1', isCurrent: () => false, reportError: () => {}};
  const {selection} = fake(wholeSnapshot(queryTarget()));
  const adapter = createLibrarySelection({playSelection: selection, actions: actions as never, reportError: () => {}});
  await assert.rejects(adapter.personal({watched: true}), /cancelled/);
  assert.equal(requests, 0);
});


test('actual web toolbar renders count-free disabled Select all with a visible accessible search reason and retains page selection', () => {
  const {selection, calls} = fake({}, {selectWhole: () => {throw new Error('unavailable has no executor');}});
  let refusal: {reason: 'search_not_bounded'} | undefined = {reason: 'search_not_bounded'};
  const adapter = createLibrarySelection({playSelection: selection, reportError() {},
    wholeSelectionUnavailable: {subscribe: () => () => {}, getSnapshot: () => refusal}});
  const html = rendered(adapter);
  assert.match(html, />Select all</); assert.doesNotMatch(html, /Select all [0-9]/);
  assert.match(html, /aria-describedby="([^"]+)"[^>]*disabled=""/);
  const id = html.match(/aria-describedby="([^"]+)"/)![1]!;
  assert.ok(html.includes(`id="${id}">Unavailable for this search.</small>`));
  assert.match(html, /1 selected/); assert.match(html, />Play</);
  adapter.entry({membershipId: 'm1', entry} as never, 0)?.onToggle({range: true, source: 'pointer'});
  assert.deepEqual(calls, ['toggle:key-0:true']);
  refusal = undefined;
  assert.doesNotMatch(rendered(adapter), /Unavailable for this search|Select all/);
});
