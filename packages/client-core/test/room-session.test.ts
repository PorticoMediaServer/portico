import test from 'node:test';
import assert from 'node:assert/strict';
import {RoomSessionService as GroupSessionService, RoomFollower as GroupFollower, type RoomPlayer as GroupPlayer} from '../src/room-session.ts';
import {parseRoom, parseGroupSummaryPage, roomPositionMs} from '../src/room-protocol.ts';
import {serverEventsFrom} from '../src/server-events.ts';
const T0 = Date.parse('2026-11-16T21:00:00Z');
const stamp = (at = T0) => new Date(at).toISOString();
function room(patch: Record<string, unknown> = {}) {
  return {id: 'room', name: 'Movie night', control: 'host', you: {memberId: 'host', host: true, mayControl: true, mayBecomeHost: false},
    invitation: {code: 'ABCDEFGH', expiresAt: stamp(T0 + 900000), joinsLeft: 8},
    clock: {positionMs: 0, at: stamp(), playing: false, ready: 1, waitingFor: 1}, host: {present: true},
    members: [{id: 'host', name: 'Alex', host: true, you: true, status: 'in_step'}], queue: [], inStepMs: 750, jumpMs: 3000, reportEveryMs: 10000, ...patch};
}
const entries = [{entryId: 'first', itemId: 'movie', title: 'Movie', available: true}, {entryId: 'second', itemId: 'movie', title: 'Movie', available: true}, {entryId: 'hidden', available: false}];
const settle = async () => {for (let i = 0; i < 30; i++) await Promise.resolve();};
function fixture(initial = room()) {
  let current = initial, nextError: {status: number; headers?: Record<string, string>; body?: unknown} | undefined;
  const calls: {path: string; method: string; body?: any; headers?: Readonly<Record<string, string>>; signal?: AbortSignal}[] = [];
  const listeners = new Map<string, Function>(); let statusListener = () => {};
  const events = {on(type: string, fn: Function) {listeners.set(type, fn); return () => {listeners.delete(type);};}, onResync(fn: Function) {listeners.set('resync', fn); return () => {listeners.delete('resync');};}, subscribeStatus(fn: () => void) {statusListener = fn; return () => {statusListener = () => {};};}, getStatus: () => ({connected: true})};
  const api = {async requestRaw(path: string, method: string, options: any = {}) {
    calls.push({path, method, ...options});
    if (nextError) {const result = nextError; nextError = undefined; return {headers: {}, ...result};}
    if (path.endsWith('/queue') && method === 'PUT') current = {...current, queue: options.body.entries.map((entry: any, at: number) => entry.entryId ? current.queue.find((row: any) => row.entryId === entry.entryId) : {entryId: 'added-' + at, itemId: entry.itemId, available: true})};
    return {status: method === 'DELETE' ? 204 : 200, headers: {}, body: path.startsWith('/v1/groups?') || path === '/v1/groups' && method === 'GET' ? {items: [{id: 'room', name: 'Movie night', members: 1, host: true}]} : current};
  }};
  const service = new GroupSessionService({api, events: events as never, key: () => 'create-key', now: () => T0});
  return {service, calls, set: (value: typeof initial) => {current = value;}, fail: (value: typeof nextError) => {nextError = value;}, emit: (type: string, value?: any) => listeners.get(type)?.(value), reconnect: () => statusListener()};
}

test('current directory/Room parse; private members and unavailable duplicate occurrences stay opaque', () => {
  assert.deepEqual(parseGroupSummaryPage({items: []}).items, []);
  const value = parseRoom(room({queue: entries, members: [{id: 'guest', private: true, name: 'secret', username: 'secret', host: false, you: false, status: 'buffering'}]}));
  assert.equal(value.members[0].name, undefined); assert.equal(value.members[0].username, undefined);
  assert.equal(value.queue[0].itemId, value.queue[1].itemId); assert.notEqual(value.queue[0].entryId, value.queue[1].entryId);
  assert.deepEqual(value.queue[2], {entryId: 'hidden', available: false});
  assert.ok(Object.isFrozen(value.queue));
  assert.throws(() => parseRoom(room({queue: [entries[0], entries[0]]})));
  assert.throws(() => parseRoom(room({reportEveryMs: 0})));
  assert.throws(() => parseRoom({protocolVersion: '1.0', group: room()}));
  assert.equal(roomPositionMs(parseRoom(room({clock: {entryId: 'first', positionMs: 500, at: stamp(), playing: true, waitUntil: stamp(T0 + 5000), ready: 1, waitingFor: 2}})), T0 + 10000), 500);
});

test('create uses header key and current fields, code join has exact refusal and opens presence on this device', async () => {
  const f = fixture(); try {
    assert.equal(await f.service.create({name: ' Movie night ', displayName: ' Alex ', control: 'host'}), true);
    assert.deepEqual(f.calls[0].body, {name: 'Movie night', displayName: 'Alex', control: 'host'});
    assert.deepEqual(f.calls[0].headers, {'Idempotency-Key': 'create-key'});
    assert.deepEqual(f.calls[1].body, {state: 'ready', positionMs: 0, here: true});
    f.fail({status: 404, body: {error: {code: 'code_invalid'}}});
    assert.equal(await f.service.join(' bad-code ', 'Alex'), false);
    assert.deepEqual(f.service.getSnapshot().joinFailure, {code: 'code_invalid', inviteCode: 'BADCODE'});
    assert.equal(await f.service.join(' abcd-efgh ', 'Alex'), true);
    const join = f.calls.findLast(c => c.path.endsWith('/join'))!; assert.deepEqual(join.body, {code: 'ABCDEFGH', displayName: 'Alex'});
  } finally {f.service.dispose();}
});

test('commands, queue replacement and host/code edits use current routes and actual occurrence IDs', async () => {
  const f = fixture(room({queue: entries})); try {
    await f.service.open('room');
    await f.service.transport('seek', {positionMs: 1234});
    assert.deepEqual(f.calls.at(-1)?.body, {action: 'seek', positionMs: 1234}); assert.equal(f.calls.at(-1)?.path, '/v1/groups/room/playback');
    await f.service.queueRemove('first'); assert.deepEqual(f.calls.at(-1)?.body, {entries: [{entryId: 'second'}, {entryId: 'hidden'}]});
    await f.service.queueMove('hidden', 'second', 'before'); assert.deepEqual(f.calls.at(-1)?.body, {entries: [{entryId: 'hidden'}, {entryId: 'second'}]});
    await f.service.watch('movie'); assert.equal(f.calls.at(-1)?.body.action, 'watch'); assert.equal(f.calls.at(-1)?.body.entryId, 'added-2');
    await f.service.makeHost('guest'); assert.deepEqual(f.calls.at(-1)?.body, {host: 'guest'});
    await f.service.makeHost('host'); assert.deepEqual(f.calls.at(-1)?.body, {becomeHost: true});
    await f.service.invite({joins: 3}); assert.deepEqual(f.calls.at(-1)?.body, {newCode: {joins: 3}});
    assert.equal(f.calls.at(-1)?.method, 'PATCH');
    assert.ok(f.calls.every(c => !JSON.stringify(c.body ?? {}).includes('Revision') && !JSON.stringify(c.body ?? {}).includes('protocolVersion')));
    await f.service.leave(); assert.equal(f.service.getSnapshot().phase, 'left');
    assert.ok(f.calls.some(c => c.path === '/v1/groups/room/me' && c.method === 'DELETE'));
    await f.service.open('room'); assert.equal(await f.service.end(), true);
    assert.equal(f.service.getSnapshot().group?.ended?.reason, 'ended_by_host');
  } finally {f.service.dispose();}
});

test('group feed change, resync and reconnect re-read; fallback and reports obey retry-after and cadence', async t => {
  t.mock.timers.enable({apis: ['setTimeout']}); const f = fixture();
  try {
    await f.service.open('room');
    assert.deepEqual(serverEventsFrom({id: '4', event: 'change', data: JSON.stringify({topic: 'group', id: 'room'})}), [{id: '4', type: 'group.changed', resource: {kind: 'group', id: 'room'}}]);
    f.set(room({name: 'Changed'})); f.emit('group.changed', {resource: {id: 'room'}}); await settle(); assert.equal(f.service.getSnapshot().group?.name, 'Changed');
    f.set(room({name: 'Resynced'})); f.emit('resync'); await settle(); assert.equal(f.service.getSnapshot().group?.name, 'Resynced');
    f.set(room({name: 'Reconnected'})); f.reconnect(); await settle(); assert.equal(f.service.getSnapshot().group?.name, 'Reconnected');
    const reads = f.calls.length; f.fail({status: 503, headers: {'retry-after': '60'}}); await f.service.refresh(); assert.equal(f.service.getSnapshot().phase, 'reconnecting');
    await f.service.refresh(); assert.equal(f.calls.length, reads + 1);
    t.mock.timers.tick(10000); await settle(); assert.equal(f.calls.length, reads + 1);
  } finally {f.service.dispose();}
});

test('late opening and mutation replies cannot restore a dismissed viewer/room; failed leave stays retryable', async () => {
  let resolve!: (value: any) => void;
  const service = new GroupSessionService({api: {requestRaw: async () => new Promise(done => {resolve = done;})}});
  const life = new AbortController(); const opening = service.open('room', {signal: life.signal, isCurrent: () => true});
  life.abort(); resolve({status: 200, headers: {}, body: room()}); assert.equal(await opening, false); assert.equal(service.getSnapshot().group, undefined); service.dispose();
  const f = fixture(); try {await f.service.open('room'); f.fail({status: 503}); await f.service.leave(); assert.equal(f.service.getSnapshot().phase, 'live'); assert.ok(f.service.getSnapshot().error);}
  finally {f.service.dispose();}
});

test('follower waits for Room readiness, reports ms and uses server correction bands with seek settling', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  let now = T0, current = room({queue: entries, clock: {entryId: 'first', positionMs: 10000, at: stamp(), playing: true, waitUntil: stamp(T0 + 5000), ready: 0, waitingFor: 1}});
  const reports: any[] = [], service = new GroupSessionService({now: () => now, api: {requestRaw: async (_path, _method, options) => {if (_path.endsWith('/me')) reports.push(options?.body); return {status: 200, headers: {}, body: current};}}});
  const changes: any[] = []; let local = {itemId: 'movie', positionSeconds: 10, playing: false, buffering: false, ready: true, seeking: false, durationSeconds: 100};
  const player: GroupPlayer = {state: () => local, load: (...args) => changes.push(['load', ...args]), play: () => {changes.push(['play']); local.playing = true;}, pause: () => {changes.push(['pause']); local.playing = false;}, seek: seconds => {changes.push(['seek', seconds]); local.seeking = true;}, setRate: rate => changes.push(['rate', rate])};
  await service.open('room'); const follower = new GroupFollower(service, player, () => now);
  try {
    follower.start(); assert.ok(!changes.some(c => c[0] === 'play'));
    current = room({queue: entries, clock: {entryId: 'first', positionMs: 10000, at: stamp(), playing: true, ready: 1, waitingFor: 1}});
    await service.refresh(); assert.ok(changes.some(c => c[0] === 'play'));
    local.positionSeconds = 9; t.mock.timers.tick(1000); await settle(); assert.ok(changes.some(c => c[0] === 'rate' && c[1] > 1));
    local.positionSeconds = 0; t.mock.timers.tick(1000); await settle(); assert.equal(changes.filter(c => c[0] === 'seek').length, 1);
    t.mock.timers.tick(1000); await settle(); assert.equal(changes.filter(c => c[0] === 'seek').length, 1, 'a seek already in flight is not restarted');
    now += 10000; local.positionSeconds = 12; local.seeking = false; t.mock.timers.tick(1000); await settle();
    assert.ok(reports.some(report => report.positionMs === 12000 && report.entryId === 'first'));
  } finally {follower.stop(); service.dispose();}
});

test('late command cannot restore a closed room', async () => {
  let pending: ((answer: any) => void) | undefined;
  const service = new GroupSessionService({now: () => T0, api: {requestRaw: async (_path, method) => {
    if (method === 'POST') return new Promise(done => {pending = done;});
    return {status: 200, headers: {}, body: room()};
  }}});
  try {
    await service.open('room'); const command = service.transport('pause'); await settle();
    assert.ok(pending); service.dismiss(); pending!({status: 200, headers: {}, body: room({name: 'Late'})});
    assert.equal(await command, false); assert.equal(service.getSnapshot().group, undefined);
  } finally {service.dispose();}
});

test('presence uses reportEveryMs, retry hints are floors and disposal cancels fallback', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  let now = T0; const calls: {method: string; path: string; body: any}[] = [];
  const service = new GroupSessionService({now: () => now, api: {requestRaw: async (path, method, options) => {
    calls.push({method, path, body: options?.body}); return {status: 200, headers: {}, body: room({clock: {positionMs: 0, at: stamp(now), playing: false, ready: 1, waitingFor: 1}})};
  }}});
  try {
    await service.open('room'); await service.report('paused', 1234);
    assert.equal(calls.filter(c => c.method === 'PUT').length, 1);
    now += 9999; t.mock.timers.tick(9999); await settle(); assert.equal(calls.filter(c => c.method === 'PUT').length, 1);
    now++; t.mock.timers.tick(1); await settle(); assert.equal(calls.filter(c => c.method === 'PUT').length, 2);
    assert.equal(calls.findLast(c => c.method === 'PUT')?.body.positionMs, 1234);
    assert.ok(calls.every(c => !c.path.endsWith('/heartbeat') && !c.path.endsWith('/events')));
    service.dispose(); const count = calls.length; now += 10000; t.mock.timers.tick(10000); await settle(); assert.equal(calls.length, count);
  } finally {service.dispose();}
});


test('a report queued behind a command cannot overwrite that command with old clock state', async () => {
  let now = T0, current = room(), finish!: () => void;
  const methods: string[] = [];
  const service = new GroupSessionService({now: () => now, api: {requestRaw: async (_path, method) => {
    methods.push(method);
    if (method === 'POST') await new Promise<void>(resolve => {finish = () => {current = room({name: 'Paused'}); resolve();};});
    return {status: 200, headers: {}, body: current};
  }}});
  try {
    await service.open('room'); const command = service.transport('pause'); await settle();
    now += 10000; const report = service.report('paused', 500); await settle();
    assert.equal(methods.at(-1), 'POST'); finish(); await command; await report;
    assert.equal(methods.at(-1), 'PUT'); assert.equal(service.getSnapshot().group?.name, 'Paused');
  } finally {service.dispose();}
});
