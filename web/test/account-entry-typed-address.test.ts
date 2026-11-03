import test from 'node:test';
import assert from 'node:assert/strict';
import {AppSessionStore, type AppSessionRecord} from '@core/app-sessions.ts';
import type {ServerSignInProbe} from '@core/sign-in.ts';
import {presentError} from '@core/presentation/errors.ts';
import {defaultI18n} from '@i18n';
import {BrowserAccountEntry} from '../src/platform/account-entry.ts';

// AU-3: "Use a local server" on web refused a bare address ("192.168.1.20:32500"), the way people type one, with a
// generic sentence. It now tries the same short list of addresses the apps try; a typed scheme is kept exactly.

const serverId = 'srv_' + 's'.repeat(43);
const pin = {serverId, publicKey: 'p'.repeat(43), fingerprint: 'f'.repeat(43)};
const system = {id: serverId, name: 'Home', version: '1', signIn: 'local', move: null, setupRequired: true, hostedConfigured: false, hostedAttached: false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []};

function entryAnswering(answers: string) {
  let raw: AppSessionRecord | undefined, origin: string | undefined, error: unknown;
  const tried: string[] = [], setup: string[] = [];
  const store = new AppSessionStore({read: async () => raw, change: async update => {raw = update(raw);}});
  const probe = {
    inspect: async (candidate: string) => {tried.push(candidate); if (candidate === answers) {origin = candidate; error = undefined; return system;} origin = undefined; error = Object.assign(new Error('unreachable'), {code: 'unreachable'}); return undefined;},
    getSnapshot: () => ({origin, error}), cancel() {}, dispose() {},
  } as unknown as ServerSignInProbe;
  const entry = new BrowserAccountEntry({store, actions: {} as never, environment: () => ({storage: store.connectionStorage(serverId)}), restore: async () => undefined,
    inspect: async () => pin, connect: (async () => {throw new Error('not reached');}) as never, remember: async () => {}, retire: async () => {},
    setupRequired: (at: string) => {setup.push(at);}} as never, probe);
  return {entry, tried, setup};
}

test('a bare home address tries the server port over HTTP first and opens the one that answers', async () => {
  const f = entryAnswering('http://192.168.1.20:32500');
  await f.entry.inspect('192.168.1.20:32500');
  assert.deepEqual(f.tried, ['http://192.168.1.20:32500']);
  assert.deepEqual(f.setup, ['http://192.168.1.20:32500']);
  f.entry.dispose();
});

test('a name with no port tries the likely addresses in order until one answers', async () => {
  const f = entryAnswering('https://media.example.com');
  await f.entry.inspect('media.example.com');
  assert.deepEqual(f.tried, ['https://media.example.com']);
  const g = entryAnswering('https://nas.local:32500');
  await g.entry.inspect('nas.local');
  assert.deepEqual(g.tried, ['http://nas.local:32500', 'https://nas.local:32500']);
  assert.deepEqual(g.setup, ['https://nas.local:32500']);
  f.entry.dispose(); g.entry.dispose();
});

test('a typed scheme is tried exactly as typed, and nothing is guessed around it', async () => {
  const f = entryAnswering('nothing');
  await f.entry.inspect('http://media.example.com:8080');
  assert.deepEqual(f.tried, ['http://media.example.com:8080']);
  assert.equal(f.entry.getSnapshot().phase, 'address');
  f.entry.dispose();
});

test('something that can\'t be an address says what an address looks like', async () => {
  const f = entryAnswering('nothing');
  await f.entry.inspect('ftp://nas');
  assert.deepEqual(f.tried, []);
  const error = (f.entry.getSnapshot() as {error?: unknown}).error;
  assert.equal(presentError(error, 'sign-in', {operation: 'action'}).body, defaultI18n.t('web.signIn.error.address'));
  f.entry.dispose();
});
