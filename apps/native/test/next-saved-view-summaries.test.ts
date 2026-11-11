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
