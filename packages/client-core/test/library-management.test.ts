import test from 'node:test';
import assert from 'node:assert/strict';
import {LibraryManagementService} from '../src/library-management.ts';

// The owner's Libraries page on the rebuilt server: `GET /v1/admin/libraries` and `/{id}`, a scan, its Cancel,
// a rename and adding a library, as the server's own routes answer them.
const scope = {serverId: 'server', viewerId: 'owner'};
const library = (id = 'lib_one', more: Record<string, unknown> = {}): any => ({id, name: 'Films', kind: 'movies', revision: '3',
  folders: [{path: '/media/' + id, available: true, watched: true, scanEveryMinutes: 0, lastScanAt: '2026-11-12T01:00:00Z'}],
  options: {depth: 'basic', trash: {mode: 'days', days: 30}, allowDelete: false, rescanMinutes: 0}, scanning: false,
  lastScanStartedAt: '2026-11-12T00:59:00Z', lastScanFinishedAt: '2026-11-12T01:00:00Z', ...more});
type Call = {path: string; method: string; body?: unknown};
function server(answer: (call: Call) => unknown = () => undefined) {
  const calls: Call[] = [];
  const api = {request: async <T,>(path: string, method = 'GET', body?: unknown): Promise<T> => {
    const call = {path, method, ...(body === undefined ? {} : {body})}; calls.push(call);
    const custom = answer(call); if (custom instanceof Error) throw custom; if (custom !== undefined) return custom as T;
    const u = new URL(path, 'https://x');
    if (method !== 'GET') return (method === 'PATCH' ? library('lib_one', {name: (body as {name: string}).name}) : {id: 'job_1', kind: 'scan', state: 'waiting'}) as T;
    if (u.pathname === '/v1/admin/libraries') return {items: [library()]} as T;
    return library(decodeURIComponent(u.pathname.split('/').at(-1)!)) as T;
  }};
  return {api, calls};
}
const made: LibraryManagementService[] = [];
test.afterEach(() => { for (const s of made.splice(0)) s.dispose(); });
const make = (api: ReturnType<typeof server>['api']) => { const s = new LibraryManagementService({scope, api, readRetries: 1, retryBaseMs: 1}); made.push(s); return s; };

test('the list is the rebuilt server\'s libraries in the page\'s shape: kind, folder and whether it was found, last scan', async () => {
  const {api, calls} = server(call => call.path.startsWith('/v1/admin/libraries') && call.method === 'GET' && !call.path.includes('lib_') ? {items: [
    library('lib_one'),
    library('lib_two', {kind: 'shows', scanning: true, folders: [{path: '/media/tv', available: false, problem: 'not mounted', watched: true, scanEveryMinutes: 0}]}),
    library('lib_three', {kind: 'holograms'}),
  ], nextCursor: 'c2'} : undefined);
  const s = make(api);
  await s.loadLibraries();
  const page = s.getSnapshot().directory;
  assert.equal(page.phase, 'ready');
  assert.deepEqual(new URL(calls[0].path, 'https://x').pathname, '/v1/admin/libraries');
  const [films, shows] = page.data!.items;
  assert.equal(page.data!.items.length, 2, 'a kind this app does not know is left out');
  assert.equal(page.data!.nextCursor, 'c2');
  assert.deepEqual([films.kind, films.source.path, films.source.availability, films.lastScan?.status, films.revision], ['movie', '/media/lib_one', 'available', 'complete', 3]);
  assert.deepEqual([shows.kind, shows.source.availability, shows.source.message, shows.lastScan?.status, shows.lastScan?.actions], ['tv', 'unavailable', 'not mounted', 'running', ['cancel']]);
  assert.deepEqual(films.actions, ['rename', 'scan', 'remove']);
  await s.nextLibraries();
  assert.equal(new URL(calls.at(-1)!.path, 'https://x').searchParams.get('cursor'), 'c2');
});

test('a library is read by its own address; scan, Scan all files, Cancel and rename use the server\'s routes', async () => {
  let scanning = false;
  const {api, calls} = server(call => call.method === 'GET' && call.path === '/v1/admin/libraries/lib_one' ? library('lib_one', {scanning}) : undefined);
  const s = make(api);
  await s.selectLibrary('lib_one');
  assert.equal(s.getSnapshot().selected.data?.library.name, 'Films');
  const done = () => assert.equal(s.getSnapshot().mutation.phase, 'complete', JSON.stringify(s.getSnapshot().mutation));
  await s.scan(); done();
  await s.scan('full'); done();
  scanning = true;
  await s.selectLibrary('lib_one');
  const running = s.getSnapshot().selected.data!.library.lastScan!;
  await s.cancelJob(running.id); done();
  await s.rename('Movies', s.getSnapshot().selected.data!.library.revision); done();
  assert.deepEqual(calls.filter(c => c.method !== 'GET').map(c => [c.method, c.path, c.body]), [
    ['POST', '/v1/admin/libraries/lib_one/scan', {}],
    ['POST', '/v1/admin/libraries/lib_one/scan', {allFiles: true}],
    ['DELETE', '/v1/admin/libraries/lib_one/scan', undefined],
    ['PATCH', '/v1/admin/libraries/lib_one', {name: 'Movies'}],
  ]);
});

test('a new library is created with the server\'s word for its kind and every chosen folder', async () => {
  const {api, calls} = server(call => call.method === 'POST' && call.path === '/v1/admin/libraries' ? library('lib_new') : undefined);
  const s = make(api);
  await s.createLibrary({name: ' Shows ', kind: 'tv', folders: ['/media/tv/', '/mnt/more']});
  assert.deepEqual(calls.find(c => c.method === 'POST')?.body, {name: 'Shows', kind: 'shows', folders: ['/media/tv', '/mnt/more']});
  assert.equal(s.getSnapshot().mutation.phase, 'complete');
  assert.equal(s.getSnapshot().directory.phase, 'ready', 'the list is read again after it');
});

test('a 503 on the list is tried again, then shows the error; a refused owner keeps nothing', async () => {
  let failures = 1;
  const flaky = server(call => call.method === 'GET' && failures-- > 0 ? Object.assign(new Error('busy'), {status: 503}) : undefined);
  const s = make(flaky.api);
  await s.loadLibraries();
  assert.equal(s.getSnapshot().directory.phase, 'ready');
  assert.equal(flaky.calls.length, 2);
  const refused = server(() => Object.assign(new Error('no'), {status: 403, code: 'forbidden'}));
  const r = make(refused.api);
  await r.loadLibraries().catch(() => undefined);
  assert.equal(r.getSnapshot().directory.phase, 'error');
  assert.equal(r.getSnapshot().directory.data, null);
});

test('while a scan runs the list and the selected library are read again, and no longer once it has ended', async () => {
  let scanning = true, reads = 0;
  const {api} = server(call => {
    if (call.method !== 'GET') return undefined;
    reads++;
    return call.path.startsWith('/v1/admin/libraries/lib_one') ? library('lib_one', {scanning}) : {items: [library('lib_one', {scanning})]};
  });
  const s = new LibraryManagementService({scope, api, readRetries: 1, retryBaseMs: 1, scanRecheckMs: 5}); made.push(s);
  await s.loadLibraries(); await s.selectLibrary('lib_one');
  assert.equal(s.getSnapshot().directory.data?.items[0].lastScan?.status, 'running');
  scanning = false;
  for (let n = 0; n < 50 && s.getSnapshot().selected.data?.library.lastScan?.status !== 'complete'; n++) await new Promise(r => setTimeout(r, 5));
  assert.equal(s.getSnapshot().directory.data?.items[0].lastScan?.status, 'complete');
  assert.equal(s.getSnapshot().selected.data?.library.lastScan?.status, 'complete');
  await new Promise(r => setTimeout(r, 20)); // a read already under way when the scan ended finishes
  const settled = reads;
  await new Promise(r => setTimeout(r, 30));
  assert.equal(reads, settled, 'nothing more is read once no scan runs');
  s.dispose();
});

test('while the tab is out of sight a running scan is not read again; once shown it is', async () => {
  let hidden = true, reads = 0;
  const {api} = server(call => { if (call.method === 'GET') reads++; return call.method === 'GET' ? {items: [library('lib_one', {scanning: true})]} : undefined; });
  const s = new LibraryManagementService({scope, api, readRetries: 1, retryBaseMs: 1, scanRecheckMs: 5, hidden: () => hidden}); made.push(s);
  await s.loadLibraries();
  const first = reads;
  await new Promise(r => setTimeout(r, 40));
  assert.equal(reads, first, 'a hidden tab reads nothing');
  hidden = false;
  for (let n = 0; n < 50 && reads === first; n++) await new Promise(r => setTimeout(r, 5));
  assert.ok(reads > first, 'shown again, the scan is followed');
});
