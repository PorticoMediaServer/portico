import test from 'node:test';
import assert from 'node:assert/strict';
import {ResyncCoordinator} from '../src/resync-coordinator.ts';
import {EventsClient} from '../src/playback-v1/events.ts';
import {FreshnessHub} from '../src/freshness-hub.ts';

const tick = async () => {for (let i = 0; i < 40; i++) await Promise.resolve();};
const gate = () => {let resolve!: () => void; const promise = new Promise<void>(r => {resolve = r;}); return {promise, resolve};};
function clock() {
  let now = 0, serial = 0;
  const timers = new Map<number, {at: number; fn: () => void}>();
  const env = {now: () => now, random: () => 0, setTimer: (fn: () => void, ms: number) => {const id = ++serial; timers.set(id, {at: now + ms, fn}); return id;}, clearTimer: (id: unknown) => {timers.delete(id as number);}};
  return {env, pending: () => timers.size, async advance(ms: number) {
    const end = now + ms;
    for (;;) {
      const next = [...timers].filter(([, t]) => t.at <= end).sort((a, b) => a[1].at - b[1].at)[0];
      if (!next) break;
      now = next[1].at; timers.delete(next[0]); next[1].fn(); await tick();
    }
    now = end; await tick();
  }};
}
const http = {send: async () => {throw new Error('unexpected feed request');}};

test('barriers install debt before cursor acknowledgement; repeated concurrent resyncs share four reads and one follow-up', async () => {
  const c = clock(), events = new EventsClient({http, ...c.env});
  const flights = Array.from({length: 7}, gate), calls = Array(7).fill(0);
  let active = 0, maximum = 0;
  const registrations = flights.map((flight, i) => events.onResyncRead(async context => {
    calls[i]++; active++; maximum = Math.max(maximum, active);
    if (calls[i] === 1) await flight.promise;
    if (context.isCurrent()) assert.equal(context.signal.aborted, false);
    active--;
  }));
  let installed = false;
  const off = events.subscribeStatus(() => {installed = registrations.every(r => r.getState().owed);});
  events.deliver({id: 'barrier-1', type: 'stream.resync'});
  assert.equal(installed, true); await tick(); assert.equal(active, 4);
  for (let i = 0; i < 30; i++) events.deliver({id: `barrier-${i + 2}`, type: 'stream.resync'});
  await tick(); assert.deepEqual(calls, [1, 1, 1, 1, 0, 0, 0]);
  flights.forEach(f => f.resolve()); await tick();
  assert.equal(maximum, 4); assert.equal(registrations.slice(0, 4).every(r => r.getState().owed), true);
  await c.advance(1000);
  assert.deepEqual(calls, [2, 2, 2, 2, 1, 1, 1]);
  assert.equal(registrations.every(r => !r.getState().owed), true);
  off(); registrations.forEach(r => r.dispose()); events.resyncReads.dispose(); assert.equal(c.pending(), 0);
});

test('hidden debt activates once; stale completion cannot clear newer debt or release an occupied permit early', async () => {
  const c = clock(), coordinator = new ResyncCoordinator(c.env), first = gate();
  let calls = 0, signal!: AbortSignal;
  const registration = coordinator.register(async context => {calls++; signal = context.signal; if (calls === 1) await first.promise;}, {open: false});
  coordinator.invalidate(); await tick(); assert.equal(calls, 0);
  registration.setOpen(true); await tick(); assert.equal(calls, 1);
  registration.setOpen(false); assert.equal(signal.aborted, true);
  registration.setOpen(true); coordinator.invalidate(); await tick(); assert.equal(calls, 1);
  first.resolve(); await tick(); assert.equal(registration.getState().owed, true);
  await c.advance(1000); assert.equal(calls, 2); assert.equal(registration.getState().owed, false);
  registration.dispose(); coordinator.dispose(); assert.equal(c.pending(), 0);
});

test('retirement aborts owners and cancels queued reads without starting them', async () => {
  const coordinator = new ResyncCoordinator(), held = gate(), controller = new AbortController();
  const signals: AbortSignal[] = [];
  for (let i = 0; i < 4; i++) coordinator.register(async context => {signals.push(context.signal); await held.promise;});
  coordinator.invalidate(); await tick();
  let called = false;
  const pending = coordinator.run(controller.signal, async () => {called = true;});
  coordinator.dispose(); await assert.rejects(pending, /resync_retired/);
  assert.equal(signals.every(s => s.aborted), true); held.resolve(); await tick(); assert.equal(called, false);
});

test('failure debt preserves the complete server wait across more barriers and hidden/open changes', async () => {
  const c = clock(), coordinator = new ResyncCoordinator(c.env);
  let calls = 0;
  const registration = coordinator.register(async () => {if (++calls === 1) throw {retryAfterMs: 3_000_000};});
  coordinator.invalidate(); await tick(); assert.equal(calls, 1);
  coordinator.invalidate(); registration.setOpen(false); registration.setOpen(true);
  await c.advance(2_999_999); assert.equal(calls, 1); assert.equal(registration.getState().owed, true);
  await c.advance(1); assert.equal(calls, 2); assert.equal(registration.getState().owed, false);
  registration.dispose(); coordinator.dispose(); assert.equal(c.pending(), 0);
});

test('registration overflow is explicit and retired slots can be reused', () => {
  const coordinator = new ResyncCoordinator();
  const registrations = Array.from({length: 128}, () => coordinator.register(async () => {}));
  assert.throws(() => coordinator.register(async () => {}), /resync_service_limit/);
  registrations[0]!.dispose(); coordinator.register(async () => {}); coordinator.dispose();
});

test('actual attached freshness documents, interests and imperative recovery share the four-read admission', async () => {
  const c = clock(), events = new EventsClient({http, ...c.env}), hub = new FreshnessHub({...c.env, gatherMs: 0});
  hub.attach(events);
  const held = gate(); let active = 0, maximum = 0, starts = 0;
  const read = async () => {starts++; active++; maximum = Math.max(maximum, active); await held.promise; active--;};
  const urgent = events.onResyncRead(read, {priority: 0});
  const documents = Array.from({length: 3}, () => hub.document({dependencies: [], revalidate: read}));
  documents.forEach(d => d.setOpen(true));
  const interests = Array.from({length: 3}, (_, i) => hub.interest({name: `interest-${i}`, dependencies: [], read: async () => {await read(); return {kind: 'answered'};}}));
  events.deliver({id: 'barrier', type: 'stream.resync'}); await tick(); await c.advance(0);
  assert.equal(active, 4); assert.equal(starts, 4);
  held.resolve(); await tick(); assert.equal(starts, 7); assert.equal(maximum, 4);
  documents.forEach(d => d.dispose()); interests.forEach(i => i.dispose()); urgent.dispose(); hub.dispose(); events.resyncReads.dispose(); assert.equal(c.pending(), 0);
});

test('fan-out owners admit each actual read without nested permits or exceeding four', async () => {
  const coordinator = new ResyncCoordinator(), held = gate(), signal = new AbortController().signal;
  let active = 0, maximum = 0, started = 0;
  for (let i = 0; i < 6; i++) coordinator.register(async () => {
    await Promise.all(Array.from({length: 2}, () => coordinator.run(signal, async () => {
      started++; active++; maximum = Math.max(maximum, active); await held.promise; active--;
    })));
  }, {admission: 'external'});
  coordinator.invalidate(); await tick(); assert.equal(active, 4); assert.equal(started, 4);
  held.resolve(); await tick(); assert.equal(started, 12); assert.equal(maximum, 4); coordinator.dispose();
});

test('a background barrier keeps visible services owed while urgent retained authority recovers', async () => {
  const coordinator = new ResyncCoordinator(); let visible = 0, urgent = 0;
  const document = coordinator.register(async () => {visible++;});
  coordinator.register(async () => {urgent++;}, {priority: 0});
  coordinator.setActive(false); coordinator.invalidate(); await tick();
  assert.equal(visible, 0); assert.equal(urgent, 1); assert.equal(document.getState().owed, true);
  coordinator.setActive(true); await tick(); assert.equal(visible, 1); assert.equal(document.getState().owed, false); coordinator.dispose();
});
