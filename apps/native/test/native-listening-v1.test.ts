/** Plan §9.3 C3: the JS side of the v1 native listening owner (PorticoListeningSessionOwner). */
import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {NativeListeningV1Adapter, parseNativeListeningV1Event, type NativeListeningBridge} from '../src-next/platform/playback/NativeListeningAdapter.ts';
import type {NativeListeningPolicy, NativeListeningV1Context, NativeListeningV1Event} from '../../../packages/client-core/src/playback/native-listening.ts';

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
  const policies: {owner: string; policy: NativeListeningPolicy}[] = [];
  const held: ((value: unknown) => void)[] = [];
  let answer: unknown = true;
  let sink: ((e: unknown) => void) | undefined;
  let sequence = 0;
  let latest: unknown;
  const b: NativeListeningBridge = {
    adoptListeningSession: async context => { calls.push({name: 'adopt', args: [context]}); const s = (context as {session: {id: string}}).session as Record<string, unknown>; latest = raw(context.owner as string, ++sequence, s); return latest; },
    listeningSnapshot: async owner => { calls.push({name: 'snapshot', args: [owner]}); return latest; },
    acknowledgeListening: async (owner, seq) => { calls.push({name: 'ack', args: [owner, seq]}); return true; },
    listeningAction: async () => { throw new Error('Unguarded action must not be used.'); },
    listeningActionFor: async (owner, target, action, value) => { calls.push({name: 'action', args: [owner, action, value, target]}); return true; },
    listeningAdvance: async (owner, reason, entryId) => { calls.push({name: 'advance', args: [owner, reason, entryId]}); return true; },
    listeningDeadlines: async () => true,
    listeningCredentials: async (owner, token) => { calls.push({name: 'credentials', args: [owner, token]}); return true; },
    // The unified policy port: records what the owner would apply and answers as the test says.
    listeningPolicy: async (owner, policy) => {
      policies.push({owner, policy});
      return answer === 'hold' ? await new Promise<unknown>(resolve => { held.push(resolve); }) : answer;
    },
    retireListening: async (owner, stop) => { calls.push({name: 'retire', args: [owner, stop]}); return true; },
  };
  return {b, calls, policies, held, answer: (value: unknown) => { answer = value; }, push: (e: unknown) => { latest = e; sink?.(e); }, deliver: (e: unknown) => sink?.(e), subscribe: (s: (e: unknown) => void) => { sink = s; return () => { sink = undefined; }; }, next: () => ++sequence};
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
  assert.deepEqual(x.calls.find(c => c.name === 'action')!.args, ['owner-1', 'pause', null, {binding: events.at(-1)!.binding, commandRevision: 2, queue: {queueId: 'q1', entryId: 'e-s3'}}]);
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


test('compatibility listening parser strips raw diagnostics and keeps only canonical display messages', () => {
  const event = parseNativeListeningV1Event(raw('o', 1, session('s1'), {error: 'private native diagnostic', rateError: 'private native diagnostic', notice: 'private native diagnostic', noticeCode: 'private native diagnostic', extra: 'private native diagnostic'}), 'o', 's1');
  assert.equal(JSON.stringify(event).includes('private native diagnostic'), false);
  assert.equal('notice' in event, false); assert.equal('noticeCode' in event, false); assert.equal('extra' in event, false);
  assert.equal(event.session.id, 's1'); assert.ok(event.error);
});


test('a held credential refresh cannot retarget seek/rate/retry to a same-item successor', async () => {
 for(const action of ['seek','rate','retry'] as const){
  const x=bridge(),adapter=new NativeListeningV1Adapter({bridge:x.b,principal:{origin:'https://portico.test',serverId:'srv',authority:'local',accountId:'acc',profileId:'pro'},bearer:()=> 'token',id:()=> 'owner-1',subscribe:x.subscribe});
  await adapter.adopt({session:session('s1') as never,queueId:'q1',queueRevision:'7',durationSeconds:200,lastSeq:0,leaseMs:118000,transitions:true,intent:'playing'},()=>{});
  let release!:()=>void;
  x.b.listeningCredentials=()=>new Promise(resolve=>{release=()=>resolve(true);});
  const pending=adapter.action(action,action==='rate'?1.5:10);
  x.push(raw('owner-1',x.next(),session('s2',{itemId:'item-s1'}),{transition:{previousSessionId:'s1',queue:null}}));
  release();await assert.rejects(pending,/no longer available/);
  assert.equal(x.calls.some(call=>call.name==='action'),false);
  await adapter.detach(false);
 }
});

test('a caller target is retained exactly and cannot fall back to owner-only dispatch', async () => {
 const x=bridge(),adapter=new NativeListeningV1Adapter({bridge:x.b,principal:{origin:'https://portico.test',serverId:'srv',authority:'local',accountId:'acc',profileId:'pro'},bearer:()=> 'token',id:()=> 'owner-1',subscribe:x.subscribe});
 await adapter.adopt({session:session('s1') as never,queueId:'q1',queueRevision:'7',durationSeconds:200,lastSeq:0,leaseMs:118000,transitions:true,intent:'playing'},()=>{});
 const target={binding:{lease:'lease-1',intentId:1,sessionId:'s1',generation:1,itemId:'item-s1'},commandRevision:2,queue:{queueId:'q1',entryId:'e-s1'}};
 let release!:()=>void;
 x.b.listeningCredentials=()=>new Promise(resolve=>{release=()=>resolve(true);});
 const pending=adapter.action('seek',30,target);
 target.binding.sessionId='s2';target.queue.entryId='e-s2';release();await pending;
 assert.equal((x.calls.find(call=>call.name==='action')!.args[3] as typeof target).binding.sessionId,'s1');
 assert.equal((x.calls.find(call=>call.name==='action')!.args[3] as typeof target).queue.entryId,'e-s1');
 x.b.listeningActionFor=undefined;
 await assert.rejects(adapter.action('pause'),/no longer available/);
 await adapter.detach(false);
});

// ---- policy acceptance (main 64e7b77d): the owner captured before the caller's getters, the
// attempt ordinal kept across the accept await, and the occurrence/ordered boundary frozen.
// Source-only regressions: authored, not executed here.
const policyPrincipal = {origin: 'https://portico.test', serverId: 'srv', authority: 'local' as const, accountId: 'acc', profileId: 'pro'};
type PolicyTimer = {choice: string | number; label: string; occurrence?: {sessionId: string; generation: number; itemId: string; playbackRun: number; queue: {queueId: string; entryId: string} | null}; bookBoundary?: {revision: {catalog: number; viewer: number}; items: string[]}};
type PolicyDraft = {revision: number; preferences: {bookRate: number; passoutMinutes: number}; activityRevision: number; activityAtMs: number; timerRevision: number; effects: {gapless: boolean; crossfadeSeconds: number; normalization: 'off' | 'track' | 'album'}; timer: PolicyTimer | null};
type PolicyAdapter = {adopt: (context: NativeListeningV1Context, sink: (event: NativeListeningV1Event) => void) => Promise<void>; configure: (policy: NativeListeningPolicy) => Promise<boolean>; detach: (stop: boolean) => Promise<void>};
type PolicyBridge = ReturnType<typeof bridge>;
const policyDraft = (revision: number, timer: PolicyTimer | null = null): PolicyDraft => ({revision, preferences: {bookRate: 1, passoutMinutes: 45}, activityRevision: 3, activityAtMs: 1_760_000_000_000, timerRevision: revision, effects: {gapless: true, crossfadeSeconds: 0, normalization: 'off'}, timer});
const policyAdapters: readonly (readonly [string, (x: PolicyBridge) => PolicyAdapter])[] = [
  ['current', x => new NativeListeningV1Adapter({bridge: x.b, principal: policyPrincipal, bearer: () => 'token', id: () => 'owner-1', subscribe: x.subscribe})],
];
for (const [path, make] of policyAdapters) {
  const open = () => {const x = bridge(); return {x, adapter: make(x)};};
  const play = (adapter: PolicyAdapter, id = 's1') => adapter.adopt({session: session(id) as never, queueId: 'q1', queueRevision: '7', durationSeconds: 200, lastSeq: 0, leaseMs: 118000, transitions: true, intent: 'playing'}, () => {});
  const handover = (x: PolicyBridge) => x.calls.filter(call => call.name === 'adopt').at(-1)!.args[0] as {session: {id: string}; policy: NativeListeningPolicy | null};

  test(`${path}: a timer getter that retires and re-adopts refuses the superseded policy without dispatching it`, async () => {
    const {x, adapter} = open();
    await play(adapter);
    let armed = true, readopt: Promise<void> = Promise.resolve();
    const superseded: PolicyDraft = {...policyDraft(4), get timer() {
      if (armed) {
        armed = false;
        // Synchronous inside the capture: the current owner is retired and a successor adopted.
        void adapter.detach(false);
        readopt = play(adapter, 's9');
      }
      return null;
    }};
    assert.equal(await adapter.configure(superseded), false);
    await readopt;
    assert.equal(x.policies.length, 0, 'the superseded policy never reaches the successor');
    assert.equal(handover(x).session.id, 's9');
    // Nothing was ever cached before this attempt (the cache write happens only after the
    // capture and its owner check), so this successor is handed null. A replacement after a
    // policy was already cached keeps that earlier frozen intent instead; see the held-ACK case.
    assert.equal(handover(x).policy, null, 'no earlier policy existed to carry');
  });

  test(`${path}: a getter that nests a newer configure keeps the newer policy; the outer attempt never dispatches`, async () => {
    const {x, adapter} = open();
    await play(adapter);
    let armed = true, inner: Promise<boolean> | undefined;
    const newer = policyDraft(9, {choice: 'end-of-book', label: 'End of book', occurrence: {sessionId: 's1', generation: 1, itemId: 'item-s1', playbackRun: 7, queue: {queueId: 'q1', entryId: 'e-s1'}}});
    const outer: PolicyDraft = {...policyDraft(8), get timer() {
      if (armed) {armed = false; inner = adapter.configure(newer);}
      return {choice: 'off', label: 'Off'};
    }};
    assert.equal(await adapter.configure(outer), false);
    assert.ok(inner, 'the getter submits the newer policy synchronously while the outer one is captured');
    assert.equal(await inner!, true);
    assert.equal(x.policies.length, 1, 'the replaced outer attempt never dispatches');
    assert.equal(x.policies[0]!.policy.revision, 9);
    assert.equal(x.policies.some(entry => entry.policy.revision === 8), false);
    // The newer capture is what a later handover carries, not the withdrawn outer one.
    await adapter.detach(false);
    await play(adapter, 's2');
    assert.equal(handover(x).policy?.revision, 9);
    assert.equal(handover(x).policy?.timer?.occurrence?.playbackRun, 7);
  });

  test(`${path}: a held acknowledgement is refused after replacement, and the cached intent keeps its old scope`, async () => {
    const {x, adapter} = open();
    await play(adapter);
    const occurrence = {sessionId: 's1', generation: 1, itemId: 'item-s1', playbackRun: 7, queue: {queueId: 'q1', entryId: 'e-s1'}};
    const boundary = {revision: {catalog: 11, viewer: 22}, items: ['b1', 'b2']};
    const intent: PolicyDraft = policyDraft(5, {choice: 90, label: '90 minutes', occurrence, bookBoundary: boundary});
    x.answer('hold');
    const pending = adapter.configure(intent);
    assert.equal(x.policies.length, 1);
    await adapter.detach(false);
    await play(adapter, 's2');
    // The frozen intent stays cached for the successor's native re-admission, but it still names
    // the original occurrence. This fake bridge only records the JS intent the owner is handed;
    // it makes no claim that a native owner accepted or requalified it.
    const successor = handover(x);
    assert.equal(successor.policy?.revision, 5);
    assert.deepEqual(successor.policy?.timer?.occurrence, {sessionId: 's1', generation: 1, itemId: 'item-s1', playbackRun: 7, queue: {queueId: 'q1', entryId: 'e-s1'}});
    assert.deepEqual(successor.policy?.timer?.bookBoundary, {revision: {catalog: 11, viewer: 22}, items: ['b1', 'b2']});
    assert.equal(Object.isFrozen(successor.policy?.timer?.occurrence), true);
    assert.equal(Object.isFrozen(successor.policy?.timer?.bookBoundary?.items), true);
    // The successor's own session and later caller mutation never retarget that cached intent.
    occurrence.sessionId = 's2'; occurrence.playbackRun = 8; boundary.items.push('b3');
    x.held.shift()!(true);
    assert.equal(await pending, false, 'the superseded acknowledgement is refused');
    assert.equal(x.policies.length, 1, 'no configure dispatch reaches the successor');
    assert.equal(x.policies[0]!.owner, 'owner-1');
    assert.deepEqual(handover(x).policy?.timer?.occurrence, {sessionId: 's1', generation: 1, itemId: 'item-s1', playbackRun: 7, queue: {queueId: 'q1', entryId: 'e-s1'}});
    // A still later handover carries the same old frozen tuple, never the latest one.
    await adapter.detach(false);
    await play(adapter, 's3');
    assert.deepEqual(handover(x).policy?.timer?.occurrence, {sessionId: 's1', generation: 1, itemId: 'item-s1', playbackRun: 7, queue: {queueId: 'q1', entryId: 'e-s1'}});
    assert.deepEqual(handover(x).policy?.timer?.bookBoundary, {revision: {catalog: 11, viewer: 22}, items: ['b1', 'b2']});
  });

  test(`${path}: a held acknowledgement cannot overtake a newer policy; only the newer literal true stands`, async () => {
    const {x, adapter} = open();
    await play(adapter);
    x.answer('hold');
    const older = adapter.configure(policyDraft(6));
    const newer = adapter.configure(policyDraft(7));
    assert.equal(x.policies.length, 2);
    x.held[0]!(true);
    assert.equal(await older, false);
    x.held[1]!(true);
    assert.equal(await newer, true);
    assert.deepEqual(x.policies.map(entry => entry.policy.revision), [6, 7]);
  });

  test(`${path}: only a literal native true for a current owner is accepted`, async () => {
    const {x, adapter} = open();
    assert.equal(await adapter.configure(policyDraft(1)), false, 'no owner is not acceptance');
    assert.equal(x.policies.length, 0);
    await play(adapter);
    x.answer(false);
    assert.equal(await adapter.configure(policyDraft(2)), false);
    x.answer(undefined);
    assert.equal(await adapter.configure(policyDraft(3)), false);
    x.answer({accepted: true});
    assert.equal(await adapter.configure(policyDraft(4)), false, 'a truthy object is not the literal true');
    delete x.b.listeningPolicy;
    assert.equal(await adapter.configure(policyDraft(5)), false, 'a host without the port refuses instead of throwing');
  });

  test(`${path}: an own accepted snapshot that pauses and moves the queue is not a rejection`, async () => {
    const {x, adapter} = open();
    await play(adapter);
    x.answer('hold');
    const pending = adapter.configure(policyDraft(10, {choice: 90, label: '90 minutes', occurrence: {sessionId: 's1', generation: 1, itemId: 'item-s1', playbackRun: 7, queue: {queueId: 'q1', entryId: 'e-s1'}}}));
    // While the accept is in flight the same owner pauses and moves the queue by itself.
    x.push(raw('owner-1', x.next(), session('s2'), {intent: 'paused', transition: {previousSessionId: 's1', queue: null}}));
    x.held[0]!(true);
    assert.equal(await pending, true, 'the header moved; the accepted occurrence and owner did not');
  });

  test(`${path}: the dispatched policy keeps its frozen occurrence and book boundary through caller mutation`, async () => {
    const {x, adapter} = open();
    await play(adapter);
    const occurrence = {sessionId: 's1', generation: 1, itemId: 'item-s1', playbackRun: 7, queue: {queueId: 'q1', entryId: 'e-s1'}};
    const boundary = {revision: {catalog: 11, viewer: 22}, items: ['b1', 'b2', 'b3']};
    const timer: PolicyTimer = {choice: 'end-of-book', label: 'End of book', occurrence, bookBoundary: boundary};
    const draft = policyDraft(11, timer);
    x.answer('hold');
    const pending = adapter.configure(draft);
    // Everything the caller still holds changes after dispatch.
    occurrence.queue.entryId = 'e-mutated'; occurrence.playbackRun = 99; occurrence.sessionId = 's9';
    boundary.items.push('b4'); boundary.revision.viewer = 23;
    timer.label = 'Mutated'; draft.timerRevision = 88; draft.effects.gapless = false;
    x.held[0]!(true);
    assert.equal(await pending, true);
    const sent = x.policies.at(-1)!.policy;
    assert.deepEqual(sent.timer?.occurrence, {sessionId: 's1', generation: 1, itemId: 'item-s1', playbackRun: 7, queue: {queueId: 'q1', entryId: 'e-s1'}});
    assert.deepEqual(sent.timer?.bookBoundary, {revision: {catalog: 11, viewer: 22}, items: ['b1', 'b2', 'b3']});
    assert.equal(sent.effects.gapless, true);
    assert.equal(sent.timerRevision, 11);
    assert.equal(Object.isFrozen(sent.timer?.occurrence), true);
    assert.equal(Object.isFrozen(sent.timer?.bookBoundary?.items), true);
    // What was acknowledged is still what a later handover carries.
    await adapter.detach(false);
    await play(adapter, 's3');
    assert.deepEqual(handover(x).policy?.timer?.occurrence?.queue, {queueId: 'q1', entryId: 'e-s1'});
    assert.equal(handover(x).policy?.timer?.bookBoundary?.items.length, 3);
  });
}

test('handover captures original-family scope and calibrated native key clock', async () => {
 const x=bridge(),scope={serverId:'srv',origin:'https://portico.test',authority:'local',accountId:'acc',profileId:'pro',familyId:'original-family'};
 const adapter=new NativeListeningV1Adapter({bridge:x.b,principal:{origin:'https://portico.test',serverId:'srv',authority:'local',accountId:'acc',profileId:'pro'},mutationScope:()=>scope,mutationClock:async()=>-600000,bearer:()=> 'token',id:()=> 'owner-1',subscribe:x.subscribe});
 await adapter.adopt({session:session('s1') as never,queueId:'q1',queueRevision:'7',durationSeconds:200,lastSeq:2,leaseMs:118000,transitions:true,intent:'playing'},()=>{});
 const context=x.calls.find(c=>c.name==='adopt')!.args[0] as Record<string,unknown>;
 assert.deepEqual(context.mutationScope,scope);assert.equal(context.mutationClockOffsetMs,-600000);
 await adapter.detach(false);
});
test('native pending/review/not-queued Stop is explicit and retains a rejected draft',async()=>{
 for(const state of ['pending','review','quarantined','not_queued']){
  const x=bridge(),draft={generation:1,positionMs:3400};
  x.b.retireListening=async()=>({state,key:'p251_1793200000000_00000000000000000000000000000001',code:state==='pending'?'mutation_pending':'mutation_not_queued',draft});
  const adapter=new NativeListeningV1Adapter({bridge:x.b,principal:{origin:'https://portico.test',serverId:'srv',authority:'local',accountId:'acc',profileId:'pro'},bearer:()=> 'token',id:()=> 'owner-1',subscribe:x.subscribe});
  await adapter.adopt({session:session('s1') as never,queueId:'q1',queueRevision:'7',durationSeconds:200,lastSeq:2,leaseMs:118000,transitions:true,intent:'playing'},()=>{});
  await assert.rejects(adapter.detach(true),(error:any)=>error.code===(state==='pending'?'mutation_pending':'mutation_not_queued')&&error.draft===draft);
 }
});
test('a family change during policy clock read never hands the successor credential to old owner',async()=>{
 const x=bridge();let familyId='original';let release!:()=>void;
 const clock=new Promise<void>(resolve=>{release=resolve;});
 const adapter=new NativeListeningV1Adapter({bridge:x.b,principal:{origin:'https://portico.test',serverId:'srv',authority:'local',accountId:'acc',profileId:'pro'},mutationScope:()=>({origin:'https://portico.test',serverId:'srv',authority:'local',accountId:'acc',profileId:'pro',familyId}),mutationClock:async()=>{await clock;return 0;},bearer:()=> 'current-token',id:()=> 'owner-1',subscribe:x.subscribe});
 const adopting=adapter.adopt({session:session('s1') as never,queueId:'q1',queueRevision:'7',durationSeconds:200,lastSeq:2,leaseMs:118000,transitions:true,intent:'playing'},()=>{});
 familyId='successor';release();await assert.rejects(adopting,{code:'mutation_scope_changed'});assert.equal(x.calls.some(c=>c.name==='adopt'),false);
});
