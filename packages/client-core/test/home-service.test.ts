import test from 'node:test';
import assert from 'node:assert/strict';
import {validateHomeDocument, type HomeApi} from '../src/home.ts';
import {HomeService, mergeHomeDocument} from '../src/home-service.ts';
import {HomeRowPager} from '../src/home-row-page.ts';

function entry(id: string, extra: Record<string, unknown> = {}) {
  return {id, kind: 'movie', title: `Title ${id}`, libraryId: 'movies', available: true, navigation: {view: 'item', entityId: id}, playback: {itemId: id}, ...extra};
}
function row(id: string, ids: string[], changes: Record<string, unknown> = {}): any {
  return {
    id, title: id, kind: 'recent', artworkShape: 'poster', endpoint: '/v1/home/rows/' + id, libraryId: 'movies', viewerFence: 'fence', privacySensitivity: 'catalog', policyState: 'available', priority: 100,
    required: false, hideable: true, reorderable: true, critical: false, cursorCapable: true,
    entries: ids.map(i => entry(i)), total: 50, start: 0, limit: ids.length, hasMore: true, nextCursor: 'first.' + id, revision: {catalog: 3, viewer: 1}, ...changes,
  };
}
function document(rows: any[], changes: Record<string, unknown> = {}): any {
  return {serverId: 'server', viewerFence: 'fence', rows, spotlight: [], layout: {revision: 2, rowOrder: rows.map(r => r.id), hiddenRowIds: []}, revision: {catalog: 3, viewer: 1}, generatedAt: '2026-09-16T12:00:00.000Z', ...changes};
}
const api = (handler: (path: string, method?: string, body?: any) => unknown): HomeApi => ({request: async <T>(path: string, method?: string, body?: unknown) => handler(path, method, body) as T});
const ids = (service: HomeService, rowId: string) => service.getSnapshot().document!.rows.find(r => r.id === rowId)!.entries.map(e => e.id).join(',');

test('an explicit initial window reaches both Home and deferred shelves; omission keeps the default request', async () => {
  for (const limit of [undefined, 20]) {
    const seen: string[] = [];
    const service = new HomeService(api(path => {
      seen.push(path);
      if (path === '/v1/home' + (limit ? '?limit=' + limit : '')) return document([row('recent', [], {deferred: true, limit: limit ?? 12})]);
      if (path === '/v1/home/rows/recent?limit=' + (limit ?? 12)) return row('recent', Array.from({length: limit ?? 12}, (_, i) => 'm' + i));
      throw new Error(path);
    }), {initialLimit: limit});
    await service.load();
    await new Promise(resolve => setImmediate(resolve));
    assert.deepEqual(seen, ['/v1/home' + (limit ? '?limit=' + limit : ''), '/v1/home/rows/recent?limit=' + (limit ?? 12)]);
    assert.equal(service.getSnapshot().document?.rows[0]?.entries.length, limit ?? 12);
    service.dispose();
  }
  for (const initialLimit of [0, -1, 61, 1.5, NaN, Infinity]) assert.throws(() => new HomeService(api(() => document([])), {initialLimit}), RangeError);
});

test('a quiet read updates Home in place: unchanged rows are the same objects, a new card arrives, nothing is blanked', async () => {
  let current = document([row('continue', ['a', 'b']), row('recent', ['m1', 'm2'])]);
  const service = new HomeService(api(() => JSON.parse(JSON.stringify(current))));
  await service.load();
  const before = service.getSnapshot().document!;
  const phases: string[] = [];
  service.subscribe(() => phases.push(service.getSnapshot().phase + ':' + (service.getSnapshot().document ? 'shown' : 'blank')));
  // Nothing moved: the very same document, and no listener is told anything.
  await service.revalidate();
  assert.equal(service.getSnapshot().document, before);
  assert.deepEqual(phases, []);
  // A new film was added, and the server stamps a new revision on every row.
  current = document([row('continue', ['a', 'b'], {revision: {catalog: 4, viewer: 1}}), row('recent', ['new', 'm1'], {revision: {catalog: 4, viewer: 1}})], {revision: {catalog: 4, viewer: 1}});
  await service.revalidate();
  const after = service.getSnapshot().document!;
  assert.equal(after.rows[0], before.rows[0], 'the untouched row keeps its identity, so its cards do not re-render');
  assert.equal(ids(service, 'recent'), 'new,m1', 'a refreshed revision keeps only the authoritative loaded pages');
  assert.equal(after.rows[1]!.entries[1], before.rows[1]!.entries[0], 'an unchanged card is the same object');
  assert.deepEqual(phases, ['ready:shown']);
});

test('a refreshed revision replaces a sideways row and its continuation together', async () => {
  let current = document([row('recent', ['m1', 'm2'])]);
  const service = new HomeService(api(path => {
    if (path.startsWith('/v1/home/rows/recent')) return row('recent', ['m3', 'm4'], {nextCursor: 'second.recent', start: 2});
    return JSON.parse(JSON.stringify(current));
  }));
  await service.load();
  await service.more('recent');
  assert.equal(ids(service, 'recent'), 'm1,m2,m3,m4');
  current = document([row('recent', ['new', 'm1'])]);
  await service.revalidate();
  assert.equal(ids(service, 'recent'), 'new,m1', 'a previous revision cannot retain an unverified tail');
  assert.equal(service.getSnapshot().document!.rows[0]!.nextCursor, 'first.recent', 'the row continues from the authoritative first page');
  // The row shrank to a complete, short list (the rest was watched elsewhere): the tail goes.
  current = document([row('recent', ['m1'], {nextCursor: '', hasMore: false, total: 1})]);
  await service.revalidate();
  assert.equal(ids(service, 'recent'), 'm1');
});

test('a failed quiet read changes nothing and rejects; a failed first load is an error with a retry', async () => {
  let fail = false;
  const service = new HomeService(api(() => { if (fail) throw Object.assign(new Error('down'), {status: 503}); return document([row('recent', ['m1'])]); }));
  await service.load();
  const shown = service.getSnapshot();
  fail = true;
  await assert.rejects(service.revalidate(), {status: 503});
  assert.equal(service.getSnapshot(), shown, 'what is on screen is untouched');
  // An explicit refresh that fails keeps the rows too.
  await service.load();
  assert.equal(service.getSnapshot().phase, 'error');
  assert.equal(service.getSnapshot().document, shown.document);
  fail = false;
  await service.retry();
  assert.equal(service.getSnapshot().phase, 'ready');
});

test('a row page the server refuses as out of date brings Home up to date quietly instead of failing', async () => {
  let current = document([row('recent', ['m1', 'm2'])]);
  let rowReads = 0;
  const service = new HomeService(api(path => {
    if (path.startsWith('/v1/home/rows/')) { rowReads++; throw Object.assign(new Error('stale'), {code: 'stale_continuation', status: 409}); }
    return JSON.parse(JSON.stringify(current));
  }));
  await service.load();
  current = document([row('recent', ['new', 'm1'], {nextCursor: 'fresh.recent'})]);
  await service.more('recent');
  assert.equal(rowReads, 1);
  assert.equal(service.getSnapshot().phase, 'ready');
  assert.equal(service.getSnapshot().error, undefined);
  assert.equal(service.getSnapshot().paging.size, 0);
  assert.equal(ids(service, 'recent'), 'new,m1');
});

for (const rowId of ['new_on_server', 'because_you_watched', 'listen_next', 'continue_books', 'community_watching']) {
  test(`${rowId} recovers a refused fingerprint cursor without retaining the old overlapping tail`, async () => {
    let changed = false;
    const cursors: string[] = [];
    const service = new HomeService(api(path => {
      if (path === '/v1/home') return document([changed
        ? row(rowId, ['x', 'a'], {nextCursor: 'membership-b-first'})
        : row(rowId, ['a', 'b'], {nextCursor: 'membership-a-first'})]);
      const cursor = new URL(path, 'http://fixture.invalid').searchParams.get('cursor')!;
      cursors.push(cursor);
      if (changed && cursor.startsWith('membership-a')) throw {status: 409, code: 'stale_continuation'};
      if (cursor === 'membership-a-first') return row(rowId, ['c', 'd'], {start: 2, nextCursor: 'membership-a-tail'});
      assert.equal(cursor, 'membership-b-first');
      return row(rowId, ['e', 'f'], {start: 2, nextCursor: '', hasMore: false, total: 4});
    }));
    try {
      await service.load();
      await service.more(rowId);
      assert.equal(ids(service, rowId), 'a,b,c,d');
      changed = true;
      await service.more(rowId);
      assert.equal(ids(service, rowId), 'x,a', 'removed tail entries cannot survive a changed membership');
      assert.equal(service.getSnapshot().document!.rows[0].nextCursor, 'membership-b-first');
      await service.more(rowId);
      assert.equal(ids(service, rowId), 'x,a,e,f');
      assert.deepEqual(cursors, ['membership-a-first', 'membership-a-tail', 'membership-b-first']);
      assert.equal(service.getSnapshot().paging.size, 0);
    } finally { service.dispose(); }
  });
}

test('deferred causal hydration replaces the old reason, paged tail and cursor together', async () => {
  let changed = false;
  const reason = (id: string) => ({kind: 'because_you_watched', source: {id, name: id}, text: {code: 'next.home.becauseYouWatched', params: {title: id}, fallback: 'Because you watched ' + id}});
  const service = new HomeService(api(path => {
    if (path === '/v1/home') return document([row('because_you_watched', [], {deferred: true, limit: 2})]);
    const cursor = new URL(path, 'http://fixture.invalid').searchParams.get('cursor');
    if (cursor) return row('because_you_watched', ['old-c', 'old-d'], {reason: reason('old-source'), start: 2, nextCursor: 'old-tail'});
    return changed
      ? row('because_you_watched', ['x', 'a'], {reason: reason('new-source'), nextCursor: 'new-first'})
      : row('because_you_watched', ['a', 'b'], {reason: reason('old-source'), nextCursor: 'old-first'});
  }));
  try {
    await service.load();
    await new Promise(resolve => setImmediate(resolve));
    await service.more('because_you_watched');
    assert.equal(ids(service, 'because_you_watched'), 'a,b,old-c,old-d');
    changed = true;
    await service.revalidate();
    await new Promise(resolve => setImmediate(resolve));
    const refreshed = service.getSnapshot().document!.rows[0];
    assert.equal(ids(service, 'because_you_watched'), 'x,a');
    assert.equal(refreshed.reason!.source!.id, 'new-source');
    assert.equal(refreshed.nextCursor, 'new-first');
  } finally { service.dispose(); }
});

test('quiet refreshes replace every revision-pinned row and cursor together', async () => {
  for (const rowId of ['new_on_server', 'recent']) {
    const first = Array.from({length: 60}, (_, i) => 'item-' + i);
    const tail = Array.from({length: 40}, (_, i) => 'item-' + (i + 60));
    let changed = false;
    const service = new HomeService(api(path => path === '/v1/home'
      ? document([row(rowId, changed ? ['inserted', ...first.slice(0, 59)] : first, {nextCursor: changed ? 'fresh-first' : 'old-first', total: 200})])
      : row(rowId, tail, {start: 60, limit: 60, nextCursor: 'old-tail', total: 200})));
    try {
      await service.load(); await service.more(rowId);
      assert.equal(service.getSnapshot().document!.rows[0].entries.length, 100);
      changed = true;
      await service.revalidate();
      const refreshed = service.getSnapshot().document!.rows[0];
      assert.equal(refreshed.entries.length, 60);
      assert.equal(refreshed.entries[0].id, 'inserted');
      assert.equal(refreshed.entries.length, 60);
      assert.equal(refreshed.nextCursor, 'fresh-first');
    } finally { service.dispose(); }
  }
});

test('a refused fenced cursor is unavailable while the authoritative Home refresh is pending', async () => {
  let finish!: (value: unknown) => void;
  let refresh = false;
  const service = new HomeService(api(path => {
    if (path !== '/v1/home') throw {status: 409, code: 'stale_continuation'};
    if (refresh) return new Promise(resolve => { finish = resolve; });
    return document([row('new_on_server', ['a', 'b'], {nextCursor: 'old'})]);
  }));
  try {
    await service.load(); refresh = true;
    const pending = service.more('new_on_server');
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(service.getSnapshot().document!.rows[0].nextCursor, '');
    assert.equal(service.getSnapshot().phase, 'ready');
    finish(document([row('new_on_server', ['x', 'a'], {nextCursor: 'current'})]));
    await pending;
    assert.equal(ids(service, 'new_on_server'), 'x,a');
    assert.equal(service.getSnapshot().document!.rows[0].nextCursor, 'current');
  } finally { service.dispose(); }
});

test('a fenced See all refresh rereads every loaded page and continues from its actual new cursor', async () => {
  let changed = false;
  const pager = new HomeRowPager(api(path => {
    const cursor = new URL(path, 'http://fixture.invalid').searchParams.get('cursor');
    const page = cursor ? Number(cursor.split('-')[1]) : 0;
    if (changed) {
      if (cursor) assert.ok(cursor.startsWith('new-'));
      return row('because_you_watched', page === 0 ? ['x', 'a'] : [`new-${page}-a`, `new-${page}-b`], {start: page * 2, nextCursor: `new-${page + 1}`});
    }
    return row('because_you_watched', page === 0 ? ['a', 'b'] : [`old-${page}-a`, `old-${page}-b`], {start: page * 2, nextCursor: `old-${page + 1}`});
  }), 'because_you_watched', 2);
  try {
    await pager.load();
    for (let i = 0; i < 5; i++) await pager.more();
    assert.equal(pager.getSnapshot().entries.length, 12);
    changed = true;
    await pager.revalidate();
    assert.deepEqual(pager.getSnapshot().entries.map(e => e.id), ['x', 'a', 'new-1-a', 'new-1-b', 'new-2-a', 'new-2-b', 'new-3-a', 'new-3-b', 'new-4-a', 'new-4-b','new-5-a','new-5-b']);
    assert.equal(pager.getSnapshot().nextCursor, 'new-6');
    await pager.more();
    assert.deepEqual(pager.getSnapshot().entries.slice(-2).map(e => e.id), ['new-6-a', 'new-6-b']);
  } finally { pager.dispose(); }
});

test('a fenced See all refresh restarts if membership changes during its page reload', async () => {
  let membership = 'old';
  const pager = new HomeRowPager(api(path => {
    const cursor = new URL(path, 'http://fixture.invalid').searchParams.get('cursor');
    if (cursor === 'old-first') return row('listen_next', ['c', 'd'], {start: 2, nextCursor: 'old-tail'});
    if (cursor === 'intermediate-first') { membership = 'current'; throw {status: 409, code: 'stale_continuation'}; }
    if (cursor === 'current-first') return row('listen_next', ['e', 'f'], {start: 2, nextCursor: '', hasMore: false});
    assert.equal(cursor, null);
    return row('listen_next', membership === 'old' ? ['a', 'b'] : membership === 'intermediate' ? ['x', 'a'] : ['y', 'x'], {nextCursor: membership + '-first'});
  }), 'listen_next', 2);
  try {
    await pager.load(); await pager.more();
    membership = 'intermediate';
    await pager.revalidate();
    assert.deepEqual(pager.getSnapshot().entries.map(e => e.id), ['y', 'x']);
    assert.equal(pager.getSnapshot().nextCursor, 'current-first');
    assert.equal(pager.getSnapshot().restarts, 1);
    await pager.more();
    assert.deepEqual(pager.getSnapshot().entries.map(e => e.id), ['y', 'x', 'e', 'f']);
  } finally { pager.dispose(); }
});

test('saving a layout changed elsewhere requires an explicit reload and never silently overwrites it', async () => {
  let revision = 2;
  const writes: any[] = [];
  const service = new HomeService(api((path, method, body) => {
    if (path === '/v1/home/layout' && method === 'PUT') {
      writes.push(body);
      if (body.expectedRevision !== revision) throw Object.assign(new Error('moved'), {code: 'home_layout_conflict', status: 409});
      revision++;
      return {revision, rowOrder: body.rowOrder, hiddenRowIds: body.hiddenRowIds};
    }
    if (path === '/v1/home/layout') return {revision, rowOrder: ['a', 'b'], hiddenRowIds: [], rows: []};
    return document([row('a', ['m1']), row('b', ['m2'])], {layout: {revision, rowOrder: ['a', 'b'], hiddenRowIds: []}});
  }));
  await service.load();
  revision = 7; // another device rearranged Home
  let key = 0;
  await assert.rejects(service.saveLayout(['b','a'],[],()=> 'key-'+(++key)),{code:'home_layout_conflict'});
  assert.deepEqual(writes.map(w=>w.expectedRevision),[2]);
  const layout = await service.loadLayout();
  const saved = await service.saveLayout(['b','a'],[],()=> 'key-'+(++key),layout.revision);
  assert.equal(saved.revision,8);
  assert.deepEqual(writes.map(w=>w.expectedRevision),[2,7]);
  assert.equal(service.getSnapshot().phase, 'ready');
});

test('removing a card is immediate and can be put back', async () => {
  const service = new HomeService(api(() => document([row('continue', ['a', 'b']), row('recent', ['a', 'm2'])])));
  await service.load();
  const undo = service.withoutEntry('a');
  assert.deepEqual([ids(service, 'continue'), ids(service, 'recent')], ['b', 'm2']);
  undo();
  assert.deepEqual([ids(service, 'continue'), ids(service, 'recent')], ['a,b', 'a,m2']);
});

test('another viewer or server is never merged into what is shown', () => {
  const shown = validateHomeDocument(document([row('recent', ['m1'])]));
  const other = validateHomeDocument(document([row('recent', ['x'], {viewerFence:'someone-else'})], {viewerFence: 'someone-else'}));
  assert.equal(mergeHomeDocument(shown, other), other);
});

test('Home publishes fast shelves while another shelf is still loading', async () => {
  let finishSlow!: (value: unknown) => void;
  const slow = new Promise(resolve => { finishSlow = resolve; });
  const service = new HomeService(api(path => {
    if (path === '/v1/home') return document([row('slow', [], {deferred: true, limit: 12}), row('fast', [], {deferred: true, limit: 12}), row('tail', [], {deferred: true, limit: 12})]);
    if (path.includes('/rows/slow')) return slow;
    if (path.includes('/rows/fast')) return row('fast', ['fast-card']);
    if (path.includes('/rows/tail')) return row('tail', ['tail-card']);
    throw new Error(path);
  }));
  await service.load();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(ids(service, 'fast'), 'fast-card');
  assert.equal(ids(service, 'tail'), 'tail-card');
  assert.equal(ids(service, 'slow'), '');
  assert.equal(service.getSnapshot().phase, 'ready');
  finishSlow(row('slow', ['slow-card']));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(ids(service, 'slow'), 'slow-card');
  service.dispose();
});

test('a deferred quiet refresh keeps previous cards and rejects a row from another viewer', async () => {
  let deferred = false;
  const service = new HomeService(api(path => {
    if (path === '/v1/home') return document([row('recent', deferred ? [] : ['old'], deferred ? {deferred: true, limit: 12} : {})]);
    return row('recent', ['private'], {viewerFence: 'another-viewer'});
  }));
  await service.load();
  const before = service.getSnapshot().document!.rows[0];
  deferred = true;
  await service.revalidate();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(service.getSnapshot().document!.rows[0], before);
  assert.equal(ids(service, 'recent'), 'old');
  assert.equal(service.getSnapshot().error, undefined);
  service.dispose();
});

test('the server expands the personal family in its published position', async () => {
  const service = new HomeService(api(path => {
    if (path === '/v1/home') return document([row('first', [], {deferred: true, limit: 12}), row('for_you', [], {deferred: true, kind: 'family', limit: 12}), row('last', [], {deferred: true, limit: 12})]);
    if (path.includes('/rows/for_you')) return row('for_you', [], {children: [row('for_you:one', ['one'], {family: 'for_you'}), row('for_you:two', ['two'], {family: 'for_you'})]});
    if (path.includes('/rows/first')) return row('first', ['first']);
    return row('last', ['last']);
  }));
  await service.load();
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(service.getSnapshot().document!.rows.map(row => row.id), ['first', 'for_you:one', 'for_you:two', 'last']);
  service.dispose();
});

test('a row timeout releases its queue slot even if its API ignores cancellation', async () => {
  const never = new Promise(() => {});
  const service = new HomeService(api(path => {
    if (path === '/v1/home') return document([row('one', [], {deferred: true, limit: 12}), row('two', [], {deferred: true, limit: 12}), row('ready', [], {deferred: true, limit: 12})]);
    if (path.includes('/rows/ready')) return row('ready', ['card']);
    return never;
  }), {timeoutMs: 20});
  await service.load();
  await new Promise(resolve => setTimeout(resolve, 50));
  assert.equal(ids(service, 'ready'), 'card');
  assert.equal(service.getSnapshot().error, undefined);
  service.dispose();
});

test('deferred row arrival cannot select or replace the server spotlight', async () => {
  let finishListening!: (value: unknown) => void;
  const spotlight=[{entry:entry('selected'),reason:{kind:'recommended',text:{code:'next.home.reason.recommended',fallback:'Recommended for you'}}}];
  const service = new HomeService(api(path => {
    if (path === '/v1/home') return document([row('continue', [], {deferred: true}), row('listen_next', [], {deferred: true})],{spotlight,hero:{rowId:'continue',entryId:'video'}});
    if (path.includes('/rows/listen_next')) return new Promise(resolve => { finishListening = resolve; });
    return row('continue', ['video'], {hero: {rowId: 'continue', entryId: 'video'}});
  }));
  await service.load();
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.equal(service.getSnapshot().document!.spotlight[0].entry.id,'selected');
  finishListening(row('listen_next', ['audio'], {hero: {rowId: 'listen_next', entryId: 'audio'}}));
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.equal(service.getSnapshot().document!.spotlight[0].entry.id,'selected');
  assert.equal(JSON.stringify(service.getSnapshot().document).includes('"hero"'),false);
  service.dispose();
});

test('a page from a different viewer cannot be appended', async () => {
  const service = new HomeService(api(path => path.includes('/rows/') ? row('recent', ['other'], {viewerFence: 'other-viewer'}) : document([row('recent', ['mine'])])));
  await service.load(); await service.more('recent');
  assert.equal(ids(service, 'recent'), 'mine');
  assert.equal(service.getSnapshot().paging.size, 0);
  service.dispose();
});


test('See all quiet refresh retains all1203 authorized cards beyond five pages',async()=>{
 const total=1203,limit=37;
 let reads=0;
 const pager=new HomeRowPager(api(path=>{
  reads++;
  const start=Number(new URL(path,'http://fixture.invalid').searchParams.get('cursor')??0);
  const end=Math.min(start+limit,total);
  return row('new_on_server',Array.from({length:end-start},(_,i)=>String(start+i)),{start,total,limit,nextCursor:end<total?String(end):'',hasMore:end<total});
 }),'new_on_server',limit);
 try{
  await pager.load();while(pager.getSnapshot().nextCursor)await pager.more();
  const prefix=pager.getSnapshot().entries.map(e=>e.id);
  reads=0;await pager.revalidate();
  assert.equal(reads,33);assert.equal(pager.getSnapshot().entries.length,total);
  assert.deepEqual(pager.getSnapshot().entries.map(e=>e.id),prefix);
  assert.equal(pager.getSnapshot().nextCursor,'');
 }finally{pager.dispose()}
});

function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((accept, refuse) => {resolve = accept; reject = refuse;});
  return {promise, resolve, reject};
}

test('saving a layout supersedes an earlier quiet read with or without an editor lifetime',async()=>{
 for(const scoped of [false,true]){
  const stale=deferred<unknown>();let reads=0,settles=0;
  const old=document([row('a',['one']),row('b',['two'])]);
  const fresh=document([row('b',['two'])],{layout:{revision:3,rowOrder:['b','a'],hiddenRowIds:['a']}});
  const service=new HomeService(api((path,method)=>{
   if(path==='/v1/home/layout'&&method==='PUT')return fresh.layout;
   if(path==='/v1/home')return ++reads===1?old:reads===2?stale.promise:fresh;
   throw new Error(path);
  }));
  try{
   await service.load();
   const quiet=service.revalidate({settle:async()=>{settles++;}});
   const controller=new AbortController();
   await service.saveLayout(['b','a'],['a'],()=> 'layout-save',2,scoped?{signal:controller.signal,isCurrent:()=>true}:undefined);
   const saved=service.getSnapshot().document;
   assert.equal(saved?.layout.revision,3);
   stale.resolve(old);await quiet;
   assert.equal(service.getSnapshot().document,saved);
   assert.equal(settles,0,'the obsolete quiet reader cannot enter its settle callback');
   assert.deepEqual(saved?.rows.map(r=>r.id),['b']);
  }finally{service.dispose();}
 }
});

test('Home rejects an older layout document and never hydrates its hidden shelves',async()=>{
 const fresh=document([row('b',['two'])],{layout:{revision:3,rowOrder:['b','a'],hiddenRowIds:['a']}});
 const old=document([row('a',[],{deferred:true}),row('b',['two'])]);
 let reads=0,shelfReads=0;
 const service=new HomeService(api(path=>{
  if(path==='/v1/home')return reads++?old:fresh;
  shelfReads++;return row('a',['one']);
 }));
 try{
  await service.load();const shown=service.getSnapshot().document!;
  await service.revalidate();await new Promise<void>(resolve=>setImmediate(resolve));
  assert.equal(service.getSnapshot().document,shown);
  assert.equal(shelfReads,0);
  const other=validateHomeDocument({...old,viewerFence:'new-viewer',rows:old.rows.map((r:any)=>({...r,viewerFence:'new-viewer'}))});
  assert.equal(mergeHomeDocument(shown,other),other,'layout revisions from different viewers are unrelated');
 }finally{service.dispose();}
});

test('a fenced See all quiet read cannot discard a page appended before it settles',async()=>{
 for(const duringSettle of [false,true]){
  const first=deferred<unknown>(),settle=deferred<void>();let reads=0,settles=0;
  const pager=new HomeRowPager(api(path=>{
   if(path.includes('cursor='))return row('because_you_watched',['c','d'],{start:2,nextCursor:'third'});
   return reads++?first.promise:row('because_you_watched',['a','b'],{nextCursor:'second'});
  }),'because_you_watched',2);
  try{
   await pager.load();
   const quiet=pager.revalidate({settle:()=>{settles++;return settle.promise;}});
   if(duringSettle){first.resolve(row('because_you_watched',['new','a'],{nextCursor:'fresh-second'}));await new Promise<void>(resolve=>setImmediate(resolve));}
   await pager.more();const appended=pager.getSnapshot();
   first.resolve(row('because_you_watched',['new','a'],{nextCursor:'fresh-second'}));settle.resolve();await quiet;
   assert.equal(pager.getSnapshot(),appended);
   assert.deepEqual(appended.entries.map(e=>e.id),['a','b','c','d']);
   assert.equal(appended.nextCursor,'third');
   assert.equal(settles,duringSettle?1:0);
  }finally{pager.dispose();}
 }
});

test('quiet reads settle in order: the later-started answer wins and owns deferred shelves', async () => {
  const reads: ReturnType<typeof deferred<unknown>>[] = [];
  let shelfCalls = 0;
  const service = new HomeService(api(path => {
    if (path === '/v1/home') {const read = deferred<unknown>(); reads.push(read); return read.promise;}
    if (path.startsWith('/v1/home/rows/shelf')) {shelfCalls++; return row('shelf', ['s' + shelfCalls], {deferred: false});}
    throw new Error(path);
  }));
  const initial = service.load();
  reads.shift()!.resolve(document([row('recent', ['a'])]));
  await initial;

  const first = service.revalidate();
  const second = service.revalidate();
  assert.equal(reads.length, 2, 'two distinct quiet reads were issued');
  // The later-started read finishes first and publishes revision 3.
  reads[1]!.resolve(document([row('recent', ['new', 'a'], {revision: {catalog: 3, viewer: 1}}), row('shelf', [], {deferred: true, limit: 2})], {revision: {catalog: 3, viewer: 1}}));
  await second;
  assert.equal(ids(service, 'recent'), 'new,a');
  // The older read finishes later with revision 2; it must not replace the newer document or hydrate.
  reads[0]!.resolve(document([row('recent', ['old'], {revision: {catalog: 2, viewer: 1}}), row('shelf', [], {deferred: true, limit: 2})], {revision: {catalog: 2, viewer: 1}}));
  await first;
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(ids(service, 'recent'), 'new,a', 'a delayed older answer cannot roll the document back');
  assert.equal(service.getSnapshot().document!.revision.catalog, 3);
  assert.equal(shelfCalls, 1, 'only the later-started read may start its deferred shelves');
  service.dispose();
});

test('a delayed failure from a superseded quiet read is not reported against its successor', async () => {
  const reads: ReturnType<typeof deferred<unknown>>[] = [];
  const service = new HomeService(api(path => {
    if (path === '/v1/home') {const read = deferred<unknown>(); reads.push(read); return read.promise;}
    throw new Error(path);
  }));
  const initial = service.load();
  reads.shift()!.resolve(document([row('recent', ['a'])]));
  await initial;
  const older = service.revalidate();
  const newer = service.revalidate();
  reads[1]!.resolve(document([row('recent', ['new'], {revision: {catalog: 4, viewer: 1}})], {revision: {catalog: 4, viewer: 1}}));
  await newer;
  reads[0]!.reject(Object.assign(new Error('stale read failed'), {status: 503}));
  await older; // resolves quietly; a late old failure is not the current document's failure
  assert.equal(ids(service, 'recent'), 'new');
  assert.equal(service.getSnapshot().phase, 'ready');
  service.dispose();
});

test('explicit load/clear and dispose invalidate outstanding quiet reads', async () => {
  const reads: ReturnType<typeof deferred<unknown>>[] = [];
  const service = new HomeService(api(path => {
    if (path === '/v1/home') {const read = deferred<unknown>(); reads.push(read); return read.promise;}
    throw new Error(path);
  }));
  const initial = service.load();
  reads.shift()!.resolve(document([row('recent', ['a'])]));
  await initial;

  const quiet = service.revalidate();
  const reload = service.load({clear: true});
  reads[1]!.resolve(document([row('recent', ['loaded'], {revision: {catalog: 5, viewer: 1}})], {revision: {catalog: 5, viewer: 1}}));
  await reload;
  reads[0]!.resolve(document([row('recent', ['stale'], {revision: {catalog: 2, viewer: 1}})], {revision: {catalog: 2, viewer: 1}}));
  await quiet;
  assert.equal(ids(service, 'recent'), 'loaded', 'an explicit load invalidates the outstanding quiet ticket');

  const disposedRead = service.revalidate();
  service.dispose();
  const queued = reads.pop()!;
  queued.resolve(document([row('recent', ['after-dispose'])]));
  await disposedRead;
  assert.ok(true, 'a disposed service resolves its outstanding quiet read quietly');
});

test('a superseded quiet response never invokes its settle callback', async () => {
  const reads: ReturnType<typeof deferred<unknown>>[] = [];
  const service = new HomeService(api(path => {
    if (path === '/v1/home') {const read = deferred<unknown>(); reads.push(read); return read.promise;}
    throw new Error(path);
  }));
  const initial = service.load();
  reads.shift()!.resolve(document([row('recent', ['a'])]));
  await initial;
  let settles = 0;
  const older = service.revalidate({settle: async () => {settles++;}});
  const newer = service.revalidate();
  reads[1]!.resolve(document([row('recent', ['new'], {revision: {catalog: 6, viewer: 1}})], {revision: {catalog: 6, viewer: 1}}));
  await newer;
  reads[0]!.resolve(document([row('recent', ['stale'], {revision: {catalog: 2, viewer: 1}})], {revision: {catalog: 2, viewer: 1}}));
  await older;
  assert.equal(settles, 0, 'an already-superseded response does not enter the old caller settle');
  assert.equal(ids(service, 'recent'), 'new');
  service.dispose();
});

test('a settle that was already awaiting cannot let its older answer replace a successor', async () => {
  const reads: ReturnType<typeof deferred<unknown>>[] = [];
  const service = new HomeService(api(path => {
    if (path === '/v1/home') {const read = deferred<unknown>(); reads.push(read); return read.promise;}
    throw new Error(path);
  }));
  const initial = service.load();
  reads.shift()!.resolve(document([row('recent', ['a'])]));
  await initial;
  let releaseSettle!: () => void;
  const older = service.revalidate({settle: () => new Promise<void>(resolve => {releaseSettle = resolve;})});
  reads[0]!.resolve(document([row('recent', ['mid'], {revision: {catalog: 2, viewer: 1}})], {revision: {catalog: 2, viewer: 1}}));
  await new Promise(resolve => setImmediate(resolve));
  const newer = service.revalidate();
  reads[1]!.resolve(document([row('recent', ['new'], {revision: {catalog: 7, viewer: 1}})], {revision: {catalog: 7, viewer: 1}}));
  await newer;
  releaseSettle();
  await older;
  assert.equal(ids(service, 'recent'), 'new', 'the post-settle current check rejects the older commit');
  service.dispose();
});

test('a superseded deferred-row denial cannot remove a successor row; a current denial still removes it', async () => {
  const reads: ReturnType<typeof deferred<unknown>>[] = [];
  const shelves: ReturnType<typeof deferred<unknown>>[] = [];
  const service = new HomeService(api(path => {
    if (path === '/v1/home') {const read = deferred<unknown>(); reads.push(read); return read.promise;}
    if (path.startsWith('/v1/home/rows/shelf')) {const read = deferred<unknown>(); shelves.push(read); return read.promise;}
    throw new Error(path);
  }));
  const initial = service.load();
  reads.shift()!.resolve(document([row('recent', ['a'])]));
  await initial;

  const older = service.revalidate();
  reads[0]!.resolve(document([row('recent', ['a']), row('shelf', [], {deferred: true, limit: 2})]));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(shelves.length, 1, 'the older read started its deferred shelf');
  const newer = service.revalidate();
  reads[1]!.resolve(document([row('recent', ['new']), row('shelf', ['s2'])]));
  await newer;
  shelves[0]!.reject(Object.assign(new Error('shelf forbidden'), {status: 403}));
  await older;
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(service.getSnapshot().document!.rows.some(row => row.id === 'shelf'), true, 'a superseded denial cannot remove the successor shelf');
  assert.equal(ids(service, 'recent'), 'new');

  const current = service.revalidate();
  reads[2]!.resolve(document([row('recent', ['new']), row('shelf', [], {deferred: true, limit: 2})]));
  await new Promise(resolve => setImmediate(resolve));
  shelves[1]!.reject(Object.assign(new Error('current shelf forbidden'), {status: 403}));
  await current;
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(service.getSnapshot().document!.rows.some(row => row.id === 'shelf'), false, 'a current denial still removes its deferred row');
  service.dispose();
});
