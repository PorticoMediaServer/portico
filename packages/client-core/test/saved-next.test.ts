// Saved on the rebuilt server: what SavedService reads and sends, and that the screens' own checks accept it.
import test from 'node:test';
import assert from 'node:assert/strict';
import {SavedService} from '../src/saved.ts';
import {readPlaylistOrder, readShareCandidates, sendPlaylistCommand} from '../src/saved-next.ts';

import {list, nextSavedServer as server, refused, title} from './fixtures/next-saved-server.ts';

const scope = {serverId: 'server', viewerId: '["local","account","profile"]'};
const service = (api: unknown) => new SavedService({scope, api: api as never, requestId: async () => '00000000-0000-4000-8000-000000000001', timeoutMs: 1000});

test('My List is read from the rebuilt server with real filter and shape counts, and its page passes the Saved checks', async () => {
  const s = server(), saved = service(s.api);
  await saved.select({view: 'watchlist', filter: 'unwatched', shape: 'posters', sort: 'title', direction: 'asc'});
  const shot = saved.getSnapshot();
  assert.equal(shot.phase, 'ready', JSON.stringify(shot.error));
  assert.deepEqual(shot.projection!.sections[0]!.entries.map(entry => entry.id), ['one', 'two']);
  assert.deepEqual(shot.projection!.filters[0]!.options.map(option => [option.id, option.count]), [['all', 5], ['unwatched', 5], ['inProgress', 5], ['watched', 5], ['favorites', 5]]);
  assert.deepEqual(shot.projection!.shapeCounts, {posters: 5, squares: 5});
  const page = new URL(s.calls.find(call => new URL(call.path, 'https://x').searchParams.get('limit') === '40')!.path, 'https://x').searchParams;
  assert.deepEqual([page.get('inList'), page.get('watched'), page.get('sort'), page.get('order'), page.get('kind')], ['true', 'false', 'title', 'asc', 'movie,show,season,episode,audiobook,book,comic,series,collection']);
  saved.dispose();
});

test('a playlist page lists its entries in its own order with the last entry, and the playlist says what its maker may do', async () => {
  const s = server(), saved = service(s.api);
  await saved.select({view: 'playlist', playlistId: 'pl'});
  const shot = saved.getSnapshot();
  assert.equal(shot.phase, 'ready', JSON.stringify(shot.error));
  assert.deepEqual(shot.projection!.sections[0]!.entries.map(entry => entry.id), ['e1', 'e2', 'e3'], 'a title twice is two entries');
  assert.deepEqual(shot.projection!.sections[0]!.order, {last: 'e3'});
  assert.deepEqual([shot.playlist!.revision, shot.playlist!.role, shot.playlist!.actions.includes('pin'), shot.playlist!.pinned], [7, 'owner', true, false]);
  assert.deepEqual(shot.playlist!.shares?.map(share => [share.profileId, share.role, share.displayName]), [['kid', 'viewer', 'Kid']]);
  saved.dispose();
});

test('the playlists list holds the profile\'s own and shared ones, and Pinned waits for the pins setting', async () => {
  const s = server(), saved = service(s.api);
  await saved.select({view: 'playlists', filter: 'mine'});
  assert.deepEqual(saved.getSnapshot().projection!.sections[0]!.entries.map(entry => entry.id), ['pl']);
  await saved.select({view: 'playlists', filter: 'pinned'});
  assert.equal(saved.getSnapshot().error?.code, 'not_available');
  saved.dispose();
});

test('an edit names the list\'s revision, and one on an out-of-date copy is refused as "This list changed"', async () => {
  const s = server();
  await sendPlaylistCommand(s.api, scope, {action: 'rename', name: ' Long drive '}, {playlistId: 'pl', revision: 7}, 'op');
  assert.deepEqual(s.calls.at(-1), {path: '/v1/lists/pl', method: 'PATCH', body: {name: 'Long drive'}, headers: {'If-Match': '"7"'}});
  const stale = {request: s.api.request, requestRaw: async () => ({status: 412, headers: {}, body: {error: {code: 'list_changed', message: 'This list changed. Reload and try again.'}}})};
  await assert.rejects(sendPlaylistCommand(stale, scope, {action: 'delete'}, {playlistId: 'pl', revision: 6}, 'op'), {code: 'list_changed', status: 412});
});

test('an edit through a transport that cannot send headers is refused before anything is sent', async () => {
  // The Saved screens wrap the app's transport; a wrapper that dropped `requestRaw` must not let an edit go without the
  // list's revision, where an edit on an out-of-date copy would overwrite.
  const sent: {path: string; method: string; body: unknown}[] = [];
  const withoutHeaders = {request: async <T>(path: string, method = 'GET', body?: unknown): Promise<T> => {
    sent.push({path, method, body});
    return {id: 'pl', kind: 'playlist', name: 'Long drive', role: 'owner', count: 0, covers: [], revision: '8'} as T;
  }};
  await assert.rejects(sendPlaylistCommand(withoutHeaders, scope, {action: 'rename', name: 'Long drive'}, {playlistId: 'pl', revision: 7}, 'op'), {code: 'saved_unsupported'});
  assert.deepEqual(sent, [], 'nothing may be sent without the revision');
  const s = server();
  await assert.rejects(sendPlaylistCommand(s.api, scope, {action: 'rename', name: 'Long drive'}, {playlistId: 'pl'}, 'op'), {code: 'list_changed'}, 'an edit with no revision to name');
  assert.deepEqual(s.calls, []);
});

test('an edit refused because the list changed reloads the list, says so, and Retry names the fresh revision', async () => {
  let revision = '7', name = 'Road trip';
  const s = server({
    'GET /v1/lists/pl': () => list({revision, name}),
    'PATCH /v1/lists/pl': call => {
      if (call.headers?.['If-Match'] !== `"${revision}"`) return refused(412, 'list_changed');
      revision = String(Number(revision) + 1); name = call.body.name; return list({revision, name});
    },
  }), saved = service(s.api);
  await saved.select({view: 'playlist', playlistId: 'pl'});
  revision = '8'; // someone else changed it
  const reads = () => s.calls.filter(call => call.method === 'GET' && call.path === '/v1/lists/pl').length, before = reads();
  await assert.rejects(saved.mutate({action: 'rename', name: 'Long drive'}), {code: 'list_changed'});
  const shot = saved.getSnapshot();
  assert.equal(reads(), before + 1, 'the list is read again');
  assert.deepEqual([shot.playlist!.revision, shot.mutationError?.code, shot.mutationError?.message], [8, 'list_changed', 'This list changed. Reload and try again.']);
  await saved.retryMutation();
  assert.deepEqual(s.calls.filter(call => call.method === 'PATCH').map(call => call.headers?.['If-Match']), ['"7"', '"8"'], 'Retry starts from the fresh revision');
  assert.equal(name, 'Long drive', 'the retried rename landed');
  saved.dispose();
});

test('a reorder that stops half way shows the list as the server now has it, and Retry starts from there', async () => {
  // Entries e1 e2 e3; moving [e3, e2] to the front: e3 lands, then the second move fails.
  let order = ['e1', 'e2', 'e3'], revision = 7, fail = true;
  const titles: Record<string, string> = {e1: 'a', e2: 'b', e3: 'c'};
  const s = server({
    'GET /v1/lists/pl': () => list({revision: String(revision)}),
    'GET /v1/items': (_call, url) => url.searchParams.get('list') === 'pl'
      ? {items: (url.searchParams.get('order') === 'desc' ? order.slice(-1) : order).map(entry => title(titles[entry]!, {entryId: entry})), total: 3}
      : {items: [], total: 0},
  });
  s.api.requestRaw = (async (path: string, method: string, options: {headers?: Record<string, string>; body?: any} = {}) => {
    s.calls.push({path, method, body: options.body, headers: options.headers});
    if (options.headers?.['If-Match'] !== `"${revision}"`) return {status: 412, headers: {}, body: {error: {code: 'list_changed', message: 'This list changed. Reload and try again.'}}};
    const moved = path.split('/').pop()!;
    if (order.indexOf(moved) > 0 && moved === 'e2' && fail) {fail = false; return {status: 503, headers: {}, body: {error: {code: 'unavailable', message: 'Try again.', retryable: true}}};}
    order = order.filter(entry => entry !== moved);
    order.splice(options.body.before ? order.indexOf(options.body.before) : order.indexOf(options.body.after) + 1, 0, moved);
    revision++; return {status: 200, headers: {}, body: list({revision: String(revision)})};
  }) as typeof s.api.requestRaw;
  const saved = service(s.api);
  await saved.select({view: 'playlist', playlistId: 'pl'});
  await assert.rejects(saved.mutate({action: 'reorder', entryIds: ['e3', 'e2'], afterEntryId: ''}));
  const shown = () => saved.getSnapshot().projection!.sections[0]!.entries.map(entry => entry.id);
  assert.deepEqual([shown(), saved.getSnapshot().playlist!.revision], [['e3', 'e1', 'e2'], 8], 'the first move landed and the page shows it');
  await saved.retryMutation();
  assert.deepEqual(order, ['e3', 'e2', 'e1']);
  assert.deepEqual(s.calls.filter(call => call.method === 'PATCH').map(call => call.headers?.['If-Match']), ['"7"', '"8"', '"8"', '"9"'], 'Retry names the revision the page was reloaded at');
  saved.dispose();
});

test('removing an entry offers an Undo that puts its title back after the entry in front of it', async () => {
  const s = server(), entries = [{id: 'e1', itemId: 'a'}, {id: 'e2', itemId: 'b'}, {id: 'e3', itemId: 'a'}];
  const removed = await sendPlaylistCommand(s.api, scope, {action: 'remove', entryId: 'e2'}, {playlistId: 'pl', revision: 7, entries}, 'op');
  assert.equal(s.calls.at(-1)!.path, '/v1/lists/pl/entries/e2'); assert.equal(s.calls.at(-1)!.method, 'DELETE');
  assert.ok(Date.parse(removed.undoUntil as string) - Date.now() <= 30_000);
  await sendPlaylistCommand(s.api, scope, {action: 'restore', undoToken: removed.undoToken as string}, {playlistId: 'pl'}, 'op2');
  assert.deepEqual(s.calls.at(-1)!.body, {items: ['b'], after: 'e1'});
  await assert.rejects(sendPlaylistCommand(s.api, scope, {action: 'restore', undoToken: 'nonsense'}, {playlistId: 'pl'}, 'op3'), {code: 'undo_expired'});
});

test('an Undo goes back only into the list it came from', async () => {
  const s = server(), entries = [{id: 'e1', itemId: 'a'}, {id: 'e2', itemId: 'b'}];
  const removed = await sendPlaylistCommand(s.api, scope, {action: 'remove', entryId: 'e2'}, {playlistId: 'pl', revision: 7, entries}, 'op');
  const sent = s.calls.length;
  await assert.rejects(sendPlaylistCommand(s.api, scope, {action: 'restore', undoToken: removed.undoToken as string}, {playlistId: 'other'}, 'op2'), {code: 'undo_other_list'});
  assert.equal(s.calls.length, sent, 'nothing is added to the other list');
});

test('an addition whose answer was lost is sent again with the same Idempotency-Key, so the title is not added twice', async () => {
  const s = server(), raw = s.api.requestRaw;
  let lose = true;
  s.api.requestRaw = (async (path: string, method: string, options: {headers?: Record<string, string>; body?: unknown} = {}) => {
    const answer = await raw(path, method, options);
    if (method === 'POST' && lose) {lose = false; throw new TypeError('The network connection was lost.');}
    return answer;
  }) as typeof raw;
  const saved = service(s.api);
  await saved.select({view: 'playlist', playlistId: 'pl'});
  await assert.rejects(saved.mutate({action: 'add', itemId: 'z'}));
  await saved.retryMutation();
  const posts = s.calls.filter(call => call.method === 'POST');
  assert.equal(posts.length, 2);
  assert.ok(posts[0]!.headers?.['Idempotency-Key']);
  assert.equal(posts[1]!.headers?.['Idempotency-Key'], posts[0]!.headers?.['Idempotency-Key'], 'the repeat names the same operation');
  // Restore and create name their operation too.
  const entries = [{id: 'e1', itemId: 'a'}, {id: 'e2', itemId: 'b'}];
  const removed = await sendPlaylistCommand(s.api, scope, {action: 'remove', entryId: 'e2'}, {playlistId: 'pl', revision: 7, entries}, 'remove-op');
  await sendPlaylistCommand(s.api, scope, {action: 'restore', undoToken: removed.undoToken as string}, {playlistId: 'pl'}, 'restore-op');
  await sendPlaylistCommand(s.api, scope, {action: 'create', name: 'New'}, {}, 'create-op');
  assert.deepEqual(s.calls.slice(-2).map(call => [call.path, call.headers?.['Idempotency-Key']]), [['/v1/lists/pl/entries', 'restore-op'], ['/v1/lists', 'create-op']]);
  saved.dispose();
});

test('a move places entries after an anchor, at the front, or swaps two neighbours in place', async () => {
  const entries = [{id: 'e1', itemId: 'a'}, {id: 'e2', itemId: 'b'}, {id: 'e3', itemId: 'c'}];
  const moves = async (intent: {entryIds: string[]; afterEntryId?: string}) => {
    const s = server(); await sendPlaylistCommand(s.api, scope, {action: 'reorder', ...intent}, {playlistId: 'pl', revision: 7, entries}, 'op');
    return s.calls.map(call => [call.path.split('/').pop(), call.body, call.headers?.['If-Match']]);
  };
  assert.deepEqual(await moves({entryIds: ['e3'], afterEntryId: 'e1'}), [['e3', {after: 'e1'}, '"7"']]);
  assert.deepEqual(await moves({entryIds: ['e3'], afterEntryId: ''}), [['e3', {before: 'e1'}, '"7"']]);
  assert.deepEqual(await moves({entryIds: ['e3', 'e2']}), [['e3', {after: 'e1'}, '"7"'], ['e2', {after: 'e3'}, '"8"']], 'each move names the revision the one before answered');
});

test('sharing sets who a playlist is shared with, keeping the others; a pin goes into the profile\'s pins setting', async () => {
  const s = server();
  await sendPlaylistCommand(s.api, scope, {action: 'share', actor: {authority: 'local', accountId: 'gran', profileId: 'gran'}, role: 'editor'}, {playlistId: 'pl', revision: 7}, 'op');
  assert.deepEqual(s.calls.at(-1)!.body, {shares: [{profileId: 'kid', canEdit: false}, {profileId: 'gran', canEdit: true}]});
  await sendPlaylistCommand(s.api, scope, {action: 'unshare', actor: {authority: 'local', accountId: 'kid', profileId: 'kid'}}, {playlistId: 'pl', revision: 7}, 'op');
  assert.deepEqual(s.calls.at(-1)!.body, {shares: []});
  const pinned = await sendPlaylistCommand(s.api, scope, {action: 'pin', pinned: true}, {playlistId: 'pl', revision: 7}, 'op');
  assert.deepEqual([s.pins, s.calls.at(-1)!.path, pinned.revision], [['playlist:pl'], '/v1/preferences', 7], 'the playlist itself is unchanged');
  await sendPlaylistCommand(s.api, scope, {action: 'pin', pinned: false}, {playlistId: 'pl', revision: 7}, 'op');
  assert.deepEqual(s.pins, []);
  const created = await sendPlaylistCommand(s.api, scope, {action: 'create', name: 'New', itemIds: ['a']}, {}, 'op');
  assert.deepEqual([s.calls.at(-1)!.path, s.calls.at(-1)!.body], ['/v1/lists', {kind: 'playlist', name: 'New', items: ['a']}]);
  assert.equal(created.playlistId, 'pl');
});

test('the order and the share picker read the playlist\'s own entries and everyone it is not shared with yet', async () => {
  const s = server();
  assert.deepEqual(await readPlaylistOrder(s.api, 'pl', null), {revision: 7, entryIds: ['e1', 'e2', 'e3'], nextCursor: ''});
  const picker = await readShareCandidates(s.api, 'pl', null);
  assert.deepEqual(picker.candidates.map((person: any) => [person.profileId, person.displayName]), [['gran', 'Gran']]);
});

function orderedPlaylist(start: {entryId: string; itemId: string}[]) {
  // A playlist that keeps its order as the server does: read ascending unless `order=desc`; with no `after` an add goes
  // to the end; a move names the entry it goes before; an Idempotency-Key replays its first answer.
  let entries = start, revision = 7, next = start.length + 1;
  const keys = new Map<string, unknown>(), moves: string[] = [];
  const at = (id: string) => entries.findIndex(entry => entry.entryId === id);
  const list = () => ({id: 'pl', kind: 'playlist', name: 'Road trip', owner: {id: 'me', name: 'Me'}, role: 'owner', count: entries.length, covers: [], revision: String(revision), createdAt: '', updatedAt: ''});
  const api = {
    async request<T>(path: string): Promise<T> {
      const q = new URL(path, 'https://x').searchParams, ordered = q.get('order') === 'desc' ? [...entries].reverse() : entries;
      return {items: ordered.slice(0, Number(q.get('limit') ?? 40)).map(entry => ({id: entry.itemId, kind: 'track', title: entry.itemId, entryId: entry.entryId}))} as T;
    },
    async requestRaw(path: string, method: string, options: {headers?: Record<string, string>; body?: any} = {}) {
      const key = options.headers?.['Idempotency-Key'];
      if (method === 'POST') {
        if (key && keys.has(key)) return keys.get(key) as {status: number; headers: Record<string, string>; body: unknown};
        const body = options.body; entries.splice(body.after ? at(body.after) + 1 : entries.length, 0, {entryId: 'e' + next++, itemId: body.items[0]}); revision++;
        const answer = {status: 200, headers: {}, body: {list: list(), added: 1, already: 0}}; if (key) keys.set(key, answer); return answer;
      }
      if (options.headers?.['If-Match'] !== `"${revision}"`) return {status: 412, headers: {}, body: {error: {code: 'list_changed', message: 'changed'}}};
      const entry = decodeURIComponent(path.split('/entries/')[1]!);
      if (method === 'DELETE') {entries = entries.filter(row => row.entryId !== entry); revision++; return {status: 200, headers: {}, body: list()};}
      moves.push(entry); const moving = entries.splice(at(entry), 1)[0]!; entries.splice(at(options.body.before), 0, moving); revision++; return {status: 200, headers: {}, body: list()};
    },
  };
  return {api, order: () => entries.map(entry => entry.itemId), ids: () => entries.map(entry => entry.entryId), moves, shown: () => entries.map(entry => ({id: entry.entryId, itemId: entry.itemId}))};
}

test('Undo of a playlist\'s first entry puts its title back first; a twin of the title stays where it was, and a repeat moves nothing', async () => {
  const twins = orderedPlaylist([{entryId: 'e1', itemId: 'a'}, {entryId: 'e2', itemId: 'a'}, {entryId: 'e3', itemId: 'b'}]);
  const removed = await sendPlaylistCommand(twins.api as never, scope, {action: 'remove', entryId: 'e1'}, {playlistId: 'pl', revision: 7, entries: twins.shown()}, 'op');
  await sendPlaylistCommand(twins.api as never, scope, {action: 'restore', undoToken: removed.undoToken as string}, {playlistId: 'pl'}, 'op2');
  assert.deepEqual([twins.order(), twins.ids()], [['a', 'a', 'b'], ['e4', 'e2', 'e3']], 'the restored copy is first, its twin second');
  await sendPlaylistCommand(twins.api as never, scope, {action: 'restore', undoToken: removed.undoToken as string}, {playlistId: 'pl'}, 'op2');
  assert.deepEqual([twins.ids(), twins.moves.length], [['e4', 'e2', 'e3'], 1], 'a repeat under the same key adds and moves nothing');
});

test('Undo of a playlist\'s first entry puts its title back first, not at the end (area 6 rule 32)', async () => {
  // A playlist that keeps its order: with no `after` the server adds at the end, and a move names the entry it goes before.
  let entries = [{entryId: 'e1', itemId: 'a'}, {entryId: 'e2', itemId: 'b'}, {entryId: 'e3', itemId: 'a'}], revision = 7, next = 4;
  const at = (id: string) => entries.findIndex(entry => entry.entryId === id);
  const list = () => ({id: 'pl', kind: 'playlist', name: 'Road trip', owner: {id: 'me', name: 'Me'}, role: 'owner', count: entries.length, covers: [], revision: String(revision), createdAt: '', updatedAt: ''});
  const api = {
    async request<T>(path: string): Promise<T> {
      const q = new URL(path, 'https://x').searchParams, ordered = q.get('order') === 'desc' ? [...entries].reverse() : entries;
      return {items: ordered.slice(0, Number(q.get('limit') ?? 40)).map(entry => ({id: entry.itemId, kind: 'track', title: entry.itemId, entryId: entry.entryId}))} as T;
    },
    async requestRaw(path: string, method: string, options: {headers?: Record<string, string>; body?: any} = {}) {
      if (method === 'POST') {const body = options.body; entries.splice(body.after ? at(body.after) + 1 : entries.length, 0, {entryId: 'e' + next++, itemId: body.items[0]}); revision++; return {status: 200, headers: {}, body: {list: list(), added: 1, already: 0}};}
      if (options.headers?.['If-Match'] !== `"${revision}"`) return {status: 412, headers: {}, body: {error: {code: 'list_changed', message: 'changed'}}};
      const entry = decodeURIComponent(path.split('/entries/')[1]!);
      if (method === 'DELETE') {entries = entries.filter(row => row.entryId !== entry); revision++; return {status: 200, headers: {}, body: list()};}
      const moving = entries.splice(at(entry), 1)[0]!; entries.splice(at(options.body.before), 0, moving); revision++; return {status: 200, headers: {}, body: list()};
    },
  };
  const shown = entries.map(entry => ({id: entry.entryId, itemId: entry.itemId}));
  const removed = await sendPlaylistCommand(api as never, scope, {action: 'remove', entryId: 'e1'}, {playlistId: 'pl', revision: 7, entries: shown}, 'op');
  await sendPlaylistCommand(api as never, scope, {action: 'restore', undoToken: removed.undoToken as string}, {playlistId: 'pl'}, 'op2');
  assert.deepEqual(entries.map(entry => entry.itemId), ['a', 'b', 'a'], 'the title is back in front, the playlist\'s other copy where it was');
});
