import assert from 'node:assert/strict';
import test from 'node:test';
import {PreparingStartCustody, type PreparingStop} from '../src/playback-v1/preparing-custody.ts';

const resource = {id: 'session-1', generation: 3};
const stops = () => {
  const calls: string[] = [];
  const stop: PreparingStop = async (_signal, uninstalled, claim) => {if (uninstalled() && claim()) calls.push('stop');};
  return {calls, stop};
};

test('a bound resource is cleaned up at most once, and only that exact resource', async () => {
  const custody = new PreparingStartCustody(), registration = custody.attach();
  const flight = registration.reserve('key-1', () => true);
  flight.seed();
  const {calls, stop} = stops();
  const cleanup = flight.acquire(resource, () => true, stop);
  assert.ok(cleanup);
  assert.equal(flight.acquire({...resource}, () => true, stop), cleanup, 'the same exact resource returns the same receipt');
  assert.equal(flight.acquire({id: 'session-1', generation: 4}, () => true, stop), undefined);
  assert.equal(flight.acquire({id: 'other', generation: 3}, () => true, stop), undefined);
  await cleanup(); await cleanup();
  assert.deepEqual(calls, ['stop']);
});

test('an installation seen during the flight denies cleanup even after forget', async () => {
  const custody = new PreparingStartCustody(), registration = custody.attach();
  const flight = registration.reserve('key-1', () => true);
  const {calls, stop} = stops();
  const cleanup = flight.acquire(resource, () => true, stop)!;
  assert.equal(registration.remember('session-1'), true);
  registration.forget('session-1');
  await cleanup();
  assert.deepEqual(calls, []);
});

test('a claimed stop refuses a later installation of the same id', async () => {
  const custody = new PreparingStartCustody(), registration = custody.attach();
  const flight = registration.reserve('key-1', () => true);
  let installAnswer: boolean | undefined;
  const cleanup = flight.acquire(resource, () => true, async (_signal, uninstalled, claim) => {
    assert.ok(uninstalled() && claim());
    installAnswer = registration.remember('session-1');
  })!;
  await cleanup();
  assert.equal(installAnswer, false);
});

test('keys are reserved once, invalid resources never bind, and the history fails closed', async () => {
  const custody = new PreparingStartCustody(), registration = custody.attach();
  const flight = registration.reserve('key-1', () => true);
  assert.throws(() => registration.reserve('key-1', () => true), {name: 'AbortError'});
  assert.throws(() => registration.reserve('', () => true), {name: 'AbortError'});
  assert.equal(flight.acquire({id: '', generation: 1}, () => true, async () => {}), undefined);
  assert.equal(flight.acquire({id: 'x', generation: -1}, () => true, async () => {}), undefined);
  for (let i = 0; i < 256; i++) registration.remember('installed-' + i);
  const {calls, stop} = stops();
  const cleanup = flight.acquire(resource, () => true, stop)!;
  registration.remember('one-too-many');
  await cleanup();
  assert.deepEqual(calls, [], 'past 256 installs, cleanup is denied');
});

test('owner or registry authority loss and finished flights refuse cleanup', async () => {
  let owner = true;
  const custody = new PreparingStartCustody(() => owner), registration = custody.attach();
  const flight = registration.reserve('key-1', () => true);
  const {calls, stop} = stops();
  const cleanup = flight.acquire(resource, () => true, stop)!;
  owner = false;
  await cleanup();
  assert.deepEqual(calls, []);
  owner = true;
  const later = registration.reserve('key-2', () => true);
  later.finish();
  assert.equal(later.acquire(resource, () => true, stop), undefined);
  assert.equal(later.held(), false);
  custody.dispose();
  const disposed = registration.reserve('key-3', () => true);
  assert.equal(disposed.current(), false, 'a disposed registry admits no continuation');
  disposed.finish();
});

test('WEB002 custody pending observes real flights and remembered IDs, never empty registrations', () => {
  const custody = new PreparingStartCustody(), changes: boolean[] = [];
  const off = custody.subscribePending(() => changes.push(custody.pending()));
  const first = custody.attach(), second = custody.attach();
  assert.equal(custody.pending(), false);assert.deepEqual(changes, []);
  const a = first.reserve('a', () => true), b = second.reserve('b', () => true);
  assert.equal(custody.pending(), true);assert.deepEqual(changes, [true]);
  first.remember('installed');first.remember('installed');a.finish();b.finish();
  assert.equal(custody.pending(), true, 'an actual installed ID survives final flight release');
  second.dispose();first.forget('missing');assert.deepEqual(changes, [true]);
  first.forget('installed');assert.equal(custody.pending(), false);assert.deepEqual(changes, [true, false]);
  first.remember('again');first.dispose();assert.deepEqual(changes, [true, false, true, false]);
  off();custody.dispose();
});

test('WEB002 cleanup flight retains through registration retirement and finish until authenticated cleanup returns', async () => {
  const custody = new PreparingStartCustody(), registration = custody.attach(), changes: boolean[] = [];
  custody.subscribePending(() => changes.push(custody.pending()));
  const flight = registration.reserve('cleanup', () => true);
  let release!: () => void;
  const wait = new Promise<void>(done => {release = done;});
  const cleanup = flight.acquire(resource, () => true, async (_signal, _uninstalled, claim) => {assert.equal(claim(), true);await wait;})!;
  const pending = cleanup();registration.dispose();flight.finish();
  assert.equal(flight.held(), false, 'continuation finished while cleanup owns the flight');
  assert.equal(custody.pending(), true);assert.deepEqual(changes, [true]);
  release();await pending;
  assert.equal(custody.pending(), false);assert.deepEqual(changes, [true, false]);custody.dispose();
});

test('WEB002 owner disposal releases pending once and aborts cleanup without late resurrection', async () => {
  const custody = new PreparingStartCustody(), registration = custody.attach(), changes: boolean[] = [];
  custody.subscribePending(() => changes.push(custody.pending()));
  const flight = registration.reserve('cleanup', () => true);
  let signal!: AbortSignal, release!: () => void;
  const wait = new Promise<void>(done => {release = done;});
  const cleanup = flight.acquire(resource, () => true, async (abort, _uninstalled, claim) => {signal = abort;assert.equal(claim(), true);await wait;})!;
  const pending = cleanup();registration.remember('another');custody.dispose();custody.dispose();
  assert.equal(signal.aborted, true);assert.equal(custody.pending(), false);assert.deepEqual(changes, [true, false]);
  release();await pending;flight.finish();registration.forget('another');registration.dispose();
  assert.equal(custody.pending(), false);assert.deepEqual(changes, [true, false]);
});

test('WEB002 pending observers cannot interrupt canonical custody and unsubscribe detaches', () => {
  const custody = new PreparingStartCustody(), registration = custody.attach();let heard = 0;
  const off = custody.subscribePending(() => {heard++;throw new Error('observer');});
  const flight = registration.reserve('original', () => true);assert.equal(flight.current(), true);
  off();flight.finish();assert.equal(heard, 1);assert.equal(custody.pending(), false);custody.dispose();
});

test('E-web-25: a Live start resource at generation 0 is accepted; negative and non-integer generations are not', async () => {
  const custody = new PreparingStartCustody(), registration = custody.attach();
  const flight = registration.reserve('key-gen0', () => true);
  const {calls, stop} = stops();
  assert.equal(flight.acquire({id: 's', generation: -1}, () => true, stop), undefined);
  assert.equal(flight.acquire({id: 's', generation: 0.5}, () => true, stop), undefined);
  assert.equal(flight.acquire({id: 's', generation: Number.NaN}, () => true, stop), undefined);
  const cleanup = flight.acquire({id: 's', generation: 0}, () => true, stop);
  assert.ok(cleanup, 'generation 0 is claimable');
  await cleanup!();
  assert.deepEqual(calls, ['stop']);
});
