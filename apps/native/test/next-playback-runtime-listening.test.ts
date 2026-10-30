/** Plan §9.3 C3: the JS side of the v1 native listening owner (PorticoListeningSessionOwner). */
import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {NativeListeningV1Adapter, parseNativeListeningV1Event, type NativeListeningBridge} from '../src-next/platform/playback/NativeListeningAdapter.ts';
import type {NativeListeningV1Event} from '../../../packages/client-core/src/playback/native-listening.ts';

const session = (id: string, over: Record<string, unknown> = {}) => ({
  id, revision: '1', kind: 'audio', role: 'local', state: 'playing', itemId: `item-${id}`,
  queue: {queueId: 'q1', entryId: `e-${id}`}, lease: {reportEveryMs: 10000},
  presentation: {generation: 1, mode: 'direct', url: `/v1/media/grant-${id}/audio`, startPositionMs: 0, subtitles: [], decision: {}},
  ...over,
});
// Read the actual producer lists, rather than a hand-maintained seven-speed fixture.
const androidProducer = readFileSync(new URL('../android/app/src/main/java/tv/getportico/portico/native/PorticoPlaybackController.kt', import.meta.url), 'utf8');
const appleProducer = readFileSync(new URL('../Native/PorticoAudioRender.m', import.meta.url), 'utf8');
const androidRates = androidProducer.match(/private val RATES = listOf\(([^)]+)\)/)![1].split(',').map(Number);
const appleRates = appleProducer.match(/@"supportedRates":@\[([^\]]+)\]/)![1].split(',').map(value => Number(value.replace('@', '')));
const producerStatus = (supportedRates: number[]) => ({effectiveRate: 1, supportedRates, rendering: true, timerStopped: false, rateWaiting: false, rateError: '', policyRevision: 0, expiredTimerRevision: -1});

const raw = (owner: string, sequence: number, s: Record<string, unknown>, extra: Record<string, unknown> = {}) => ({
  ...producerStatus(androidRates),
  owner, sequence, intent: 'playing', rate: 1, positionSeconds: 3, observed: true, ready: true, seeking: false, ended: false, error: null, terminal: false,
  route: 'Speaker', deadlines: {sleepAtMs: null, passoutAtMs: null}, commandRevision: 2,
  binding: {lease: 'lease-1', intentId: 1, sessionId: s.id, generation: 1, itemId: s.itemId},
  session: s, durationSeconds: 200, queue: {id: 'q1', revision: '7'}, lastSeq: 4, ...extra,
});

test('a v1 snapshot reads its session, queue, seq and binding', () => {
  const e = parseNativeListeningV1Event(raw('o', 1, session('s1')), 'o', 's1');
  assert.equal(e.session.id, 's1');
  assert.equal(e.session.presentation.generation, 1);
  assert.deepEqual(e.queue, {id: 'q1', revision: '7'});
  assert.equal(e.lastSeq, 4);
  assert.equal(e.durationSeconds, 200);
});

for (const [producer, rates] of [['Android', androidRates], ['Apple AudioRender', appleRates]] as const) {
  test(`${producer}'s actual nine-speed capabilities parse at 1x, 2.5x and 3x`, () => {
    assert.deepEqual(rates, [.5, .75, 1, 1.25, 1.5, 1.75, 2, 2.5, 3]);
    for (const effectiveRate of [0, 1, 2.5, 3]) {
      const event = parseNativeListeningV1Event(raw('o', 1, session('s1'), {...producerStatus(rates), effectiveRate}), 'o', 's1');
      assert.equal(event.effectiveRate, effectiveRate);
      assert.deepEqual(event.supportedRates, rates);
    }
  });
}

test('native rate validation still rejects malformed and out-of-contract values', () => {
  for (const effectiveRate of [-1, 3.01, NaN, Infinity, '3']) {
    assert.throws(() => parseNativeListeningV1Event(raw('o', 1, session('s1'), {effectiveRate}), 'o', 's1'), /effective rate/);
  }
  for (const supportedRates of [[...androidRates, 1], [4], ['1'], [NaN], [Infinity], null]) {
    assert.throws(() => parseNativeListeningV1Event(raw('o', 1, session('s1'), {supportedRates}), 'o', 's1'), /supported rates/);
  }
  for (const supportedRates of [[], [1], androidRates.slice(0, 7)]) {
    assert.deepEqual(parseNativeListeningV1Event(raw('o', 1, session('s1'), {supportedRates}), 'o', 's1').supportedRates, supportedRates);
  }
});

test('a different session needs a queue move from the session JS knows (or its unacknowledged root)', () => {
  assert.throws(() => parseNativeListeningV1Event(raw('o', 2, session('s2')), 'o', 's1'), /without a queue move/);
  const moved = parseNativeListeningV1Event(raw('o', 2, session('s2'), {transition: {previousSessionId: 's1', queue: null}}), 'o', 's1');
  assert.equal(moved.transition?.previousSessionId, 's1');
  // Two moves while JS slept: the second still names the root it hasn't acknowledged.
  const again = parseNativeListeningV1Event(raw('o', 3, session('s3'), {transition: {previousSessionId: 's1', queue: null}}), 'o', 's2', 's1');
  assert.equal(again.session.id, 's3');
  assert.throws(() => parseNativeListeningV1Event(raw('o', 3, session('s3'), {transition: {previousSessionId: 'elsewhere', queue: null}}), 'o', 's2', 's1'), /did not match/);
});

test('the engine binding must name the session reported', () => {
  const s = session('s1');
  assert.throws(() => parseNativeListeningV1Event(raw('o', 1, s, {binding: {lease: 'lease-1', intentId: 1, sessionId: 'other', generation: 1, itemId: s.itemId}}), 'o', 's1'), /binding/);
  assert.equal(parseNativeListeningV1Event(raw('o', 1, s, {binding: null}), 'o', 's1').binding, null);
});

test('a terminal snapshot carries the server’s end; video is refused', () => {
  const e = parseNativeListeningV1Event(raw('o', 1, session('s1'), {terminal: true, end: {reason: 'terminated', message: 'Maintenance'}}), 'o', 's1');
  assert.deepEqual(e.end, {reason: 'terminated', message: 'Maintenance'});
  assert.throws(() => parseNativeListeningV1Event(raw('o', 1, session('s1', {kind: 'vod'})), 'o', 's1'), /audio/);
});

function bridge() {
  const calls: {name: string; args: unknown[]}[] = [];
  let sink: ((e: unknown) => void) | undefined;
  let sequence = 0;
  let latest: unknown;
  const b: NativeListeningBridge = {
    adoptListeningSession: async context => { calls.push({name: 'adopt', args: [context]}); const s = (context as {session: {id: string}}).session as Record<string, unknown>; latest = raw(context.owner as string, ++sequence, s); return latest; },
    listeningSnapshot: async owner => { calls.push({name: 'snapshot', args: [owner]}); return latest; },
    acknowledgeListening: async (owner, seq) => { calls.push({name: 'ack', args: [owner, seq]}); return true; },
    listeningAction: async () => {throw new Error('Unguarded action must not be used.');},
    listeningActionFor: async (owner, target, action, value) => { calls.push({name: 'action', args: [owner, action, value, target]}); return true; },
    listeningAdvance: async (owner, reason, entryId) => { calls.push({name: 'advance', args: [owner, reason, entryId]}); return true; },
    listeningDeadlines: async () => true,
    listeningCredentials: async (owner, token) => { calls.push({name: 'credentials', args: [owner, token]}); return true; },
    retireListening: async (owner, stop) => { calls.push({name: 'retire', args: [owner, stop]}); return true; },
  };
  return {b, calls, push: (e: unknown) => { latest = e; sink?.(e); }, deliver: (e: unknown) => sink?.(e), subscribe: (s: (e: unknown) => void) => { sink = s; return () => { sink = undefined; }; }, next: () => ++sequence};
}

test('the adapter hands the session over once, acknowledges snapshots and follows the owner’s moves', async () => {
  const x = bridge();
  const adapter = new NativeListeningV1Adapter({bridge: x.b, principal: {origin: 'https://portico.test', serverId: 'srv', authority: 'local', accountId: 'acc', profileId: 'pro'}, bearer: () => 'token', id: () => 'owner-1', subscribe: x.subscribe});
  const events: NativeListeningV1Event[] = [];
  const s1 = session('s1');
  await adapter.adopt({session: s1 as never, queueId: 'q1', queueRevision: '7', durationSeconds: 200, lastSeq: 2, leaseMs: 118000, transitions: true, intent: 'playing'}, e => events.push(e));
  const context = x.calls.find(c => c.name === 'adopt')!.args[0] as Record<string, unknown>;
  assert.equal(context.owner, 'owner-1');
  assert.equal(context.lastSeq, 2);
  assert.equal(context.queueRevision, '7');
  assert.equal(context.principalKey, JSON.stringify(['https://portico.test', 'srv', 'local', 'acc', 'pro']));
  assert.equal(events.length, 1);
  assert.ok(x.calls.some(c => c.name === 'ack' && c.args[1] === 1));
  // Two moves the owner made while JS slept arrive in order.
  x.push(raw('owner-1', x.next(), session('s2'), {transition: {previousSessionId: 's1', queue: null}}));
  x.push(raw('owner-1', x.next(), session('s3'), {transition: {previousSessionId: 's1', queue: null}}));
  assert.deepEqual(events.map(e => e.session.id), ['s1', 's2', 's3']);
  // A real nine-speed snapshot arriving after adoption must keep its owner alive too.
  x.push(raw('owner-1', x.next(), session('s3'), {...producerStatus(appleRates), rate: 3, effectiveRate: 3}));
  assert.equal(events.at(-1)?.effectiveRate, 3);
  assert.equal(x.calls.some(c => c.name === 'retire'), false);
  // A stale or foreign snapshot is ignored.
  x.deliver(raw('owner-1', 2, session('s2')));
  x.deliver(raw('someone-else', 99, session('s9')));
  assert.equal(events.length, 4);
  await adapter.advance('entry', 'e-9');
  assert.ok(x.calls.some(c => c.name === 'advance' && c.args[1] === 'entry' && c.args[2] === 'e-9'));
  await adapter.action('pause');
  assert.deepEqual(x.calls.find(c => c.name === 'action')!.args, ['owner-1', 'pause', null,
    {binding: events.at(-1)!.binding, commandRevision: 2, queue: {queueId: 'q1', entryId: 'e-s3'}}]);
  await adapter.detach(true);
  assert.deepEqual(x.calls.at(-1), {name: 'retire', args: ['owner-1', true]});
});

test('INT T1: a kind plays under the native owner only when the server lists it and the app turns it on; music and audiobooks are on', async () => {
  const {nativeListeningFor, nativeV1ListeningKinds, serverPlaybackV1Kinds} = await import('../src-next/platform/playback/native-listening-kinds.ts');
  assert.deepEqual([...nativeV1ListeningKinds], ['song', 'track', 'audiobook_file']);
  const server = serverPlaybackV1Kinds({features: {playback_v1: 'enabled', playback_v1_kinds: ['movie', 'audiobook_file', 'song']}});
  assert.deepEqual(server, ['movie', 'audiobook_file', 'song']);
  assert.equal(nativeListeningFor(server)('song'), true);
  assert.equal(nativeListeningFor(server)('audiobook_file'), true, 'audiobooks play natively');
  assert.equal(nativeListeningFor(server)('movie'), false);
  assert.equal(nativeListeningFor([], ['song'])('song'), false, 'the server must list it too');
  // What a real server sends: its features map is string to string.
  const real = serverPlaybackV1Kinds({features: {playback_v1: 'enabled', playback_v1_kinds: 'movie,episode,video,audiobook,audiobook_file,book,song,track'}});
  assert.deepEqual(real, ['movie', 'episode', 'video', 'audiobook', 'audiobook_file', 'book', 'song', 'track']);
  assert.equal(nativeListeningFor(real)('song'), true);
  assert.equal(nativeListeningFor(real)('track'), true);
  assert.deepEqual(serverPlaybackV1Kinds({features: {}}), []);
});
