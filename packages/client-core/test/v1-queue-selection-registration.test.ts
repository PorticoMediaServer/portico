import test from 'node:test';
import assert from 'node:assert/strict';
import {QueueSelectionRegistrations, retireQueuedSelectionRegistration} from '../src/playback-v1/selection-registration.ts';
import {V1PlaybackApi} from '../src/playback-v1/legacy-bridge.ts';
import {FakePlaybackServer, fakeHttp} from '../src/playback-v1/testing/fake-server.ts';
import type {Session} from '../src/playback-v1/types.ts';

// Identity and generation deliberately stay unchanged across registrations.
const session = {id: 'occurrence', presentation: {generation: 7}} as Session;
function fixture() {
  const registrations = new QueueSelectionRegistrations();
  let present = false, forgotten = 0;
  const receipt = () => registrations.capture(session.id, () => !present, () => {forgotten++; registrations.changed(session.id); return true;});
  return {registrations, receipt, register: () => {present = true; registrations.changed(session.id);}, get forgotten() {return forgotten;}};
}

test('known absence is exact: same-id registration and an absent/registered/absent cycle retire the original receipt', async () => {
  const f = fixture(), original = f.receipt(); let stops = 0;
  f.register();
  assert.equal(original.current(), false);
  assert.equal(await retireQueuedSelectionRegistration(session, original, () => {stops++; return true;}, () => true), false);
  assert.equal(stops, 0); assert.equal(f.forgotten, 0);
  const ledger = new QueueSelectionRegistrations();
  const receipt = ledger.capture(session.id, () => true, () => true);
  ledger.changed(session.id); ledger.changed(session.id);
  assert.equal(receipt.current(), false, 'an identical id/generation cannot revive a receipt');
});

test('only explicit true retirement under unchanged registration and ownership forgets the original', async () => {
  for (const result of [false, undefined, 'throw', true] as const) {
    const f = fixture(), receipt = f.receipt();
    const retire = async () => {if (result === 'throw') throw new Error('principal refused'); return result as boolean;};
    assert.equal(await retireQueuedSelectionRegistration(session, receipt, retire, () => true), result === true);
    assert.equal(f.forgotten, result === true ? 1 : 0);
  }
  const f = fixture();
  assert.equal(await retireQueuedSelectionRegistration(session, f.receipt(), undefined, () => true), false);
  assert.equal(await retireQueuedSelectionRegistration(session, undefined, () => true, () => true), false);
  assert.equal(f.forgotten, 0);
});

test('a delayed original-principal answer cannot forget re-registration or later accepted ownership', async () => {
  for (const replacement of ['registration', 'ownership'] as const) {
    const f = fixture(), receipt = f.receipt(); let owned = false, finish!: (retired: boolean) => void;
    const pending = retireQueuedSelectionRegistration(session, receipt, (_session, safe) => {
      assert.equal(safe(), true); return new Promise<boolean>(resolve => {finish = resolve;});
    }, () => !owned);
    if (replacement === 'registration') f.register(); else owned = true;
    finish(true);
    assert.equal(await pending, false); assert.equal(f.forgotten, 0);
  }
});

test('receipt eviction closes old ownership without id ABA revival', () => {
  const ledger = new QueueSelectionRegistrations();
  const original = ledger.capture(session.id, () => true, () => true);
  for (let i = 0; i < 129; i++) ledger.changed(`other-${i}`);
  ledger.capture(session.id, () => true, () => true);
  assert.equal(original.current(), false);
});


test('bridge receipt belongs to the returned adoption, never a later registration of the same object', async () => {
  const api = new V1PlaybackApi(fakeHttp(new FakePlaybackServer()), {timers: {setTimer: () => 0, clearTimer: () => {}}});
  const {session, playback} = await api.start({itemId: 'movie'}, {state: 'playing'}, 'registration-source-000000');
  const first = api.selectionRegistration(session.id, playback)!;
  assert.equal(first.current(), true);
  assert.equal(api.selectionRegistration(session.id), undefined, 'id possession cannot acquire an existing registration');
  const next = api.adopt(session, 120), second = api.selectionRegistration(session.id, next)!;
  assert.equal(first.current(), false); assert.equal(second.current(), true);
  assert.equal(first.forget(() => true), false); assert.equal(second.current(), true);
  api.forget(session.id);
});

function preparationTimers() {
  const jobs = new Map<number, () => void>(); let id = 0;
  return {
    timers: {setTimer: (fn: () => void) => {jobs.set(++id, fn); return id;}, clearTimer: (timer: unknown) => {jobs.delete(timer as number);}},
    fire: () => {const job = jobs.entries().next().value; assert.ok(job, 'a real preparation wait is installed'); jobs.delete(job[0]); job[1]();},
    pending: () => jobs.size
  };
}
const settleBridge = async () => {for (let i = 0; i < 16; i++) await Promise.resolve();};

test('a bridge start forwards every real preparation notice before it adopts or registers anything', async () => {
  let starts = 0;
  const server = new FakePlaybackServer({prepareRounds: () => {starts++; return 2;}});
  const clock = preparationTimers();
  const api = new V1PlaybackApi(fakeHttp(server), {timers: clock.timers});
  let adopts = 0, durations = 0;
  const realAdopt = api.adopt.bind(api), realDuration = api.duration.bind(api);
  api.adopt = (session, seconds) => {adopts++; return realAdopt(session, seconds);};
  api.duration = (itemId, versionId, signal, discTitle) => {durations++; return realDuration(itemId, versionId, signal, discTitle);};
  const waits: number[] = [], notices: unknown[] = [];
  let resolved = false;
  const started = api.start({itemId: 'movie'}, {state: 'playing'}, 'registration-source-000001', undefined, undefined,
    {onPreparing: (wait, notice) => {waits.push(wait); notices.push(notice);}}).then(value => {resolved = true; return value;});
  await settleBridge();
  assert.equal(resolved, false, 'a retry-only 202 resolves nothing');
  assert.deepEqual(waits, [500]);
  assert.equal(adopts, 0); assert.equal(durations, 0);
  clock.fire(); await settleBridge();
  assert.equal(resolved, false, 'the same original start is still preparing');
  assert.deepEqual(waits, [500, 500]);
  assert.equal(adopts, 0); assert.equal(durations, 0);
  for (const notice of notices) {assert.deepEqual(notice, {messageId: 'player.preparing.slowStart', retryAfterMs: 500}); assert.ok(Object.isFrozen(notice));}
  assert.equal(server.eventLog().filter(event => event.resource?.kind === 'session').length, 0, 'retry-only responses fabricate no session');
  clock.fire();
  const adopted = await started;
  assert.equal(starts, 1, 'every 202 asked for the same original start');
  assert.equal(adopts, 1); assert.equal(durations, 1);
  assert.equal(server.session(adopted.session.id)?.itemId, 'movie', 'the final response names a real created session');
  assert.equal(api.selectionRegistration(adopted.session.id, adopted.playback)!.current(), true);
});

test('bridge callback retirement stops preparation polling without a fabricated receipt', async () => {
  let starts = 0;
  const server = new FakePlaybackServer({prepareRounds: () => {starts++; return 3;}});
  const clock = preparationTimers();
  const base = fakeHttp(server);
  let posts = 0;
  const api = new V1PlaybackApi({send: request => {if (request.method === 'POST') posts++; return base.send(request);}}, {timers: clock.timers});
  const caller = new AbortController(); let notices = 0;
  await assert.rejects(api.start({itemId: 'movie'}, {state: 'playing'}, 'registration-source-000002', undefined, caller.signal,
    {onPreparing: () => {notices++; caller.abort();}}), {name: 'AbortError'});
  assert.equal(notices, 1); assert.equal(posts, 1, 'a retired callback sends no further POST'); assert.equal(starts, 1); assert.equal(clock.pending(), 0);
  assert.equal(server.eventLog().filter(event => event.resource?.kind === 'session').length, 0);
});
