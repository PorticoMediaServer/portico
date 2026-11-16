import test from 'node:test';
import assert from 'node:assert/strict';
import type {SetupAttempt, SetupRecord} from '@core/setup-store.ts';
import {BrowserSetupStore} from '../src/platform/setup-store.ts';

function slot(initial?: unknown, options: {damaged?: boolean} = {}) {
  let value = initial, damaged = options.damaged === true;
  return {
    assertAvailable: () => {},
    read: async () => {if (damaged) throw Object.assign(new Error('damaged'), {code: 'invalid_saved_connection'}); return value;},
    // The protected store reads damaged bytes as absent inside a change.
    change: async (update: (old: unknown) => unknown) => {const next = update(damaged ? undefined : value); damaged = false; value = next;},
    peek: () => value,
  };
}
const ORIGIN = 'http://192.168.1.20:32400';
const record: SetupRecord = {version: 2, serverId: 'srv', requestId: 'req', setupToken: 'tok', tokenAt: 1, name: 'Den', authMode: 'hosted'};
const handoff = {expected: {operationId: 'op', localGeneration: '1', revision: '2'}, binding: {pin: {serverId: 'srv', publicKey: 'k', fingerprint: 'f'}, origin: ORIGIN,
  hostedOrigin: 'https://hosted.example', operationId: 'op', localGeneration: '1', setupRequestId: 'req', setupRevision: '2', state: 's', codeChallenge: 'c', installationId: 'inst'},
  transactionId: 'setup_tx', expiresAt: '2099-01-01T00:00:00Z', claim: {payload: 'p', signature: 's'}, start: {payload: 'p', signature: 's'}};
const attempt: SetupAttempt = {version: 2, serverId: 'srv', requestId: 'req', attemptId: 'att', origin: ORIGIN, hostedOrigin: 'https://hosted.example', installationId: 'inst',
  state: 's', verifier: 'v', codeChallenge: 'c', createdAt: 1, expiresAt: '2098-01-01T00:00:00Z', handoff};

test('each slot is compare-and-swapped on its own id, and a successor stays', async () => {
  const r = slot(record), a = slot(attempt), store = new BrowserSetupStore('srv', {record: r, attempt: a});
  assert.deepEqual(await store.read(), {record, attempt});
  assert.equal(await store.change({requestId: 'other'}, {record: null}), false);
  assert.deepEqual(r.peek(), record, 'the successor is kept');
  assert.equal(await store.change({attemptId: 'att'}, {attempt: null}), true);
  assert.equal(a.peek(), undefined);
  assert.equal(await store.change({requestId: 'req'}, {record: null}), true);
  assert.deepEqual(await store.read(), {});
  // Another server's record is never written here.
  assert.equal(await store.change({}, {record: {...record, serverId: 'elsewhere'}}), false);
});

test('this tab\'s attempt for another server reads as none here and stays; damaged bytes read as unreadable and retire', async () => {
  const a = slot({...attempt, serverId: 'elsewhere'}), store = new BrowserSetupStore('srv', {record: slot(), attempt: a});
  assert.deepEqual(await store.read(), {});
  assert.equal((a.peek() as SetupAttempt).serverId, 'elsewhere');
  const damaged = new BrowserSetupStore('srv', {record: slot(undefined, {damaged: true}), attempt: slot()});
  assert.deepEqual(await damaged.read(), {unreadable: true});
  assert.equal(await damaged.change({}, {record: null}), true);
  assert.deepEqual(await damaged.read(), {});
});
