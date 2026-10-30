import assert from 'node:assert/strict';
import test from 'node:test';
import {defaultI18n} from '../../../packages/i18n/src';
import {parseBrowseCapabilities} from '../../../packages/client-core/src/browse.ts';
import type {LibraryContentApi} from '../../../packages/client-core/src/library-content.ts';
import type {PersonalResource} from '../../../packages/client-core/src/personal-saved.ts';
import type {SavedApi} from '../../../packages/client-core/src/saved.ts';
import {savedViewSummary} from '../../../packages/client-core/src/presentation/saved-view-summary.ts';
import {createNativeSavedViewSummaryOwner} from '../src-next/screens/saved/view-summary-owner.ts';
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
const viewWire = (id: string, libraryId = 'movies', pivotId = 'movies') => ({
  id, serverId: scope.serverId, viewerFence: 'directory-fence', kind: 'view', name: id, summary: `STORED WORDS ${id}`,
  visibility: 'private', revision: 1, role: 'owner', entryCount: 1, actions: ['pin'], status: 'ready',
  invalidComponents: [], pinned: false, pinRevision: 1, shares: [],
  definition: {libraryId, pivot: pivotId, query: null, sort: [{field: 'title', direction: 'asc'}], presentation: 'grid'},
});
const directory = (resources: readonly unknown[]) => ({serverId: scope.serverId, viewerFence: 'directory-fence', revision: 1, resources, nextCursor: ''});
const summary = {serverId: scope.serverId, viewerFence: 'summary-fence', revision: {catalog: 1, viewer: 1},
  myList: {posters: 0, squares: 0}, favorites: {posters: 0, squares: 0}, playlists: 0, collections: 0, savedViews: 6};
type Call = Readonly<{path: string; method?: string; body?: unknown; signal?: AbortSignal}>;
const deferred = <T>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {resolve, promise};};
const tick = () => new Promise<void>(resolve => setImmediate(resolve));
function fakeApi(answer: (path: string) => unknown | Promise<unknown>, calls: Call[] = []): SavedApi & LibraryContentApi {
  return {request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    calls.push({path, method, body, signal}); return await answer(path) as T;
  }};
}
const answerViews = (views: (shape: string | null) => readonly unknown[]) => (path: string) => {
  const url = new URL(path, 'https://test.invalid');
  if (url.pathname.endsWith('/browse-capabilities')) return capability(url.pathname.split('/')[3]!, url.searchParams.get('pivot')!);
  if (url.pathname.endsWith('/saved/summary')) return summary;
  if (url.pathname.includes('saved-resources')) return directory(views(url.searchParams.get('shape')));
  return {serverId: scope.serverId, viewerFence: 'saved-fence', revision: {catalog: 1, viewer: 1},
    scope: {serverId: scope.serverId, libraryId: '', libraryKind: 'mixed', view: 'watchlist', entityId: '', viewerFence: 'saved-fence'},
    heading: {key: 'saved.title', fallback: ''}, navigation: [], query: {sort: 'title', direction: 'asc', category: 'all', q: '', limit: 40, searchMode: 'none'},
    sorts: [], filters: [], sections: [{id: 'items', type: 'grid', heading: {key: 'saved.title', fallback: ''}, entries: [], totalCount: 0, nextCursor: ''}]};
};
function testContext(api: SavedApi & LibraryContentApi, libraries: string[] = ['movies', 'shows']) {
  let current = true, signedIn = true;
  const listeners = new Set<() => void>();
  const context = {session: () => (signedIn ? session : undefined), authority: () => authority, current: () => current, api,
    libraries: () => libraries.map(id => ({id})),
    subscribeAuthority: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}} as unknown as NativeShellBootstrapContext;
  return {context, live: (value: boolean) => {current = value;}, signedIn: (value: boolean) => {signedIn = value;},
    emit: () => {for (const listener of listeners) listener();}};
}
const lineOf = (resource: PersonalResource) => savedViewSummary(resource.definition!, parseBrowseCapabilities(capability(resource.definition!.libraryId, resource.definition!.pivot)), defaultI18n)!.line;

test('the Saved summary owner is inert, starts once, and draws only its actual arrived window', async () => {
  const calls: Call[] = [];
  const api = fakeApi(answerViews(shape => shape === 'squares' ? [viewWire('fresh')] : [viewWire('actual')]), calls);
  const destination = new NativeSavedViewDestination({api, scope, requestId: async () => 'request', current: () => true});
  const owner = createNativeSavedViewSummaryOwner(testContext(api).context, destination, () => true);
  try {
    assert.equal(calls.length, 0, 'construction performs no I/O');
    const started = owner.start();
    assert.equal(owner.start(), started, 'start is idempotent');
    await started;
    assert.equal(calls.length, 0, 'an owner over an idle source reads nothing');
    await destination.select({view: 'views'});
    const resource = destination.getSnapshot().personal!.resources[0]!;
    await tick();
    assert.equal(calls.filter(call => call.path.includes('browse-capabilities')).length, 1);
    assert.equal(owner.line(resource, defaultI18n), lineOf(resource));
    assert.notEqual(owner.line(resource, defaultI18n), resource.summary, 'the stored summary is never the computed line');
    assert.equal(owner.line({...resource}, defaultI18n), undefined, 'only the actual window object is accepted');
    const revision = owner.getSnapshot();
    assert.equal(owner.getSnapshot(), revision);
    await owner.start();
    assert.equal(calls.filter(call => call.path.includes('browse-capabilities')).length, 1, 'repeated start duplicates no reader work');
    await destination.select({view: 'views'});
    assert.equal(calls.filter(call => call.path.includes('browse-capabilities')).length, 1, 'the identical window needs no new read');
    assert.equal(calls.filter(call => call.path.includes('saved-resources')).length, 1, 'the owner never enumerates another directory page');
    await destination.select({view: 'views', shape: 'squares'});
    const fresh = destination.getSnapshot().personal!.resources[0]!;
    await tick();
    assert.equal(owner.line(resource, defaultI18n), undefined, 'a replaced page withholds its old object immediately');
    assert.equal(owner.line(fresh, defaultI18n), lineOf(fresh));
    assert.equal(calls.filter(call => call.path.includes('browse-capabilities')).length, 2);
    assert.ok(calls.every(call => call.method === undefined || call.method === 'GET'), 'the owner issues no Saved write');
  } finally {owner.dispose(); destination.dispose();}
});

test('two independent Saved owners keep their own readers and one disposal leaves the other and its source usable', async () => {
  const calls: Call[] = [];
  const api = fakeApi(answerViews(shape => shape === 'squares' ? [viewWire('theirs', 'shows', 'shows')] : [viewWire('mine')]), calls);
  const mine = new NativeSavedViewDestination({api, scope, requestId: async () => 'request', current: () => true});
  const theirs = new NativeSavedViewDestination({api, scope, requestId: async () => 'request', current: () => true});
  const mineOwner = createNativeSavedViewSummaryOwner(testContext(api).context, mine, () => true);
  const theirsOwner = createNativeSavedViewSummaryOwner(testContext(api).context, theirs, () => true);
  try {
    await mine.select({view: 'views'});
    await theirs.select({view: 'views', shape: 'squares'});
    const a = mine.getSnapshot().personal!.resources[0]!;
    const b = theirs.getSnapshot().personal!.resources[0]!;
    await Promise.all([mineOwner.start(), theirsOwner.start()]); await tick();
    assert.equal(mineOwner.line(a, defaultI18n), lineOf(a));
    assert.equal(theirsOwner.line(b, defaultI18n), lineOf(b));
    assert.equal(mineOwner.line(b, defaultI18n), undefined, 'one owner never draws another window object');
    assert.equal(theirsOwner.line(a, defaultI18n), undefined);
    mineOwner.dispose();
    assert.equal(mineOwner.line(a, defaultI18n), undefined);
    assert.equal(theirsOwner.line(b, defaultI18n), lineOf(b), 'the independent owner keeps its own reader and words');
    assert.equal(mine.getSnapshot().phase, 'ready', 'the borrowed Saved source is untouched');
    assert.equal(theirs.getSnapshot().phase, 'ready');
    assert.equal(calls.filter(call => call.path.includes('browse-capabilities')).length, 2);
  } finally {mineOwner.dispose(); theirsOwner.dispose(); mine.dispose(); theirs.dispose();}
});

test('owner retirement suppresses a late vocabulary reply and disposal aborts the owned read once', async () => {
  const calls: Call[] = [], gates: {gate: ReturnType<typeof deferred<unknown>>}[] = [];
  const api = fakeApi(path => {
    const url = new URL(path, 'https://test.invalid');
    if (url.pathname.endsWith('/browse-capabilities')) {const gate = deferred<unknown>(); gates.push({gate}); return gate.promise;}
    return answerViews(() => [viewWire('retiring')])(path);
  }, calls);
  const destination = new NativeSavedViewDestination({api, scope, requestId: async () => 'request', current: () => true});
  const ownerFlag = {current: true};
  const owner = createNativeSavedViewSummaryOwner(testContext(api).context, destination, () => ownerFlag.current);
  try {
    await destination.select({view: 'views'});
    const resource = destination.getSnapshot().personal!.resources[0]!;
    const started = owner.start(); await tick();
    assert.equal(owner.line(resource, defaultI18n), undefined, 'no words before the deferred vocabulary lands');
    ownerFlag.current = false;
    assert.equal(owner.line(resource, defaultI18n), undefined);
    gates[0]!.gate.resolve(capability('movies', 'movies'));
    await started; await tick();
    assert.equal(owner.line(resource, defaultI18n), undefined, 'a late reply cannot supply words after owner retirement');
    owner.dispose();
    owner.dispose();
    assert.equal(destination.getSnapshot().phase, 'ready');
  } finally {owner.dispose(); destination.dispose();}

  const abortCalls: Call[] = [];
  const abortGate = deferred<unknown>();
  const abortApi = fakeApi(path => {
    if (path.includes('browse-capabilities')) return abortGate.promise;
    return answerViews(() => [viewWire('aborting')])(path);
  }, abortCalls);
  const abortDestination = new NativeSavedViewDestination({api: abortApi, scope, requestId: async () => 'request', current: () => true});
  const abortOwner = createNativeSavedViewSummaryOwner(testContext(abortApi).context, abortDestination, () => true);
  try {
    await abortDestination.select({view: 'views'});
    const pending = abortOwner.start(); await tick();
    const capabilityCall = abortCalls.find(call => call.path.includes('browse-capabilities'))!;
    assert.equal(capabilityCall.signal?.aborted, false);
    abortOwner.dispose();
    abortOwner.dispose();
    assert.equal(capabilityCall.signal?.aborted, true, 'owner disposal aborts the one owned reader read');
    assert.equal(abortDestination.getSnapshot().phase, 'ready', 'the borrowed Saved source remains usable');
    abortGate.resolve(capability('movies', 'movies'));
    await pending; await tick();
    assert.equal(abortOwner.line(abortDestination.getSnapshot().personal!.resources[0]!, defaultI18n), undefined);
  } finally {abortOwner.dispose(); abortDestination.dispose();}
});

test('an observed owner loss retires the owned reader on the render path without notifying', async () => {
  const calls: Call[] = [];
  const api = fakeApi(answerViews(() => [viewWire('settled')]), calls);
  const destination = new NativeSavedViewDestination({api, scope, requestId: async () => 'request', current: () => true});
  const ownerFlag = {current: true};
  const owner = createNativeSavedViewSummaryOwner(testContext(api).context, destination, () => ownerFlag.current);
  try {
    await destination.select({view: 'views'});
    const resource = destination.getSnapshot().personal!.resources[0]!;
    await owner.start(); await tick();
    assert.equal(owner.line(resource, defaultI18n), lineOf(resource));
    let notifications = 0;
    owner.subscribe(() => {notifications++;});
    const revision = owner.getSnapshot();
    ownerFlag.current = false;
    assert.equal(owner.line(resource, defaultI18n), undefined, 'the render path observes the lost owner');
    assert.equal(notifications, 0, 'render-path retirement emits no notification');
    assert.equal(owner.getSnapshot(), revision, 'render-path retirement keeps the revision');
    ownerFlag.current = true;
    await owner.start();
    assert.equal(owner.line(resource, defaultI18n), undefined, 're-entry and start cannot revive old words');
    await destination.select({view: 'views', shape: 'squares'});
    await tick();
    assert.equal(owner.line(destination.getSnapshot().personal!.resources[0]!, defaultI18n), undefined, 'the retired owner accepts no later window');
    assert.equal(destination.getSnapshot().phase, 'ready', 'the borrowed Saved source is untouched');
  } finally {owner.dispose(); destination.dispose();}
});

test('a pending owned read aborts promptly on observed owner loss and cannot accept its late reply', async () => {
  const calls: Call[] = [], gate = deferred<unknown>();
  const api = fakeApi(path => path.includes('browse-capabilities') ? gate.promise : answerViews(() => [viewWire('pending')])(path), calls);
  const destination = new NativeSavedViewDestination({api, scope, requestId: async () => 'request', current: () => true});
  const ownerFlag = {current: true};
  const owner = createNativeSavedViewSummaryOwner(testContext(api).context, destination, () => ownerFlag.current);
  try {
    await destination.select({view: 'views'});
    const resource = destination.getSnapshot().personal!.resources[0]!;
    const started = owner.start(); await tick();
    const capabilityCall = calls.find(call => call.path.includes('browse-capabilities'))!;
    assert.equal(capabilityCall.signal?.aborted, false);
    let notifications = 0;
    owner.subscribe(() => {notifications++;});
    const revision = owner.getSnapshot();
    ownerFlag.current = false;
    assert.equal(owner.line(resource, defaultI18n), undefined);
    assert.equal(capabilityCall.signal?.aborted, true, 'the owned capability read aborts on the render path');
    assert.equal(notifications, 0); assert.equal(owner.getSnapshot(), revision);
    gate.resolve(capability('movies', 'movies'));
    await started; await tick();
    assert.equal(owner.line(resource, defaultI18n), undefined, 'a late reply cannot be accepted');
    ownerFlag.current = true;
    await owner.start();
    assert.equal(owner.line(resource, defaultI18n), undefined);
    assert.equal(destination.getSnapshot().phase, 'ready');
  } finally {owner.dispose(); destination.dispose();}
});

test('a genuine authority retirement aborts the pending owned reader read', async () => {
  const calls: Call[] = [], gate = deferred<unknown>();
  const api = fakeApi(path => path.includes('browse-capabilities') ? gate.promise : answerViews(() => [viewWire('authority')])(path), calls);
  const context = testContext(api);
  const destination = new NativeSavedViewDestination({api, scope, requestId: async () => 'request', current: () => true});
  const owner = createNativeSavedViewSummaryOwner(context.context, destination, () => true);
  try {
    await destination.select({view: 'views'});
    const resource = destination.getSnapshot().personal!.resources[0]!;
    const started = owner.start(); await tick();
    const capabilityCall = calls.find(call => call.path.includes('browse-capabilities'))!;
    context.live(false); context.emit();
    assert.equal(capabilityCall.signal?.aborted, true, 'authority retirement aborts the owned reader work');
    assert.equal(owner.line(resource, defaultI18n), undefined);
    gate.resolve(capability('movies', 'movies'));
    await started; await tick();
    assert.equal(owner.line(resource, defaultI18n), undefined);
    assert.equal(destination.getSnapshot().phase, 'ready', 'the borrowed Saved source is not disposed by the owner');
  } finally {owner.dispose(); destination.dispose();}
});
