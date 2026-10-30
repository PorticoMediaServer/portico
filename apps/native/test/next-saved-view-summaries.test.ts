import assert from 'node:assert/strict';
import test from 'node:test';
import {defaultI18n} from '../../../packages/i18n/src';
import {parseBrowseCapabilities} from '../../../packages/client-core/src/browse.ts';
import type {LibraryContentApi} from '../../../packages/client-core/src/library-content.ts';
import type {PersonalResource} from '../../../packages/client-core/src/personal-saved.ts';
import type {SavedApi} from '../../../packages/client-core/src/saved.ts';
import {savedViewSummary, type SavedViewSummaryText} from '../../../packages/client-core/src/presentation/saved-view-summary.ts';
import {NativeHomeSavedViewSummaries} from '../src-next/screens/home/saved-view-summaries.ts';
import {NativeSavedDestination} from '../src-next/screens/saved/model.ts';
import {createNativeSavedViewSummaryBinding} from '../src-next/screens/saved/view-summaries.ts';
import {NativeSavedViewDestination} from '../src-next/screens/saved/view-model.ts';
import type {NativeShellBootstrapContext} from '../src-next/shell/bootstrap.ts';

const scope = {serverId: 'server', viewerId: 'local/account/profile'};
const authority = Object.freeze({serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'});
const viewer = Object.freeze({serverId: 'server', accountId: 'account', profileId: 'profile', authority: 'local', role: 'owner'});
const session = {accessToken: 'access', expiresAt: '2099-01-01T00:00:00Z', viewer, sessionFamilyId: 'family', tokenGeneration: '1', authorizationHorizon: '2099-01-01T00:00:00Z'};
const queries = {maximumDepth: 6, maximumClauses: 50, maximumBytes: 8192, maximumSorts: 1, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 600000};

const pivot = (id: string) => ({id, labelKey: 'library.tab.movies', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc'}],
  supportedViews: ['grid'], sorts: [], filters: [], browsable: true});
const capability = (libraryId: string, pivotId: string) => ({
  library: {id: libraryId, name: libraryId, kind: 'movie', defaultView: 'browse', pinned: false},
  pivots: [pivot(pivotId)], resolvedPivot: pivot(pivotId), fields: [],
  sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc'], defaultDirection: 'asc', expensive: false, applicableKinds: ['movie']}],
  quickFilters: [], queryLimits: queries,
});
type ViewOptions = Readonly<{libraryId?: string; pivot?: string; sortField?: string; status?: 'ready' | 'needs-review'}>;
const viewWire = (id: string, options: ViewOptions = {}) => ({
  id, serverId: scope.serverId, viewerFence: 'directory-fence', kind: 'view', name: id, summary: `STORED WORDS ${id}`,
  visibility: 'private', revision: 1, role: 'owner', entryCount: 1, actions: ['pin'], status: options.status ?? 'ready',
  invalidComponents: [], pinned: false, pinRevision: 1, shares: [],
  ...(options.status === 'needs-review' ? {} : {definition: {libraryId: options.libraryId ?? 'movies', pivot: options.pivot ?? 'movies',
    query: null, sort: [{field: options.sortField ?? 'title', direction: 'asc'}], presentation: 'grid'}}),
});
const directory = (resources: readonly unknown[], nextCursor = '') => ({serverId: scope.serverId, viewerFence: 'directory-fence', revision: 1, resources, nextCursor});
const summary = (savedViews = 6) => ({serverId: scope.serverId, viewerFence: 'summary-fence', revision: {catalog: 1, viewer: 1},
  myList: {posters: 0, squares: 0}, favorites: {posters: 0, squares: 0}, playlists: 0, collections: 0, savedViews});
const pins = {serverId: scope.serverId, viewerFence: 'pins-fence', revision: 1, order: []};
const hubList = (view: string) => ({
  scope: {serverId: scope.serverId, libraryId: '', libraryKind: 'mixed', view, entityId: '', viewerFence: 'saved-fence'},
  revision: {catalog: 1, viewer: 1}, heading: {key: 'saved.title', fallback: ''}, navigation: [],
  query: {sort: 'title', direction: 'asc', category: 'all', q: '', limit: 40, searchMode: 'none'}, sorts: [], filters: [],
  sections: [{id: 'items', type: 'grid', heading: {key: 'saved.title', fallback: ''}, entries: [], totalCount: 0, nextCursor: ''}],
});
type Call = Readonly<{path: string; method?: string; body?: unknown; signal?: AbortSignal}>;
const deferred = <T>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {resolve, promise};};
const tick = () => new Promise<void>(resolve => setImmediate(resolve));
function fakeApi(answer: (path: string) => unknown | Promise<unknown>, calls: Call[] = []): SavedApi & LibraryContentApi {
  return {request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    calls.push({path, method, body, signal}); return await answer(path) as T;
  }};
}
const answerSaved = (views: (shape: string | null) => readonly unknown[]) => (path: string) => {
  const url = new URL(path, 'https://test.invalid');
  if (url.pathname.endsWith('/browse-capabilities')) return capability(url.pathname.split('/')[3]!, url.searchParams.get('pivot')!);
  if (url.pathname.endsWith('/saved/summary')) return summary();
  if (url.pathname.includes('saved-pins')) return pins;
  if (url.pathname.includes('saved-resources')) return directory(views(url.searchParams.get('shape')));
  return hubList(url.searchParams.get('view') ?? 'watchlist');
};
function testContext() {
  let current = true, signedIn = true;
  const listeners = new Set<() => void>();
  const context = {session: () => (signedIn ? session : undefined), authority: () => authority, current: () => current,
    subscribeAuthority: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}} as unknown as NativeShellBootstrapContext;
  return {context, live: (value: boolean) => {current = value;}, signedIn: (value: boolean) => {signedIn = value;},
    emit: () => {for (const listener of listeners) listener();}};
}
function readerFor(api: LibraryContentApi, current: () => boolean = () => true, libraries: () => readonly string[] = () => ['movies']) {
  return new NativeHomeSavedViewSummaries({api, serverId: scope.serverId, current, libraryIds: libraries});
}
const lineOf = (resource: PersonalResource) => savedViewSummary(resource.definition!, parseBrowseCapabilities(capability(resource.definition!.libraryId, resource.definition!.pivot)), defaultI18n)!.line;

test('the binding is inert, starts once, and reads the one current library/pivot vocabulary at render time', async () => {
  const calls: Call[] = [], libraries: string[] = ['movies'];
  const api = fakeApi(answerSaved(() => [viewWire('ready'), viewWire('needs', {status: 'needs-review'}), viewWire('other', {libraryId: 'shows', pivot: 'shows'})]), calls);
  const destination = new NativeSavedViewDestination({api, scope, requestId: async () => 'request', current: () => true});
  const reader = readerFor(api, () => true, () => libraries);
  const binding = createNativeSavedViewSummaryBinding(testContext().context, destination, () => true, reader);
  try {
    assert.equal(calls.length, 0, 'constructing the binding performs no I/O');
    assert.equal(binding.getSnapshot(), 0);
    const started = binding.start();
    assert.equal(binding.start(), started, 'starting again joins the same explicit start');
    await started;
    assert.equal(calls.length, 0, 'a binding over an idle source reads nothing');
    await destination.select({view: 'views'});
    const resources = destination.getSnapshot().personal!.resources;
    assert.deepEqual(resources.map(resource => resource.id), ['ready', 'needs', 'other']);
    await tick();
    const capabilityCalls = calls.filter(call => call.path.includes('browse-capabilities'));
    assert.equal(capabilityCalls.length, 1, 'only the ready view in an admitted library is read');
    assert.ok(capabilityCalls.every(call => call.method === 'GET' && call.body === undefined));
    assert.equal(calls.filter(call => call.path.includes('saved-resources')).length, 1, 'starting enumerates no further directory page');
    assert.ok(calls.every(call => call.method === undefined || call.method === 'GET'), 'the binding issues no Saved write');
    assert.equal(binding.line(resources[0]!, defaultI18n), lineOf(resources[0]!));
    assert.notEqual(binding.line(resources[0]!, defaultI18n), resources[0]!.summary, 'the stored summary is never the computed line');
    assert.equal(binding.line(resources[1]!, defaultI18n), undefined, 'a needs-review definition supplies no words');
    assert.equal(binding.line(resources[2]!, defaultI18n), undefined, 'an unadmitted library supplies no words');
    assert.equal(binding.line({...resources[0]!}, defaultI18n), undefined, 'an equal copy is not the current page object');
    libraries.splice(0);
    assert.equal(binding.line(resources[0]!, defaultI18n), undefined, 'a removed library cannot expose old vocabulary');
  } finally {binding.dispose(); destination.dispose();}
});

test('the hub directory count is a separate fact and never becomes a resource window', async () => {
  const calls: Call[] = [];
  const api = fakeApi(answerSaved(() => []), calls);
  const hub = new NativeSavedDestination({api, scope, requestId: async () => 'request', current: () => true});
  const reader = readerFor(api);
  const binding = createNativeSavedViewSummaryBinding(testContext().context, hub, () => true, reader);
  try {
    await hub.load();
    assert.equal(hub.getSnapshot().summary?.savedViews, 6);
    assert.deepEqual(hub.getSnapshot().views.resources, [], 'the hub has not arrived a views page');
    const before = calls.length;
    await binding.start();
    assert.equal(calls.length, before, 'an empty hub window reads no vocabulary or directory');
    const held: PersonalResource = {...viewWire('held')} as unknown as PersonalResource;
    assert.equal(binding.line(held, defaultI18n), undefined);
  } finally {binding.dispose(); hub.dispose();}
});

test('a replaced window rejects old objects and late vocabulary from the previous page', async () => {
  const calls: Call[] = [], reads: {library: string; pivot: string; gate: ReturnType<typeof deferred<unknown>>}[] = [];
  const api = fakeApi(path => {
    const url = new URL(path, 'https://test.invalid');
    if (url.pathname.endsWith('/browse-capabilities')) {
      const library = url.pathname.split('/')[3]!, pivot = url.searchParams.get('pivot')!;
      const gate = deferred<unknown>(); reads.push({library, pivot, gate}); return gate.promise;
    }
    if (url.pathname.endsWith('/saved/summary')) return summary();
    if (url.pathname.includes('saved-resources')) return directory(url.searchParams.get('shape') === 'squares' ? [viewWire('fresh')] : [viewWire('old'), viewWire('other', {libraryId: 'shows', pivot: 'shows'})]);
    return hubList(url.searchParams.get('view') ?? 'watchlist');
  }, calls);
  const destination = new NativeSavedViewDestination({api, scope, requestId: async () => 'request', current: () => true});
  const reader = readerFor(api, () => true, () => ['movies', 'shows']);
  const binding = createNativeSavedViewSummaryBinding(testContext().context, destination, () => true, reader);
  try {
    await destination.select({view: 'views'});
    const [old, other] = destination.getSnapshot().personal!.resources;
    const started = binding.start();
    await tick();
    assert.equal(reads.length, 2, 'each distinct library/pivot reads once');
    await destination.select({view: 'views', shape: 'squares'});
    const fresh = destination.getSnapshot().personal!.resources[0]!;
    assert.notEqual(fresh, old);
    assert.equal(reads.length, 3, 'the replaced window starts its own genuine read');
    for (const read of reads.slice(0, 2)) read.gate.resolve(capability(read.library, read.pivot));
    await started; await tick();
    assert.equal(binding.line(old!, defaultI18n), undefined, 'an old page handle cannot regain words');
    assert.equal(binding.line(other!, defaultI18n), undefined);
    assert.equal(binding.line(fresh, defaultI18n), undefined, 'the previous page vocabulary never reaches the new window');
    reads[2]!.gate.resolve(capability(reads[2]!.library, reads[2]!.pivot));
    await tick();
    assert.equal(binding.line(fresh, defaultI18n), lineOf(fresh));
    assert.equal(binding.line(old!, defaultI18n), undefined);
  } finally {binding.dispose(); destination.dispose();}
});

test('six distinct vocabularies stay within four concurrent reads and identical keys deduplicate', async () => {
  const calls: Call[] = [];
  let inFlight = 0, peak = 0;
  const six = Array.from({length: 6}, (_, index) => viewWire(`view-${index}`, {libraryId: `library-${index}`, pivot: `pivot-${index}`}));
  const api = fakeApi(async path => {
    const url = new URL(path, 'https://test.invalid');
    if (url.pathname.endsWith('/browse-capabilities')) {
      inFlight++; peak = Math.max(peak, inFlight);
      await new Promise(resolve => setTimeout(resolve, 2));
      inFlight--;
      return capability(url.pathname.split('/')[3]!, url.searchParams.get('pivot')!);
    }
    if (url.pathname.endsWith('/saved/summary')) return summary();
    if (url.pathname.includes('saved-resources')) return directory(six);
    return hubList(url.searchParams.get('view') ?? 'watchlist');
  }, calls);
  const destination = new NativeSavedViewDestination({api, scope, requestId: async () => 'request', current: () => true});
  const reader = readerFor(api, () => true, () => six.map((_, index) => `library-${index}`));
  const binding = createNativeSavedViewSummaryBinding(testContext().context, destination, () => true, reader);
  try {
    await destination.select({view: 'views'});
    await binding.start(); await tick();
    assert.equal(calls.filter(call => call.path.includes('browse-capabilities')).length, 6);
    assert.equal(peak, 4, 'the existing reader keeps at most four capability reads in flight');
    assert.equal(new Set(calls.filter(call => call.path.includes('browse-capabilities')).map(call => call.path)).size, 6);
  } finally {binding.dispose(); destination.dispose();}
  const deduped: Call[] = [];
  const same = [viewWire('one'), viewWire('two'), viewWire('three')];
  const dedupedApi = fakeApi(answerSaved(() => same), deduped);
  const dedupedDestination = new NativeSavedViewDestination({api: dedupedApi, scope, requestId: async () => 'request', current: () => true});
  const dedupedReader = readerFor(dedupedApi);
  const dedupedBinding = createNativeSavedViewSummaryBinding(testContext().context, dedupedDestination, () => true, dedupedReader);
  try {
    await dedupedDestination.select({view: 'views'});
    await dedupedBinding.start(); await tick();
    assert.equal(deduped.filter(call => call.path.includes('browse-capabilities')).length, 1, 'one library/pivot vocabulary is read once for three views');
  } finally {dedupedBinding.dispose(); dedupedDestination.dispose();}
});

test('disposal and a retired borrowed source stop words but leave the source and reader usable', async () => {
  const calls: Call[] = [], gate = deferred<unknown>();
  let sourceAlive = true;
  const api = fakeApi(path => {
    const url = new URL(path, 'https://test.invalid');
    if (url.pathname.endsWith('/browse-capabilities')) return gate.promise;
    if (url.pathname.endsWith('/saved/summary')) return summary();
    if (url.pathname.includes('saved-resources')) return directory([viewWire('actual')]);
    return hubList(url.searchParams.get('view') ?? 'watchlist');
  }, calls);
  const destination = new NativeSavedViewDestination({api, scope, requestId: async () => 'request', current: () => sourceAlive});
  const reader = readerFor(api, () => true);
  const binding = createNativeSavedViewSummaryBinding(testContext().context, destination, () => true, reader);
  try {
    await destination.select({view: 'views'});
    const resource = destination.getSnapshot().personal!.resources[0]!;
    const started = binding.start();
    await tick();
    let notifications = 0;
    binding.subscribe(() => {notifications++;});
    binding.dispose();
    gate.resolve(capability('movies', 'movies'));
    await started; await tick();
    assert.equal(binding.line(resource, defaultI18n), undefined, 'a disposed binding supplies no line');
    assert.equal(notifications, 0);
    assert.equal(destination.getSnapshot().phase, 'ready', 'the borrowed Saved source is not disposed');
    await destination.select({view: 'views', shape: 'squares'});
    assert.equal(destination.getSnapshot().phase, 'ready', 'the source still serves real routes');
    await reader.load(destination.getSnapshot().personal!.resources);
    assert.ok(reader.line(destination.getSnapshot().personal!.resources[0]!, defaultI18n), 'the Saved-owned reader is not disposed');
  } finally {binding.dispose(); destination.dispose(); reader.dispose();}
  const late = deferred<unknown>();
  let lateAlive = true;
  const lateApi = fakeApi(path => {
    const url = new URL(path, 'https://test.invalid');
    if (url.pathname.endsWith('/browse-capabilities')) return late.promise;
    if (url.pathname.endsWith('/saved/summary')) return summary();
    if (url.pathname.includes('saved-resources')) return directory([viewWire('retired')]);
    return hubList(url.searchParams.get('view') ?? 'watchlist');
  });
  const retiredDestination = new NativeSavedViewDestination({api: lateApi, scope, requestId: async () => 'request', current: () => lateAlive});
  const lateReader = readerFor(lateApi);
  const lateBinding = createNativeSavedViewSummaryBinding(testContext().context, retiredDestination, () => true, lateReader);
  try {
    await retiredDestination.select({view: 'views'});
    const retired = retiredDestination.getSnapshot().personal!.resources[0]!;
    void lateBinding.start(); await tick();
    lateAlive = false;
    assert.equal(lateBinding.line(retired, defaultI18n), undefined, 'a retired borrowed source supplies no line');
    late.resolve(capability('movies', 'movies'));
    await tick();
    assert.equal(lateBinding.line(retired, defaultI18n), undefined, 'late vocabulary cannot revive a retired source');
  } finally {lateBinding.dispose(); retiredDestination.dispose(); lateReader.dispose();}
});

test('a retired owner stops local words and subscriptions without disposing the borrowed services', async () => {
  const calls: Call[] = [];
  const api = fakeApi(answerSaved(() => [viewWire('owned')]), calls);
  const destination = new NativeSavedViewDestination({api, scope, requestId: async () => 'request', current: () => true});
  const reader = readerFor(api);
  const owner = {current: true};
  const context = testContext();
  const binding = createNativeSavedViewSummaryBinding(context.context, destination, () => owner.current, reader);
  try {
    await destination.select({view: 'views'});
    const resource = destination.getSnapshot().personal!.resources[0]!;
    await binding.start(); await tick();
    assert.equal(binding.line(resource, defaultI18n), lineOf(resource));
    let notified = 0;
    binding.subscribe(() => {notified++;});
    owner.current = false; context.emit();
    assert.equal(notified, 1, 'losing the owner notifies once so drawn words disappear');
    assert.equal(binding.line(resource, defaultI18n), undefined);
    assert.equal(destination.getSnapshot().phase, 'ready', 'the owner still controls its own Saved source');
    await binding.start();
    assert.equal(binding.line(resource, defaultI18n), undefined, 'an idempotent start cannot revive a retired owner');
  } finally {binding.dispose(); destination.dispose(); reader.dispose();}
});

test('one borrowed reader drops the old window as soon as a replacement starts and cannot revive a retired owner', async () => {
  const owner = {current: true};
  const context = testContext();
  const directories: {shape: string | null; gate: ReturnType<typeof deferred<unknown>>}[] = [];
  const capabilities: {library: string; pivot: string; gate: ReturnType<typeof deferred<unknown>>}[] = [];
  const api = fakeApi(path => {
    const url = new URL(path, 'https://test.invalid');
    if (url.pathname.endsWith('/saved/summary')) return summary();
    if (url.pathname.includes('saved-resources')) {
      const entry = {shape: url.searchParams.get('shape'), gate: deferred<unknown>()};
      directories.push(entry); return entry.gate.promise;
    }
    if (url.pathname.endsWith('/browse-capabilities')) {
      const entry = {library: url.pathname.split('/')[3]!, pivot: url.searchParams.get('pivot')!, gate: deferred<unknown>()};
      capabilities.push(entry); return entry.gate.promise;
    }
    return hubList(url.searchParams.get('view') ?? 'watchlist');
  });
  const destination = new NativeSavedViewDestination({api, scope, requestId: async () => 'request', current: () => true});
  const reader = readerFor(api);
  const binding = createNativeSavedViewSummaryBinding(context.context, destination, () => owner.current, reader);
  try {
    const first = destination.select({view: 'views'});
    directories[0]!.gate.resolve(directory([viewWire('a')]));
    await first;
    const a = destination.getSnapshot().personal!.resources[0]!;
    const started = binding.start(); await tick();
    capabilities[0]!.gate.resolve(capability('movies', 'movies'));
    await started; await tick();
    assert.equal(binding.line(a, defaultI18n), lineOf(a));

    const replacing = destination.select({view: 'views', shape: 'squares'});
    assert.equal(destination.getSnapshot().phase, 'loading');
    assert.equal(binding.line(a, defaultI18n), undefined, 'the old page object is gone as soon as its window is replaced');
    directories[1]!.gate.resolve(directory([viewWire('b')]));
    await replacing;
    const b = destination.getSnapshot().personal!.resources[0]!;
    assert.equal(binding.line(b, defaultI18n), undefined, 'a replaced window has no vocabulary until its own read lands');
    capabilities[1]!.gate.resolve(capability('movies', 'movies'));
    await tick();
    assert.equal(binding.line(b, defaultI18n), lineOf(b));
    assert.equal(binding.line(a, defaultI18n), undefined);

    const third = destination.select({view: 'views', shape: 'posters'});
    assert.equal(binding.line(b, defaultI18n), undefined);
    directories[2]!.gate.resolve(directory([viewWire('c')]));
    await third;
    const c = destination.getSnapshot().personal!.resources[0]!;
    owner.current = false; context.emit();
    capabilities[2]!.gate.resolve(capability('movies', 'movies'));
    await tick();
    assert.equal(binding.line(c, defaultI18n), undefined, 'a late reply cannot supply words after the owner retires');
  } finally {binding.dispose(); destination.dispose(); reader.dispose();}
});

test('independent Saved-owned readers and sources keep separate windows when one binding is disposed', async () => {
  const calls: Call[] = [];
  const api = fakeApi(answerSaved(shape => shape === 'squares' ? [viewWire('theirs', {libraryId: 'shows', pivot: 'shows'})] : [viewWire('mine')]), calls);
  const mine = new NativeSavedViewDestination({api, scope, requestId: async () => 'request', current: () => true});
  const theirs = new NativeSavedViewDestination({api, scope, requestId: async () => 'request', current: () => true});
  const reader = readerFor(api), otherReader = readerFor(api, () => true, () => ['movies', 'shows']);
  const originalDispose = reader.dispose;
  let readerDisposals = 0;
  reader.dispose = () => {readerDisposals++; originalDispose();};
  const binding = createNativeSavedViewSummaryBinding(testContext().context, mine, () => true, reader);
  const otherBinding = createNativeSavedViewSummaryBinding(testContext().context, theirs, () => true, otherReader);
  try {
    await mine.select({view: 'views'});
    await theirs.select({view: 'views', shape: 'squares'});
    const a = mine.getSnapshot().personal!.resources[0]!;
    const b = theirs.getSnapshot().personal!.resources[0]!;
    await Promise.all([binding.start(), otherBinding.start()]); await tick();
    assert.equal(binding.line(a, defaultI18n), lineOf(a));
    assert.equal(otherBinding.line(b, defaultI18n), lineOf(b));
    assert.equal(binding.line(b, defaultI18n), undefined, 'one Saved reader never draws another window object');
    assert.equal(otherBinding.line(a, defaultI18n), undefined);
    binding.dispose();
    assert.equal(readerDisposals, 0, 'local disposal never disposes the borrowed Saved-owned reader');
    assert.equal(binding.line(a, defaultI18n), undefined);
    assert.equal(otherBinding.line(b, defaultI18n), lineOf(b), 'the independent binding keeps its own words');
    assert.equal(mine.getSnapshot().phase, 'ready', 'the borrowed Saved source is still its owner\'s');
    await reader.load(mine.getSnapshot().personal!.resources);
    assert.equal(reader.line(a, defaultI18n), lineOf(a), 'the borrowed reader is still usable after local disposal');
    assert.ok(calls.every(call => call.method === undefined || call.method === 'GET'));
  } finally {binding.dispose(); otherBinding.dispose(); reader.dispose(); mine.dispose(); theirs.dispose(); otherReader.dispose();}
});

test('a borrowed reader publish failure never escapes and the Saved-owned reader stays reusable', async () => {
  const calls: Call[] = [];
  const api = fakeApi(answerSaved(() => [viewWire('survivor')]), calls);
  const destination = new NativeSavedViewDestination({api, scope, requestId: async () => 'request', current: () => true});
  const reader = readerFor(api);
  let throwOnce = true;
  reader.subscribe(() => {if (throwOnce) {throwOnce = false; throw new Error('borrowed publish failed');}});
  const binding = createNativeSavedViewSummaryBinding(testContext().context, destination, () => true, reader);
  const rejections: unknown[] = [];
  const onRejection = (error: unknown) => {rejections.push(error);};
  process.on('unhandledRejection', onRejection);
  try {
    await destination.select({view: 'views'});
    const resource = destination.getSnapshot().personal!.resources[0]!;
    await binding.start(); await tick();
    assert.equal(rejections.length, 0, 'a borrowed reader failure is contained by the binding');
    assert.equal(binding.line(resource, defaultI18n), undefined);
    await reader.load([...destination.getSnapshot().personal!.resources]); await tick();
    assert.equal(binding.line(resource, defaultI18n), lineOf(resource), 'the owner can reuse its reader');
  } finally {process.off('unhandledRejection', onRejection); binding.dispose(); destination.dispose(); reader.dispose();}
});

test('the line follows the actual i18n and the newest capability with no cached summary', async () => {
  const calls: Call[] = [];
  let capabilityReads = 0;
  const api = fakeApi(path => {
    const url = new URL(path, 'https://test.invalid');
    if (url.pathname.endsWith('/browse-capabilities')) {
      capabilityReads++;
      const response = capability('movies', 'movies');
      return capabilityReads === 1 ? response : {...response, sorts: []};
    }
    if (url.pathname.endsWith('/saved/summary')) return summary();
    if (url.pathname.includes('saved-resources')) return directory([viewWire('cache')]);
    return hubList(url.searchParams.get('view') ?? 'watchlist');
  }, calls);
  const destination = new NativeSavedViewDestination({api, scope, requestId: async () => 'request', current: () => true});
  const reader = readerFor(api);
  const binding = createNativeSavedViewSummaryBinding(testContext().context, destination, () => true, reader);
  const plainCapabilities = () => parseBrowseCapabilities(capability('movies', 'movies'));
  const marked: SavedViewSummaryText = {t: (id, values) => `«${defaultI18n.t(id, values)}»`, has: defaultI18n.has, locale: defaultI18n.locale};
  const british: SavedViewSummaryText = {t: defaultI18n.t, has: defaultI18n.has, locale: 'en-GB'};
  const missing: SavedViewSummaryText = {t: defaultI18n.t, has: (id: string): id is never => id === '', locale: defaultI18n.locale};
  try {
    await destination.select({view: 'views'});
    const resource = destination.getSnapshot().personal!.resources[0]!;
    await binding.start(); await tick();
    const plain = lineOf(resource);
    assert.equal(binding.line(resource, defaultI18n), plain);
    const markedLine = savedViewSummary(resource.definition!, plainCapabilities(), marked)!.line;
    assert.notEqual(markedLine, plain, 'the actual supplied i18n is used at read time');
    assert.equal(binding.line(resource, marked), markedLine);
    assert.equal(binding.line(resource, british), savedViewSummary(resource.definition!, plainCapabilities(), british)!.line);
    assert.equal(binding.line(resource, missing), undefined, 'suppressed vocabulary withholds the line');
    assert.equal(binding.line(resource, defaultI18n), plain, 'an earlier text is never cached');

    await destination.select({view: 'views', shape: 'squares'});
    const replaced = destination.getSnapshot().personal!.resources[0]!;
    assert.notEqual(replaced, resource);
    await tick();
    assert.equal(capabilityReads, 2, 'the replaced window reads its newest vocabulary');
    assert.equal(binding.line(resource, defaultI18n), undefined);
    assert.equal(binding.line(replaced, defaultI18n), undefined, 'the newest capability withholds the withdrawn sort');
  } finally {binding.dispose(); destination.dispose(); reader.dispose();}
});

test('an observed owner retirement is permanent on the render path and emits nothing', async () => {
  const calls: Call[] = [];
  const api = fakeApi(answerSaved(() => [viewWire('latched')]), calls);
  const destination = new NativeSavedViewDestination({api, scope, requestId: async () => 'request', current: () => true});
  const reader = readerFor(api);
  const owner = {current: true};
  const binding = createNativeSavedViewSummaryBinding(testContext().context, destination, () => owner.current, reader);
  try {
    await destination.select({view: 'views'});
    const resource = destination.getSnapshot().personal!.resources[0]!;
    await binding.start(); await tick();
    assert.equal(binding.line(resource, defaultI18n), lineOf(resource));
    let notifications = 0;
    binding.subscribe(() => {notifications++;});
    const revision = binding.getSnapshot();
    owner.current = false;
    assert.equal(binding.line(resource, defaultI18n), undefined, 'the render path observes the retired owner');
    assert.equal(notifications, 0, 'retiring from render emits no React notification');
    assert.equal(binding.getSnapshot(), revision, 'the render path does not bump the store revision');
    owner.current = true;
    assert.equal(binding.line(resource, defaultI18n), undefined, 're-entering the same owner cannot revive old words');
    await binding.start();
    assert.equal(binding.line(resource, defaultI18n), undefined, 'an idempotent start cannot revive the retired owner');
    assert.equal(destination.getSnapshot().phase, 'ready', 'the borrowed Saved source is untouched');
    assert.ok(reader.line(resource, defaultI18n), 'the borrowed reader is untouched');
  } finally {binding.dispose(); destination.dispose(); reader.dispose();}
});
