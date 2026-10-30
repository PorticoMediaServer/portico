import {test} from 'node:test';
import assert from 'node:assert/strict';
import {GroupSessionService} from '../src/group-session.ts';
import {SOCIAL_PROTOCOL} from '../src/social-playback.ts';

const T0 = Date.parse('2026-09-16T20:00:00Z');
const sync = {noCorrectionUnderMs: 750, rateCorrectionMinimum: '0.90', rateCorrectionMaximum: '1.10', rateCorrectionMaxMs: 4000, seekAtOrOverMs: 3000};
const timeline = {itemId: 'item', currentEntryId: 'ent_1', state: 'playing', anchorPositionUs: '10000000', anchorAt: '2026-09-16T20:00:00Z', rate: {numerator: '1', denominator: '1'}, queuePosition: 0, waitUntil: null};
const host = {id: 'mem_1', displayName: 'Host', private: false, role: 'host', state: 'joined', readiness: 'ready', positionUs: '0', reportedAt: '2026-09-16T20:00:00Z', presence: 'connected', joinedAt: '2026-09-16T19:59:00Z'};
const group = (over: Record<string, unknown> = {}) => ({
  id: 'grp_1', name: 'Night', state: 'playing', hostAuthority: 'host-only', hostMemberId: 'mem_1', revision: '7', playbackRevision: '3', settingsRevision: '1', governanceRevision: '1', queueRevision: '2', reconnectGeneration: '1',
  lastCommand: 'play', lastCommandId: 'p', endedReason: '', eventOrdinal: '9', createdAt: '2026-09-16T19:59:00Z', updatedAt: '2026-09-16T20:00:00Z',
  permissions: {isHost: true, canControl: true, canManageQueue: true}, authority: {deviceId: 'dev_1', playbackId: 'pb1', state: 'bound'},
  host: {presence: 'connected', lastSeenAt: '2026-09-16T20:00:00Z', pauseAt: null, endAt: null}, timeline, settings: {shuffleEnabled: false, repeatMode: 'none'}, sync,
  readiness: {aggregate: 'ready', ready: 1, buffering: 0, lagging: 0, stale: 0, memberCount: 1}, members: [host], queue: null, viewerMemberId: 'mem_1', ...over,
});
const snap = (over: Record<string, unknown> = {}) => ({protocolVersion: SOCIAL_PROTOCOL, serverTime: '2026-09-16T20:00:00Z', group: group(over)});
const queueDoc = {protocolVersion: SOCIAL_PROTOCOL, serverTime: 'x', groupId: 'grp_1', queue: {revision: '3', position: 0, entries: [{entryId: 'ent_9', position: 0, itemId: 'wanted', unavailable: false, addedBy: 'mem_1'}], eligibility: null}};
const settle = () => new Promise((r) => setTimeout(r, 15));

function server() {
  const calls: {path: string; method: string; body?: unknown}[] = [];
  let current = snap();
  const api = {
    baseUrl: 'https://s',
    request: async <T,>(path: string, method = 'GET', body?: unknown): Promise<T> => {
      calls.push({path, method, body});
      if (path === '/v1/groups') return {protocolVersion: SOCIAL_PROTOCOL, serverTime: 'x', groups: [current.group]} as T;
      if (path.endsWith('/settings')) return {} as T;
      if (path.endsWith('/queue')) {
        if (method === 'POST') return queueDoc as T;
        return queueDoc as T;
      }
      if (path.endsWith('/transport')) {
        return {protocolVersion: SOCIAL_PROTOCOL, groupId: 'grp_1', idempotencyKey: (body as {idempotencyKey: string}).idempotencyKey, disposition: 'accepted', groupState: 'playing', playbackRevision: '4', command: (body as {command: string}).command, revision: '8', queueRevision: '3', serverTime: '2026-09-16T20:00:01Z', recordedAt: '2026-09-16T20:00:01Z', timeline: {...timeline, state: 'playing'}, settings: {shuffleEnabled: false, repeatMode: 'none'}, override: null} as T;
      }
      if (path.endsWith('/leave') || path.endsWith('/end') || path.endsWith('/readiness')) return {} as T;
      return current as T;
    },
  };
  const stream = async () => new Response(new ReadableStream({start(c) { c.close(); }}), {status: 200});
  return {api, stream, calls, set: (next: ReturnType<typeof snap>) => { current = next; }};
}
const make = (s: ReturnType<typeof server>) => {
  let n = 0;
  return new GroupSessionService({api: s.api, stream: s.stream, key: () => 'key-' + ++n, now: () => T0, heartbeatMs: 3600000});
};
const posts = (calls: {path: string; method: string}[], suffix: string) =>
  calls.filter((c) => c.path.endsWith(suffix) && c.method === 'POST');

test('settings, queue remove and move send fenced revisions', async () => {
  const s = server(), service = make(s);
  try {
    await service.open('grp_1');
    assert.equal(await service.settings({shuffleEnabled: true}), true);
    assert.deepEqual(posts(s.calls, '/settings').at(-1)!.body, {protocolVersion: SOCIAL_PROTOCOL, idempotencyKey: 'key-1', expectedRevision: '1', shuffleEnabled: true});
    assert.equal(await service.queueRemove('ent_1'), true);
    assert.deepEqual(posts(s.calls, '/queue').at(-1)!.body, {protocolVersion: SOCIAL_PROTOCOL, idempotencyKey: 'key-2', expectedRevision: '2', operation: 'remove', entryId: 'ent_1'});
    assert.equal(await service.queueMove('ent_1', 'ent_2', 'before'), true);
    assert.deepEqual(posts(s.calls, '/queue').at(-1)!.body, {protocolVersion: SOCIAL_PROTOCOL, idempotencyKey: 'key-3', expectedRevision: '3', operation: 'move', entryId: 'ent_1', destinationEntryId: 'ent_2', placement: 'before'});
  } finally {
    service.dispose();
  }
});

test('watch queues then selects the new entry; leave and end close the room', async () => {
  const s = server(), service = make(s);
  try {
    await service.open('grp_1');
    assert.equal(await service.watch('wanted'), true);
    const load = posts(s.calls, '/transport').at(-1)!;
    assert.equal((load.body as {command: string}).command, 'load');
    assert.equal((load.body as {entryId: string}).entryId, 'ent_9');
    await service.leave();
    assert.equal(service.getSnapshot().phase, 'left');
    await service.open('grp_1');
    assert.equal(await service.end(), true);
    assert.equal(service.getSnapshot().phase, 'ended');
  } finally {
    service.dispose();
  }
});

test('a failed directory refresh keeps the list; notices speak and dismiss clears', async () => {
  const s = server(), service = make(s);
  try {
    await service.refreshDirectory();
    assert.equal(service.getSnapshot().directory.length, 1);
    service.notice('Hello');
    assert.equal(service.getSnapshot().error, 'Hello');
    service.dismiss();
    assert.equal(service.getSnapshot().phase, 'idle');
    assert.equal(service.getSnapshot().error, undefined);
  } finally {
    service.dispose();
  }
  const failing = new GroupSessionService({
    api: {baseUrl: 'https://s', request: async () => { throw new TypeError('Failed to fetch'); }},
    stream: s.stream, heartbeatMs: 3600000,
  });
  try {
    await failing.refreshDirectory();
    assert.equal(failing.getSnapshot().directoryPhase, 'error');
  } finally {
    failing.dispose();
  }
  await settle();
});
