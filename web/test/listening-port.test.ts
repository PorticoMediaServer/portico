import test from 'node:test';
import assert from 'node:assert/strict';
import {PlayerChaptersService} from '@core/player-chapters.ts';
import {LyricsService} from '@core/lyrics.ts';
import type {QueuePlaylistSave} from '@core/playback-v1/queue.ts';
import type {V1PlaybackTarget, V1QueuePolicyIntent} from '@core/playback-v1/v1-queue-player.ts';
import type {PlaybackFence} from '../../packages/player-core/src/index.ts';
import {createWebListeningPort, type WebListeningInput, type WebListeningLifetime, type WebListeningApi} from '../src/platform/playback/listening-port.ts';
import {createQueueEditOpening} from '../src/platform/playback/queue-edit-adapter.ts';
import type {WebQueueListPort, WebQueueListSnapshot} from '../src/platform/playback/queue-list.ts';

const fence = {kind: 'queue', sessionId: 'session', generation: 2, itemId: 'item', entryId: 'entry', sourceId: 'source'} as unknown as PlaybackFence;
const target = {libraryId: 'library', itemId: 'item', sessionId: 'session', sessionGeneration: 2, sourceId: 'source',
  versionId: 'version', playbackRun: 4, queueId: 'queue', entryId: 'entry'};
const scope = {serverId: 'server', viewerId: 'local-owner-profile', libraryId: 'library'};
const chaptersTarget = {libraryId: 'library', itemId: 'item', sessionId: 'session', sessionGeneration: 2, sourceId: 'source'};

function chaptersData(sourceId = 'source') {
  return {scope: {serverId: 'server', libraryId: 'library', itemId: 'item', sessionId: 'session', generation: 2, sourceId, viewerFence: 'fence'},
    revision: 'revision', status: 'available', reason: null, durationMs: 60000,
    chapters: [{id: 'source:1', index: 1, title: 'One', startMs: 0, endMs: 30000, action: {id: 'seek', positionMs: 0}},
      {id: 'source:2', index: 2, title: 'Two', startMs: 30000, endMs: 60000, action: {id: 'seek', positionMs: 30000}}],
    totalCount: 2, nextCursor: ''};
}

function deferred<T>() {let resolve!: (value: T) => void; const promise = new Promise<T>(r => {resolve = r;}); return {promise, resolve};}

type Options = Readonly<{lyricsEnabled?: boolean; queue?: boolean; request?: <T>() => Promise<T>; lyricsRequest?: WebListeningInput['requestLyrics']; captureLyrics?: (service: LyricsService) => void; wrapLyricsApi?: (api: WebListeningApi) => WebListeningApi; onCurrent?: () => void; inspectCommand?: (command: WebListeningLifetime) => void; openEdit?: WebListeningInput['openEdit']; reenterDispose?: boolean; reenterHostRetire?: boolean; debugNoDispatch?: () => void}>;

function harness(options: Options = {}) {
  const seeks: number[] = [], policies: {target: V1PlaybackTarget; revision: string; intent: V1QueuePolicyIntent}[] = [], saves: {queueId: string; name: string}[] = [];
  let current = true, lifetimeCurrent = true, disposed = false, port: ReturnType<typeof createWebListeningPort>;
  const lifetimeController = new AbortController();
  const snapshot: WebQueueListSnapshot = Object.freeze({current: true, queueId: 'queue', revision: 'queue-revision', currentEntryId: 'entry', entries: Object.freeze([]), more: null, change: 'idle'});
  const calls: string[] = [];
  const rawQueue = options.queue === false ? undefined : ({
    getSnapshot: () => snapshot, subscribe: () => () => {}, isCurrent: () => true,
    play: async () => {calls.push('play');}, move: async () => {calls.push('move');}, remove: async () => {calls.push('remove');},
  } as WebQueueListPort);
  let observed: (() => void) | undefined;
  const lifetime: WebListeningLifetime = {signal: lifetimeController.signal, isCurrent: () => {
    if (options.reenterDispose && disposed) return true;
    if (options.reenterDispose) {port?.dispose(); disposed = true; return true;}
    return lifetimeCurrent;
  }};
  const transports = {request: 0, requestLyrics: 0};
  const request = <T,>() => {transports.request++; return options.request ? options.request<T>() : Promise.resolve(chaptersData() as T);};
  const requestLyrics: WebListeningInput['requestLyrics'] = async <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => {transports.requestLyrics++; return options.lyricsRequest ? options.lyricsRequest<T>(path, method, body, signal) : {} as T;};
  const input: WebListeningInput = {fence, lifetime, target,
    current: () => {options.onCurrent?.(); if (options.reenterHostRetire) {lifetimeController.abort(); return true;} return current;}, // retires the host, still true
    request, requestLyrics,
    chaptersTarget, makeChapters: api => new PlayerChaptersService({api, scope, pageSize: 100, timeoutMs: 1000}),
    makeLyrics: api => {const service = new LyricsService(options.wrapLyricsApi?.(api) ?? api, scope, {libraryId: 'library', itemId: 'item', sessionId: 'session', sessionGeneration: 2, sourceId: 'source'}); options.captureLyrics?.(service); return service;},
    lyricsEnabled: () => options.lyricsEnabled ?? true, observe: retired => {observed = retired; return () => {observed = undefined;};},
    ...(rawQueue ? {queue: rawQueue} : {}),
    policy: async (expected, revision, intent, command) => {options.inspectCommand?.(command); if (!command.isCurrent() || command.signal.aborted) throw new Error('stale_listening_port'); policies.push({target: expected, revision, intent});},
    save: async (queueId, name, command) => {if (!command.isCurrent() || command.signal.aborted) throw new Error('stale_listening_port'); saves.push({queueId, name});
      return {playlistId: 'playlist', revision: '1', entries: 3, state: 'saved'} as QueuePlaylistSave;},
    seek: async (seconds, command) => {if (!command.isCurrent() || command.signal.aborted) throw new Error('stale_listening_port');
      options.debugNoDispatch?.(); seeks.push(seconds);},
    openEdit: options.openEdit ?? (() => createQueueEditOpening({current: () => current, lifetime,
      currentEntryId: 'entry', hidden: () => false,
      reader: {read: async () => ({start: 0, total: 1, revision: 'queue-revision', items: [], canPrevious: false}),
        next: async shown => shown, previous: async shown => shown, edit: async shown => shown, dispose: () => {}}})),
  };
  port = createWebListeningPort(input);
  return {port, lifetime, lifetimeController, snapshot, transports, calls, seeks, policies, saves,
    retire: () => {current = false;}, retireLifetime: () => {lifetimeCurrent = false;}, fireRetirement: () => observed?.()};
}

test('the captured record loads and seeks only its own original target, source and drawn row', async () => {
  const h = harness();
  await h.port.chapters.load();
  const snapshot = h.port.chapters.getSnapshot();
  assert.equal(snapshot.target?.sessionId, 'session');
  assert.equal(snapshot.target?.sourceId, 'source');
  await h.port.chapters.seek(snapshot, snapshot.data!.chapters[1], h.lifetime);
  assert.deepEqual(h.seeks, [30]);
  await assert.rejects(() => h.port.chapters.seek(snapshot, {...snapshot.data!.chapters[1], action: {id: 'seek', positionSeconds: 45}}, h.lifetime), /listening_command_unavailable/);
});

test('a chapter response from another source is never adopted as a new source', async () => {
  const h = harness({request: async <T>() => chaptersData('other') as T});
  await h.port.chapters.load();
  assert.equal(h.port.chapters.getSnapshot().data, null);
  assert.notEqual(h.port.chapters.getSnapshot().phase, 'ready');
});

test('a late response after retirement is neither published nor returned', async () => {
  const io = deferred<unknown>();
  const h = harness({request: async <T>() => io.promise as T});
  const loading = h.port.chapters.load();
  // The admitted 'loading' state is published synchronously by the real service
  // before this subscription exists; from here only the retired record could notify.
  let published = 0;
  h.port.chapters.subscribe(() => {published++;});
  h.port.dispose();
  io.resolve(chaptersData());
  await assert.rejects(loading, /stale_listening_port/);
  assert.equal(published, 0);
  assert.equal(h.port.isCurrent(), false);
});

test('runtime retirement callback disposes the record without a manual dispose', async () => {
  const h = harness();
  await h.port.chapters.load();
  assert.equal(h.port.chapters.getSnapshot().phase, 'ready');
  h.retire();          // the actual source/run drifts first, then the runtime notifies
  h.fireRetirement();
  const retired = h.port.chapters.getSnapshot();
  assert.equal(retired.target, null);
  assert.equal(retired.data, null);
  assert.equal(h.port.isCurrent(), false);
  assert.equal(h.port.chapters.getSnapshot(), retired);
});

test('the last external predicate may dispose; the command must not dispatch and nothing retires twice', async () => {
  let dispatched = 0;
  const h = harness({reenterDispose: true, debugNoDispatch: () => {dispatched++;}});
  const shown = h.port.queue!.list.getSnapshot();
  await assert.rejects(() => h.port.queue!.policy(shown, {action: 'repeat', repeat: 'all'}, h.lifetime), /stale_listening_port/);
  assert.equal(h.policies.length, 0);
  assert.equal(dispatched, 0);
  assert.equal(h.port.isCurrent(), false);
});

test('queue policy and save carry the rendered revision and original queue key', async () => {
  const h = harness();
  await h.port.queue!.policy(h.port.queue!.list.getSnapshot(), {action: 'repeat', repeat: 'all'}, h.lifetime);
  assert.equal(h.policies[0].revision, 'queue-revision');
  assert.deepEqual([h.policies[0].target.queueId, h.policies[0].target.versionId, h.policies[0].target.playbackRun], ['queue', 'version', 4]);
  const receipt = await h.port.queue!.save(h.port.queue!.list.getSnapshot(), '  Night drive  ', h.lifetime);
  assert.equal(receipt.state, 'saved');
  assert.deepEqual(h.saves.map(s => [s.queueId, s.name]), [['queue', 'Night drive']]);
});

test('the bound raw queue list refuses after disposal and keeps one stable snapshot', async () => {
  const h = harness();
  const shown = h.port.queue!.list.getSnapshot();
  assert.equal(h.port.queue!.list.getSnapshot(), shown);
  h.port.dispose();
  assert.equal(h.port.queue!.list.getSnapshot(), h.port.queue!.list.getSnapshot());
  assert.equal(h.port.queue!.list.isCurrent(), false);
  await assert.rejects(() => h.port.queue!.list.play(shown, 'entry', h.lifetime), /stale_listening_port/);
  assert.deepEqual(h.calls, []);
});

test('caller cancellation stops the command and suppresses later progress', async () => {
  const h = harness();
  const shown = h.port.queue!.list.getSnapshot();
  h.lifetimeController.abort();
  await assert.rejects(() => h.port.queue!.save(shown, 'Night drive', h.lifetime, () => {throw new Error('published after cancel');}), /stale_listening_port/);
  assert.equal(h.saves.length, 0);
});

test('the runtime predicate that reentrantly retires the host and still answers true cannot dispatch', async () => {
  const h = harness({reenterHostRetire: true});
  await assert.rejects(() => h.port.chapters.load(), /stale_listening_port/);
  assert.equal(h.lifetimeController.signal.aborted, true);                       // the original host lifetime really retired
  assert.equal(h.transports.request + h.transports.requestLyrics, 0);            // no transport was dispatched
  assert.equal(h.port.isCurrent(), false);                                       // latched stale
});

test('lyrics choice refuses when no verified drawn source is available', async () => {
  const h = harness();
  await assert.rejects(() => h.port.lyrics.choose(h.port.lyrics.getSnapshot(), null, h.lifetime), /listening_command_unavailable/);
  assert.equal(h.port.isCurrent(), true);
});

test('one drawer cancelling does not retire the record or another drawer command', async () => {
  const h = harness();
  const other = new AbortController();
  const shown = h.port.queue!.list.getSnapshot();
  const cancelled = {signal: new AbortController().signal, isCurrent: () => false};
  await assert.rejects(() => h.port.queue!.save(shown, 'First', cancelled), /stale_listening_port/);
  assert.equal(h.port.isCurrent(), true);
  await h.port.queue!.save(shown, 'Second', {signal: other.signal, isCurrent: () => true});
  assert.deepEqual(h.saves.map(s => s.name), ['Second']);
});

test('lyrics stays withdrawn when the canonical preference is not true', async () => {
  const h = harness({lyricsEnabled: false});
  await assert.rejects(() => h.port.lyrics.refresh(), /listening_command_unavailable/);
  await assert.rejects(() => h.port.lyrics.choose(h.port.lyrics.getSnapshot(), null, h.lifetime), /listening_command_unavailable/);
});

test('a record without an admitted queue exposes no queue facade at all', () => {
  assert.equal(harness({queue: false}).port.queue, undefined);
});

const lyricResource = {id: 'lyric', revision: 1, scope: 'private' as const, canManage: false, deleted: false, language: 'en', offsetMs: 0,
  format: 'text' as const, digest: 'c'.repeat(64), provenance: {origin: 'upload' as const, providerId: '', label: 'Uploaded', rights: ''}, createdAt: '2026-10-27T00:00:00Z',
  document: {format: 'text' as const, text: 'Original words', lines: [{atMs: null, text: 'Original words'}], embeddedOffsetMs: 0}};
function lyricsData(selected: typeof lyricResource | null = lyricResource, selectionRevision = 1) {
  const source = {id: 'source', version: 'b'.repeat(64), startMs: 0, durationMs: 60000, sourceDurationMs: 60000, local: true};
  return {scope: {serverId: 'server', libraryId: 'library', itemId: 'item', sessionId: 'session', sessionGeneration: 2, viewerFence: 'a'.repeat(64)},
    sources: [source], source, resources: [lyricResource], selection: {revision: selectionRevision, resource: selected}, canShare: false, providers: {lrclib: false, configured: true, revision: 1}};
}
async function lyricsHarness(options: Options = {}) {
  const reads: {path: string; method?: string; body?: unknown; signal: AbortSignal; resolve: (raw: unknown) => void; reject: (error: unknown) => void}[] = [];
  let service!: LyricsService;
  const h = harness({...options, captureLyrics: value => {service = value;}, lyricsRequest: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => {
    assert.ok(signal); return new Promise<T>((resolve, reject) => {reads.push({path, method, body, signal, resolve: raw => resolve(raw as T), reject});});
  }});
  const loading = h.port.lyrics.refresh(); reads[0]!.resolve(lyricsData()); await loading;
  return {...h, reads, service};
}
const lyricTurn = () => new Promise<void>(resolve => setImmediate(resolve));

for (const off of [false, true]) test(`actual facade and real LyricsService accept original ${off ? 'Off' : 'offered row'} through validated readback`, async t => {
  const h = await lyricsHarness(); t.after(() => h.port.dispose());
  const shown = h.port.lyrics.getSnapshot(), row = off ? null : shown.data!.resources[0]!;
  const chosen = h.port.lyrics.choose(shown, row, {signal: new AbortController().signal, isCurrent: () => true});
  assert.equal(h.reads[1]!.method, 'PUT'); assert.deepEqual(h.reads[1]!.body, {sourceId: 'source', sourceVersion: 'b'.repeat(64), sessionId: 'session', expectedSelectionRevision: 1, resourceId: row?.id ?? '', revision: row?.revision ?? 0});
  h.reads[1]!.resolve({revision: 2, resource: off ? null : lyricResource}); await lyricTurn();
  assert.equal(h.reads[2]!.method, 'GET'); h.reads[2]!.resolve(lyricsData(off ? null : lyricResource, 2)); await chosen;
  assert.equal(h.port.lyrics.getSnapshot().data?.selection.revision, 2); assert.equal(h.port.isCurrent(), true);
});

test('actual lyric facade refuses forged shown/row and retired drawer without retiring the record', async t => {
  const h = await lyricsHarness(); t.after(() => h.port.dispose()); const shown = h.port.lyrics.getSnapshot();
  const current = {signal: new AbortController().signal, isCurrent: () => true};
  await assert.rejects(h.port.lyrics.choose({...shown}, shown.data!.resources[0]!, current), /unavailable/);
  await assert.rejects(h.port.lyrics.choose(shown, {...shown.data!.resources[0]!}, current), /unavailable/);
  const controller = new AbortController(); controller.abort();
  await assert.rejects(h.port.lyrics.choose(shown, null, {signal: controller.signal, isCurrent: () => true}), /stale/);
  assert.equal(h.reads.length, 1); assert.equal(h.port.isCurrent(), true); assert.strictEqual(h.port.lyrics.getSnapshot(), shown);
});

for (const phase of ['PUT', 'GET'] as const) for (const sourceRetires of [false, true]) {
 test(`actual facade ${sourceRetires ? 'source' : 'drawer'} retirement during ${phase} suppresses late success/error`, async t => {
  const h = await lyricsHarness(); t.after(() => h.port.dispose()); const controller = new AbortController();
  const chosen = h.port.lyrics.choose(h.port.lyrics.getSnapshot(), null, {signal: controller.signal, isCurrent: () => true});
  const rejected = assert.rejects(chosen);
  if (phase === 'GET') {h.reads[1]!.resolve({revision: 2, resource: null}); await lyricTurn();}
  const held = h.reads.at(-1)!;
  if (sourceRetires) {h.retire(); h.fireRetirement();} else controller.abort();
  await rejected; const after = h.service.getSnapshot(), count = h.reads.length;
  if (phase === 'GET') held.reject(new Error('late old GET')); else held.resolve({revision: 2, resource: null});
  await lyricTurn(); assert.strictEqual(h.service.getSnapshot(), after); assert.equal(h.reads.length, count);
  assert.equal(h.port.isCurrent(), !sourceRetires); assert.equal(held.signal.aborted, true);
 });
}

test('canceling lyric drawer A leaves the same real reader and drawer B command usable', async t => {
  const h = await lyricsHarness(); t.after(() => h.port.dispose());
  let notifications = 0; const unsubscribe = h.port.lyrics.subscribe(() => {notifications++;}); t.after(unsubscribe);
  const a = new AbortController(), b = new AbortController();
  const first = h.port.lyrics.choose(h.port.lyrics.getSnapshot(), null, {signal: a.signal, isCurrent: () => true}); const rejected = assert.rejects(first);
  a.abort(); await rejected; assert.equal(h.port.isCurrent(), true); assert.equal(h.reads[0]!.signal.aborted, false);
  const second = h.port.lyrics.choose(h.port.lyrics.getSnapshot(), null, {signal: b.signal, isCurrent: () => true});
  h.reads[1]!.reject(new Error('late drawer A')); await lyricTurn(); assert.equal(h.reads[2]!.signal.aborted, false);
  h.reads[2]!.resolve({revision: 2, resource: null}); await lyricTurn(); h.reads[3]!.resolve(lyricsData(null, 2)); await second;
  assert.equal(h.port.lyrics.getSnapshot().data?.selection.revision, 2); assert.ok(notifications > 0);
});

for (const phase of ['PUT', 'GET'] as const) test(`final real transport-wrapper host predicate aborts ${phase} drawer then returns true: zero old dispatch`, async t => {
  const drawer = new AbortController(); let method: string | undefined, boundary = false, armed = false, hostCalls = 0;
  const h = await lyricsHarness({
    // Instrument only entry into the REAL composed wrapper; the service and its IO stay real.
    wrapLyricsApi: api => ({...api, requestLyrics: <T,>(path: string, verb?: string, body?: unknown, signal?: AbortSignal) => {
      boundary = true; method = verb; try {return api.requestLyrics<T>(path, verb, body, signal);} finally {boundary = false;}
    }}),
    onCurrent: () => {if (armed && boundary && method === phase && ++hostCalls === 2) drawer.abort();},
  });
  t.after(() => h.port.dispose()); armed = true;
  const chosen = h.port.lyrics.choose(h.port.lyrics.getSnapshot(), null, {signal: drawer.signal, isCurrent: () => true}); const rejected = assert.rejects(chosen);
  if (phase === 'GET') {assert.equal(h.reads[1]!.method, 'PUT'); h.reads[1]!.resolve({revision: 2, resource: null});}
  await rejected; assert.equal(drawer.signal.aborted, true);
  assert.equal(h.reads.length, phase === 'PUT' ? 1 : 2, 'no already-aborted PUT/readback reaches the original transport');
  assert.equal(h.port.isCurrent(), true); assert.equal(h.service.getSnapshot().data?.selection.revision, 1);
  armed = false;
  const other = h.port.lyrics.choose(h.port.lyrics.getSnapshot(), null, {signal: new AbortController().signal, isCurrent: () => true});
  const put = h.reads.at(-1)!; put.resolve({revision: 2, resource: null}); await lyricTurn(); h.reads.at(-1)!.resolve(lyricsData(null, 2)); await other;
  assert.equal(h.port.lyrics.getSnapshot().data?.selection.revision, 2, 'independent drawer B remains usable');
});

for (const predicate of ['caller', 'host'] as const) test(`composed currentness cannot return true after its ${predicate} predicate aborts the caller`, async t => {
  const drawer = new AbortController(); let armed = false, checked = false;
  const h = harness({onCurrent: () => {if (armed && predicate === 'host') drawer.abort();}, inspectCommand: command => {
    armed = true; assert.equal(command.isCurrent(), false); assert.equal(drawer.signal.aborted, true); checked = true;
  }}); t.after(() => h.port.dispose());
  const lifetime = {signal: drawer.signal, isCurrent: () => {if (armed && predicate === 'caller') drawer.abort(); return true;}};
  await assert.rejects(h.port.queue!.policy(h.port.queue!.list.getSnapshot(), {action: 'repeat', repeat: 'all'}, lifetime), /stale/);
  assert.equal(checked, true); assert.equal(h.policies.length, 0); assert.equal(h.port.isCurrent(), true);
});

function registeredEdits(onDispose?: (index: number) => void) {
  const issued: {opening: ReturnType<typeof createQueueEditOpening>; disposals: number; edits: number}[] = [];
  const openEdit: WebListeningInput['openEdit'] = (_shown, lifetime) => {
    const index = issued.length;
    const canonical = Object.freeze({start: 0, total: 2, revision: 'rev', canPrevious: false, items: Object.freeze([
      {entryId: 'entry', itemId: 'item', kind: 'track' as const, title: 'Playing', position: 0, available: true},
      {entryId: 'next', itemId: 'next-item', kind: 'track' as const, title: 'Next', position: 1, available: true},
    ])});
    const row = {opening: undefined as unknown as ReturnType<typeof createQueueEditOpening>, disposals: 0, edits: 0};
    const original = createQueueEditOpening({current: () => true, lifetime, currentEntryId: 'entry', hidden: () => false,
      reader: {read: async () => canonical, next: async () => canonical, previous: async () => canonical, edit: async () => {row.edits++; return canonical;}, dispose: () => {}}});
    row.opening = Object.freeze({...original, dispose: () => {row.disposals++; original.dispose(); onDispose?.(index);}});
    issued.push(row); return row.opening;
  };
  return {issued, openEdit};
}

test('explicit edit disposal unregisters only its own exact capability and preserves original page receipts', async () => {
  const registry = registeredEdits(), h = harness({openEdit: registry.openEdit});
  for (let i = 0; i < 40; i++) {
    const opening = h.port.queue!.edit(h.port.queue!.list.getSnapshot(), h.lifetime);
    await opening.open(); assert.strictEqual(opening.getSnapshot(), registry.issued[i]!.opening.getSnapshot());
    const shown = opening.getSnapshot().page!; await opening.remove(shown, 'next'); assert.equal(registry.issued[i]!.edits, 1);
    opening.dispose(); opening.dispose(); assert.equal(registry.issued[i]!.disposals, 1);
  }
  const live = h.port.queue!.edit(h.port.queue!.list.getSnapshot(), h.lifetime); await live.open();
  h.port.dispose(); assert.ok(registry.issued.every(row => row.disposals === 1), 'whole retirement does not revisit any explicitly disposed registration');
  assert.equal(live.getSnapshot().phase, 'retired'); live.dispose(); assert.equal(registry.issued.at(-1)!.disposals, 1);
});

test('old edit disposal reentry preserves a live sibling and synchronously opened successor until whole-source retirement', async () => {
  let h!: ReturnType<typeof harness>, successor: ReturnType<WebListeningInput['openEdit']> | undefined;
  const registry = registeredEdits(index => {if (index === 0) successor = h.port.queue!.edit(h.port.queue!.list.getSnapshot(), h.lifetime);});
  h = harness({openEdit: registry.openEdit});
  const first = h.port.queue!.edit(h.port.queue!.list.getSnapshot(), h.lifetime), sibling = h.port.queue!.edit(h.port.queue!.list.getSnapshot(), h.lifetime);
  first.dispose(); assert.ok(successor); await sibling.open(); await successor.open();
  assert.equal(registry.issued[0]!.disposals, 1); assert.equal(registry.issued[1]!.disposals, 0); assert.equal(registry.issued[2]!.disposals, 0);
  assert.equal(h.port.isCurrent(), true); h.port.dispose();
  assert.ok(registry.issued.every(row => row.disposals === 1)); assert.equal(sibling.getSnapshot().phase, 'retired'); assert.equal(successor.getSnapshot().phase, 'retired');
});
