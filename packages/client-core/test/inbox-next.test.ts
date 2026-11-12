// The notification inbox on the rebuilt server: one inbox per profile (`GET /v1/notifications?view=`), worded in the app
// from each notice's kind and facts, marked one notice at a time (`PATCH /v1/notifications/{id}`) or all read at once
// (`POST /v1/notifications/read-all`), and read again when the events feed says a notice moved.
import {test} from 'node:test';
import assert from 'node:assert/strict';
import {InboxService} from '../src/inbox.ts';
import {noticeOf} from '../src/notifications-next.ts';
import {serverEventsFrom} from '../src/server-events.ts';
import {FreshnessHub} from '../src/freshness-hub.ts';
import {createViewerQueries, type ViewerQueries} from '../src/query/viewer-queries.ts';

type Call = {path: string; method: string; body?: any; signal?: AbortSignal};
const notice = (id: string, extra: Record<string, unknown> = {}) => ({id, kind: 'message', body: 'Server maintenance tonight at 11.', createdAt: '2026-11-12T10:00:00Z', read: false, archived: false, ...extra});
/** A rebuilt server holding a profile's notices. Writes can be held open or refused; reads can be held open. */
function server(start = [notice('n-1'), notice('n-2')]) {
  const calls: Call[] = [];
  let items = start.map(item => ({...item})), refuse: Error | undefined, holdWrite = false, holdRead = false, nextCursor = '';
  const held: {resolve: () => void; reject: (error: unknown) => void}[] = [], reads: {resolve: () => void}[] = [];
  const page = (view: string, cursor: string | null) => {
    const shown = items.filter(item => view === 'archived' ? item.archived : view === 'unread' ? !item.archived && !item.read : !item.archived);
    return {items: cursor ? [notice('n-9')] : shown, unreadCount: items.filter(item => !item.read && !item.archived).length, ...(nextCursor && !cursor ? {nextCursor} : {})};
  };
  const api = {request: async <T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> => {
    calls.push({path, method, body, signal});
    const url = new URL(path, 'https://x');
    if (method === 'GET' && url.pathname === '/v1/notifications') {
      if (holdRead) await new Promise<void>(resolve => reads.push({resolve}));
      return page(url.searchParams.get('view') ?? 'unread', url.searchParams.get('cursor')) as T;
    }
    if (refuse) {const error = refuse; refuse = undefined; throw error;}
    if (holdWrite) await new Promise<void>((resolve, reject) => held.push({resolve, reject}));
    const one = /^\/v1\/notifications\/([^/]+)$/.exec(url.pathname);
    if (method === 'PATCH' && one) {items = items.map(item => item.id === one[1] ? {...item, ...(body as object)} : item); return undefined as T;}
    if (method === 'POST' && url.pathname === '/v1/notifications/read-all') {items = items.map(item => ({...item, read: true})); return undefined as T;}
    throw new Error(`unexpected ${method} ${path}`);
  }};
  return {api, calls, held, reads, add: (item: ReturnType<typeof notice>) => {items = [item, ...items];}, refuseNext: (error: Error) => {refuse = error;},
    holdWrites: (value: boolean) => {holdWrite = value;}, holdReads: (value: boolean) => {holdRead = value;}, setNextCursor: (value: string) => {nextCursor = value;}};
}
const tick = () => new Promise(resolve => setTimeout(resolve, 5));
const writes = (calls: Call[]) => calls.filter(call => call.method !== 'GET').map(call => [call.method, call.path, call.body]);
const reads = (calls: Call[], view?: string) => calls.filter(call => call.method === 'GET' && (!view || new URL(call.path, 'https://x').searchParams.get('view') === view)).length;
const refused = Object.assign(new Error('not found'), {status: 404, code: 'not_found'});

test('a notice is worded in the app from its kind and facts; a person\'s own words are shown as they are', () => {
  assert.deepEqual([noticeOf(notice('m') as never).title, noticeOf(notice('m') as never).body], ['A message from the server owner', 'Server maintenance tonight at 11.']);
  const remote = noticeOf({id: 'r', kind: 'remote_unreachable', params: {source: 'Cloud films', problem: 'The login was refused.'}, createdAt: '2026-11-12T10:00:00Z', read: false, archived: false});
  assert.deepEqual([remote.title, remote.body, remote.severity], ["Cloud films can't be reached", 'The login was refused.', 'critical']);
  const update = noticeOf({id: 'u', kind: 'update_available', params: {current: '1.2', latest: '1.3'}, createdAt: '2026-11-12T10:00:00Z', read: true, archived: false});
  assert.deepEqual([update.title, update.body, update.read], ['An update is available', 'Portico 1.3 is ready to install. This server runs 1.2.', true]);
  assert.equal(noticeOf({id: 'x', kind: 'something_new', createdAt: '2026-11-12T10:00:00Z', read: false, archived: false}).title, 'Something on your server needs a look', 'a kind this build does not know is said plainly, not dropped');
});

test('the inbox loads a view of the profile\'s notices and the badge counts its unread', async () => {
  const s = server(), service = new InboxService(s.api, () => 'op');
  await service.load('profile', 'unread'); await tick();
  const shot = service.getSnapshot();
  assert.deepEqual([shot.phase, shot.items.map(item => item.id), shot.counts.unread, shot.unread, shot.audiences], ['ready', ['n-1', 'n-2'], 2, 2, ['profile']]);
  await service.load('profile', 'archived');
  assert.equal(reads(s.calls, 'archived'), 1, 'the archived view is its own read');
  service.dispose();
});

test('the administrators\' audience has no inbox of its own: it is empty and asks the server nothing', async () => {
  const s = server(), service = new InboxService(s.api, () => 'op');
  await service.load('account-admin', 'unread'); await tick();
  assert.deepEqual([service.getSnapshot().items.length, reads(s.calls)], [0, 1], 'only the profile badge is read');
  service.dispose();
});

test('a failed read keeps what is on screen; only a first load with nothing is an error', async () => {
  const s = server(), service = new InboxService(s.api, () => 'op');
  const failing = {request: async () => {throw Object.assign(new Error('offline'), {status: 503});}};
  const empty = new InboxService(failing as never, () => 'op');
  await empty.load(); assert.equal(empty.getSnapshot().phase, 'error'); empty.dispose();
  await service.load(); s.api.request = failing.request as never;
  await service.load();
  assert.deepEqual([service.getSnapshot().phase, service.getSnapshot().items.length, !!service.getSnapshot().error], ['ready', 2, true]);
  service.dispose();
});

test('more appends the next page without repeats; without a cursor it asks nothing; a failed one keeps the list', async () => {
  const s = server(); s.setNextCursor('50');
  const service = new InboxService(s.api, () => 'op');
  await service.load(); await service.more();
  assert.deepEqual(service.getSnapshot().items.map(item => item.id), ['n-1', 'n-2', 'n-9']);
  const before = s.calls.length; await service.more(); assert.equal(s.calls.length, before, 'the last page has no cursor');
  service.dispose();
});

test('marking read is immediate, leaves the unread view, and is put back if the server refuses', async () => {
  const s = server(), service = new InboxService(s.api, () => 'op');
  await service.load();
  await service.apply('read', ['n-1']);
  assert.deepEqual(writes(s.calls), [['PATCH', '/v1/notifications/n-1', {read: true}]]);
  assert.deepEqual(service.getSnapshot().items.map(item => item.id), ['n-2']);
  await tick();
  assert.ok(reads(s.calls, 'unread') >= 3, 'the view is read again for the server\'s own counts');
  s.refuseNext(refused);
  await service.apply('read', ['n-2']);
  assert.deepEqual([service.getSnapshot().items.map(item => item.id), !!service.getSnapshot().error], [['n-2'], true]);
  service.dispose();
});

test('archive and unarchive say so on each notice; read-all marks every row at once and is put back if refused', async () => {
  const s = server(), service = new InboxService(s.api, () => 'op');
  await service.load('profile', 'all');
  await service.apply('archive', ['n-1', 'n-2']); await tick();
  assert.deepEqual(writes(s.calls), [['PATCH', '/v1/notifications/n-1', {archived: true}], ['PATCH', '/v1/notifications/n-2', {archived: true}]]);
  const t = server(), other = new InboxService(t.api, () => 'op');
  await other.load(); t.refuseNext(refused);
  await other.readAll();
  assert.deepEqual([writes(t.calls), other.getSnapshot().items.map(item => item.read)], [[['POST', '/v1/notifications/read-all', undefined]], [false, false]]);
  await tick(); await other.readAll();
  assert.deepEqual(writes(t.calls).at(-1), ['POST', '/v1/notifications/read-all', undefined]);
  service.dispose(); other.dispose();
});

test('one change at a time: a repeat or another change sends nothing while one is out, and closing ends it', async () => {
  const s = server(), service = new InboxService(s.api, () => 'op');
  await service.load(); s.holdWrites(true);
  const first = service.apply('read', ['n-1']);
  await tick();
  await service.apply('read', ['n-1']); await service.apply('archive', ['n-2']);
  assert.equal(writes(s.calls).length, 1);
  service.dispose(); s.held.forEach(write => write.resolve()); await first;
  assert.equal(writes(s.calls).length, 1);
});

test('Mark all read pressed while the inbox is busy runs once it is free, never dropped', async () => {
  const s = server(), service = new InboxService(s.api, () => 'op');
  await service.load(); s.holdWrites(true);
  const first = service.apply('read', ['n-1']); await tick();
  const all = service.readAll(); await all;
  assert.equal(writes(s.calls).length, 1, 'held until the change out answers');
  s.holdWrites(false); s.held.forEach(write => write.resolve()); await first; await tick(); await tick();
  assert.deepEqual(writes(s.calls).map(write => write[1]), ['/v1/notifications/n-1', '/v1/notifications/read-all']);
  service.dispose();
});

test('a notice moving on another device re-reads the open view, keeping its rows meanwhile; an idle inbox is not loaded by it', async () => {
  const idle = server(), unopened = new InboxService(idle.api, () => 'op');
  unopened.refresh(); await tick(); assert.equal(idle.calls.length, 0); unopened.dispose();
  const s = server(), service = new InboxService(s.api, () => 'op');
  await service.load(); s.add(notice('n-3')); s.holdReads(true);
  service.refresh(); await tick();
  assert.deepEqual(service.getSnapshot().items.map(item => item.id), ['n-1', 'n-2'], 'the rows stay while it reads');
  s.holdReads(false); s.reads.forEach(read => read.resolve()); await tick(); await tick();
  assert.deepEqual(service.getSnapshot().items.map(item => item.id), ['n-3', 'n-1', 'n-2']);
  service.dispose();
});

test('the events feed\'s notice topic is the inbox\'s change hint', () => {
  const events = serverEventsFrom({id: '7', event: 'change', data: JSON.stringify({topic: 'notice', op: 'created'})} as never);
  assert.deepEqual(events.map(event => event.type), ['notification.changed']);
});

const made: ViewerQueries[] = [];
test.afterEach(() => {for (const queries of made.splice(0)) queries.dispose();});
test('the viewer\'s inboxes share one read of a view, and a view opened again shows at once', async () => {
  const s = server(), queries = createViewerQueries({scope: {serverId: 'server', viewerId: 'viewer'}, hub: new FreshnessHub({settleMs: 0}), api: s.api, gcTimeMs: Infinity});
  made.push(queries);
  const a = new InboxService(s.api, () => 'op', {queries}), b = new InboxService(s.api, () => 'op', {queries});
  await Promise.all([a.load(), b.load()]); await tick();
  const first = reads(s.calls);
  const c = new InboxService(s.api, () => 'op', {queries});
  await c.load(); await tick();
  assert.deepEqual([c.getSnapshot().phase, c.getSnapshot().items.length], ['ready', 2]);
  assert.equal(reads(s.calls), first, 'nothing is read again for a view held in the cache');
  a.dispose(); b.dispose(); c.dispose();
});

test('a notice opens what it is about, from its kind and ref, as the Dashboard\'s alert does; a message has nothing to open', async () => {
  const {notificationIntent} = await import('../src/notification-actions.ts');
  const of = (kind: string, ref?: string) => noticeOf({id: 'x', kind, ...(ref ? {ref} : {}), createdAt: '2026-11-12T10:00:00Z', read: false, archived: false});
  const opens = (kind: string, ref?: string) => of(kind, ref).actions.map(action => [action.label, notificationIntent(action, of(kind, ref))]);
  assert.deepEqual(opens('storage_nearly_full'), [['Storage & backups', {kind: 'server-storage'}]]);
  assert.deepEqual(opens('backup_failed'), [['Storage & backups', {kind: 'server-storage'}]]);
  assert.deepEqual(opens('update_available'), [['Dashboard', {kind: 'dashboard'}]]);
  assert.deepEqual(opens('remote_unreachable', 'src_9'), [['Remote sources', {kind: 'server-remote-sources', sourceId: 'src_9'}]]);
  assert.deepEqual(opens('report_reply', 'rep_4'), [['Open report', {kind: 'own-feedback', reportId: 'rep_4'}]]);
  assert.deepEqual([opens('message'), opens('tool_message'), opens('report_reply'), opens('something_new')], [[], [], [], []], 'a reply without its report has nothing to open');
  assert.equal(of('remote_unreachable', 'src_9').arguments.ref, 'src_9', 'the ref is kept on the notice');
});

test('a change hint that arrives during a write waits for the write, then reads the view once', async () => {
  const s = server(), service = new InboxService(s.api, () => 'op');
  await service.load(); s.holdWrites(true);
  const write = service.apply('read', ['n-1']); await tick();
  const before = reads(s.calls, 'unread');
  service.refresh(); await tick();
  assert.equal(reads(s.calls, 'unread'), before, 'nothing is read while the write is out');
  s.holdWrites(false); s.held.forEach(held => held.resolve()); await write; await tick(); await tick();
  assert.ok(reads(s.calls, 'unread') > before, 'the view is read once the write answers');
  assert.deepEqual(service.getSnapshot().items.map(item => item.id), ['n-2'], 'the read view keeps the change');
  service.dispose();
});
