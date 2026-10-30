import test from 'node:test';
import assert from 'node:assert/strict';
import type {AccountEntryState} from '../src/platform/account-entry.ts';
import {initializeAccountSignIn} from '../src/screens/accounts/sign-in-start.ts';

const captured = Object.freeze({href: 'https://server.test/auth/portico-return', fragment: '#opaque-captured-value'});
const deferred = <T,>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {promise, resolve};};
function setup() {
  const calls: unknown[] = [];
  let state: AccountEntryState = {phase: 'welcome'};
  const entry = {
    getSnapshot: () => state,
    finishPorticoReturn: async (value: typeof captured) => {calls.push(['finish', value]);},
    inspectMoveLink: async (origin: string, value: Readonly<{code: string; id?: string}>) => {calls.push(['move', origin, value]);},
    loadProfiles: async () => {calls.push(['profiles']);},
    inspect: async (origin: string) => {calls.push(['inspect', origin]); return undefined;},
  };
  const ports = {pageOrigin: 'https://actual-page.test', isCurrent: () => true,
    selectedOrigin: async (): Promise<string | undefined> => {calls.push(['selected']); return undefined;}, username: (value: string) => {calls.push(['username', value]);}};
  return {entry, ports, calls, set: (value: AccountEntryState) => {state = value;}};
}
test('the one-use original return goes straight to its canonical controller without another consent or store read', async () => {
  const f = setup(); let takes = 0;
  await initializeAccountSignIn(f.entry, {...f.ports, takeReturn: () => {takes++; return captured;}});
  assert.equal(takes, 1); assert.deepEqual(f.calls, [['finish', captured]]);
  assert.equal((f.calls[0] as unknown[])[1], captured);
});
test('a captured move link keeps its expected identity and proves the page server before any saved-session restore', async () => {
  const f = setup(), link = Object.freeze({code: 'a'.repeat(32), id: 'srv_' + 'a'.repeat(42) + 'A'});
  let takes = 0;
  await initializeAccountSignIn(f.entry, {...f.ports, takeMove: () => {takes++; return link;}});
  assert.equal(takes, 1);
  assert.deepEqual(f.calls, [['move', f.ports.pageOrigin, link]]);
  assert.equal((f.calls[0] as unknown[])[2], link, 'forward the original capture, including its expected server');
});
test('an original Portico return never consumes a move link or falls back to it after failure', async () => {
  const f = setup(), failure = new Error('return refused');
  f.entry.finishPorticoReturn = async value => {f.calls.push(['finish', value]); throw failure;};
  let moveTakes = 0;
  await assert.rejects(initializeAccountSignIn(f.entry, {...f.ports, takeReturn: () => captured,
    takeMove: () => {moveTakes++;return {code: 'a'.repeat(32)};},
  }), error => error === failure);
  assert.equal(moveTakes, 0);assert.deepEqual(f.calls, [['finish', captured]]);
});
test('move proof failure never restores a saved session or retries without the expected identity', async () => {
  const f = setup(), failure = new Error('different server'), link = Object.freeze({code: 'a'.repeat(32), id: 'srv_' + 'a'.repeat(42) + 'A'});
  f.entry.inspectMoveLink = async (origin, value) => {f.calls.push(['move', origin, value]);throw failure;};
  await assert.rejects(initializeAccountSignIn(f.entry, {...f.ports, takeMove: () => link}), error => error === failure);
  assert.deepEqual(f.calls, [['move', f.ports.pageOrigin, link]]);
});
test('an invalid synchronously scrubbed move capture is terminal before any saved-session read', async () => {
  const f = setup(), failure = new Error('invalid move link');
  let takes = 0;
  await assert.rejects(initializeAccountSignIn(f.entry, {...f.ports,
    takeMove: () => {takes++;throw failure;},
  }), error => error === failure);
  assert.equal(takes, 1);assert.deepEqual(f.calls, []);
});
test('retirement while taking a move capture prevents its dispatch', async () => {
  const f = setup();let current = true;
  await initializeAccountSignIn(f.entry, {...f.ports, isCurrent: () => current,
    takeMove: () => {current = false;return Object.freeze({code: 'a'.repeat(32)});},
  });
  assert.deepEqual(f.calls, []);
});
test('a fresh server page proves its actual origin before showing credentials', async () => {
  const f = setup(); await initializeAccountSignIn(f.entry, f.ports);
  assert.deepEqual(f.calls, [['selected'], ['inspect', f.ports.pageOrigin]]);
});
test('restored restricted accounts re-enter existing sign-in using only actual name and origin facts', async () => {
  const f = setup(); f.set({phase: 'two-step-setup', origin: 'https://proved-server.test', snapshot: {account: {username: 'genuine-user'}} as NonNullable<AccountEntryState['snapshot']>});
  await initializeAccountSignIn(f.entry, {...f.ports, selectedOrigin: async () => 'https://saved-server.test'});
  assert.deepEqual(f.calls, [['profiles'], ['username', 'genuine-user'], ['inspect', 'https://proved-server.test']]);
});
test('a replaced page cannot use a selected-origin answer that arrives after disposal', async () => {
  const f = setup(), pending = deferred<string | undefined>(); let current = true;
  const work = initializeAccountSignIn(f.entry, {...f.ports, selectedOrigin: () => pending.promise, isCurrent: () => current});
  current = false; pending.resolve('https://old.test'); await work;
  assert.deepEqual(f.calls, []);
});
