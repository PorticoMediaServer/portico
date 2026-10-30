import assert from 'node:assert/strict';
import test from 'node:test';
import {SearchService} from '../../../packages/client-core/src/search';
import {NativeSearchDestination} from '../src-next/screens/search/model';

const scope = {serverId: 'server', viewerId: 'the-actual-viewer'};
const tick = () => new Promise<void>(resolve => setImmediate(resolve));
function answer(url: URL) {
  const group = url.searchParams.get('group'), page = url.searchParams.get('cursor');
  const groups = group ? [group] : ['movies', 'shows'];
  return {scope: {serverId: 'server', libraryId: '', libraryKind: 'mixed', view: 'search', entityId: '', viewerFence: 'Search-own-fence'}, revision: {catalog: 3, viewer: 7},
    query: {q: url.searchParams.get('q'), group: group ?? '', groups, limit: 40, sort: 'relevance', direction: 'desc', libraryIds: [], recorded: false},
    capabilities: {groups: ['movies', 'shows'].map(id => ({id, title: '', available: true, reason: '', sorts: ['relevance']})), sorts: ['relevance'], directions: ['desc'], maxLimit: 40, groupBudgetMs: 1000},
    groups: groups.map(id => ({id, entityKind: id === 'movies' ? 'movie' : 'show', title: '', status: 'success', errorCode: '', totalCount: 2, totalCountExact: true,
      nextCursor: page ? '' : `${id}-next`, items: [{id: `${id}-${page ? 2 : 1}`, kind: id === 'movies' ? 'movie' : 'show', libraryId: id, title: 'Served title',
        navigation: {view: id === 'movies' ? 'item' : 'show', entityId: `${id}-${page ? 2 : 1}`}}]}))};
}
function fixture() {
  const paths: URL[] = [], signals: AbortSignal[] = [];
  let reply = async (url: URL): Promise<unknown> => answer(url), current = true;
  const api = {request: async <T>(path: string, _method?: string, _body?: unknown, signal?: AbortSignal): Promise<T> => {
    const url = new URL(path, 'https://fixture.invalid'); paths.push(url); if (signal) signals.push(signal); return await reply(url) as T;
  }};
  const native = new NativeSearchDestination({api, scope, current: () => current, initialQuery: 'bridge', rememberHistory: () => false,
    reportError(error) {throw error;}});
  return {native, api, paths, signals, reply: (value: typeof reply) => {reply = value;}, retire: () => {current = false;}};
}
test('a continuation fork accepts only a genuinely offered cursor and never changes its parent All read', async () => {
  const f = fixture(), service = new SearchService({api: f.api, scope});
  try {
    assert.equal(service.forkContinuation('movies'), undefined);
    await service.select('bridge', {record: true}); const original = service.getSnapshot();
    assert.equal(service.forkContinuation('people'), undefined);
    const child = service.forkContinuation('movies')!;
    try {await child.next('movies');
      assert.equal(service.getSnapshot(), original);
      assert.equal(child.getSnapshot().pagination.cursor, 'movies-next');
      assert.equal(f.paths.at(-1)!.searchParams.get('group'), 'movies');
      assert.equal(f.paths[0]!.searchParams.get('record'), '1');
      assert.equal(f.paths.at(-1)!.searchParams.get('record'), null, 'continuation is not another history recording intent');
      assert.equal(child.getSnapshot().groups[0]!.items[0]!.id, 'movies-2');
    } finally {child.dispose();}
  } finally {service.dispose(); f.native.dispose();}
});
test('All sections keep their own projection and offered continuation when another section pages or the route pauses', async () => {
  const f = fixture();
  try {
    await f.native.commit(); const original = f.native.getSnapshot().search;
    await f.native.next('movies');
    assert.deepEqual(f.native.getSnapshot().groups.map(group => [group.id, group.items.map(item => item.id)]), [['movies', ['movies-1', 'movies-2']], ['shows', ['shows-1']]]);
    assert.equal(f.native.getSnapshot().search, original, 'All query/capabilities remain the actual original read');
    assert.deepEqual(f.native.getSnapshot().paging?.movies?.fulfilled, {waitingId: JSON.stringify(['search-tail', 'movies', 'movies-next']), firstItemId: JSON.stringify(['movies', 'movie', 'movies-2'])});
    f.native.pause(); f.native.resume();
    assert.equal(f.native.getSnapshot().groups[0]!.items.length, 2, 'pause does not replace arrived section pages with the preview');
    await f.native.next('shows');
    assert.deepEqual(f.native.getSnapshot().groups.map(group => group.items.map(item => item.id)), [['movies-1', 'movies-2'], ['shows-1', 'shows-2']]);
    assert.deepEqual(f.paths.map(url => [url.searchParams.get('group'), url.searchParams.get('cursor')]), [[null, null], ['movies', 'movies-next'], ['shows', 'shows-next']]);
  } finally {f.native.dispose();}
});
test('a new draft retires an old group read before an abort-ignoring response arrives', async () => {
  const f = fixture(); let resolve!: (value: unknown) => void;
  try {
    await f.native.commit(); f.reply(url => url.searchParams.has('cursor') ? new Promise(done => {resolve = done;}) : Promise.resolve(answer(url)));
    const page = f.native.next('movies'); await tick(); const oldURL = f.paths.at(-1)!;
    f.native.setText('new draft'); assert.equal(f.signals.at(-1)!.aborted, true);
    resolve(answer(oldURL)); await page; await tick();
    assert.equal(f.native.getSnapshot().text, 'new draft'); assert.deepEqual(f.native.getSnapshot().groups, []);
  } finally {f.native.dispose();}
});
test('a known group access denial retires retained All facts before its canonical reread', async () => {
  const f = fixture();
  try {
    await f.native.commit(); f.reply(async () => {throw Object.assign(new Error('denied'), {status: 403, code: 'forbidden'});});
    await f.native.next('movies'); await tick();
    assert.deepEqual(f.native.getSnapshot().groups, []);
    assert.equal(f.native.getSnapshot().search.error?.code, 'forbidden');
  } finally {f.native.dispose();}
});

test('same-query type changes retain genuine capability controls while a different draft clears them', async () => {
  const f = fixture(); let resolve!: (value: unknown) => void;
  try {
    await f.native.commit(); const capabilities = f.native.getSnapshot().capabilities;
    f.reply(url => url.searchParams.has('group') ? new Promise(done => {resolve = done;}) : Promise.resolve(answer(url)));
    f.native.selectGroup('movies'); await tick();
    assert.equal(f.native.getSnapshot().search.data, null);
    assert.equal(f.native.getSnapshot().capabilities, capabilities, 'the focused chip remains in the actual offered control set');
    const pendingURL = f.paths.at(-1)!; resolve(answer(pendingURL)); await tick();
    f.native.setText('different draft'); assert.equal(f.native.getSnapshot().capabilities, undefined);
  } finally {f.native.dispose();}
});

test('a changed continuation viewer fence discards mixed projections and rereads All through the original parser', async () => {
  const f = fixture();
  try {
    await f.native.commit();
    f.reply(async url => {const data = answer(url); return {...data, scope: {...data.scope, viewerFence: 'new-search-fence'}};});
    await f.native.next('movies'); await tick();
    assert.equal(f.native.getSnapshot().search.data?.scope.viewerFence, 'new-search-fence');
    assert.deepEqual(f.native.getSnapshot().groups.map(group => group.items.map(entry => entry.id)), [['movies-1'], ['shows-1']]);
    assert.equal(f.paths.at(-1)!.searchParams.get('group'), null, 'the canonical All selection owns the fresh projection');
  } finally {f.native.dispose();}
});

test('an HTTP-successful failed group retains the arrived projection and retries its exact offered cursor', async () => {
  const f = fixture();
  try {
    await f.native.commit(); let failed = true;
    f.reply(async url => {
      const data = answer(url);
      return failed && url.searchParams.has('cursor') ? {...data, groups: data.groups.map(group => ({...group, status: 'error', errorCode: 'search_group_timeout', items: [], nextCursor: ''}))} : data;
    });
    await f.native.next('movies');
    assert.deepEqual(f.native.getSnapshot().groups[0]!.items.map(entry => entry.id), ['movies-1']);
    assert.equal(f.native.getSnapshot().groups[0]!.nextCursor, 'movies-next');
    assert.equal(f.native.getSnapshot().paging?.movies?.error?.code, 'search_group_timeout');
    f.native.pause(); f.native.resume();
    assert.equal(f.native.getSnapshot().paging?.movies?.error?.code, 'search_group_timeout', 'hidden route retains the actual group failure, not a fake ready tail');
    failed = false; await f.native.retryGroup('movies');
    assert.deepEqual(f.native.getSnapshot().groups[0]!.items.map(entry => entry.id), ['movies-1', 'movies-2']);
    assert.equal(f.native.getSnapshot().paging?.movies?.error, null);
    assert.equal(f.paths.at(-1)!.searchParams.get('cursor'), 'movies-next');
  } finally {f.native.dispose();}
});

test('pausing an interrupted group-error retry preserves the last genuinely arrived failure', async () => {
  const f = fixture(); let resolve!: (value: unknown) => void;
  try {
    await f.native.commit();
    f.reply(async url => {const data = answer(url); return {...data, groups: data.groups.map(group => ({...group, status: 'error', errorCode: 'search_group_timeout', items: [], nextCursor: ''}))};});
    await f.native.next('movies');
    f.reply(() => new Promise(done => {resolve = done;}));
    const pending = f.native.retryGroup('movies'); await tick(); const pendingURL = f.paths.at(-1)!;
    f.native.pause(); f.native.resume();
    assert.equal(f.native.getSnapshot().paging?.movies?.error?.code, 'search_group_timeout');
    resolve(answer(pendingURL)); await pending; assert.equal(f.native.getSnapshot().groups[0]!.items.length, 1, 'late canceled success does not replace the retained read');
  } finally {f.native.dispose();}
});

test('an authoritative smaller count causes a canonical reread instead of retaining impossible membership or guessing survivors', async () => {
  const f = fixture(); let shrunk = false;
  try {
    f.reply(async url => {
      const data = answer(url), cursor = url.searchParams.get('cursor');
      return {...data, groups: data.groups.map(group => group.id !== 'movies' ? group : cursor === 'movies-next'
        ? {...group, totalCount: 3, nextCursor: 'movies-last'} : cursor === 'movies-last'
          ? {...group, totalCount: 1, items: [], nextCursor: ''} : shrunk
            ? {...group, totalCount: 1, nextCursor: '', items: [{...group.items[0]!, id: 'actual-survivor', navigation: {view: 'item', entityId: 'actual-survivor'}}]} : {...group, totalCount: 3})};
    });
    await f.native.commit(); await f.native.next('movies');
    assert.equal(f.native.getSnapshot().groups[0]!.items.length, 2);
    shrunk = true; await f.native.next('movies'); await tick();
    assert.deepEqual(f.native.getSnapshot().groups[0]!.items.map(entry => entry.id), ['actual-survivor']);
    assert.equal(f.paths.at(-1)!.searchParams.get('group'), null);
    assert.equal(f.paths.at(-1)!.searchParams.get('cursor'), null);
  } finally {f.native.dispose();}
});
