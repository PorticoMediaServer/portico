import test from 'node:test';
import assert from 'node:assert/strict';
import { LibraryContentService, contentPageQuery, libraryContentPages, validateContentEntry, type ContentRoute, type LibraryContentApi } from '../src/library-content.ts';
import {serverPath} from '../src/query/server-api.ts';
import type {HomeRow, Item} from '../../contracts/src/next/api.ts';
import {withQueries} from './fixtures/viewer-queries.ts';
const scope = { viewerId: JSON.stringify(['hosted','account','profile']), serverId: 'server' };
const route: ContentRoute = { libraryId: 'movies', view: 'discover' };
const libraryPrefix = serverPath('GET /v1/libraries') + '/';
type LandingItem = Pick<Item, 'id' | 'kind' | 'title' | 'summary' | 'tags'>;
function item(id = 'item'): LandingItem { return {id, kind: 'movie', title: `Server title ${id}`}; }
function shelf(id = 'titles', items = [item()], nextCursor = '', total = items.length) {
  return {id, title: 'Server-selected heading', shape: 'poster', items, total, nextCursor} satisfies Omit<HomeRow, 'items'> & {items: LandingItem[]};
}
function discoverPage(path: string, rows = [shelf()]) {
  return new URL(path, 'https://server.test').searchParams.has('cursor') ? rows[0] : {rows};
}
function fixtureApi(read: (path: string, signal?: AbortSignal) => Promise<unknown>): LibraryContentApi {
  return { request: async <T>(path: string, _method?: string, _body?: unknown, signal?: AbortSignal): Promise<T> => {
    const libraryId = path.slice(libraryPrefix.length);
    if (path.startsWith(libraryPrefix) && !libraryId.includes('/')) return {id: decodeURIComponent(libraryId), kind: 'movie', name: 'Named Library'} as T;
    return await read(path, signal) as T;
  } };
}
function service(read: (path: string, signal?: AbortSignal) => Promise<unknown>, timeoutMs = 1000) {
  return new LibraryContentService(withQueries({api: fixtureApi(read), scope, timeoutMs}));
}
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(r => { resolve = r; }); return { resolve, promise }; }

test('an unknown card kind drops only that entry and retains the section total', async () => {
  const future = {...item('future'), kind: 'interactive'};
  const nav = service(async () => ({rows: [shelf('personal', [item('first'), future, item('last')], 'next', 7)]}));
  await nav.select(route);
  assert.equal(nav.getSnapshot().phase, 'ready');
  assert.deepEqual(nav.getSnapshot().sections[0].entries.map(card => card.id), ['first', 'last']);
  assert.equal(nav.getSnapshot().sections[0].totalCount, 7);
  assert.equal(nav.getSnapshot().sections[0].nextCursor, 'personal~next');
  nav.dispose();
});

test('Discover keeps full rails within their display bound', async () => {
  const rail = (id: string, length: number) => shelf(id, Array.from({length}, (_, i) => item(`${id}-${i}`)));
  const nav = service(async () => ({rows: [rail('artists', 40), rail('recent', 13), rail('long', 101)]}));
  await nav.select(route);
  assert.equal(nav.getSnapshot().phase, 'ready');
  assert.deepEqual(nav.getSnapshot().sections.map(section => [section.id, section.entries.length]), [['artists', 40], ['recent', 13], ['long', 100]]);
  nav.dispose();
});

test('catalog entry retains every valid genre beyond the former preview size', async () => {
  const genres = Array.from({length: 25}, (_, i) => `Genre ${i}`);
  const nav = service(async () => ({rows: [shelf('titles', [{...item(), tags: genres.map(name => ({kind: 'genre', id: name, name}))}])]}));
  await nav.select(route);
  assert.deepEqual(nav.getSnapshot().projection!.sections[0].entries[0].genres, genres);
  nav.dispose();
});

test('provider synopsis line breaks survive the library projection boundary', async () => {
  for (const overview of ['An office.\r\nA second paragraph.', 'A first line.\n\tAn indented line.', 'x'.repeat(16384)]) {
    const nav = service(async () => ({rows: [shelf('titles', [{...item('show'), kind: 'show', title: 'The Office', summary: overview}])]}));
    await nav.select(route);
    assert.equal(nav.getSnapshot().phase, 'ready');
    assert.equal(nav.getSnapshot().sections[0].entries[0].overview, overview);
    nav.dispose();
  }
});

test('the shared card parser keeps valid synopsis line breaks and drops unsafe text', () => {
  for (const overview of ['Unsafe\u0000text', 'Unsafe\u000btext', 'Unsafe\u007ftext', 'x'.repeat(16385), 42]) {
    assert.equal(validateContentEntry({...item(), overview}).overview, undefined);
  }
  const row = validateContentEntry({...item(), overview: 'Valid\ntext', posterUrl: 'https://example.test/\nimage'});
  assert.deepEqual([row.overview, row.posterUrl], ['Valid\ntext', undefined]);
  assert.throws(() => validateContentEntry({...item(), overview: 'Valid\r\ntext', title: 'Invalid\nname'}), {code: 'invalid_projection'});
});

test('publishes rebuilt rows in server order and freezes their entries', async () => {
  const first = shelf('first', [item('title-9'), item('title-1')]);
  const nav = service(async () => ({rows: [first, shelf('second')]}));
  await nav.select(route);
  const state = nav.getSnapshot();
  assert.equal(state.phase, 'ready');
  assert.deepEqual(state.sections.map(section => section.id), ['first', 'second']);
  assert.deepEqual(state.sections[0].entries.map(entry => entry.id), ['title-9', 'title-1']);
  assert.equal(state.sections[0].heading.fallback, first.title);
  assert.ok(Object.isFrozen(state.sections[0].entries));
  nav.dispose();
});

test('Discover reads an encoded library ID and sends an opaque row cursor unchanged', async () => {
  const paths: string[] = [], libraryId = 'library/a', cursor = 'a&b? c';
  const nav = service(async path => {
    paths.push(path);
    return discoverPage(path, [shelf('titles', [item()], cursor)]);
  });
  await nav.select({...route, libraryId});
  assert.equal(nav.getSnapshot().phase, 'ready');
  assert.equal(paths[0], serverPath('GET /v1/libraries/{id}/landing', {id: libraryId}));
  await nav.next('titles');
  const next = new URL(paths[1], 'https://server.test');
  assert.equal(next.pathname, serverPath('GET /v1/home/rows/{id}', {id: 'titles'}));
  assert.equal(next.searchParams.get('library'), libraryId);
  assert.equal(next.searchParams.get('cursor'), cursor);
  assert.equal(next.searchParams.get('limit'), '40');
  nav.dispose();
});

test('rapid library switch aborts and fences a late response even if transport ignores abort', async () => {
  const old = deferred<unknown>(); let oldSignal: AbortSignal | undefined;
  const nav = service(async (path, signal) => { if (path.includes('/old/')) { oldSignal = signal; return old.promise; } return discoverPage(path); });
  const pending = nav.select({ libraryId: 'old', view: 'discover' });
  await nav.select(route); await pending;
  assert.equal(oldSignal?.aborted, true);
  old.resolve({rows: [shelf()]}); await Promise.resolve();
  assert.equal(nav.getSnapshot().projection!.scope.libraryId, 'movies');
});

test('same in-flight route is deduplicated without client entry membership changes', async () => {
  const result = deferred<unknown>(); let calls = 0;
  const nav = service(async () => { calls++; return result.promise; });
  const first = nav.select(route), second = nav.select(route);
  assert.equal(first, second); assert.equal(calls, 1);
  result.resolve({rows: [shelf()]}); await first;
  assert.equal(nav.getSnapshot().phase, 'ready');
});

test('a library is read only from its own server\'s viewer cache', () => {
  const other = withQueries({ api: fixtureApi(async () => ({rows: [shelf()]})), scope: { serverId: 'other-server', viewerId: 'viewer' } });
  assert.throws(() => new LibraryContentService({ queries: other.queries, scope }), /own server/);
});

test('one bounded page replaces previous page and continuation addresses selected section only', async () => {
  const paths: string[] = [];
  const nav = service(async p => {
    paths.push(p); const u = new URL(p, 'https://server.test'); const cursor = u.searchParams.get('cursor');
    return discoverPage(p, [shelf('titles', [item(cursor ?? 'first')], cursor ? '' : 'signed-cursor', 10)]);
  });
  await nav.select(route); await nav.next('titles');
  assert.equal(nav.getSnapshot().sections[0].entries.length, 1); assert.equal(nav.getSnapshot().sections[0].entries[0].id, 'signed-cursor');
  assert.equal(nav.getSnapshot().pagination.canPrevious, true);
  await nav.previous(); assert.equal(nav.getSnapshot().sections[0].entries[0].id, 'first');
  assert.equal(nav.getSnapshot().pagination.canPrevious, false);
  await nav.select({ libraryId: 'new', view: 'discover' });
  assert.equal(new URL(paths.at(-1)!, 'https://server.test').searchParams.has('cursor'), false);
});

test('a continuation the server no longer honours restarts the list from its first page without a refresh state', async () => {
  for (const code of ['stale_continuation', 'invalid_cursor']) {
    const paths: string[] = []; const phases: string[] = [];
    const nav = service(async path => {
      paths.push(path);
      if (new URL(path, 'https://server.test').searchParams.has('cursor')) throw Object.assign(new Error('changed'), { code, status: code === 'invalid_cursor' ? 400 : 409, retryable: true });
      return discoverPage(path, [shelf('titles', [item()], 'old')]);
    });
    await nav.select(route);
    nav.subscribe(() => phases.push(nav.getSnapshot().phase + ':' + nav.getSnapshot().sections.length));
    await nav.next('titles');
    // The person never sees "the library changed": what was shown stays until the first page is back.
    assert.equal(nav.getSnapshot().phase, 'ready', code); assert.equal(nav.getSnapshot().error, null);
    assert.ok(!phases.some(p => p.startsWith('refresh-required') || p.startsWith('error')), phases.join());
    assert.ok(phases.every(p => !p.endsWith(':0')), 'the list was never emptied');
    assert.equal(paths.length, 3); assert.ok(paths[1].includes('cursor=old')); assert.ok(!paths[2].includes('cursor='));
    assert.equal(nav.getSnapshot().pagination.cursor, null);
  }
});

test('the original continuation promise stays pending until its recovered head finishes', async () => {
  const head = deferred<{rows: ReturnType<typeof shelf>[]}>();
  let calls = 0, settled = false;
  const nav = service(async path => {
    calls++;
    if (path.includes('cursor=')) throw Object.assign(new Error('changed'), {code: 'stale_continuation', status: 409});
    if (calls > 1) return head.promise;
    return discoverPage(path, [shelf('titles', [item()], 'old')]);
  });
  try {
    await nav.select(route);
    const next = nav.next('titles').then(() => {settled = true;});
    for (let turn = 0; turn < 20 && calls < 3; turn++) await Promise.resolve();
    assert.equal(calls, 3, 'the same continuation started its real replacement head');
    await Promise.resolve();
    assert.equal(settled, false, 'next() does not report completion while its own head is pending');
    assert.equal(nav.getSnapshot().phase, 'loading');
    head.resolve({rows: [shelf()]});
    await next;
    assert.equal(settled, true);
    assert.equal(nav.getSnapshot().phase, 'ready');
    assert.equal(nav.getSnapshot().pagination.cursor, null);
  } finally {nav.dispose(); head.resolve({rows: [shelf()]});}
});

test('a continuation recovery cannot replace a successor opened while it reads the first page again', async () => {
  const paths: string[] = [];
  let recovery: ReturnType<typeof deferred<unknown>> | undefined;
  const nav = service(async path => {
    paths.push(path);
    if (path.includes('cursor=')) throw Object.assign(new Error('changed'), {code: 'stale_continuation', status: 409});
    if (path === serverPath('GET /v1/libraries/{id}/landing', {id: route.libraryId}) && paths.some(p => p.includes('cursor='))) { recovery = deferred<unknown>(); return recovery.promise; }
    return discoverPage(path, [shelf('titles', [item()], 'old')]);
  });
  try {
    await nav.select(route);
    const more = nav.next('titles');
    for (let i = 0; i < 20 && !recovery; i++) await new Promise(resolve => setImmediate(resolve));
    assert.ok(recovery, 'the refused cursor restarts the first page');
    const successor = nav.select({libraryId: 'other', view: 'discover'});
    recovery!.resolve({rows: [shelf()]});
    await Promise.allSettled([more, successor]);
    assert.equal(nav.getSnapshot().phase, 'ready');
    assert.equal(nav.getSnapshot().route?.libraryId, 'other', 'the recovered head never replaces the successor');
  } finally {nav.dispose();}
});

test('unreadable and repeated landing rows are left out while readable rows stay', async () => {
  const nav = service(async () => ({rows: [shelf('first', [item('a'), item('a'), item('b')], '', 7),
    shelf('first', [item('duplicate')]), shelf(''), {id: 'missing-items'}, shelf('last')]}));
  await nav.select(route);
  assert.equal(nav.getSnapshot().phase, 'ready');
  const sections = nav.getSnapshot().sections;
  assert.deepEqual(sections.map(section => section.id), ['first', 'last']);
  assert.deepEqual(sections[0].entries.map(entry => entry.id), ['a', 'b']);
  assert.equal(sections[0].totalCount, 7);
  nav.dispose();
});

test('timeout and cancel settle locally even with permanently hung transport', async () => {
  const nav = service(() => new Promise(() => {}), 5); await nav.select(route);
  assert.equal(nav.getSnapshot().phase, 'error'); assert.equal(nav.getSnapshot().error!.code, 'timeout'); assert.equal(nav.getSnapshot().error!.retryable, true);
  const cancelled = service(() => new Promise(() => {}), 10000); const pending = cancelled.select(route); cancelled.cancel(); await pending;
  assert.equal(cancelled.getSnapshot().phase, 'idle'); assert.equal(cancelled.getSnapshot().projection, null);
});

test('permission failure clears rows immediately and remains an explicit server error', async () => {
  let deny = false;
  const nav = service(async p => { if (deny) throw Object.assign(new Error('Permission revoked'), { code: 'permission_denied', status: 403, retryable: false }); return discoverPage(p); });
  await nav.select(route); deny = true; const pending = nav.refresh();
  assert.equal(nav.getSnapshot().phase, 'loading'); await pending;
  assert.equal(nav.getSnapshot().error!.code, 'permission_denied'); assert.equal(nav.getSnapshot().projection, null);
});

test('continuation history is bounded and a cyclic cursor ends the section', async () => {
  let page = 0;
  const nav = service(async p => { return discoverPage(p, [shelf('titles', [item()], `page-${++page}`)]); });
  await nav.select(route); for (let i = 0; i < 80; i++) await nav.next('titles');
  let backs = 0; while (nav.getSnapshot().pagination.canPrevious) { await nav.previous(); backs++; }
  assert.equal(backs, 63);
  // A cursor that comes back again is the end of that section, never a loop and never a refresh state.
  const cycle = service(async p => { return discoverPage(p, [shelf('titles', [item()], 'same')]); });
  await cycle.select(route); await cycle.next('titles'); assert.equal(cycle.getSnapshot().phase, 'ready');
  assert.equal(cycle.getSnapshot().sections[0].nextCursor, ''); assert.deepEqual(cycle.getSnapshot().pagination.next, []);
});

test('invalid requests fail before transport and disposal fences late results', async () => {
  let calls = 0; const nav = service(async p => { calls++; return discoverPage(p); });
  assert.throws(() => nav.select({ libraryId: 'movies', view: 'item' } as any));
  assert.throws(() => nav.select({ libraryId: 'movies', view: 'show' }));
  assert.throws(() => nav.select(route, { cursor: 'x'.repeat(4097) })); assert.equal(calls, 0);
  const late = deferred<unknown>(); const disposed = service(async () => late.promise);
  const pending = disposed.select(route); const snapshot = disposed.getSnapshot(); disposed.dispose(); late.resolve({rows: [shelf()]}); await pending;
  assert.equal(disposed.getSnapshot(), snapshot); assert.throws(() => disposed.refresh(), /disposed/);
});

test('an unrebuilt view fails with not_found without sending a request', async () => {
  const paths: string[] = [];
  const api = {request: async <T>(path: string): Promise<T> => {
    paths.push(path);
    throw new Error('Unexpected request');
  }};
  const unrebuilt: ContentRoute = {libraryId: route.libraryId, view: 'browse'};
  const nav = new LibraryContentService(withQueries({api, scope}));
  try {
    assert.throws(() => libraryContentPages({api, scope, route: unrebuilt}), {code: 'not_found'});
    await nav.select(unrebuilt);
    assert.equal(nav.getSnapshot().phase, 'error');
    assert.equal(nav.getSnapshot().error?.code, 'not_found');
    assert.equal(nav.getSnapshot().error?.retryable, false);
    assert.deepEqual(paths, []);
  } finally {nav.dispose();}
});

test('Home is read through the composed Home API, not the library content service',()=>{
 const nav=service(async path=>discoverPage(path));
 assert.throws(()=>nav.select({view:'home'} as any),/Invalid library content route/);
 assert.throws(()=>nav.select({libraryId:'movies',view:'home'} as any),/Invalid library content route/);
});

test('same-destination refresh retains bounded content until replacement; another destination clears it', async () => {
  const response = deferred<unknown>(); let count = 0;
  const nav = service(async path => ++count === 1 ? discoverPage(path) : response.promise);
  await nav.select(route);
  const original = nav.getSnapshot().projection;
  const refresh = nav.refresh();
  assert.equal(nav.getSnapshot().phase, 'loading');
  assert.equal(nav.getSnapshot().projection, original);
  // Another destination never shows this one's rows while it reads.
  void nav.select({ ...route, libraryId: 'other' }).catch(() => {});
  assert.equal(nav.getSnapshot().projection, null);
  response.resolve({rows: [shelf()]});
  await refresh;
  assert.equal(nav.getSnapshot().projection, null);
});

test('temporary refresh failure retains content but invalid and denied responses do not', async () => {
  for (const failure of [Object.assign(new Error('Unavailable'), {status:503}), new TypeError('Network failed'), Object.assign(new Error('Denied'), {status:403}), Object.assign(new Error('Invalid'), {code:'invalid_projection'})]) {
    let fail = false;
    const nav = service(async path => {if(fail)throw failure;return discoverPage(path);});
    await nav.select(route); fail = true;
    await nav.refresh();
    assert.equal(nav.getSnapshot().phase,'error');
    assert.equal(nav.getSnapshot().sections.length, failure instanceof TypeError || 'status' in failure && (failure as { status: unknown }).status===503 ? 1 : 0);
    nav.dispose();
  }
});

test('album artist links, release roles and category mosaics remain optional on shared cards', () => {
  const album = {id: 'album-1', kind: 'album', title: 'Blue', year: 1971};
  assert.deepEqual(validateContentEntry({...album, artist: {id: 'artist-1', name: 'Joni Mitchell'}}).artist,
    {id: 'artist-1', name: 'Joni Mitchell'});
  assert.equal(validateContentEntry({...album, artist: {id: '', name: ''}}).artist, undefined);
  const roles = ['album', 'appearance', 'ep', 'single', 'compilation', 'bootleg', 42];
  assert.deepEqual(roles.map(role => validateContentEntry({...album, role}).role),
    ['album', 'appearance', 'ep', 'single', 'compilation', undefined, undefined]);
  const category = {id: 'genre:Rock', kind: 'category', title: 'Rock'};
  assert.deepEqual(validateContentEntry({...category, artworkPaths: [null, '/a', '/b', '/c', '/d', '/e']}).artworkPaths,
    ['/a', '/b', '/c', '/d']);
  assert.equal(validateContentEntry({...category, artworkPaths: 'not-an-array'}).artworkPaths, undefined);
});

test('a quiet read replaces the first page in place with no loading state, and leaves a failed read invisible', async () => {
  let ids = ['one', 'two'], fail = false;
  const nav = service(async path => { if (fail) throw Object.assign(new Error('down'), { status: 503 }); return discoverPage(path, [shelf('titles', ids.map(item))]); });
  await nav.select(route);
  const phases: string[] = []; nav.subscribe(() => phases.push(nav.getSnapshot().phase));
  ids = ['new', 'one', 'two'];
  await quietRead(nav);
  assert.deepEqual(nav.getSnapshot().sections[0].entries.map(e => e.id), ['new', 'one', 'two']);
  assert.deepEqual(phases, ['ready'], 'never loading, never empty');
  assert.equal(nav.getSnapshot().head!.serial, 1);
  const shown = nav.getSnapshot();
  fail = true;
  await assert.rejects(quietRead(nav), { status: 503 });
  assert.equal(nav.getSnapshot(), shown);
  // Waiting for a pause in the person's input before an answer is shown is the hub's, for every cached document (viewer-queries.test.ts).
});

test('a quiet read while on a later page keeps that page and publishes the fresh first page as head', async () => {
  let firstPage = 'page-one';
  const nav = service(async path => { const later = path.includes('cursor='); return discoverPage(path, [shelf('titles', [item(later ? 'page-two' : firstPage)], later ? '' : 'next')]); });
  await nav.select(route); await nav.next('titles');
  assert.equal(nav.getSnapshot().sections[0].entries[0].id, 'page-two');
  // A new title arrives at the front of the library: the hub reads the first page again.
  firstPage = 'page-one-new';
  await quietRead(nav);
  assert.equal(nav.getSnapshot().sections[0].entries[0].id, 'page-two', 'the page the service is on is untouched');
  assert.equal(nav.getSnapshot().pagination.cursor, 'titles~next');
  assert.equal(nav.getSnapshot().head!.sections[0].entries[0].id, 'page-one-new');
  // A title that is gone, or access that was removed, becomes the screen's own state through an ordinary load.
  let isGone = false;
  const gone = service(async path => { if (isGone) throw Object.assign(new Error('gone'), { status: 404, code: 'not_found', retryable: false }); return discoverPage(path); });
  await gone.select(route);
  isGone = true;
  await quietRead(gone).catch(() => {});
  await new Promise(resolve => setTimeout(resolve, 10));
  assert.equal(gone.getSnapshot().phase, 'error'); assert.equal(gone.getSnapshot().error!.code, 'not_found');
});

test('everything read so far is one list per section: later pages append, More works on any section, and a quiet read merges in place', async () => {
  const row = (id: string, ids: string[], nextCursor = '') => shelf(id, ids.map(item), nextCursor, 9);
  let first = [row('a', ['a1', 'a2'], 'a-next'), row('b', ['b1', 'b2'], 'b-next')];
  const nav = service(async path => {
    const cursor = new URL(path, 'https://server.test').searchParams.get('cursor');
    if (cursor === 'a-next') return row('a', ['a3', 'a2']);
    if (cursor === 'b-next') return row('b', ['b3']);
    return {rows: first};
  });
  await nav.select(route);
  await nav.next('a');
  const ids = () => nav.getSnapshot().appended.map(s => s.entries.map(e => e.id).join(','));
  assert.deepEqual(ids(), ['a1,a2,a3', 'b1,b2'], 'the page is appended to its own section, a repeated entry is dropped, the other section stays');
  // The service is on a page of section a; More on section b still works.
  await nav.next('b');
  assert.deepEqual(ids(), ['a1,a2,a3', 'b1,b2,b3']);
  const untouched = nav.getSnapshot().appended[1];
  // A new title arrives at the front of section a while the screen is open.
  first = [row('a', ['new', 'a1'], 'a-next-2'), row('b', ['b1', 'b2'], 'b-next')];
  await quietRead(nav);
  assert.deepEqual(ids(), ['new,a1,a2,a3', 'b1,b2,b3'], 'the fresh first page leads; what was scrolled into stays');
  assert.equal(nav.getSnapshot().appended[1], untouched, 'a section that did not change is the same object');
  assert.equal(nav.getSnapshot().appended[0].nextCursor, '', 'the section continues from where the person was (its end)');
});

test('refresh-required policy keeps committed rows for an explicit current retry',async()=>{
 let calls=0; const ctx={signal:new AbortController().signal,isCurrent:()=>true};
 const service=new LibraryContentService(withQueries({scope,continuationRecovery:'refresh-required',api:fixtureApi(async (path:string)=>{calls++; if(new URL(path,'https://x').searchParams.get('cursor'))throw Object.assign(new Error('stale'),{code:'stale_continuation'}); return discoverPage(path, [shelf('titles', [item()], 'next')]);})}));
 await service.select(route,{context:ctx});
 assert.equal(calls,1);
 await service.next('titles',ctx);
 assert.equal(calls,2,'the refused continuation is not retried as page one');
 assert.equal(service.getSnapshot().phase,'refresh-required');
 assert.equal(service.getSnapshot().error?.code,'stale_continuation');
 assert.equal(service.getSnapshot().sections[0]!.entries[0]!.id,'item','committed rows stay visible');
 assert.deepEqual(service.getSnapshot().pagination.next,[]);
 await service.retry(ctx);
 assert.equal(calls,3,'explicit current retry starts page one');
 assert.equal(service.getSnapshot().phase,'ready');
 service.dispose();
});

test('an original-opening abort retires only its flight and restores the committed baseline',async()=>{
 const initial=deferred<unknown>(); const late=deferred<unknown>(); let calls=0, signal:AbortSignal|undefined;
 const service=new LibraryContentService(withQueries({scope,api:fixtureApi(async (_path, s) => {calls++;signal=s;return calls===1?initial.promise:late.promise;})}));
 const first=service.select(route); initial.resolve({rows: [shelf('titles', [item()], 'next')]}); await first;
 const controller=new AbortController(); const ctx={signal:controller.signal,isCurrent:()=>true};
 const pending=service.next('titles',ctx); await new Promise(r=>setImmediate(r));
 controller.abort();
 await pending;
 assert.equal(signal?.aborted,true,'the HTTP controller is aborted');
 assert.equal(service.getSnapshot().phase,'ready');
 assert.equal(service.getSnapshot().sections[0]!.entries[0]!.id,'item');
 assert.equal(service.getSnapshot().error,null);
 late.resolve(shelf('titles'));
 await new Promise(r=>setImmediate(r));
 assert.equal(service.getSnapshot().sections[0]!.entries[0]!.id,'item','a late success cannot publish');
 service.dispose();
});

test('same key with a new opening context is isolated; duplicate same opening coalesces',async()=>{
 const reads:ReturnType<typeof deferred<unknown>>[]=[]; let calls=0;
 const service=new LibraryContentService(withQueries({scope,api:fixtureApi(async ()=>{calls++;const d=deferred<unknown>();reads.push(d);return d.promise;})}));
 const c1={signal:new AbortController().signal,isCurrent:()=>true},c2={signal:new AbortController().signal,isCurrent:()=>true};
 const a=service.select(route,{context:c1}), b=service.select(route,{context:c1});
 assert.strictEqual(a,b,'the same opening and key coalesce');
 assert.equal(calls,1);
 const c=service.select(route,{context:c2});
 assert.notStrictEqual(c,a,'a new opening does not inherit the old promise');
 assert.equal(calls,2);
 reads[0]!.resolve({rows: [shelf('titles', [item('old')])]});
 reads[1]!.resolve({rows: [shelf('titles', [item('new')])]});
 await Promise.allSettled([a,c]);
 assert.equal(service.getSnapshot().sections[0]!.entries[0]!.id,'new');
 service.dispose();
});

test('a synchronous loading subscriber can coalesce the same opening without recursion',async()=>{
 let calls=0, nested:Promise<void>|undefined;
 const ctx={signal:new AbortController().signal,isCurrent:()=>true};
 const nav=new LibraryContentService(withQueries({scope,api:fixtureApi(async(path:string)=>{calls++;return discoverPage(path, [shelf('titles', [item()], 'next')]);})}));
 nav.subscribe(()=>{if(nav.getSnapshot().phase==='loading'&&!nested)nested=nav.select(route,{context:ctx});});
 await nav.select(route,{context:ctx});
 if(nested)await nested;
 assert.equal(calls,1,'the nested same-opening call joins the established flight');
 assert.equal(nav.getSnapshot().phase,'ready');
 nav.dispose();
});

test('isCurrent-only retirement after a successful response restores the committed baseline',async()=>{
 const response=deferred<unknown>(); let current=true,calls=0;
 const nav=new LibraryContentService(withQueries({scope,api:fixtureApi(async(path:string)=>{calls++;return calls===1?discoverPage(path, [shelf('titles', [item()], 'next')]):response.promise;})}));
 const ctx={signal:new AbortController().signal,isCurrent:()=>current};
 await nav.select(route,{context:ctx});
 const pending=nav.next('titles',ctx);
 current=false;
 response.resolve(shelf('titles', [item('late')]));
 await pending;
 assert.equal(nav.getSnapshot().phase,'ready','the retired flight restores rather than stranding loading');
 assert.equal(nav.getSnapshot().sections[0]!.entries[0]!.id,'item');
 nav.dispose();
});

test('aborting the predecessor after a successor opening installs cannot erase it',async()=>{
 const reads:ReturnType<typeof deferred<unknown>>[]=[]; let calls=0;
 const nav=new LibraryContentService(withQueries({scope,api:fixtureApi(async()=>{calls++;const d=deferred<unknown>();reads.push(d);return d.promise;})}));
 const first=nav.select(route,{context:{signal:new AbortController().signal,isCurrent:()=>true}});
 reads[0]!.resolve({rows: [shelf()]});
 await first;
 const c1=new AbortController(), c2=new AbortController();
 // Each asks the server again (an explicit Refresh): the two openings share one read of the page.
 const older=nav.select(route,{context:{signal:c1.signal,isCurrent:()=>true},fresh:true});
 const newer=nav.select(route,{context:{signal:c2.signal,isCurrent:()=>true},fresh:true});
 assert.equal(calls,2);
 c1.abort();
 reads[1]!.resolve({rows: [shelf('titles', [item('successor')])]});
 await Promise.allSettled([older,newer]);
 assert.equal(nav.getSnapshot().phase,'ready');
 assert.equal(nav.getSnapshot().sections[0]!.entries[0]!.id,'successor');
 nav.dispose();
});

test('a reentrant currentness predicate cannot overwrite the successor it installs',async()=>{
 const reads:ReturnType<typeof deferred<unknown>>[]=[]; let reenter=false,successor:Promise<void>|undefined;
 const nav=new LibraryContentService(withQueries({scope,api:fixtureApi(async()=>{const d=deferred<unknown>();reads.push(d);return d.promise;})}));
 const ctx={signal:new AbortController().signal,isCurrent:()=>{if(reenter){reenter=false;successor=nav.select({libraryId:'other',view:'discover'});return false;}return true;}};
 const first=nav.select({libraryId:'movies',view:'discover'},{context:ctx});
 reads[0]!.resolve({rows: [shelf('titles', [item()], 'next')]});
 await first;
 const pending=nav.next('titles',ctx);
 reenter=true;
 reads[1]!.resolve(shelf('titles'));
 await pending;
 assert.ok(successor,'the predicate installed a successor');
 reads[2]!.resolve({rows: [shelf()]});
 await successor;
 assert.equal(nav.getSnapshot().route?.libraryId,'other','the successor remains current');
 assert.equal(nav.getSnapshot().phase,'ready');
 nav.dispose();
});

test('an abort listener installing a successor during cleanup is not rolled back',async()=>{
 const reads:ReturnType<typeof deferred<unknown>>[]=[];let successor:Promise<void>|undefined;
 const nav=new LibraryContentService(withQueries({scope,api:fixtureApi(async()=>{const d=deferred<unknown>();reads.push(d);return d.promise;})}));
 const first=nav.select(route,{context:{signal:new AbortController().signal,isCurrent:()=>true}});
 reads[0]!.resolve({rows: [shelf('titles', [item()], 'next')]});
 await first;
 const controller=new AbortController();
 const ctx={signal:controller.signal,isCurrent:()=>true};
 // Registered before the flight's own listener, so it runs first during abort.
 controller.signal.addEventListener('abort',()=>{successor=nav.select({libraryId:'other',view:'discover'});},{once:true});
 const pending=nav.next('titles',ctx);
 await new Promise(r=>setImmediate(r));
 controller.abort();
 assert.ok(successor,'the abort listener installed a successor');
 reads[2]!.resolve({rows: [shelf()]});
 await successor;
 await pending;
 assert.equal(nav.getSnapshot().route?.libraryId,'other');
 assert.equal(nav.getSnapshot().phase,'ready');
 nav.dispose();
});

/** What the hub does when something a route's first page shows moved: read it again through the viewer's cache. */
async function quietRead(nav: LibraryContentService): Promise<void> {
  const q = (nav as any).queries, route = nav.getSnapshot().route!;
  try { await q.client.fetchQuery({...contentPageQuery(q, route, null, (nav as any).pageSize), staleTime: 0}); }
  finally { await new Promise(resolve => setTimeout(resolve, 2)); }
}

function openingHarness(options: { continuationRecovery?: 'restart' | 'refresh-required'; timeoutMs?: number } = {}) {
 const reads: { path: string; signal: AbortSignal; resolve: (value: unknown) => void; reject: (reason: unknown) => void }[] = [];
 const api = fixtureApi((path, signal) => {
  assert.ok(signal);
  return new Promise((resolve, reject) => { reads.push({path, signal, resolve, reject}); });
 });
 const nav = new LibraryContentService(withQueries({ scope, api, ...options }));
 const context = () => ({ signal: new AbortController().signal, isCurrent: () => true });
 const complete = (index: number, id = 'item', cursor = 'next') => {
  const read = reads[index]!;
  read.resolve(discoverPage(read.path, [shelf('titles', [item(id)], cursor)]));
 };
 const seed = async () => { const pending = nav.select(route); complete(0); await pending; };
 return { nav, reads, context, complete, seed };
}

for (const trigger of ['replacement', 'retirement'] as const) {
 test(`actual HTTP abort during ${trigger} preserves the synchronously installed successor`, async () => {
  const h = openingHarness(); await h.seed();
  const opening = new AbortController();
  const old = h.nav.next('titles', { signal: opening.signal, isCurrent: () => true });
  let successor: Promise<void> | undefined;
  // This is the HTTP controller supplied to the transport, NOT the caller signal.
  h.reads[1]!.signal.addEventListener('abort', () => {
   successor = h.nav.select({ libraryId: 'other', view: 'discover' }, { context: h.context() });
  }, { once: true });
  const outer = trigger === 'replacement' ? h.nav.select(route, { context: h.context() }) : (opening.abort(), Promise.resolve());
  assert.ok(successor);
  assert.notStrictEqual(old, successor);
  assert.notStrictEqual(outer, successor, 'the retired operation does not adopt a successor completion');
  await Promise.all([old, outer]);
  assert.equal(h.reads.length, 3, 'the superseded outer request is never dispatched');
  assert.equal(h.reads[2]!.signal.aborted, false);
  assert.equal(h.nav.getSnapshot().phase, 'loading');
  h.reads[1]!.reject(new Error('ignored-abort old failure'));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(h.reads[2]!.signal.aborted, false, 'old finally cannot abort the successor');
  h.complete(2, 'successor', ''); await successor;
  assert.equal(h.nav.getSnapshot().route?.libraryId, 'other');
  assert.equal(h.nav.getSnapshot().sections[0]!.entries[0]!.id, 'successor');
  assert.equal(h.nav.getSnapshot().phase, 'ready'); h.nav.dispose();
 });

 for (const action of ['cancel', 'dispose'] as const) {
  test(`actual HTTP abort during ${trigger}: no-flight ${action} wins over the outer read`, async () => {
   const h = openingHarness(); await h.seed();
   const opening = new AbortController();
   const old = h.nav.next('titles', { signal: opening.signal, isCurrent: () => true });
     h.reads[1]!.signal.addEventListener('abort', () => {
    if (action === 'cancel') h.nav.cancel();
    else h.nav.dispose();
   }, { once: true });
   const outer = trigger === 'replacement' ? h.nav.select(route, { context: h.context() }) : (opening.abort(), Promise.resolve());
   await Promise.all([old, outer]);
   const stopped = h.nav.getSnapshot();
   assert.equal(h.reads.length, 2);
   if (action !== 'dispose') { assert.equal(stopped.phase, 'idle'); assert.equal(stopped.projection, null); }
   h.complete(1, 'late'); await new Promise(resolve => setImmediate(resolve));
   assert.strictEqual(h.nav.getSnapshot(), stopped, 'late completion cannot restore the predecessor');
   if (action === 'dispose') assert.throws(() => h.nav.refresh(), /disposed/);
   h.nav.dispose();
  });
 }
}

for (const predicateCall of [1, 2, 3]) {
 test(`TRUE admission predicate ${predicateCall} cannot overwrite a successor`, async () => {
  const h = openingHarness(); await h.seed();
  const old = h.nav.next('titles', h.context());
  let calls = 0, successor: Promise<void> | undefined;
  const incoming = { signal: new AbortController().signal, isCurrent: () => {
   if (++calls === predicateCall) successor = h.nav.select({ libraryId: 'other', view: 'discover' }, { context: h.context() });
   return true;
  } };
  const outer = h.nav.select(route, { context: incoming });
  assert.ok(successor); await Promise.all([old, outer]);
  assert.equal(h.reads.length, 3); assert.equal(h.reads[2]!.signal.aborted, false);
  assert.equal(h.nav.getSnapshot().route?.libraryId, 'other');
  h.complete(2, 'successor', ''); await successor;
  assert.equal(h.nav.getSnapshot().sections[0]!.entries[0]!.id, 'successor'); h.nav.dispose();
 });
}

test('incoming opening retired by HTTP cleanup restores its exact committed checkpoint', async () => {
 const h = openingHarness(); await h.seed();
 const baseline = h.nav.getSnapshot();
 const old = h.nav.next('titles', h.context());
 let current = true;
 const incoming = { signal: new AbortController().signal, isCurrent: () => current };
 h.reads[1]!.signal.addEventListener('abort', () => { current = false; }, { once: true });
 await assert.rejects(h.nav.select(route, { context: incoming }), /retired/); await old;
 assert.equal(h.reads.length, 2); assert.equal(h.nav.getSnapshot().phase, 'ready');
 assert.strictEqual(h.nav.getSnapshot().projection, baseline.projection);
 assert.strictEqual(h.nav.getSnapshot().appended, baseline.appended);
 assert.deepEqual(h.nav.getSnapshot().pagination, baseline.pagination);
 assert.ok(h.nav.getSnapshot().generation > baseline.generation);
 const next = h.nav.next('titles', h.context()); assert.equal(h.reads.length, 3);
 assert.equal(new URL(h.reads[2]!.path, 'https://x').searchParams.get('cursor'), 'next');
 h.complete(2, 'next-page', ''); await next; h.nav.dispose();
});

// A next page under another viewer fence is refused by the server (its cursor is bound to the fence): `invalid_cursor`.
for (const refusal of ['stale_continuation', 'invalid_cursor'] as const) {
 test(`explicit continuation policy retains metadata and cursor for ${refusal} until current Refresh`, async () => {
  const h = openingHarness({ continuationRecovery: 'refresh-required' }); await h.seed();
  const baseline = h.nav.getSnapshot(); const context = h.context();
  const next = h.nav.next('titles', context);
  h.reads[1]!.reject(Object.assign(new Error('refused'), { code: refusal }));
  await next;
  assert.equal(h.reads.length, 2); assert.equal(h.nav.getSnapshot().phase, 'refresh-required');
  assert.equal(h.nav.getSnapshot().error?.code, refusal);
  for (const key of ['projection', 'sections', 'appended', 'head'] as const) assert.strictEqual(h.nav.getSnapshot()[key], baseline[key]);
  assert.equal(h.nav.getSnapshot().sections[0]!.nextCursor, 'titles~next');
  assert.deepEqual(h.nav.getSnapshot().pagination.next, []); assert.equal(h.nav.getSnapshot().pagination.canPrevious, false);
  await h.nav.next('titles', context); assert.equal(h.reads.length, 2);
  const refreshed = h.nav.retry(context); assert.equal(h.reads.length, 3);
  assert.equal(new URL(h.reads[2]!.path, 'https://x').searchParams.has('cursor'), false);
  h.complete(2, 'refreshed', ''); await refreshed;
  assert.equal(h.nav.getSnapshot().appended[0]!.entries.length, 1);
  assert.equal(h.nav.getSnapshot().appended[0]!.entries[0]!.id, 'refreshed'); h.nav.dispose();
 });
}

test('already-retired openings do not touch an existing flight and disposed guards remain enforced', async () => {
 const h = openingHarness(); const pending = h.nav.select(route); const loading = h.nav.getSnapshot();
 const controller = new AbortController(); controller.abort(); let predicates = 0;
 const dead = { signal: controller.signal, isCurrent: () => { predicates++; return true; } };
 for (const context of [dead, { signal: new AbortController().signal, isCurrent: () => false }]) {
  for (const read of [() => h.nav.select(route, { context }), () => h.nav.refresh(context), () => h.nav.retry(context), () => h.nav.next('titles', context), () => h.nav.previous(context)]) {
   await assert.rejects(read(), /retired/); assert.strictEqual(h.nav.getSnapshot(), loading); assert.equal(h.reads[0]!.signal.aborted, false);
  }
 }
 assert.equal(predicates, 0); assert.equal(h.reads.length, 1); h.complete(0); await pending;
 h.nav.dispose(); assert.throws(() => h.nav.select(route, { context: dead }), /disposed/);
});

for (const stage of ['registration', 'loading'] as const) {
 test(`original abort during ${stage} settles without dispatch or stranded loading`, async () => {
  const h = openingHarness(); await h.seed(); const baseline = h.nav.getSnapshot();
  const controller = new AbortController();
  if (stage === 'registration') {
   const original = controller.signal.addEventListener.bind(controller.signal);
   controller.signal.addEventListener = ((...args: Parameters<AbortSignal['addEventListener']>) => { original(...args); controller.abort(); }) as AbortSignal['addEventListener'];
  } else h.nav.subscribe(() => { if (h.nav.getSnapshot().phase === 'loading') controller.abort(); });
  await h.nav.next('titles', { signal: controller.signal, isCurrent: () => true });
  assert.equal(h.reads.length, 1); assert.equal(h.nav.getSnapshot().phase, 'ready');
  assert.strictEqual(h.nav.getSnapshot().projection, baseline.projection); h.nav.dispose();
 });
}

test('a committed ready publication cannot be rolled back by its opening abort', async () => {
 const h = openingHarness(); const controller = new AbortController();
 h.nav.subscribe(() => { if (h.nav.getSnapshot().phase === 'ready') controller.abort(); });
 const pending = h.nav.select(route, { context: { signal: controller.signal, isCurrent: () => true } });
 h.complete(0, 'committed', ''); await pending;
 assert.equal(h.nav.getSnapshot().phase, 'ready'); assert.equal(h.nav.getSnapshot().sections[0]!.entries[0]!.id, 'committed'); h.nav.dispose();
});

test('retired Refresh and late error preserve refresh-required cache without a hidden read', async () => {
 const h = openingHarness({ continuationRecovery: 'refresh-required' }); await h.seed();
 const next = h.nav.next('titles', h.context()); h.reads[1]!.reject(Object.assign(new Error('stale'), { code: 'stale_continuation' })); await next;
 const baseline = h.nav.getSnapshot(); const controller = new AbortController();
 const refreshed = h.nav.refresh({ signal: controller.signal, isCurrent: () => true }); controller.abort(); await refreshed;
 assert.equal(h.nav.getSnapshot().phase, 'refresh-required'); assert.strictEqual(h.nav.getSnapshot().appended, baseline.appended);
 h.reads[2]!.reject(new Error('late refresh error')); await new Promise(resolve => setImmediate(resolve));
 assert.equal(h.reads.length, 3); assert.equal(h.nav.getSnapshot().phase, 'refresh-required');
 assert.strictEqual(h.nav.getSnapshot().error, baseline.error); h.nav.dispose();
});

test('successful explicit Refresh replaces a prior quiet head with its new first-page metadata', async () => {
 const h = openingHarness({ continuationRecovery: 'refresh-required' }); await h.seed();
 const more = h.nav.next('titles', h.context()); h.complete(1, 'second-page', 'tail'); await more;
 const quiet = quietRead(h.nav); await new Promise(resolve => setImmediate(resolve)); h.complete(2, 'quiet-head', 'next'); await quiet;
 const head = h.nav.getSnapshot().head; assert.ok(head);
 const stale = h.nav.next('titles', h.context());
 h.reads[3]!.reject(Object.assign(new Error('stale'), { code: 'stale_continuation' })); await stale;
 assert.strictEqual(h.nav.getSnapshot().head, head, 'refusal retains the committed quiet metadata');
 const refreshed = h.nav.refresh(h.context()); h.complete(4, 'fresh-first-page', ''); await refreshed;
 assert.equal(h.nav.getSnapshot().head, undefined, 'accepted page one retires prior quiet head metadata');
 assert.equal(h.nav.getSnapshot().projection?.sections[0]!.entries[0]!.id, 'fresh-first-page');
 assert.equal(h.nav.getSnapshot().appended[0]!.entries.length, 1);
 assert.equal(h.nav.getSnapshot().pagination.cursor, null); assert.equal(h.nav.getSnapshot().pagination.canPrevious, false);
 h.nav.dispose();
});

test('opening timeout remains retryable, while legacy restart carries its original opening', async () => {
 const h = openingHarness({ timeoutMs: 5 }); const pending = h.nav.select(route, { context: h.context() }); await pending;
 assert.equal(h.nav.getSnapshot().error?.code, 'timeout'); assert.equal(h.nav.getSnapshot().error?.retryable, true); h.nav.dispose();
 const restart = openingHarness(); await restart.seed(); const controller = new AbortController();
 const next = restart.nav.next('titles', { signal: controller.signal, isCurrent: () => true });
 restart.reads[1]!.reject(Object.assign(new Error('stale'), { code: 'stale_continuation' })); await new Promise(resolve => setImmediate(resolve));
 assert.equal(restart.reads.length, 3); assert.equal(new URL(restart.reads[2]!.path, 'https://x').searchParams.has('cursor'), false);
 controller.abort(); await next;
 // The page still shows this route, so its first page stays in the cache and is read to its end.
 assert.equal(restart.reads[2]!.signal.aborted, false); assert.equal(restart.nav.getSnapshot().phase, 'ready'); restart.nav.dispose();
});
