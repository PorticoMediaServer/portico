import test from 'node:test';
import assert from 'node:assert/strict';
import {createRecordingsController} from '../src/screens/live/recordings-controller.ts';
import type {ChannelApi} from '@core/channel-guide.ts';

type Call = Readonly<{path: string; method: string; body?: unknown; signal?: AbortSignal}>;
type Reply = (call: Call) => unknown;
const hex = (length = 64, character = 'a') => character.repeat(length);
const time = '2026-09-06T12:00:00Z', end = '2026-09-06T13:00:00Z';
/** Padding on the wire is milliseconds; the DVR DTO keeps seconds client-side. */
const wireOptions = {beforeMs: 0, afterMs: 0, priority: 0, retentionDays: 0, episodeLimit: 0};
const recording = (id: string, patch: Record<string, unknown> = {}) => ({id, revision: 1,
  occurrence: {sourceId: hex(48), channelId: hex(64, 'b'), generation: hex(48, 'c'), programmeId: hex(64, 'd')},
  programme: {id: hex(64, 'd'), channelId: hex(64, 'b'), title: 'Airing', start: time, end, lineage: 'provider-id', seriesId: 'series', episodeId: 'episode', newEvidence: 'unknown'},
  options: {...wireOptions}, start: time, end, state: 'scheduled', reason: '', keep: false, itemId: '', libraryId: '',
  coverageStart: '', coverageEnd: '', bytes: 0, ruleId: '', conflicts: [], ...patch});
const pageOf = (recordings: unknown[], nextCursor = '', revision = 1) => ({recordings, rules: [], nextCursor, revision,
  captureAvailable: true, captureUnavailableReason: '', deletionAvailable: true, usage: {bytes: 0, pendingDeleteBytes: 0, recordings: recordings.length}});
const summaryFact = {upcoming: 3, recorded: 12, rules: 2, conflicts: 1, groups: 5, unwatched: 4};
const groupsFact = (order: string, nextCursor: string) => ({revision: 8, nextCursor, groups: [{key: `${order}-one`, title: 'Show', latest: end,
  recordings: 9, unwatched: 2, kept: 1, exceptions: 4, recording: recording(hex(64, '5'))}]});
const storageFact = () => ({policy: {revision: 4, retentionDays: 30, episodeLimit: 10, floorBytes: 1000, capBytes: 2000},
  measurement: {freeBytes: 300, usedBytes: 400, reservedBytes: 50, writeHealthy: true, measuredAt: time},
  pendingDeleteBytes: 5, forecastBytes: 600, forecastHours: 12, forecastDescription: 'Room for a day', captureAvailable: true, warning: ''});
const detailFact = (id: string) => ({recording: recording(id), capacity: {known: true, effective: 2}, overlaps: [], nextOverlap: ''});
const envelope = (result: unknown, serverId = 'server') => ({protocolVersion: '1.0', serverId, result});
const held = <T>() => {
  let resolve!: (value: T) => void, reject!: (failure: unknown) => void;
  const promise = new Promise<T>((done, failed) => { resolve = done; reject = failed; });
  return {promise, resolve, reject};
};
const url = (call: Call) => new URL(call.path, 'http://portico.invalid');
const params = (call: Call) => url(call).searchParams;
const pathname = (call: Call) => url(call).pathname;
const tick = () => new Promise<void>(done => setTimeout(done, 0));

/** The canonical envelope every DVR read has to receive, with a per-test reply. */
function fixtureServer(reply: Reply) {
  const calls: Call[] = [];
  const api: ChannelApi = {
    async request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> {
      const call = Object.freeze({path, method: method ?? 'GET', body, signal});
      calls.push(call);
      const value = await reply(call);
      return (value && typeof value === 'object' && 'protocolVersion' in value ? value : envelope(value)) as T;
    },
  };
  return {api, calls};
}
function controllerFor(api: ChannelApi, overrides: Partial<{requestId: () => Promise<string>; signal: AbortSignal; isCurrent: () => boolean}> = {}) {
  return createRecordingsController({api, serverId: 'server', requestId: async () => hex(48, 'e'),
    signal: new AbortController().signal, isCurrent: () => true, ...overrides});
}

test('construction is inert: no HTTP, no minted receipt and an empty snapshot', () => {
  let receipts = 0;
  const {api, calls} = fixtureServer(() => pageOf([]));
  const controller = createRecordingsController({api, serverId: 'server',
    requestId: async () => { receipts++; return hex(48, 'e'); }, signal: new AbortController().signal, isCurrent: () => true});
  assert.equal(calls.length, 0);
  assert.equal(receipts, 0);
  assert.deepEqual(controller.getSnapshot(), {query: {view: 'upcoming'}, phase: 'idle', page: null, detail: null, storage: null,
    summary: null, groups: null, groupsSort: 'recent', paging: {busy: false, error: '', previous: false, next: false}, accessDenied: false, error: '', mutating: false, commandError: ''});
  controller.dispose();
  assert.equal(calls.length, 0);
  assert.equal(receipts, 0);
});

test('all four destinations read one canonical envelope with the existing DVR query', async () => {
  const upcoming = pageOf([recording(hex(64, '1'))], 'upcoming-next', 3);
  const recorded = pageOf([recording(hex(64, '2'))], '', 4);
  const history = pageOf([recording(hex(64, '3'))], '', 5);
  const detail = detailFact(hex(64, '4'));
  const {api, calls} = fixtureServer(call => {
    if (pathname(call) === '/v1/dvr/summary') return summaryFact;
    if (pathname(call) === '/v1/dvr/groups') return groupsFact('recent', '');
    if (pathname(call) === '/v1/dvr/storage') return storageFact();
    if (pathname(call).startsWith('/v1/dvr/recordings/')) return detail;
    const state = params(call).get('state');
    return state === 'upcoming' ? upcoming : state === 'recorded' ? recorded : history;
  });
  const controller = controllerFor(api);
  try {
    await controller.load({view: 'upcoming'});
    const first = params(calls.at(-1)!);
    assert.equal(first.get('state'), 'upcoming');
    assert.equal(first.get('limit'), '50');
    assert.equal(first.get('sort'), 'recent');
    assert.equal(first.has('cursor'), false);
    assert.deepEqual(controller.getSnapshot().page?.recordings.map(value => value.id), [hex(64, '1')]);
    assert.equal(controller.getSnapshot().page?.nextCursor, 'upcoming-next');
    assert.equal(controller.getSnapshot().phase, 'ready');

    await controller.load({view: 'recorded'});
    assert.equal(params(calls.at(-1)!).get('state'), 'recorded');
    assert.deepEqual(controller.getSnapshot().page?.recordings.map(value => value.id), [hex(64, '2')]);

    await controller.load({view: 'history'});
    assert.equal(params(calls.at(-1)!).get('state'), 'history');
    assert.deepEqual(controller.getSnapshot().page?.recordings.map(value => value.id), [hex(64, '3')]);

    await controller.load({view: 'storage'});
    assert.equal(pathname(calls.at(-1)!), '/v1/dvr/storage');
    assert.deepEqual(controller.getSnapshot().storage, storageFact());
    assert.equal(controller.getSnapshot().page, null);

    await controller.load({view: 'recorded', recordingId: hex(64, '4')});
    assert.equal(pathname(calls.at(-1)!), '/v1/dvr/recordings/' + hex(64, '4'));
    assert.equal(controller.getSnapshot().detail?.recording.id, hex(64, '4'));
    assert.equal(controller.contains(controller.getSnapshot().detail!.recording), true);
    assert.ok(calls.every(call => call.method === 'GET' && call.body === undefined), 'reads never mutate');
    assert.equal('act' in controller, false, 'the door exposes narrow commands, never an arbitrary work port');
    assert.equal(controller.getSnapshot().mutating, false);
    assert.equal(controller.getSnapshot().commandError, '');
  } finally { controller.dispose(); }
});

test('an opaque cursor is carried forward exactly and the reply nextCursor is the only next token', async () => {
  const pages: Record<string, unknown> = {'': pageOf([recording(hex(64, '1'))], 'opaque-two', 1),
    'opaque-two': pageOf([recording(hex(64, '2'))], 'opaque-three', 2), 'opaque-three': pageOf([recording(hex(64, '3'))], '', 3)};
  const {api, calls} = fixtureServer(call => pages[params(call).get('cursor') ?? '']);
  const controller = controllerFor(api);
  try {
    await controller.load({view: 'recorded'});
    assert.equal(params(calls.at(-1)!).has('cursor'), false);
    assert.equal(controller.getSnapshot().page?.nextCursor, 'opaque-two');
    await controller.load({view: 'recorded', cursor: controller.getSnapshot().page!.nextCursor});
    assert.equal(params(calls.at(-1)!).get('cursor'), 'opaque-two');
    assert.equal(controller.getSnapshot().page?.revision, 2);
    await controller.load({view: 'recorded', cursor: 'opaque-three'});
    assert.equal(params(calls.at(-1)!).get('cursor'), 'opaque-three');
    assert.deepEqual(controller.getSnapshot().page?.recordings.map(value => value.id), [hex(64, '3')]);
    assert.equal(controller.getSnapshot().page?.nextCursor, '');
  } finally { controller.dispose(); }
});

test('genuine aggregates are the server counts, never the loaded page', async () => {
  const {api, calls} = fixtureServer(call => pathname(call) === '/v1/dvr/summary' ? summaryFact
    : pathname(call) === '/v1/dvr/groups' ? groupsFact(params(call).get('sort') ?? 'recent', 'group-next')
    : pageOf([recording(hex(64, '1'))], '', 1));
  const controller = controllerFor(api);
  try {
    await controller.load({view: 'recorded'});
    assert.equal(controller.getSnapshot().page?.recordings.length, 1);
    assert.equal(controller.getSnapshot().summary, null, 'a page is not a summary');
    assert.equal(controller.getSnapshot().groups, null);

    const summary = await controller.readSummary();
    assert.deepEqual(summary, summaryFact);
    assert.deepEqual(controller.getSnapshot().summary, summaryFact);
    assert.notEqual(controller.getSnapshot().summary?.recorded, controller.getSnapshot().page?.recordings.length);

    const groups = await controller.readGroups('title');
    assert.equal(params(calls.at(-1)!).get('sort'), 'title');
    assert.equal(params(calls.at(-1)!).has('cursor'), false);
    assert.equal(groups.groups[0].recordings, 9);
    assert.equal(groups.groups[0].unwatched, 2);
    assert.equal(groups.groups[0].kept, 1);
    assert.equal(groups.groups[0].exceptions, 4);
    assert.equal(groups.revision, 8);
    assert.equal(controller.getSnapshot().groupsSort, 'title');
    assert.equal(controller.getSnapshot().groups?.nextCursor, 'group-next');

    await controller.readGroups('title', 'group-next');
    assert.equal(params(calls.at(-1)!).get('cursor'), 'group-next');
    assert.equal(controller.getSnapshot().groups?.nextCursor, 'group-next');
  } finally { controller.dispose(); }
});

test('a replaced group order erases the old order and a late reply cannot restore it', async () => {
  const first = held<unknown>(), second = held<unknown>();
  let served = 0;
  const {api, calls} = fixtureServer(() => (served++ === 0 ? first.promise : second.promise));
  const controller = controllerFor(api);
  try {
    const recent = controller.readGroups('recent');
    const oldest = controller.readGroups('oldest');
    assert.equal(controller.getSnapshot().groups, null);
    second.resolve(groupsFact('oldest', 'oldest-next'));
    await oldest;
    assert.equal(controller.getSnapshot().groups?.nextCursor, 'oldest-next');
    assert.equal(controller.getSnapshot().groupsSort, 'oldest');
    first.resolve(groupsFact('recent', 'recent-next'));
    await assert.rejects(recent, failure => failure instanceof DOMException && failure.name === 'AbortError');
    assert.equal(controller.getSnapshot().groups?.nextCursor, 'oldest-next');
    assert.equal(controller.getSnapshot().groups?.groups[0].key, 'oldest-one');
    assert.equal(calls.length, 2);
    assert.equal(params(calls[0]).get('sort'), 'recent');
    assert.equal(params(calls[1]).get('sort'), 'oldest');
  } finally { controller.dispose(); }
});

test('a superseded view load settles without republishing its page', async () => {
  const upcoming = held<unknown>(), history = held<unknown>();
  let served = 0;
  const {api} = fixtureServer(() => (served++ === 0 ? upcoming.promise : history.promise));
  const controller = controllerFor(api);
  try {
    const first = controller.load({view: 'upcoming'});
    const second = controller.load({view: 'history'});
    history.resolve(pageOf([recording(hex(64, '9'))], '', 2));
    await second;
    upcoming.resolve(pageOf([recording(hex(64, '8'))], '', 1));
    await assert.rejects(first, failure => failure instanceof DOMException && failure.name === 'AbortError');
    const snapshot = controller.getSnapshot();
    assert.equal(snapshot.query.view, 'history');
    assert.equal(snapshot.page?.revision, 2);
    assert.deepEqual(snapshot.page?.recordings.map(value => value.id), [hex(64, '9')]);
  } finally { controller.dispose(); }
});

test('navigation and authority loss retire every fact and refuse further reads', async () => {
  let live = true;
  const {api, calls} = fixtureServer(call => pathname(call) === '/v1/dvr/summary' ? summaryFact
    : pathname(call) === '/v1/dvr/groups' ? groupsFact('recent', '') : pageOf([recording(hex(64, '1'))], 'next', 1));
  const controller = controllerFor(api, {isCurrent: () => live});
  try {
    await controller.load({view: 'recorded'});
    await controller.readSummary();
    await controller.readGroups();
    let noticed = 0;
    controller.subscribe(() => { noticed++; });
    live = false;
    const snapshot = controller.getSnapshot();
    assert.equal(snapshot.phase, 'retired');
    assert.equal(snapshot.page, null);
    assert.equal(snapshot.detail, null);
    assert.equal(snapshot.storage, null);
    assert.equal(snapshot.summary, null);
    assert.equal(snapshot.groups, null);
    assert.ok(noticed <= 1);
    const before = calls.length;
    await assert.rejects(controller.load({view: 'history'}), failure => failure instanceof DOMException && failure.name === 'AbortError');
    await assert.rejects(controller.readSummary(), failure => failure instanceof DOMException && failure.name === 'AbortError');
    await assert.rejects(controller.requestReceipt(), failure => failure instanceof DOMException && failure.name === 'AbortError');
    assert.equal(calls.length, before, 'a retired viewer starts no request');
    assert.equal(controller.contains(recording(hex(64, '1'))), false);
  } finally { controller.dispose(); }
});

test('dispose aborts the in-flight read and erases every fact', async () => {
  let active: AbortSignal | undefined;
  const {api} = fixtureServer(call => {
    active = call.signal;
    return new Promise<unknown>((_done, failed) => {
      const stop = () => failed(new DOMException('aborted', 'AbortError'));
      if (call.signal?.aborted) stop();else call.signal?.addEventListener('abort', stop, {once: true});
    });
  });
  const controller = controllerFor(api);
  const pending = controller.load({view: 'recorded'});
  await tick();
  assert.equal(active?.aborted, false);
  controller.dispose();
  assert.equal(active?.aborted, true, 'disposal aborts the request signal');
  await assert.rejects(pending, failure => failure instanceof DOMException && failure.name === 'AbortError');
  await tick();
  const snapshot = controller.getSnapshot();
  assert.equal(snapshot.phase, 'retired');
  assert.equal(snapshot.page, null);
  assert.equal(snapshot.detail, null);
  assert.equal(snapshot.storage, null);
  assert.equal(snapshot.summary, null);
  assert.equal(snapshot.groups, null);
  assert.equal(controller.subscribe(() => {}) instanceof Function, true);
});

test('a page 403 clears page, detail, storage and both aggregates', async () => {
  let deny = false;
  const {api} = fixtureServer(call => {
    if (deny) throw {status: 403};
    if (pathname(call) === '/v1/dvr/summary') return summaryFact;
    if (pathname(call) === '/v1/dvr/groups') return groupsFact('recent', '');
    return pageOf([recording(hex(64, '1'))], '', 1);
  });
  const controller = controllerFor(api);
  try {
    await controller.load({view: 'recorded'});
    await controller.readSummary();
    await controller.readGroups();
    deny = true;
    await controller.load({view: 'history'});
    const snapshot = controller.getSnapshot();
    assert.equal(snapshot.accessDenied, true);
    assert.equal(snapshot.page, null);
    assert.equal(snapshot.detail, null);
    assert.equal(snapshot.storage, null);
    assert.equal(snapshot.summary, null);
    assert.equal(snapshot.groups, null);
    assert.equal(controller.contains(recording(hex(64, '1'))), false);

    deny = false;
    await controller.load({view: 'recorded'});
    assert.equal(controller.getSnapshot().accessDenied, false);
    assert.equal(controller.getSnapshot().page?.recordings.length, 1);
  } finally { controller.dispose(); }
});

test('a summary 401 clears every fact and fences the aggregate read', async () => {
  let deny = false;
  const {api} = fixtureServer(call => {
    if (deny && pathname(call) === '/v1/dvr/summary') throw {status: 401};
    if (pathname(call) === '/v1/dvr/summary') return summaryFact;
    if (pathname(call) === '/v1/dvr/groups') return groupsFact('recent', '');
    return pageOf([recording(hex(64, '1'))], '', 1);
  });
  const controller = controllerFor(api);
  try {
    await controller.load({view: 'recorded'});
    await controller.readSummary();
    await controller.readGroups();
    deny = true;
    await assert.rejects(controller.readSummary(), failure => failure !== null && typeof failure === 'object' && (failure as {status?: number}).status === 401);
    const snapshot = controller.getSnapshot();
    assert.equal(snapshot.accessDenied, true);
    assert.equal(snapshot.page, null);
    assert.equal(snapshot.detail, null);
    assert.equal(snapshot.storage, null);
    assert.equal(snapshot.summary, null);
    assert.equal(snapshot.groups, null);
  } finally { controller.dispose(); }
});

test('the receipt is the genuine 48-hex DVR operation id, fenced before and after the await', async () => {
  let minted = 0;
  const {api, calls} = fixtureServer(() => pageOf([]));
  const controller = controllerFor(api, {requestId: async () => { minted++; return hex(48, 'f'); }});
  try {
    assert.equal(await controller.requestReceipt(), hex(48, 'f'));
    assert.equal(minted, 1);
    assert.equal(calls.length, 0);
  } finally { controller.dispose(); }

  const uuid = controllerFor(fixtureServer(() => pageOf([])).api, {requestId: async () => '123e4567-e89b-12d3-a456-426614174000'});
  await assert.rejects(uuid.requestReceipt(), /recordings-query-invalid/);
  uuid.dispose();

  let live = true;
  const pendingId = held<string>();
  const fenced = controllerFor(fixtureServer(() => pageOf([])).api, {requestId: () => pendingId.promise, isCurrent: () => live});
  const receipt = fenced.requestReceipt();
  live = false;
  pendingId.resolve(hex(48, 'a'));
  await assert.rejects(receipt, failure => failure instanceof DOMException && failure.name === 'AbortError');
  fenced.dispose();
});

test('undrawn destinations, cursors and group orders perform no request', async () => {
  const {api, calls} = fixtureServer(() => pageOf([]));
  const controller = controllerFor(api);
  try {
    await assert.rejects(controller.load({view: 'rules' as never}), /recordings-query-invalid/);
    await assert.rejects(controller.load({view: 'recorded', recordingId: 'recording-one'}), /recordings-query-invalid/);
    await assert.rejects(controller.load({view: 'recorded', cursor: 'c'.repeat(4097)}), /recordings-query-invalid/);
    await assert.rejects(controller.readGroups('newest' as never), /recordings-query-invalid/);
    await assert.rejects(controller.readGroups('recent', 'c'.repeat(4097)), /recordings-query-invalid/);
    assert.equal(calls.length, 0);
    assert.equal(controller.getSnapshot().phase, 'idle');
  } finally { controller.dispose(); }
});

test('a foreign envelope is refused and leaves no private facts', async () => {
  const {api, calls} = fixtureServer(() => envelope(pageOf([recording(hex(64, '1'))]), 'another-server'));
  const controller = controllerFor(api);
  try {
    await controller.load({view: 'recorded'});
    const snapshot = controller.getSnapshot();
    assert.equal(calls.length, 1);
    assert.equal(snapshot.page, null);
    assert.equal(snapshot.detail, null);
    assert.equal(snapshot.storage, null);
    assert.equal(snapshot.phase, 'error');
    assert.ok(snapshot.error.length > 0);
    assert.ok(!snapshot.error.includes('another-server'));
  } finally { controller.dispose(); }
});

test('contains accepts only the exact resident objects of this generation', async () => {
  const resident = recording(hex(64, '1')), copy = {...resident};
  const {api} = fixtureServer(call => pathname(call).startsWith('/v1/dvr/recordings/') ? detailFact(hex(64, '2')) : pageOf([resident], '', 1));
  const controller = controllerFor(api);
  try {
    await controller.load({view: 'recorded'});
    const row = controller.getSnapshot().page!.recordings[0];
    assert.equal(row.id, resident.id);
    assert.equal(controller.contains(row), true);
    assert.equal(controller.contains(copy), false);
    await controller.load({view: 'recorded', recordingId: hex(64, '2')});
    assert.equal(controller.contains(row), false, 'a different destination is not resident');
    assert.equal(controller.contains(controller.getSnapshot().detail!.recording), true);
  } finally { controller.dispose(); }
});

// Queue item 1 — aggregate replacement, group order and view races on the real DVRModel.
test('the newest summary reply wins even when an older one settles last', async () => {
  const older = held<unknown>(), newer = held<unknown>();
  let served = 0;
  const {api} = fixtureServer(call => pathname(call) === '/v1/dvr/summary'
    ? (served++ === 0 ? older.promise : newer.promise) : pageOf([]));
  const controller = controllerFor(api);
  try {
    const first = controller.readSummary();
    const second = controller.readSummary();
    const newest = {upcoming: 1, recorded: 2, rules: 3, conflicts: 4, groups: 5, unwatched: 6};
    newer.resolve(newest);
    assert.deepEqual(await second, newest);
    older.resolve({upcoming: 99, recorded: 99, rules: 99, conflicts: 99, groups: 99, unwatched: 99});
    await assert.rejects(first, failure => failure instanceof DOMException && failure.name === 'AbortError');
    assert.deepEqual(controller.getSnapshot().summary, newest);
  } finally { controller.dispose(); }
});

test('a same-order groups refresh keeps only the newest genuine page', async () => {
  const older = held<unknown>(), newer = held<unknown>();
  let served = 0;
  const {api} = fixtureServer(call => pathname(call) === '/v1/dvr/groups'
    ? (served++ === 0 ? older.promise : newer.promise) : pageOf([]));
  const controller = controllerFor(api);
  try {
    const first = controller.readGroups('recent');
    const second = controller.readGroups('recent');
    newer.resolve(groupsFact('recent', 'newest-token'));
    await second;
    assert.equal(controller.getSnapshot().groups?.nextCursor, 'newest-token');
    older.resolve(groupsFact('recent', 'stale-token'));
    await assert.rejects(first, failure => failure instanceof DOMException && failure.name === 'AbortError');
    const snapshot = controller.getSnapshot();
    assert.equal(snapshot.groupsSort, 'recent');
    assert.equal(snapshot.groups?.nextCursor, 'newest-token');
    assert.equal(snapshot.groups?.groups[0].recordings, 9);
  } finally { controller.dispose(); }
});

test('an aggregate authority failure fences a page reply that lands later', async () => {
  const pageReply = held<unknown>();
  let deny = false;
  const {api, calls} = fixtureServer(call => {
    if (pathname(call) === '/v1/dvr/summary') { if (deny) throw {status: 401};return summaryFact; }
    return pageReply.promise;
  });
  const controller = controllerFor(api);
  try {
    const loading = controller.load({view: 'recorded'});
    deny = true;
    await assert.rejects(controller.readSummary(), failure => (failure as {status?: number}).status === 401);
    pageReply.resolve(pageOf([recording(hex(64, '1'))], '', 4));
    await loading;
    const snapshot = controller.getSnapshot();
    assert.equal(calls.filter(call => pathname(call) === '/v1/dvr').length, 1);
    assert.equal(snapshot.page, null, 'the page reply settled after the authority epoch changed');
    assert.equal(snapshot.summary, null);
    assert.equal(snapshot.accessDenied, true);
    assert.notEqual(snapshot.phase, 'retired');
  } finally { controller.dispose(); }
});

test('a view change keeps the server aggregates that are not page facts', async () => {
  const {api} = fixtureServer(call => {
    if (pathname(call) === '/v1/dvr/summary') return summaryFact;
    if (pathname(call) === '/v1/dvr/groups') return groupsFact('title', 'group-next');
    return params(call).get('state') === 'recorded'
      ? pageOf([recording(hex(64, '1'))], '', 1) : pageOf([recording(hex(64, '2'))], '', 2);
  });
  const controller = controllerFor(api);
  try {
    await controller.load({view: 'recorded'});
    await controller.readSummary();
    await controller.readGroups('title');
    await controller.load({view: 'history'});
    const snapshot = controller.getSnapshot();
    assert.equal(snapshot.query.view, 'history');
    assert.equal(snapshot.page?.revision, 2);
    assert.deepEqual(snapshot.summary, summaryFact, 'the summary is the server total, not the old page');
    assert.equal(snapshot.groupsSort, 'title');
    assert.equal(snapshot.groups?.groups[0].recordings, 9);
    assert.equal(snapshot.groups?.nextCursor, 'group-next');
  } finally { controller.dispose(); }
});

// Queue item 2 — two controllers never share captured state, lifetimes or receipts.
test('two controllers on one API keep separate facts, lifetimes and generations', async () => {
  let served = 0;
  const {api} = fixtureServer(() => {
    const revision = ++served;
    return pageOf([recording(hex(64, revision === 1 ? '1' : '2'))], '', revision);
  });
  const first = controllerFor(api), second = controllerFor(api);
  try {
    await first.load({view: 'recorded'});
    await second.load({view: 'recorded'});
    assert.equal(first.getSnapshot().page?.revision, 1);
    assert.equal(second.getSnapshot().page?.revision, 2);
    first.dispose();
    assert.equal(first.getSnapshot().phase, 'retired');
    assert.equal(second.getSnapshot().phase, 'ready', 'one disposal cannot retire its neighbour');
    assert.equal(second.getSnapshot().page?.revision, 2);
    await second.load({view: 'recorded'});
    assert.equal(second.getSnapshot().page?.revision, 3);
  } finally { second.dispose(); }
});

test('an authority failure in one controller leaves the other controller’s facts intact', async () => {
  let deny = false;
  const {api} = fixtureServer(call => {
    if (deny && params(call).get('state') === 'history') throw {status: 403};
    if (pathname(call) === '/v1/dvr/groups') return groupsFact('recent', '');
    return pageOf([recording(hex(64, '1'))], '', 1);
  });
  const first = controllerFor(api), second = controllerFor(api);
  try {
    await first.load({view: 'recorded'});
    await second.load({view: 'recorded'});
    deny = true;
    await first.load({view: 'history'});
    assert.equal(first.getSnapshot().accessDenied, true);
    assert.equal(first.getSnapshot().page, null);
    assert.notEqual(first.getSnapshot().phase, 'retired', 'a denied read is not a retirement');
    assert.equal(second.getSnapshot().accessDenied, false);
    assert.equal(second.getSnapshot().page?.recordings.length, 1);
    await second.readGroups();
    assert.equal(second.getSnapshot().groups?.groups[0].recordings, 9);
    assert.equal(second.getSnapshot().groupsSort, 'recent');
  } finally { first.dispose(); second.dispose(); }
});

test('each controller keeps the API and receipt source it captured', async () => {
  const captured = fixtureServer(() => pageOf([recording(hex(64, '1'))], '', 1));
  const other = fixtureServer(() => pageOf([recording(hex(64, '2'))], '', 2));
  const options = {api: captured.api, serverId: 'server', requestId: async () => hex(48, 'e'),
    signal: new AbortController().signal, isCurrent: () => true};
  const controller = createRecordingsController(options);
  const second = controllerFor(other.api, {requestId: async () => hex(48, 'b')});
  try {
    options.api = other.api;
    options.requestId = async () => hex(48, '9');
    await controller.load({view: 'recorded'});
    assert.equal(captured.calls.length, 1, 'the API captured at construction is the one used');
    assert.equal(other.calls.length, 0);
    assert.equal(await controller.requestReceipt(), hex(48, 'e'), 'the captured receipt source is the one used');
    assert.equal(other.calls.length, 0);
    controller.dispose();
    await assert.rejects(controller.requestReceipt(), failure => failure instanceof DOMException && failure.name === 'AbortError');
    assert.equal(await second.requestReceipt(), hex(48, 'b'), 'one retirement cannot refuse another controller’s receipt');
    assert.equal(second.getSnapshot().phase, 'idle');
  } finally { second.dispose(); }
});

// Reviewer lifecycle fix 1 — replacement clears resident membership before it publishes.
test('a replacement destination retires resident membership before the new query publishes', async () => {
  const pending: Record<string, ReturnType<typeof held<unknown>>> = {};
  const reply = (call: Call) => {
    const name = pathname(call) === '/v1/dvr/storage' ? 'storage'
      : pathname(call).startsWith('/v1/dvr/recordings/') ? 'detail' : params(call).get('state') ?? '';
    if (name === 'recorded') return pageOf([recording(hex(64, '1'))], '', 1);
    pending[name] ??= held<unknown>();
    return pending[name].promise;
  };
  const {api} = fixtureServer(reply);
  const controller = controllerFor(api);
  try {
    await controller.load({view: 'recorded'});
    const row = controller.getSnapshot().page!.recordings[0];
    assert.equal(controller.contains(row), true);

    const refreshing = controller.load({view: 'recorded'});
    assert.equal(controller.contains(row), false, 'a refresh in flight is not a settled destination');
    await refreshing;
    assert.notEqual(controller.getSnapshot().page!.recordings[0], row);
    assert.equal(controller.contains(row), false, 'a refresh republishes new resident objects');
    assert.equal(controller.contains(controller.getSnapshot().page!.recordings[0]), true);

    const history = controller.load({view: 'history'});
    assert.equal(controller.contains(row), false, 'membership ends before the replacement publishes');
    let snapshot = controller.getSnapshot();
    assert.equal(snapshot.query.view, 'history');
    assert.equal(snapshot.phase, 'loading');
    assert.equal(snapshot.page, null, 'the old page cannot pose as the new destination');
    assert.equal(snapshot.detail, null);
    assert.equal(snapshot.storage, null);
    pending['history'].reject(new Error('history read failed'));
    await history;
    assert.equal(controller.contains(row), false, 'a failed replacement cannot revive the old row');
    assert.equal(controller.getSnapshot().page, null);
    assert.equal(controller.getSnapshot().phase, 'error');

    const storage = controller.load({view: 'storage'});
    assert.equal(controller.getSnapshot().storage, null, 'the storage fact is not resident yet');
    assert.equal(controller.contains(row), false);
    pending['storage'].resolve(storageFact());
    await storage;
    assert.deepEqual(controller.getSnapshot().storage, storageFact());

    const detailId = hex(64, '2');
    const detail = controller.load({view: 'recorded', recordingId: detailId});
    assert.equal(controller.getSnapshot().detail, null, 'the new detail is not resident yet');
    assert.equal(controller.getSnapshot().storage, null, 'the previous destination storage is not resident');
    assert.equal(controller.getSnapshot().page, null, 'no page belongs to a detail destination');
    assert.equal(controller.contains(row), false);
    pending['detail'].resolve(detailFact(detailId));
    await detail;
    assert.equal(controller.getSnapshot().detail?.recording.id, detailId);
    assert.equal(controller.contains(controller.getSnapshot().detail!.recording), true);
  } finally { controller.dispose(); }
});

// Reviewer finding 2 (narrowed) — profile-wide aggregates and the receipt are
// fenced by the borrowed lease, not by a local destination change.
test('profile-wide summary, groups and receipt finish across a local destination change on one lease', async () => {
  const summaryReply = held<unknown>(), groupsReply = held<unknown>(), receiptReply = held<string>();
  const {api} = fixtureServer(call => {
    if (pathname(call) === '/v1/dvr/summary') return summaryReply.promise;
    if (pathname(call) === '/v1/dvr/groups') return groupsReply.promise;
    return params(call).get('state') === 'recorded'
      ? pageOf([recording(hex(64, '1'))], '', 1) : pageOf([recording(hex(64, '2'))], '', 2);
  });
  const controller = controllerFor(api, {requestId: () => receiptReply.promise});
  try {
    await controller.load({view: 'recorded'});
    const summary = controller.readSummary();
    const groups = controller.readGroups('recent');
    const receipt = controller.requestReceipt();
    await controller.load({view: 'history'});

    summaryReply.resolve(summaryFact);
    groupsReply.resolve(groupsFact('recent', 'profile-token'));
    receiptReply.resolve(hex(48, 'a'));
    assert.deepEqual(await summary, summaryFact, 'a profile-wide total is not tied to the tab');
    assert.equal((await groups).nextCursor, 'profile-token');
    assert.equal(await receipt, hex(48, 'a'));

    const snapshot = controller.getSnapshot();
    assert.equal(snapshot.query.view, 'history');
    assert.equal(snapshot.page?.revision, 2);
    assert.deepEqual(snapshot.summary, summaryFact);
    assert.equal(snapshot.groups?.groups[0].recordings, 9);
    assert.equal(snapshot.groupsSort, 'recent');
  } finally { controller.dispose(); }
});

test('profile-wide summary, groups and receipt are refused when the borrowed lease retires', async () => {
  const summaryReply = held<unknown>(), groupsReply = held<unknown>(), receiptReply = held<string>();
  let live = true;
  const {api} = fixtureServer(call => {
    if (pathname(call) === '/v1/dvr/summary') return summaryReply.promise;
    if (pathname(call) === '/v1/dvr/groups') return groupsReply.promise;
    return pageOf([recording(hex(64, '1'))], '', 1);
  });
  const controller = controllerFor(api, {requestId: () => receiptReply.promise, isCurrent: () => live});
  try {
    await controller.load({view: 'recorded'});
    const summary = controller.readSummary();
    const groups = controller.readGroups('recent');
    const receipt = controller.requestReceipt();
    live = false;
    summaryReply.resolve(summaryFact);
    groupsReply.resolve(groupsFact('recent', 'late-token'));
    receiptReply.resolve(hex(48, 'b'));
    await assert.rejects(summary, failure => failure instanceof DOMException && failure.name === 'AbortError');
    await assert.rejects(groups, failure => failure instanceof DOMException && failure.name === 'AbortError');
    await assert.rejects(receipt, failure => failure instanceof DOMException && failure.name === 'AbortError');
    const snapshot = controller.getSnapshot();
    assert.equal(snapshot.phase, 'retired');
    assert.equal(snapshot.summary, null);
    assert.equal(snapshot.groups, null);
    assert.equal(snapshot.page, null);
    assert.equal(snapshot.query.view, 'recorded');
  } finally { controller.dispose(); }
});

// Queue item 3 — 401/403 retirement/recovery and resident-detail membership.
test('a 401 does not retire: page, aggregates and detail recover on genuine reads', async () => {
  let deny = false;
  const id = hex(64, '3');
  const {api} = fixtureServer(call => {
    if (deny) throw {status: 401};
    if (pathname(call) === '/v1/dvr/summary') return summaryFact;
    if (pathname(call) === '/v1/dvr/groups') return groupsFact('recent', '');
    if (pathname(call).startsWith('/v1/dvr/recordings/')) return detailFact(id);
    return pageOf([recording(hex(64, '1'))], '', 1);
  });
  const controller = controllerFor(api);
  try {
    await controller.load({view: 'recorded'});
    await controller.readSummary();
    await controller.readGroups();
    deny = true;
    await assert.rejects(controller.readSummary(), failure => (failure as {status?: number}).status === 401);
    let snapshot = controller.getSnapshot();
    assert.notEqual(snapshot.phase, 'retired', 'an authority failure is not a retirement');
    assert.equal(snapshot.accessDenied, true);
    assert.equal(snapshot.page, null);
    assert.equal(snapshot.detail, null);
    assert.equal(snapshot.storage, null);
    assert.equal(snapshot.summary, null);
    assert.equal(snapshot.groups, null);
    assert.equal(controller.contains(recording(hex(64, '1'))), false);

    deny = false;
    await controller.load({view: 'recorded'});
    await controller.readSummary();
    await controller.readGroups();
    await controller.load({view: 'recorded', recordingId: id});
    snapshot = controller.getSnapshot();
    assert.equal(snapshot.accessDenied, false);
    assert.equal(snapshot.phase, 'ready');
    assert.equal(snapshot.page, null, 'the detail destination replaced the page');
    assert.equal(snapshot.detail?.recording.id, id);
    assert.equal(snapshot.summary?.recorded, summaryFact.recorded);
    assert.equal(snapshot.groups?.groups[0].recordings, 9);
    assert.equal(controller.contains(snapshot.detail!.recording), true);
  } finally { controller.dispose(); }
});

test('a resident detail dies with its generation and a 403 retires it without retiring the view', async () => {
  let deny = false;
  const id = hex(64, '4');
  const {api} = fixtureServer(call => {
    if (deny && pathname(call).startsWith('/v1/dvr/recordings/')) throw {status: 403};
    if (pathname(call).startsWith('/v1/dvr/recordings/')) return detailFact(id);
    return pageOf([recording(hex(64, '1'))], '', 1);
  });
  const controller = controllerFor(api);
  try {
    await controller.load({view: 'recorded', recordingId: id});
    const resident = controller.getSnapshot().detail!.recording;
    assert.equal(controller.contains(resident), true);
    assert.equal(controller.contains({...resident}), false, 'the same id is not the resident object');

    await controller.load({view: 'recorded'});
    assert.equal(controller.contains(resident), false, 'a page destination retires the detail');

    await controller.load({view: 'recorded', recordingId: id});
    const fresh = controller.getSnapshot().detail!.recording;
    assert.notEqual(fresh, resident, 'a re-read is a new resident object');
    assert.equal(controller.contains(fresh), true);
    assert.equal(controller.contains(resident), false);

    deny = true;
    await controller.load({view: 'recorded', recordingId: id});
    const denied = controller.getSnapshot();
    assert.equal(denied.accessDenied, true);
    assert.equal(denied.detail, null);
    assert.equal(controller.contains(fresh), false);
    assert.notEqual(denied.phase, 'retired', 'the view stays usable after a detail 403');
    assert.equal(denied.query.recordingId, id, 'the destination is retained for a retry');

    deny = false;
    await controller.load({view: 'recorded', recordingId: id});
    assert.equal(controller.contains(controller.getSnapshot().detail!.recording), true);
    assert.equal(controller.contains(fresh), false, 'the recovered object is a new generation');
  } finally { controller.dispose(); }
});

// Step 1 — resident Cancel and Keep through the same private model.
const commandRecording = (id: string, patch: Record<string, unknown> = {}) => recording(id, patch);
const commandPage = () => pageOf([commandRecording(hex(64, '1')),
  commandRecording(hex(64, '2'), {state: 'completed'}), commandRecording(hex(64, '3'), {state: 'failed'})], 'page-two', 1);

test('a held cancel keeps the resident row until the ACK, then adopts the genuine reload', async () => {
  const cancelReply = held<unknown>();
  let pageServed = 0;
  const {api, calls} = fixtureServer(call => {
    if (call.method !== 'GET') return cancelReply.promise;
    pageServed++;
    return pageOf([commandRecording(hex(64, '1'), {revision: pageServed === 1 ? 1 : 2, state: pageServed === 1 ? 'scheduled' : 'cancelled'})], 'page-two', pageServed);
  });
  const controller = controllerFor(api);
  try {
    await controller.load({view: 'recorded', cursor: 'page-two'});
    const row = controller.getSnapshot().page!.recordings[0];
    const pending = controller.cancel(row);
    pending.catch(() => {});
    await tick();
    assert.equal(controller.getSnapshot().mutating, true, 'the command is in flight');
    assert.equal(controller.getSnapshot().page?.recordings[0], row, 'no optimistic removal or rewrite');
    assert.equal(controller.contains(row), true, 'the row is still the resident object until the ACK');
    const command = calls.at(-1)!;
    assert.equal(command.method, 'POST');
    assert.equal(pathname(command), '/v1/dvr/recordings/' + hex(64, '1') + '/cancel');
    assert.deepEqual(Object.keys(command.body as object).sort(), ['expectedRevision', 'requestId']);
    assert.equal((command.body as {expectedRevision: number}).expectedRevision, 1);
    assert.match(String((command.body as {requestId: string}).requestId), /^[a-f0-9]{48}$/);

    cancelReply.resolve(commandRecording(hex(64, '1'), {revision: 2, state: 'cancelled'}));
    const outcome = await pending;
    assert.deepEqual(outcome, {committed: true, refreshed: true, busy: false});
    const reload = calls.at(-1)!;
    assert.equal(reload.method, 'GET', 'the genuine destination is reloaded after the ACK');
    assert.equal(params(reload).get('state'), 'recorded');
    assert.equal(params(reload).get('cursor'), 'page-two', 'the offered cursor is the one reloaded');
    const snapshot = controller.getSnapshot();
    assert.equal(snapshot.mutating, false);
    assert.equal(snapshot.commandError, '');
    assert.equal(snapshot.page?.revision, 2);
    assert.equal(snapshot.page?.recordings[0].state, 'cancelled');
    assert.equal(controller.contains(row), false, 'the ACK republishes new resident objects');
    assert.equal(controller.contains(snapshot.page!.recordings[0]), true);
  } finally { controller.dispose(); }
});

test('keep toggles only the legacy keepable states and cancel only live states', async () => {
  const {api, calls} = fixtureServer(call => call.method !== 'GET'
    ? commandRecording(hex(64, '2'), {revision: 2, keep: true}) : commandPage());
  const controller = controllerFor(api);
  try {
    await controller.load({view: 'recorded'});
    const [scheduled, completed, failed] = controller.getSnapshot().page!.recordings;
    await assert.rejects(controller.keep(scheduled), /recordings-command-not-offered/);
    await assert.rejects(controller.cancel(completed), /recordings-command-not-offered/);
    await assert.rejects(controller.cancel(failed), /recordings-command-not-offered/);
    await assert.rejects(controller.cancel({...scheduled}), /recordings-row-stale/);
    assert.equal(calls.filter(call => call.method !== 'GET').length, 0, 'a refused command performs no request');

    const outcome = await controller.keep(completed);
    assert.deepEqual(outcome, {committed: true, refreshed: true, busy: false});
    const command = calls.filter(call => call.method !== 'GET').at(-1)!;
    assert.equal(command.method, 'PUT');
    assert.equal(pathname(command), '/v1/dvr/recordings/' + hex(64, '2') + '/keep');
    assert.equal((command.body as {keep: boolean}).keep, true, 'the kept value is the exact toggle for this revision');
    assert.equal((command.body as {expectedRevision: number}).expectedRevision, 1);

    await assert.rejects(controller.keep(controller.getSnapshot().page!.recordings[0]), /recordings-command-not-offered/);
  } finally { controller.dispose(); }
});

test('a refused command keeps the resident facts, its revision and the genuine error fact', async () => {
  const {api, calls} = fixtureServer(call => {
    if (call.method !== 'GET') throw {status: 409};
    return commandPage();
  });
  const controller = controllerFor(api);
  try {
    await controller.load({view: 'recorded'});
    const row = controller.getSnapshot().page!.recordings[0];
    const outcome = await controller.cancel(row);
    assert.deepEqual(outcome, {committed: false, refreshed: false, busy: false});
    const snapshot = controller.getSnapshot();
    assert.equal(snapshot.page?.recordings[0], row, 'no optimistic or rebased row');
    assert.equal(snapshot.page?.recordings[0].revision, 1, 'the genuine revision stays with the row');
    assert.equal(controller.contains(row), true, 'a refused command leaves the row retryable');
    assert.equal(snapshot.commandError.length > 0, true, 'the conflict is a command fact');
    assert.equal(snapshot.error, '', 'a command failure is not a destination failure');
    assert.equal(calls.filter(call => call.method === 'GET').length, 1, 'a failed command reloads nothing');

    // The explicit retry of the same semantic command keeps the revision and the
    // exact receipt; a conflict is never rebased into a different request.
    const first = calls.filter(call => call.method !== 'GET')[0].body as {requestId: string; expectedRevision: number};
    const retried = await controller.cancel(row);
    assert.deepEqual(retried, {committed: false, refreshed: false, busy: false});
    const second = calls.filter(call => call.method !== 'GET')[1].body as {requestId: string; expectedRevision: number};
    assert.equal(second.expectedRevision, first.expectedRevision);
    assert.equal(second.requestId, first.requestId, 'the same receipt is retried, never a second id');
  } finally { controller.dispose(); }
});

test('a second command while one is in flight is busy and sends nothing', async () => {
  const heldReply = held<unknown>();
  const {api, calls} = fixtureServer(call => call.method !== 'GET' ? heldReply.promise : commandPage());
  const controller = controllerFor(api);
  try {
    await controller.load({view: 'recorded'});
    const row = controller.getSnapshot().page!.recordings[0];
    const first = controller.cancel(row);
    first.catch(() => {});
    const second = await controller.cancel(row);
    assert.deepEqual(second, {committed: false, refreshed: false, busy: true});
    assert.equal(calls.filter(call => call.method !== 'GET').length, 1, 'one click sends one command');
    heldReply.resolve(commandRecording(hex(64, '1'), {revision: 2, state: 'cancelled'}));
    assert.deepEqual(await first, {committed: true, refreshed: true, busy: false});
  } finally { controller.dispose(); }
});

// Step 2 — bound delete preview and confirm through the same private model.
const deletePreviewFact = (id: string) => ({recordingId: id, revision: 7, bytes: 1234, keep: false,
  activeReaders: false, result: 'Removes the recording and its file'});
const rowId = (call: Call) => pathname(call).split('/')[4];

test('delete is offered only by the actual page and its returned preview is the one confirmed', async () => {
  const deleteReply = held<unknown>();
  let served = 0;
  const {api, calls} = fixtureServer(call => {
    const name = pathname(call);
    if (call.method !== 'GET') return deleteReply.promise;
    if (name.endsWith('/delete-preview')) return deletePreviewFact(rowId(call));
    if (name.startsWith('/v1/dvr/recordings/')) return detailFact(rowId(call));
    served++;
    return served === 1
      ? pageOf([commandRecording(hex(64, '1'), {state: 'completed'}), commandRecording(hex(64, '2'), {state: 'recording'})], '', 1)
      : pageOf([], '', 2);
  });
  const controller = controllerFor(api);
  try {
    await controller.load({view: 'recorded'});
    const [completed, active] = controller.getSnapshot().page!.recordings;
    assert.equal(await controller.deletePreview(active), undefined, 'an active recording is never offered');
    assert.equal(calls.filter(call => pathname(call).endsWith('/delete-preview')).length, 0);

    const preview = await controller.deletePreview(completed);
    assert.deepEqual(preview, deletePreviewFact(hex(64, '1')), 'the exact returned preview is preserved');
    assert.equal(preview!.revision, 7);
    assert.equal(preview!.keep, false);
    assert.equal(preview!.activeReaders, false);
    assert.equal(preview!.result, deletePreviewFact(hex(64, '1')).result);

    const held = controller.confirmDelete(completed);
    held.catch(() => {});
    await tick();
    const command = calls.filter(call => call.method === 'DELETE').at(-1)!;
    assert.equal(pathname(command), '/v1/dvr/recordings/' + hex(64, '1'));
    assert.equal((command.body as {expectedRevision: number}).expectedRevision, 7, 'the preview revision is sent, not the row revision');
    assert.equal(controller.getSnapshot().page?.recordings.length, 2, 'nothing is removed before the ACK');
    assert.equal(controller.getSnapshot().mutating, true);
    deleteReply.resolve(commandRecording(hex(64, '1'), {revision: 9, state: 'deleted'}));

    assert.deepEqual(await held, {committed: true, refreshed: true, busy: false});
    assert.equal(controller.getSnapshot().page?.recordings.length, 0, 'the genuine reload removes the row');

    await controller.load({view: 'recorded', recordingId: hex(64, '5')});
    assert.equal(await controller.deletePreview(controller.getSnapshot().detail!.recording), undefined,
      'a detail destination never invents a delete offer');
  } finally { controller.dispose(); }
});

test('a cross-row, rebound or stale preview can never be confirmed', async () => {
  const {api, calls} = fixtureServer(call => {
    const name = pathname(call);
    if (call.method !== 'GET') return commandRecording(rowId(call), {revision: 9, state: 'deleted'});
    if (name.endsWith('/delete-preview')) return deletePreviewFact(rowId(call));
    return commandPage();
  });
  const controller = controllerFor(api);
  try {
    await controller.load({view: 'recorded'});
    const [scheduled, completed, failed] = controller.getSnapshot().page!.recordings;
    await controller.deletePreview(completed);
    await assert.rejects(controller.confirmDelete(failed), /recordings-delete-preview-stale/, 'another row cannot use it');
    await assert.rejects(controller.confirmDelete({...completed}), /recordings-row-stale/, 'a copy is not the resident row');
    assert.equal(calls.filter(call => call.method === 'DELETE').length, 0);

    await controller.load({view: 'recorded'});
    const [freshScheduled, reread] = controller.getSnapshot().page!.recordings;
    await assert.rejects(controller.confirmDelete(reread), /recordings-delete-preview-stale/,
      'a page reload ends the preview generation');

    await controller.deletePreview(reread);
    await assert.rejects(controller.confirmDelete(freshScheduled), /recordings-delete-preview-stale/);
    assert.equal(calls.filter(call => call.method === 'DELETE').length, 0);
  } finally { controller.dispose(); }
});

test('a confirmed delete held before its ACK sends exactly one command', async () => {
  const deleteReply = held<unknown>();
  const {api, calls} = fixtureServer(call => {
    const name = pathname(call);
    if (call.method === 'DELETE') return deleteReply.promise;
    if (name.endsWith('/delete-preview')) return deletePreviewFact(rowId(call));
    return commandPage();
  });
  const controller = controllerFor(api);
  try {
    await controller.load({view: 'recorded'});
    const row = controller.getSnapshot().page!.recordings[1];
    await controller.deletePreview(row);
    const first = controller.confirmDelete(row);
    first.catch(() => {});
    await assert.rejects(controller.confirmDelete(row), /recordings-delete-preview-stale/,
      'the bound preview is single use');
    assert.equal(calls.filter(call => call.method === 'DELETE').length, 1);
    deleteReply.resolve(commandRecording(row.id, {revision: 9, state: 'deleted'}));
    assert.deepEqual(await first, {committed: true, refreshed: true, busy: false});
  } finally { controller.dispose(); }
});

test('an ACK with a failing reload is committed but not refreshed and removes nothing optimistically', async () => {
  let failReload = false, served = 0;
  const {api} = fixtureServer(call => {
    const name = pathname(call);
    if (call.method !== 'GET') return commandRecording(rowId(call), {revision: 9, state: 'deleted'});
    if (name.endsWith('/delete-preview')) return deletePreviewFact(rowId(call));
    served++;
    if (failReload && served > 1) throw {status: 500};
    return served === 1 ? pageOf([commandRecording(hex(64, '1'), {state: 'completed'})], '', 1) : pageOf([], '', 2);
  });
  const controller = controllerFor(api);
  try {
    await controller.load({view: 'recorded'});
    const row = controller.getSnapshot().page!.recordings[0];
    await controller.deletePreview(row);
    failReload = true;
    assert.deepEqual(await controller.confirmDelete(row), {committed: true, refreshed: false, busy: false});
    const snapshot = controller.getSnapshot();
    assert.equal(snapshot.page?.recordings.length, 1, 'the ACK never removes the row optimistically');
    assert.equal(snapshot.page?.recordings[0], row);
    assert.equal(snapshot.commandError, '', 'the command committed');
    assert.ok(snapshot.error.length > 0, 'the reload failure is a destination fact');
    assert.equal(controller.contains(row), false, 'membership waits for a genuine reload');

    failReload = false;
    await controller.load({view: 'recorded'});
    assert.equal(controller.getSnapshot().page?.recordings.length, 0);
  } finally { controller.dispose(); }
});

// Step 3 — one cached receipt per exact command, no automatic retry or rebase.
test('a command waits for its one genuine receipt before any HTTP', async () => {
  const receipt = held<string>();
  let minted = 0;
  const {api, calls} = fixtureServer(call => call.method !== 'GET'
    ? commandRecording(hex(64, '1'), {revision: 2, state: 'cancelled'}) : commandPage());
  const controller = controllerFor(api, {requestId: () => { minted++; return receipt.promise; }});
  try {
    await controller.load({view: 'recorded'});
    const row = controller.getSnapshot().page!.recordings[0];
    const pending = controller.cancel(row);
    pending.catch(() => {});
    await tick();
    assert.equal(minted, 1);
    assert.equal(calls.filter(call => call.method !== 'GET').length, 0, 'no command leaves before the receipt');
    assert.equal(controller.getSnapshot().mutating, true);
    receipt.resolve(hex(48, 'c'));
    await tick();
    const command = calls.filter(call => call.method !== 'GET').at(-1)!;
    assert.deepEqual(command.body, {expectedRevision: 1, requestId: hex(48, 'c')});
    assert.deepEqual(await pending, {committed: true, refreshed: true, busy: false});
    assert.equal(minted, 1, 'the public receipt door was never used for a second id');
  } finally { controller.dispose(); }
});

test('an unknown outcome retries the identical body with the same cached receipt', async () => {
  const bodies: unknown[] = [];
  let attempts = 0, minted = 0;
  const {api} = fixtureServer(call => {
    if (call.method === 'GET') return commandPage();
    attempts++;
    bodies.push(call.body);
    if (attempts === 1) throw new Error('Recording request timed out.');
    return commandRecording(hex(64, '1'), {revision: 2, state: 'cancelled'});
  });
  const controller = controllerFor(api, {requestId: async () => { minted++; return hex(48, 'd'); }});
  try {
    await controller.load({view: 'recorded'});
    const row = controller.getSnapshot().page!.recordings[0];
    assert.deepEqual(await controller.cancel(row), {committed: false, refreshed: false, busy: false});
    assert.equal(controller.contains(row), true, 'the unknown outcome leaves the row retryable');
    assert.deepEqual(await controller.cancel(row), {committed: true, refreshed: true, busy: false});
    assert.equal(attempts, 2);
    assert.deepEqual(bodies[0], bodies[1], 'the explicit retry is the same semantic command');
    assert.equal(minted, 1, 'one 48-hex receipt serves the identical command');
    assert.equal((bodies[1] as {requestId: string}).requestId, hex(48, 'd'));
  } finally { controller.dispose(); }
});

test('a changed revision is a new command and a new receipt, never a silent retry', async () => {
  let served = 0, minted = 0;
  const {api, calls} = fixtureServer(call => {
    if (call.method !== 'GET') return commandRecording(hex(64, '1'), {revision: 2, state: 'completed', keep: true});
    served++;
    return pageOf([commandRecording(hex(64, '1'), {revision: served === 1 ? 1 : 2, state: 'completed', keep: served > 1})], '', served);
  });
  const controller = controllerFor(api, {requestId: async () => { minted++; return hex(48, minted === 1 ? 'e' : 'f'); }});
  try {
    await controller.load({view: 'recorded'});
    assert.deepEqual(await controller.keep(controller.getSnapshot().page!.recordings[0]), {committed: true, refreshed: true, busy: false});
    const refreshed = controller.getSnapshot().page!.recordings[0];
    assert.equal(refreshed.revision, 2);
    assert.equal(refreshed.keep, true);
    assert.deepEqual(await controller.keep(refreshed), {committed: true, refreshed: true, busy: false});
    const commands = calls.filter(call => call.method !== 'GET').map(call => call.body as {expectedRevision: number; keep: boolean; requestId: string});
    assert.deepEqual(commands.map(command => [command.expectedRevision, command.keep]), [[1, true], [2, false]]);
    assert.notEqual(commands[0].requestId, commands[1].requestId, 'a changed body is never the old receipt');
    assert.equal(minted, 2);
  } finally { controller.dispose(); }
});

test('a command never outlives navigation, a replacement destination or disposal', async () => {
  const receipt = held<string>();
  const navigatedApi = fixtureServer(call => call.method !== 'GET'
    ? commandRecording(hex(64, '1'), {revision: 2, state: 'cancelled'}) : commandPage()).api;
  let live = true;
  const navigated = controllerFor(navigatedApi, {requestId: () => receipt.promise, isCurrent: () => live});
  await navigated.load({view: 'recorded'});
  const navigating = navigated.cancel(navigated.getSnapshot().page!.recordings[0]);
  navigating.catch(() => {});
  await tick();
  live = false;
  receipt.resolve(hex(48, '1'));
  await assert.rejects(navigating, failure => failure instanceof DOMException && failure.name === 'AbortError');
  assert.equal(navigated.getSnapshot().phase, 'retired');

  const postReply = held<unknown>();
  const postCalls: string[] = [];
  const disposedApi = fixtureServer(call => {
    if (call.method === 'GET') return commandPage();
    postCalls.push(call.method);
    return postReply.promise;
  }).api;
  const disposed = controllerFor(disposedApi, {requestId: async () => hex(48, '2')});
  await disposed.load({view: 'recorded'});
  const pending = disposed.cancel(disposed.getSnapshot().page!.recordings[0]);
  pending.catch(() => {});
  await tick();
  disposed.dispose();
  postReply.resolve(commandRecording(hex(64, '1'), {revision: 2, state: 'cancelled'}));
  await assert.rejects(pending, failure => failure instanceof DOMException && failure.name === 'AbortError');
  assert.deepEqual(postCalls, ['POST']);
  assert.equal(disposed.getSnapshot().page, null);

  const replacementReply = held<unknown>();
  const replacedApi = fixtureServer(call => {
    if (call.method !== 'GET') return replacementReply.promise;
    return params(call).get('state') === 'recorded'
      ? pageOf([commandRecording(hex(64, '1'))], '', 1) : pageOf([commandRecording(hex(64, '9'))], '', 2);
  }).api;
  const replaced = controllerFor(replacedApi, {requestId: async () => hex(48, '3')});
  await replaced.load({view: 'recorded'});
  const recorded = replaced.getSnapshot().page!.recordings[0];
  const replacedCommand = replaced.cancel(recorded);
  replacedCommand.catch(() => {});
  await tick();
  await replaced.load({view: 'history'});
  replacementReply.resolve(commandRecording(hex(64, '1'), {revision: 2, state: 'cancelled'}));
  assert.deepEqual(await replacedCommand, {committed: true, refreshed: false, busy: false},
    'a reply that already ACKed stays committed even though its reload never ran');
  const snapshot = replaced.getSnapshot();
  assert.equal(snapshot.query.view, 'history');
  assert.equal(snapshot.page?.revision, 2);
  assert.equal(snapshot.page?.recordings[0].id, hex(64, '9'), 'the ACK never revives the old destination');
  assert.equal(replaced.contains(recorded), false);
  replaced.dispose();
});

test('an authority failure during a command clears every fact without reviving it', async () => {
  const postReply = held<unknown>();
  let minted = 0;
  const {api} = fixtureServer(call => {
    if (call.method === 'GET') {
      if (pathname(call) === '/v1/dvr/summary') return summaryFact;
      return commandPage();
    }
    return postReply.promise;
  });
  const controller = controllerFor(api, {requestId: async () => { minted++; return hex(48, '4'); }});
  try {
    await controller.load({view: 'recorded'});
    await controller.readSummary();
    const row = controller.getSnapshot().page!.recordings[0];
    const pending = controller.cancel(row);
    pending.catch(() => {});
    await tick();
    postReply.reject({status: 401});
    assert.deepEqual(await pending, {committed: false, refreshed: false, busy: false});
    const snapshot = controller.getSnapshot();
    assert.equal(snapshot.accessDenied, true);
    assert.equal(snapshot.page, null);
    assert.equal(snapshot.summary, null);
    assert.equal(controller.contains(row), false);
    assert.equal(minted, 1);
  } finally { controller.dispose(); }
});

test('two controllers retry their own commands with their own receipts', async () => {
  const bodies: Array<{requestId: string; expectedRevision: number}> = [];
  const {api} = fixtureServer(call => {
    if (call.method === 'GET') return commandPage();
    bodies.push(call.body as {requestId: string; expectedRevision: number});
    return commandRecording(hex(64, '1'), {revision: 2, state: 'cancelled'});
  });
  const first = controllerFor(api, {requestId: async () => hex(48, 'a')});
  const second = controllerFor(api, {requestId: async () => hex(48, 'b')});
  try {
    await first.load({view: 'recorded'});
    await second.load({view: 'recorded'});
    assert.deepEqual(await first.cancel(first.getSnapshot().page!.recordings[0]), {committed: true, refreshed: true, busy: false});
    assert.deepEqual(await second.cancel(second.getSnapshot().page!.recordings[0]), {committed: true, refreshed: true, busy: false});
    assert.deepEqual(bodies.map(body => body.requestId), [hex(48, 'a'), hex(48, 'b')]);
    assert.deepEqual(bodies.map(body => body.expectedRevision), [1, 1]);
  } finally { first.dispose(); second.dispose(); }
});

// Reviewer fix A — an armed command dies with the destination generation it was
// captured in, even before its receipt resolves.
const heldCommand = () => {
  const receipt = held<string>();
  const state = {minted: 0};
  return {receipt, state, requestId: () => { state.minted++; return receipt.promise; }};
};
const listCalls = (calls: readonly Call[]) => calls.filter(call => call.method !== 'GET');

test('a held receipt cannot send a command after its destination was replaced', async () => {
  const {receipt, state, requestId} = heldCommand();
  let served = 0;
  const {api, calls} = fixtureServer(call => {
    if (call.method !== 'GET') return commandRecording(hex(64, '1'), {revision: 2, state: 'cancelled'});
    served++;
    return params(call).get('state') === 'history'
      ? pageOf([commandRecording(hex(64, '9'))], '', 2) : pageOf([commandRecording(hex(64, '1'))], '', served);
  });
  const controller = controllerFor(api, {requestId});
  try {
    await controller.load({view: 'recorded'});
    const row = controller.getSnapshot().page!.recordings[0];
    const pending = controller.cancel(row);
    pending.catch(() => {});
    await tick();
    assert.equal(listCalls(calls).length, 0, 'the receipt is still pending');
    await controller.load({view: 'history'});
    const before = controller.getSnapshot();
    receipt.resolve(hex(48, '7'));
    assert.deepEqual(await pending, {committed: false, refreshed: false, busy: false});
    assert.equal(listCalls(calls).length, 0, 'the stale command never leaves');
    const after = controller.getSnapshot();
    assert.equal(after.query.view, 'history');
    assert.equal(after.page, before.page, 'the new destination facts are unchanged');
    assert.equal(after.page?.revision, 2);
    assert.equal(controller.contains(row), false);
    assert.equal(state.minted, 1);
  } finally { controller.dispose(); }
});

test('a held receipt cannot send a command across a same-destination refresh', async () => {
  const {receipt, state, requestId} = heldCommand();
  let served = 0;
  const {api, calls} = fixtureServer(call => {
    if (call.method !== 'GET') return commandRecording(hex(64, '1'), {revision: 2, state: 'cancelled'});
    served++;
    return pageOf([commandRecording(hex(64, '1'), {revision: served})], '', served);
  });
  const controller = controllerFor(api, {requestId});
  try {
    await controller.load({view: 'recorded'});
    const row = controller.getSnapshot().page!.recordings[0];
    const pending = controller.cancel(row);
    pending.catch(() => {});
    await tick();
    await controller.load({view: 'recorded'});
    const before = controller.getSnapshot();
    assert.equal(before.page?.revision, 2, 'the refresh republished a new page');
    receipt.resolve(hex(48, '8'));
    assert.deepEqual(await pending, {committed: false, refreshed: false, busy: false});
    assert.equal(listCalls(calls).length, 0, 'the refreshed page fences the old command');
    assert.equal(controller.getSnapshot().page, before.page);
    assert.equal(controller.contains(row), false);
    assert.equal(state.minted, 1);
  } finally { controller.dispose(); }
});

test('A to B to A never revives a command captured in the first A', async () => {
  const {receipt, state, requestId} = heldCommand();
  let served = 0;
  const {api, calls} = fixtureServer(call => {
    if (call.method !== 'GET') return commandRecording(hex(64, '1'), {revision: 2, state: 'cancelled'});
    served++;
    return params(call).get('state') === 'history'
      ? pageOf([commandRecording(hex(64, '9'))], '', 2)
      : pageOf([commandRecording(hex(64, '1'), {revision: served})], '', served);
  });
  const controller = controllerFor(api, {requestId});
  try {
    await controller.load({view: 'recorded'});
    const row = controller.getSnapshot().page!.recordings[0];
    const pending = controller.cancel(row);
    pending.catch(() => {});
    await tick();
    await controller.load({view: 'history'});
    await controller.load({view: 'recorded'});
    const before = controller.getSnapshot();
    assert.equal(before.page?.revision, 3, 'the return to A is a fresh generation');
    receipt.resolve(hex(48, '9'));
    assert.deepEqual(await pending, {committed: false, refreshed: false, busy: false});
    assert.equal(listCalls(calls).length, 0);
    assert.equal(controller.getSnapshot().page, before.page);
    assert.equal(controller.contains(row), false, 'the first A row is not revived');
    assert.equal(state.minted, 1);
  } finally { controller.dispose(); }
});

// Reviewer fix B — a known ACK is committed even when the reload then loses authority.
test('an ACK followed by a failed reload is committed and erases every private fact', async () => {
  for (const status of [401, 403]) {
    const {receipt, state, requestId} = heldCommand();
    let served = 0;
    const {api, calls} = fixtureServer(call => {
      if (call.method !== 'GET') return commandRecording(hex(64, '1'), {revision: 2, state: 'cancelled'});
      if (pathname(call) === '/v1/dvr/summary') return summaryFact;
      if (pathname(call) === '/v1/dvr/groups') return groupsFact('recent', '');
      served++;
      if (served > 1) throw {status};
      return pageOf([commandRecording(hex(64, '1'))], '', 1);
    });
    const controller = controllerFor(api, {requestId});
    try {
      await controller.load({view: 'recorded'});
      await controller.readSummary();
      await controller.readGroups();
      const row = controller.getSnapshot().page!.recordings[0];
      const pending = controller.cancel(row);
      pending.catch(() => {});
      await tick();
      receipt.resolve(hex(48, 'a'));
      assert.deepEqual(await pending, {committed: true, refreshed: false, busy: false});
      const snapshot = controller.getSnapshot();
      assert.equal(snapshot.accessDenied, true);
      assert.equal(snapshot.page, null, 'the ACK is not kept as an optimistic fact');
      assert.equal(snapshot.detail, null);
      assert.equal(snapshot.storage, null);
      assert.equal(snapshot.summary, null);
      assert.equal(snapshot.groups, null);
      assert.equal(controller.contains(row), false);
      assert.equal(listCalls(calls).length, 1, 'no retry after the ACK');
      assert.equal(state.minted, 1, 'no second receipt');
      assert.equal(calls.filter(call => pathname(call) === '/v1/dvr/summary').length, 1);
    } finally { controller.dispose(); }
  }
});

// Reviewer round 2 — a stale command completion never touches newer destination state.
test('a stale command completion never makes a pending refresh look ready', async () => {
  const receipt = held<string>(), refreshReply = held<unknown>();
  let served = 0;
  const {api, calls} = fixtureServer(call => {
    if (call.method !== 'GET') return commandRecording(hex(64, '1'), {revision: 2, state: 'cancelled'});
    served++;
    return served === 1 ? pageOf([commandRecording(hex(64, '1'))], '', 1) : refreshReply.promise;
  });
  const controller = controllerFor(api, {requestId: () => receipt.promise});
  try {
    await controller.load({view: 'recorded'});
    const row = controller.getSnapshot().page!.recordings[0];
    const pending = controller.cancel(row);
    pending.catch(() => {});
    await tick();
    const refreshing = controller.load({view: 'recorded'});
    refreshing.catch(() => {});
    await tick();
    receipt.resolve(hex(48, 'b'));
    assert.deepEqual(await pending, {committed: false, refreshed: false, busy: false});
    assert.equal(listCalls(calls).length, 0, 'the stale command never sends HTTP');
    let snapshot = controller.getSnapshot();
    assert.equal(snapshot.phase, 'loading', 'the pending refresh keeps the destination loading');
    assert.equal(controller.contains(row), false, 'the retained old page is not ready membership');

    refreshReply.resolve(pageOf([commandRecording(hex(64, '2'))], '', 2));
    await refreshing;
    snapshot = controller.getSnapshot();
    assert.equal(snapshot.phase, 'ready');
    assert.equal(snapshot.page?.revision, 2);
    assert.equal(controller.contains(row), false);
    assert.equal(controller.contains(snapshot.page!.recordings[0]), true);
  } finally { controller.dispose(); }
});

test('an ACK that lands during a pending refresh keeps the newer loading state', async () => {
  const postReply = held<unknown>(), refreshReply = held<unknown>();
  let served = 0;
  const {api} = fixtureServer(call => {
    if (call.method !== 'GET') return postReply.promise;
    served++;
    return served === 1 ? pageOf([commandRecording(hex(64, '1'))], '', 1) : refreshReply.promise;
  });
  const controller = controllerFor(api, {requestId: async () => hex(48, 'c')});
  try {
    await controller.load({view: 'recorded'});
    const row = controller.getSnapshot().page!.recordings[0];
    const pending = controller.cancel(row);
    pending.catch(() => {});
    await tick();
    const refreshing = controller.load({view: 'recorded'});
    refreshing.catch(() => {});
    await tick();
    postReply.resolve(commandRecording(hex(64, '1'), {revision: 2, state: 'cancelled'}));
    assert.deepEqual(await pending, {committed: true, refreshed: false, busy: false});
    let snapshot = controller.getSnapshot();
    assert.equal(snapshot.phase, 'loading', 'the ACK does not publish over the pending refresh');
    assert.equal(controller.contains(row), false);

    refreshReply.resolve(pageOf([commandRecording(hex(64, '2'))], '', 2));
    await refreshing;
    snapshot = controller.getSnapshot();
    assert.equal(snapshot.phase, 'ready');
    assert.equal(snapshot.page?.revision, 2);
    assert.equal(controller.contains(snapshot.page!.recordings[0]), true);
  } finally { controller.dispose(); }
});

test('a newer destination failure survives a stale command completion', async () => {
  const receipt = held<string>(), historyReply = held<unknown>();
  let served = 0;
  const {api} = fixtureServer(call => {
    if (call.method !== 'GET') return commandRecording(hex(64, '1'), {revision: 2, state: 'cancelled'});
    served++;
    if (params(call).get('state') === 'history') return historyReply.promise;
    return pageOf([commandRecording(hex(64, '1'))], '', served);
  });
  const controller = controllerFor(api, {requestId: () => receipt.promise});
  try {
    await controller.load({view: 'recorded'});
    const row = controller.getSnapshot().page!.recordings[0];
    const pending = controller.cancel(row);
    pending.catch(() => {});
    await tick();
    const history = controller.load({view: 'history'});
    history.catch(() => {});
    await tick();
    historyReply.reject(new Error('history unavailable'));
    await history;
    const failed = controller.getSnapshot();
    assert.equal(failed.phase, 'error');
    assert.ok(failed.error.length > 0);

    receipt.resolve(hex(48, 'd'));
    assert.deepEqual(await pending, {committed: false, refreshed: false, busy: false});
    const after = controller.getSnapshot();
    assert.equal(after.phase, 'error', 'the newer destination failure survives the stale command');
    assert.equal(after.error, failed.error);
    assert.equal(after.page, null);
    assert.equal(controller.contains(row), false);
  } finally { controller.dispose(); }
});

for (const view of ['upcoming', 'history'] as const) {
  test(`${view} exposes all 101 producer rows, carries only offered cursors and revisits pages`, async () => {
    const all = Array.from({length: 101}, (_, n) => recording(n.toString(16).padStart(64, '0'), {state: view === 'history' ? 'completed' : 'scheduled'}));
    const {api, calls} = fixtureServer(call => {
      if (pathname(call).endsWith('/summary')) return summaryFact;
      const cursor = params(call).get('cursor') ?? '';
      const offset = cursor === '' ? 0 : cursor === 'opaque/50' ? 50 : 100;
      return pageOf(all.slice(offset, offset + 50), offset === 0 ? 'opaque/50' : offset === 50 ? 'opaque/100' : '', offset + 1);
    });
    const c = controllerFor(api);
    try {
      await c.load({view});await c.readSummary();
      const seen = [...c.getSnapshot().page!.recordings];
      await c.more();seen.push(...c.getSnapshot().page!.recordings);
      await c.more();seen.push(...c.getSnapshot().page!.recordings);
      assert.deepEqual(seen.map(r => r.id), all.map(r => r.id));
      assert.equal(c.getSnapshot().paging.next, false);
      const reads = calls.length;await c.more();assert.equal(calls.length, reads);
      await c.previous();assert.equal(c.getSnapshot().query.cursor, 'opaque/50');
      await c.previous();assert.equal(c.getSnapshot().query.cursor ?? '', '');
      assert.deepEqual(c.getSnapshot().summary, summaryFact);
      assert.ok(c.getSnapshot().page!.recordings.length <= 50);
      assert.deepEqual(calls.filter(call => pathname(call).endsWith('/dvr')).slice(0, 3).map(call => params(call).get('cursor')), [null, 'opaque/50', 'opaque/100']);
    } finally {c.dispose();}
  });
}

test('offered page failure preserves exact rows, coalesces clicks and retries that cursor', async () => {
  const pending = held<unknown>();let tailReads = 0;
  const {api, calls} = fixtureServer(call => {
    if (!params(call).has('cursor')) return pageOf([recording(hex())], 'two');
    tailReads++;if (tailReads === 1) return pending.promise;
    return pageOf([recording(hex(64, 'b'))], 'two'); // invalid repeated next token stops here
  });
  const c = controllerFor(api);
  try {
    await c.load();const first = c.getSnapshot().page, old = c.getSnapshot();
    const more = c.more();await c.more();assert.equal(calls.length, 2);
    assert.equal(c.getSnapshot().page, first);assert.equal(c.getSnapshot().phase, 'ready');
    pending.reject({status: 503});await more;
    assert.equal(c.getSnapshot().page, first);assert.ok(c.getSnapshot().paging.error);
    await c.retryPage();assert.equal(calls.length, 3);
    assert.equal(c.getSnapshot().paging.next, false);
    await c.more(old);assert.equal(calls.length, 3, 'saved original callback cannot advance a successor');
    await c.previous();assert.equal(c.getSnapshot().query.cursor ?? '', '');
  } finally {c.dispose();}
});

for (const change of ['view', 'refresh', 'leave', 'authority'] as const) {
  test(`pending tail cannot publish after ${change}`, async () => {
    const pending = held<unknown>();let live = true;
    const {api} = fixtureServer(call => params(call).has('cursor') ? pending.promise : pageOf([recording(hex())], 'two'));
    const c = controllerFor(api, {isCurrent: () => live});
    try {
      await c.load();const row = c.getSnapshot().page!.recordings[0];
      const tail = c.more();
      if (change === 'view') await c.load({view: 'history'});
      if (change === 'refresh') await c.load({view: 'upcoming'});
      if (change === 'leave') c.dispose();
      if (change === 'authority') live = false;
      pending.resolve(pageOf([recording(hex(64, 'b'))], 'three'));
      await assert.rejects(tail, {name: 'AbortError'});
      assert.equal(c.contains(row), false);
      assert.notEqual(c.getSnapshot().query.cursor, 'two');
      assert.equal(c.getSnapshot().paging.busy, false);
    } finally {c.dispose();}
  });
}

test('recorded directory traverses 61 genuine groups, retains counts and retries original sort/cursor', async () => {
  let fail = true;const seen: string[] = [];
  const {api, calls} = fixtureServer(call => {
    if (pathname(call).endsWith('/summary')) return summaryFact;
    if (!pathname(call).endsWith('/groups')) return pageOf([]);
    const cursor = params(call).get('cursor') ?? '';
    if (cursor === 'g30' && fail) {fail = false;throw {status: 503};}
    const offset = cursor === '' ? 0 : cursor === 'g30' ? 30 : 60;
    return {revision: offset + 1, nextCursor: offset === 0 ? 'g30' : offset === 30 ? 'g60' : '',
      groups: Array.from({length: Math.min(30, 61 - offset)}, (_, n) => ({...groupsFact('title', '').groups[0], key: `group-${offset + n}`}))};
  });
  const c = controllerFor(api);
  try {
    await c.load({view: 'recorded'});await c.readSummary();await c.readGroups('title');
    const original = c.getSnapshot().groups;seen.push(...original!.groups.map(g => g.key));
    await assert.rejects(c.more());assert.equal(c.getSnapshot().groups, original);
    await c.retryPage();seen.push(...c.getSnapshot().groups!.groups.map(g => g.key));
    await c.more();seen.push(...c.getSnapshot().groups!.groups.map(g => g.key));
    assert.equal(new Set(seen).size, 61);assert.equal(seen.at(-1), 'group-60');
    assert.equal(c.getSnapshot().paging.next, false);assert.deepEqual(c.getSnapshot().summary, summaryFact);
    assert.deepEqual(calls.filter(call => pathname(call).endsWith('/groups')).map(call => [params(call).get('sort'), params(call).get('cursor')]), [['title', null], ['title', 'g30'], ['title', 'g30'], ['title', 'g60']]);
    await c.previous();assert.equal(c.getSnapshot().groups!.groups[0].key, 'group-30');
  } finally {c.dispose();}
});

test('changing group sort retires a pending tail and its retry', async () => {
  const pending = held<unknown>();
  const {api, calls} = fixtureServer(call => pathname(call).endsWith('/groups')
    ? params(call).get('cursor') ? pending.promise : groupsFact(params(call).get('sort')!, 'tail') : pageOf([]));
  const c = controllerFor(api);
  try {
    await c.load({view: 'recorded'});await c.readGroups('recent');
    const old = c.getSnapshot(), tail = c.more();await c.readGroups('title');
    pending.resolve(groupsFact('recent', 'old-next'));await assert.rejects(tail, {name: 'AbortError'});
    await c.retryPage(old);assert.equal(calls.length, 4);
    assert.equal(c.getSnapshot().groupsSort, 'title');assert.equal(c.getSnapshot().groups!.groups[0].key, 'title-one');
  } finally {c.dispose();}
});

for (const replacement of ['view', 'refresh', 'dispose'] as const) {
  test(`busy publication ${replacement} reentry dispatches no old recording cursor`, async () => {
    const pending = held<unknown>();let initial = true;
    const {api, calls} = fixtureServer(call => {
      if (initial) {initial = false;return pageOf([recording(hex())], 'old-tail');}
      return pending.promise;
    });
    const c = controllerFor(api);let replacementRead: Promise<void> | undefined, entered = false;
    try {
      await c.load();
      c.subscribe(() => {
        if (entered || !c.getSnapshot().paging.busy) return;
        entered = true;
        if (replacement === 'dispose') c.dispose();
        else replacementRead = c.load({view: replacement === 'view' ? 'history' : 'upcoming'});
      });
      await assert.rejects(c.more(), {name: 'AbortError'});
      assert.equal(calls.some(call => params(call).get('cursor') === 'old-tail'), false);
      if (replacementRead) {
        assert.equal(c.getSnapshot().phase, 'loading');
        pending.resolve(pageOf([recording(hex(64, 'b'))], 'new-tail'));await replacementRead;
        assert.equal(c.getSnapshot().page!.recordings[0].id, hex(64, 'b'));
        assert.equal(c.getSnapshot().page!.nextCursor, 'new-tail');
        assert.equal(calls.length, 2, 'new model load was never displaced by the old tail');
      } else assert.equal(calls.length, 1);
    } finally {c.dispose();}
  });
}

for (const replacement of ['sort', 'view', 'refresh', 'dispose'] as const) {
  test(`busy publication ${replacement} reentry dispatches no old group cursor`, async () => {
    const {api, calls} = fixtureServer(call => pathname(call).endsWith('/groups') ? groupsFact(params(call).get('sort')!, 'old-tail') : pageOf([]));
    const c = controllerFor(api);let replacementRead: Promise<unknown> | undefined, entered = false;
    try {
      await c.load({view: 'recorded'});await c.readGroups('recent');
      c.subscribe(() => {
        if (entered || !c.getSnapshot().paging.busy) return;
        entered = true;
        if (replacement === 'dispose') c.dispose();
        else replacementRead = replacement === 'sort' ? c.readGroups('title') : c.load({view: replacement === 'view' ? 'history' : 'recorded'});
      });
      await assert.rejects(c.more(), {name: 'AbortError'});await replacementRead;
      assert.equal(calls.some(call => params(call).has('cursor')), false);
      assert.equal(c.getSnapshot().paging.busy, false);
      if (replacement === 'sort') assert.equal(c.getSnapshot().groupsSort, 'title');
    } finally {c.dispose();}
  });
}

test('a reentrant isCurrent true result cannot revive a disposed controller', async () => {
  let retire = false, c: ReturnType<typeof controllerFor>;
  const {api, calls} = fixtureServer(() => pageOf([recording(hex())], 'old-tail'));
  c = controllerFor(api, {isCurrent: () => {if (retire) {retire = false;c.dispose();}return true;}});
  await c.load();retire = true;await assert.rejects(c.more(), {name: 'AbortError'});
  assert.equal(calls.length, 1);assert.equal(c.getSnapshot().phase, 'retired');
});

for (const replacement of ['view', 'refresh', 'dispose'] as const) {
  test(`SECOND busy publication ${replacement} reentry cannot dispatch the original model cursor`, async () => {
    const pending = held<unknown>();let initial = true, notices = 0;
    const {api, calls} = fixtureServer(() => {
      if (initial) {initial = false;return pageOf([recording(hex())], 'old-tail');}
      return pending.promise;
    });
    const c = controllerFor(api);let newer: Promise<void> | undefined;
    try {
      await c.load();c.subscribe(() => {
        if (!c.getSnapshot().paging.busy || ++notices !== 2) return;
        if (replacement === 'dispose') c.dispose();
        else newer = c.load({view: replacement === 'view' ? 'history' : 'upcoming'});
      });
      await assert.rejects(c.more(), {name: 'AbortError'});
      assert.equal(calls.some(call => params(call).get('cursor') === 'old-tail'), false);
      if (newer) {
        assert.equal(c.getSnapshot().phase, 'loading');pending.resolve(pageOf([recording(hex(64, 'b'))], 'new-tail'));await newer;
        assert.equal(c.getSnapshot().page!.recordings[0].id, hex(64, 'b'));
        assert.equal(c.getSnapshot().page!.nextCursor, 'new-tail');assert.equal(calls.length, 2);
      } else assert.equal(calls.length, 1);
    } finally {c.dispose();}
  });
}

test('API-entry external current predicate cannot lend a nested successor read to an old model cursor', async () => {
  let armed = false, switchAtAPI = false, newer: Promise<void> | undefined;
  const {api, calls} = fixtureServer(() => pageOf([recording(hex())], 'tail'));
  let c: ReturnType<typeof controllerFor>;
  c = controllerFor(api, {isCurrent: () => {
    if (switchAtAPI) {switchAtAPI = false;newer = c.load({view: 'history'});}
    return true;
  }});
  try {
    await c.load();let notices = 0;
    c.subscribe(() => {if (armed && c.getSnapshot().paging.busy && ++notices === 2) switchAtAPI = true;});
    armed = true;await assert.rejects(c.more(), {name: 'AbortError'});await newer;
    assert.equal(calls.some(call => params(call).has('cursor')), false);
    assert.equal(c.getSnapshot().query.view, 'history');assert.equal(c.getSnapshot().phase, 'ready');
  } finally {c.dispose();}
});
