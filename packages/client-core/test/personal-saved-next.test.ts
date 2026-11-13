// Saved's History on the rebuilt server (area 6 rules 18, 19, 21): what PersonalSavedService reads and sends.
import test from 'node:test';
import assert from 'node:assert/strict';
import {PersonalSavedService} from '../src/personal-saved.ts';

const scope = {serverId: 'server', viewerId: '["local","account","profile"]'};
const item = (id: string) => ({id, kind: 'movie', title: 'Film ' + id, libraryId: 'films', state: {positionMs: 0, watched: true, playCount: 1, favorite: false, inList: false, notInterested: false}});
function server(pages: Record<string, unknown> = {}) {
  const calls: {path: string; method: string}[] = [];
  const api = {request: async <T>(path: string, method = 'GET'): Promise<T> => {
    calls.push({path, method});
    const url = new URL(path, 'https://x');
    if (method === 'GET' && url.pathname === '/v1/history') return (pages[url.searchParams.get('cursor') ?? ''] ?? {items: []}) as T;
    if (method === 'DELETE' && url.pathname === '/v1/history') return undefined as T;
    throw new Error(`unexpected ${method} ${path}`);
  }};
  return {api, calls};
}
const service = (api: unknown) => new PersonalSavedService(api as never, scope, async () => '00000000-0000-4000-8000-000000000001');

test('History lists the profile\'s finished plays, newest first, for the period chosen, and pages on', async () => {
  const s = server({'': {items: [{id: 'h1', playedAt: '2026-11-12T09:00:00Z', watchedMs: 5_400_000, kind: 'movie', title: 'Film a', item: item('a')}], nextCursor: 'next'},
    next: {items: [{id: 'h2', playedAt: '2026-11-11T09:00:00Z', kind: 'movie', title: 'Film b', item: item('b')}]}});
  const saved = service(s.api);
  await saved.select({view: 'history', period: '7d'} as never);
  const shot = saved.getSnapshot();
  assert.equal(shot.error, null);
  assert.deepEqual(shot.entries.map(entry => [entry.id, entry.media?.id, entry.updatedAt, entry.completed, entry.positionSeconds]), [['h1', 'a', '2026-11-12T09:00:00Z', true, 5400]]);
  const asked = new URL(s.calls[0]!.path, 'https://x').searchParams;
  assert.deepEqual([asked.get('period'), asked.get('limit')], ['7d', '40']);
  await saved.next();
  assert.deepEqual(saved.getSnapshot().entries.map(entry => entry.id), ['h2']);
  assert.equal(new URL(s.calls.at(-1)!.path, 'https://x').searchParams.get('cursor'), 'next');
  saved.dispose();
});

test('a title removed from the server stays in History by name and cannot be opened to play', async () => {
  const s = server({'': {items: [{id: 'h1', playedAt: '2026-11-12T09:00:00Z', kind: 'movie', title: 'A film that went'}]}});
  const saved = service(s.api);
  await saved.select({view: 'history'} as never);
  const media = saved.getSnapshot().entries[0]!.media!;
  assert.deepEqual([media.title, media.missing, media.available, 'playback' in media], ['A film that went', true, false, false]);
  saved.dispose();
});

test('Clear history empties History on the server and reads it again', async () => {
  const s = server({'': {items: [{id: 'h1', playedAt: '2026-11-12T09:00:00Z', kind: 'movie', title: 'Film a', item: item('a')}]}});
  const saved = service(s.api);
  await saved.select({view: 'history'} as never);
  assert.equal(await saved.mutate({action: 'clear-history'}), true);
  assert.deepEqual(s.calls.filter(call => call.method === 'DELETE').map(call => call.path), ['/v1/history']);
  assert.ok(s.calls.filter(call => call.method === 'GET').length >= 2, 'History is read again after it is cleared');
  saved.dispose();
});

// Collections on the rebuilt server (area 6 rules 24, 25, 28, 31 to 34): one collection the profile made, held by a
// fake list server that keeps its order and revision, and another profile's collection visible to everyone.
function lists(titles = ['a', 'b', 'c'], pageSize = 40) {
  const calls: {path: string; method: string; headers?: Record<string, string>; body?: unknown}[] = [];
  let revision = 1, next = 1, everyone = false, name = 'Rainy days';
  let entries = titles.map(itemId => ({entryId: 'le' + next++, itemId}));
  const keys = new Map<string, unknown>();
  const list = () => ({id: 'col', kind: 'collection', name, owner: {id: 'me', name: 'Me'}, role: 'owner', count: entries.length, everyone, covers: [{poster: '/art/a'}], revision: String(revision), createdAt: '2026-11-01T00:00:00Z', updatedAt: '2026-11-02T00:00:00Z'});
  const theirs = {id: 'their', kind: 'collection', name: 'Theirs', owner: {id: 'them', name: 'Them'}, role: 'viewer', count: 1, everyone: true, covers: [], revision: '4', createdAt: '2026-11-01T00:00:00Z', updatedAt: '2026-11-01T00:00:00Z'};
  const refuse = (status: number, code: string) => ({status, headers: {}, body: {error: {code, message: code}}});
  const fresh = (headers?: Record<string, string>) => headers?.['If-Match'] === `"${revision}"`;
  const at = (id: string) => entries.findIndex(entry => entry.entryId === id);
  const pins: string[] = [];
  const api = {
    async request<T>(path: string, method = 'GET', sent?: unknown): Promise<T> {
      calls.push({path, method});
      const url = new URL(path, 'https://x'), q = url.searchParams;
      // The profile's pins setting (area 6 rule 38).
      if (url.pathname === '/v1/preferences') {if (method === 'PATCH') pins.splice(0, pins.length, ...(sent as {values: {pins: string[]}}).values.pins); return {effective: {pins: [...pins]}} as T;}
      if (url.pathname === '/v1/lists') return {items: [list(), theirs]} as T;
      if (url.pathname === '/v1/lists/col') return list() as T;
      if (url.pathname === '/v1/items' && q.get('list') === 'col') {
        const ordered = q.get('order') === 'desc' ? [...entries].reverse() : entries, from = Number(q.get('cursor') ?? 0), size = Number(q.get('limit'));
        const page = ordered.slice(from, from + size);
        return {items: page.map(entry => ({...item(entry.itemId), entryId: entry.entryId})), total: entries.length, ...(from + size < entries.length ? {nextCursor: String(from + size)} : {})} as T;
      }
      throw new Error(`unexpected ${method} ${path}`);
    },
    async requestRaw(path: string, method: string, options: {headers?: Record<string, string>; body?: unknown} = {}) {
      calls.push({path, method, headers: options.headers, body: options.body});
      const body = options.body as Record<string, any>, key = options.headers?.['Idempotency-Key'];
      if (key && keys.has(key)) return keys.get(key) as {status: number; headers: Record<string, string>; body?: unknown};
      const answer = (value: unknown) => {const sent = {status: 200, headers: {}, body: value}; if (key) keys.set(key, sent); return sent;};
      const entry = decodeURIComponent(path.split('/entries/')[1] ?? '');
      if (path === '/v1/lists' && method === 'POST') return answer({...list(), id: 'made', name: body.name, everyone: body.everyone});
      if (path === '/v1/lists/col/entries' && method === 'POST') {
        const itemId = body.items[0]; if (entries.some(row => row.itemId === itemId)) return answer({list: list(), added: 0, already: 1});
        // As the server does: with no `after` the title goes to the end.
        entries.splice(body.after ? at(body.after) + 1 : entries.length, 0, {entryId: 'le' + next++, itemId}); revision++; return answer({list: list(), added: 1, already: 0});
      }
      if (!fresh(options.headers)) return refuse(412, 'list_changed');
      if (path === '/v1/lists/col' && method === 'PATCH') {if (body.name !== undefined) name = body.name; if (body.everyone !== undefined) everyone = body.everyone; revision++; return answer(list());}
      if (entry && method === 'DELETE') {entries.splice(at(entry), 1); revision++; return answer(list());}
      if (entry && method === 'PATCH') {
        const moving = entries.splice(at(entry), 1)[0]!;
        entries.splice(body.after ? at(body.after) + 1 : at(body.before), 0, moving); revision++; return answer(list());
      }
      throw new Error(`unexpected ${method} ${path}`);
    },
  };
  return {api, calls, pins, order: () => entries.map(entry => entry.itemId), bump: () => {revision++;}, pageSize};
}

test('Collections lists the profile\'s own and those visible to everyone; only its maker may change one, anyone may pin one, and chosen people are not offered', async () => {
  const s = lists();
  const saved = service(s.api);
  await saved.select({view: 'collections'} as never);
  const shot = saved.getSnapshot();
  assert.equal(shot.error, null);
  assert.deepEqual(shot.resources.map(r => [r.id, r.role, r.visibility, [...r.actions]]), [['col', 'owner', 'private', ['update', 'delete', 'entries', 'reorder', 'pin']], ['their', 'viewer', 'server', ['pin']]]);
  assert.deepEqual(shot.resources[0]!.artworkPaths, ['/art/a']);
  assert.equal(new URL(s.calls[0]!.path, 'https://x').searchParams.get('kind'), 'collection');
  saved.dispose();
});

test('a new collection visible to everyone is made once, under its own key', async () => {
  const s = lists();
  const saved = service(s.api);
  await saved.select({view: 'collections'} as never);
  assert.equal(await saved.mutate({action: 'create', kind: 'collection', name: ' Films for Sunday ', summary: 'Slow ones', visibility: 'server'}), true);
  const made = s.calls.find(call => call.method === 'POST')!;
  assert.deepEqual(made.body, {kind: 'collection', name: 'Films for Sunday', description: 'Slow ones', everyone: true});
  assert.equal(made.headers?.['Idempotency-Key'], '00000000-0000-4000-8000-000000000001');
  assert.equal(saved.getSnapshot().result?.resourceId, 'made');
  saved.dispose();
});

test('a collection opens in its own order, and is made visible to everyone naming the revision it was read at', async () => {
  const s = lists();
  const saved = service(s.api);
  await saved.select({view: 'resource', resourceId: 'col'} as never);
  const shot = saved.getSnapshot();
  assert.deepEqual(shot.entries.map(entry => entry.media?.id), ['a', 'b', 'c']);
  assert.deepEqual(shot.resource?.artworkPaths, ['/art/a'], 'the page has the server\'s covers, as its card does');
  assert.deepEqual(shot.page?.order, {last: 'le3'});
  assert.equal(await saved.mutate({action: 'update', visibility: 'server'}), true);
  const sent = s.calls.find(call => call.method === 'PATCH')!;
  assert.deepEqual([sent.body, sent.headers?.['If-Match']], [{everyone: true}, '"1"']);
  assert.equal(saved.getSnapshot().resource?.visibility, 'server');
  saved.dispose();
});

test('an edit on an out-of-date copy is refused with "This list changed", nothing is overwritten, and the collection is read again', async () => {
  const s = lists();
  const saved = service(s.api);
  await saved.select({view: 'resource', resourceId: 'col'} as never);
  s.bump();
  assert.equal(await saved.mutate({action: 'update', name: 'Other'}), false);
  const shot = saved.getSnapshot();
  assert.deepEqual([shot.mutationError?.code, shot.mutationError?.conflict], ['list_changed', true]);
  assert.equal(shot.resource?.name, 'Rainy days');
  assert.equal(shot.resource?.revision, 2, 'read again at the revision that refused it');
  saved.dispose();
});

test('removing an entry offers Undo, which puts the title back in its place', async () => {
  const s = lists();
  const saved = service(s.api);
  await saved.select({view: 'resource', resourceId: 'col'} as never);
  assert.equal(await saved.mutate({action: 'entries', removeEntryIds: ['le2']}), true);
  assert.deepEqual(s.order(), ['a', 'c']);
  const undo = saved.getSnapshot().result?.undo;
  assert.ok(undo?.token);
  assert.equal(await saved.mutate({action: 'entries', undoToken: undo.token}), true);
  assert.deepEqual(s.order(), ['a', 'b', 'c']);
  saved.dispose();
});

test('Undo of the collection\'s first entry puts it back first, and a repeat changes nothing more', async () => {
  const s = lists();
  const saved = service(s.api);
  await saved.select({view: 'resource', resourceId: 'col'} as never);
  assert.equal(await saved.mutate({action: 'entries', removeEntryIds: ['le1']}), true);
  assert.deepEqual(s.order(), ['b', 'c']);
  const token = saved.getSnapshot().result!.undo!.token;
  assert.equal(await saved.mutate({action: 'entries', undoToken: token}), true);
  assert.deepEqual(s.order(), ['a', 'b', 'c'], 'back in its place, not at the end');
  const moves = s.calls.filter(call => call.method === 'PATCH').length;
  assert.equal(await saved.mutate({action: 'entries', undoToken: token}), true);
  assert.deepEqual(s.order(), ['a', 'b', 'c']);
  assert.equal(s.calls.filter(call => call.method === 'PATCH').length, moves, 'nothing moved again');
  saved.dispose();
});

test('adding a title the collection holds says it is already in and changes nothing', async () => {
  const s = lists();
  const saved = service(s.api);
  await saved.select({view: 'resource', resourceId: 'col'} as never);
  await saved.mutate({action: 'entries', addItemIds: ['b']});
  assert.deepEqual(saved.getSnapshot().result?.entries, {added: [], removed: [], unchanged: ['b'], failed: []});
  assert.deepEqual(s.order(), ['a', 'b', 'c']);
  saved.dispose();
});

test('two neighbours swap in the collection\'s own order', async () => {
  const s = lists();
  const saved = service(s.api);
  await saved.select({view: 'resource', resourceId: 'col'} as never);
  assert.equal(await saved.mutate({action: 'reorder', entryIds: ['le3', 'le2']}), true);
  assert.deepEqual(s.order(), ['a', 'c', 'b']);
  saved.dispose();
});

test('on a later page, a move names the entry in front of the page, and to the front names the collection\'s first', async () => {
  const s = lists(Array.from({length: 45}, (_, n) => 't' + n));
  const saved = service(s.api);
  await saved.select({view: 'resource', resourceId: 'col'} as never);
  await saved.next();
  let shot = saved.getSnapshot();
  assert.deepEqual([shot.page?.start, shot.page?.order?.previous, shot.entries[0]?.media?.id], [40, 'le40', 't40']);
  // Up from the top of the page: it changes places with the entry in front of the page.
  assert.equal(await saved.mutate({action: 'reorder', entryIds: ['le41', 'le40']}), true);
  assert.deepEqual(s.order().slice(38, 42), ['t38', 't40', 't39', 't41']);
  saved.dispose();
  // To the front from a later page: before the collection's first entry, which page 1 named.
  const again = service(s.api);
  await again.select({view: 'resource', resourceId: 'col'} as never);
  await again.next();
  shot = again.getSnapshot();
  assert.equal(shot.page?.start, 40);
  const moves = s.calls.length;
  assert.equal(await again.mutate({action: 'reorder', entryIds: [shot.entries.at(-1)!.id], afterEntryId: ''}), true);
  assert.deepEqual(s.calls.slice(moves).find(call => call.method === 'PATCH')?.body, {before: 'le1'});
  assert.equal(s.order()[0], 't44');
  again.dispose();
});

// Saved views on the rebuilt server (area 6 rules 24, 36): a list of kind saved_view keeps a library and the item
// list's query; its page is that query's titles, always current.
import {libraryItemQuery, libraryQueryControls} from '../src/library-browse.ts';

test('a saved view\'s stored query reads back as the browse controls that made it', () => {
  const nodes = [{variant: 'predicate', field: 'genre', operator: 'equals', value: 'Drama'}, {variant: 'predicate', field: 'playState', operator: 'equals', value: 'unplayed'},
    {variant: 'predicate', field: 'year', operator: 'between', value: [1990, 1999]}] as const;
  const {library: _, ...list} = libraryItemQuery({id: 'films'}, 'movies', nodes as never, [{field: 'year', direction: 'desc'}]) as Record<string, unknown>;
  const query = new URLSearchParams(Object.entries(list).map(([k, v]) => [k, String(v)])).toString();
  assert.deepEqual(libraryQueryControls(query), {pivot: 'movies', nodes, sorts: [{field: 'year', direction: 'desc'}]});
  assert.equal(libraryQueryControls('kind=movie&mood=rainy'), undefined, 'something the controls cannot draw is not read as something else');
});

function views(view: Record<string, unknown> = {}) {
  const calls: {path: string; method: string; headers?: Record<string, string>; body?: unknown}[] = [];
  let revision = 3, name = 'Nineties drama', gone = false;
  const list = () => ({id: 'v1', kind: 'saved_view', name, owner: {id: 'me', name: 'Me'}, role: 'owner', count: 2, library: 'films', query: 'kind=movie&genre=Drama&sort=year&order=desc', covers: [], revision: String(revision), createdAt: '2026-11-01T00:00:00Z', updatedAt: '2026-11-01T00:00:00Z', ...view});
  const pins: string[] = [];
  const api = {
    async request<T>(path: string, method = 'GET', sent?: unknown): Promise<T> {
      calls.push({path, method});
      const url = new URL(path, 'https://x');
      if (url.pathname === '/v1/preferences') {if (method === 'PATCH') pins.splice(0, pins.length, ...(sent as {values: {pins: string[]}}).values.pins); return {effective: {pins: [...pins]}} as T;}
      if (url.pathname === '/v1/lists' && url.searchParams.get('kind') === 'saved_view') return {items: [list(), {...list(), id: 'v2', name: 'Albums', query: 'kind=album', library: 'music'}]} as T;
      if (url.pathname === '/v1/lists/v1') {if (gone) throw Object.assign(new Error('gone'), {status: 404, code: 'not_found'}); return list() as T;}
      if (url.pathname === '/v1/items' && url.searchParams.get('library') === 'films') return {items: [{...item('a'), libraryId: 'films'}, {...item('b'), libraryId: 'films'}], total: 2} as T;
      if (url.pathname === '/v1/items') return {items: [], total: 0} as T;
      throw new Error(`unexpected ${method} ${path}`);
    },
    async requestRaw(path: string, method: string, options: {headers?: Record<string, string>; body?: unknown} = {}) {
      calls.push({path, method, headers: options.headers, body: options.body});
      if (path === '/v1/lists' && method === 'POST') return {status: 201, headers: {}, body: {...list(), id: 'made', name: (options.body as {name: string}).name}};
      if (options.headers?.['If-Match'] !== `"${revision}"`) return {status: 412, headers: {}, body: {error: {code: 'list_changed', message: 'changed'}}};
      if (method === 'PATCH') {name = (options.body as {name: string}).name; revision++; return {status: 200, headers: {}, body: list()};}
      if (method === 'DELETE') {gone = true; return {status: 204, headers: {}};}
      throw new Error(`unexpected ${method} ${path}`);
    },
  };
  return {api, calls, pins};
}

test('a saved view opens with its titles, read with its own query on its library', async () => {
  const s = views();
  const saved = service(s.api);
  await saved.select({view: 'resource', resourceId: 'v1'} as never);
  const shot = saved.getSnapshot();
  assert.equal(shot.error, null);
  assert.deepEqual([shot.resource?.kind, shot.resource?.status, shot.resource?.definition?.pivot, shot.resource?.definition?.sort], ['view', 'ready', 'movies', [{field: 'year', direction: 'desc'}]]);
  assert.deepEqual(shot.entries.map(entry => entry.media?.id), ['a', 'b']);
  const asked = new URL(s.calls.find(call => call.path.startsWith('/v1/items') && call.path.includes('library='))!.path, 'https://x').searchParams;
  assert.deepEqual([asked.get('library'), asked.get('kind'), asked.get('genre'), asked.get('sort'), asked.get('order')], ['films', 'movie', 'Drama', 'year', 'desc']);
  saved.dispose();
});

test('a saved view is renamed naming its revision, and deleted', async () => {
  const s = views();
  const saved = service(s.api);
  await saved.select({view: 'resource', resourceId: 'v1'} as never);
  assert.equal(await saved.mutate({action: 'update', name: 'Drama, nineties'}), true);
  const patch = s.calls.find(call => call.method === 'PATCH')!;
  assert.deepEqual([patch.path, patch.body, patch.headers?.['If-Match']], ['/v1/lists/v1', {name: 'Drama, nineties'}, '"3"']);
  assert.equal(saved.getSnapshot().resource?.name, 'Drama, nineties');
  assert.equal(await saved.mutate({action: 'delete'}), true);
  assert.equal(saved.getSnapshot().result?.deleted, true);
  saved.dispose();
});

test('a saved view whose filter lost a part opens with the rest and says what was dropped; one the controls cannot draw is not run', async () => {
  const dropped = views({dropped: ['genre: Westerns']});
  const saved = service(dropped.api);
  await saved.select({view: 'resource', resourceId: 'v1'} as never);
  assert.deepEqual([saved.getSnapshot().resource?.status, saved.getSnapshot().resource?.invalidComponents], ['ready', ['genre: Westerns']]);
  saved.dispose();
  const odd = views({query: 'kind=movie&mood=rainy'});
  const review = service(odd.api);
  await review.select({view: 'resource', resourceId: 'v1'} as never);
  assert.equal(review.getSnapshot().resource?.status, 'needs-review');
  assert.equal(odd.calls.some(call => call.path.includes('library=')), false, 'its titles are not read');
  review.dispose();
});

test('the Views tab lists the profile\'s saved views as cards, music ones square', async () => {
  const s = views();
  const saved = service(s.api);
  await saved.select({view: 'views'} as never);
  const shot = saved.getSnapshot();
  assert.equal(shot.error, null);
  assert.deepEqual(shot.resources.map(r => [r.id, r.kind, r.shape, r.definition?.pivot, [...r.actions]]), [['v1', 'view', 'posters', 'movies', ['update', 'delete', 'pin']], ['v2', 'view', 'squares', 'albums', ['update', 'delete', 'pin']]]);
  assert.equal(new URL(s.calls[0]!.path, 'https://x').searchParams.get('kind'), 'saved_view');
  saved.dispose();
});

test('Save view keeps the library and the item list\'s query its sort and filter make, once, under its own key', async () => {
  const s = views();
  const saved = service(s.api);
  await saved.select({view: 'views'} as never);
  const definition = {libraryId: 'films', pivot: 'movies', query: {variant: 'predicate', field: 'genre', operator: 'equals', value: 'Drama'}, sort: [{field: 'year', direction: 'desc'}], presentation: 'grid'};
  assert.equal(await saved.mutate({action: 'create', kind: 'view', name: 'Drama by year', definition} as never), true);
  const made = s.calls.find(call => call.method === 'POST')!;
  assert.deepEqual(made.body, {kind: 'saved_view', name: 'Drama by year', library: 'films', query: 'kind=movie&genre=Drama&sort=year&order=desc'});
  assert.equal(made.headers?.['Idempotency-Key'], '00000000-0000-4000-8000-000000000001');
  assert.equal(saved.getSnapshot().result?.resourceId, 'made');
  saved.dispose();
});

test('a view whose filter the server cannot answer is refused before anything is sent', async () => {
  const s = views();
  const saved = service(s.api);
  await saved.select({view: 'views'} as never);
  const definition = {libraryId: 'films', pivot: 'movies', query: {variant: 'predicate', field: 'title', operator: 'starts-with', value: 'A'}, sort: [{field: 'title', direction: 'asc'}], presentation: 'grid'};
  assert.equal(await saved.mutate({action: 'create', kind: 'view', name: 'A titles', definition} as never), false);
  assert.equal(s.calls.some(call => call.method === 'POST'), false);
  assert.equal(saved.getSnapshot().mutationError?.message, 'This filter is not available yet.', 'in the browse page\'s own words');
  saved.dispose();
});

test('a saved view is read by its id, as Customize Home names it', async () => {
  const s = views();
  const {readPersonalResource} = await import('../src/personal-saved.ts');
  const row = await readPersonalResource(s.api as never, scope, 'v1');
  assert.deepEqual([row.id, row.kind, row.status, row.definition?.libraryId], ['v1', 'view', 'ready', 'films']);
  assert.deepEqual(s.calls.map(call => call.path), ['/v1/lists/v1']);
});

test('pinning a collection or a saved view goes into the profile\'s pins setting, and the page shows it pinned', async () => {
  for (const [fixture, resourceId, kind] of [[lists(), 'col', 'collection'], [views(), 'v1', 'view']] as const) {
    const saved = service(fixture.api);
    await saved.select({view: 'resource', resourceId} as never);
    assert.deepEqual([saved.getSnapshot().resource?.pinned, saved.getSnapshot().resource?.actions.includes('pin')], [false, true], kind);
    assert.equal(await saved.mutate({action: 'pin', pinned: true}), true, kind);
    assert.deepEqual([...(fixture as {pins?: string[]}).pins ?? []], [`${kind}:${resourceId}`], 'the list itself is not changed: only the profile\'s pins');
    assert.equal(saved.getSnapshot().resource?.pinned, true, 'read again, the page shows it pinned');
    assert.equal(await saved.mutate({action: 'pin', pinned: false}), true);
    assert.equal(saved.getSnapshot().resource?.pinned, false);
    saved.dispose();
  }
});

test('collection and saved-view cards say whether the profile pinned them', async () => {
  const s = lists();
  s.pins.push('collection:their');
  const saved = service(s.api);
  await saved.select({view: 'collections'} as never);
  assert.deepEqual(saved.getSnapshot().resources.map(r => [r.id, r.pinned]), [['col', false], ['their', true]]);
  saved.dispose();
  const v = views();
  v.pins.push('view:v2');
  const viewsPage = service(v.api);
  await viewsPage.select({view: 'views'} as never);
  assert.deepEqual(viewsPage.getSnapshot().resources.map(r => [r.id, r.pinned]), [['v1', false], ['v2', true]]);
  viewsPage.dispose();
});

test('when the profile\'s pins cannot be read, a collection or saved view still opens, without Pin', async () => {
  for (const [fixture, resourceId] of [[lists(), 'col'], [views(), 'v1']] as const) {
    const api = {request: (path: string, method?: string, body?: unknown) => new URL(path, 'https://x').pathname === '/v1/preferences'
      ? Promise.reject(Object.assign(new Error('down'), {status: 503, code: 'unavailable'})) : fixture.api.request(path, method, body)};
    const saved = service(api);
    await saved.select({view: 'resource', resourceId} as never);
    const shot = saved.getSnapshot();
    assert.equal(shot.error, null, resourceId);
    assert.deepEqual([shot.resource?.id, shot.resource?.pinned, shot.resource?.actions.includes('pin')], [resourceId, false, false]);
    saved.dispose();
  }
});
