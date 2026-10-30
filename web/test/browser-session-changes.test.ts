import test from 'node:test';
import assert from 'node:assert/strict';
import {BrowserProtectedStore, browserAppSessions, observeBrowserSessionChanges} from '../src/platform/server-connections.ts';

test('actual app-session writer emits a noncredential read hint only after commit, in this tab and other tabs', async t => {
  const page = new EventTarget(), channels: Channel[] = [], sent: unknown[] = [];
  const restoreGlobals: (() => void)[] = []; let stop: (() => void) | undefined;
  t.after(() => {stop?.(); for (const channel of [...channels]) channel.close(); for (const restore of restoreGlobals) restore();});
  class Channel extends EventTarget {
    constructor(readonly name: string) {super(); channels.push(this);}
    postMessage(data: unknown) {sent.push(data); for (const other of channels) if (other !== this && other.name === this.name) other.dispatchEvent(new MessageEvent('message', {data}));}
    close() {channels.splice(channels.indexOf(this), 1);}
  }
  for (const [name, value] of [['window', page], ['BroadcastChannel', Channel]] as const) {
    const old = Object.getOwnPropertyDescriptor(globalThis, name);
    Object.defineProperty(globalThis, name, {configurable: true, value});
    restoreGlobals.push(() => {if (old) Object.defineProperty(globalThis, name, old); else delete (globalThis as Record<string, unknown>)[name];});
  }
  const connection = (id: string) => ({signIn: 'local', connection: {version: '1', selectionId: id.padEnd(32, '_'), logicalOrigin: 'https://' + id + '.test', pin: {serverId: id, publicKey: 'p', fingerprint: 'f'}, routes: {paired: ['https://' + id + '.test']}, session: {sessionFamilyId: 'family-' + id, tokenGeneration: '1', accessToken: 'private-fixture-credential-' + id, expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z', viewer: {serverId: id, accountId: 'account', profileId: 'main', authority: 'local', role: 'member'}}}});
  let raw: unknown = {version: 1, revision: 1, selectedServerId: 'first', servers: [connection('first'), connection('second')]};
  let release: (() => void) | undefined, commit: Promise<void> | undefined, failing = false, committed = false;
  t.mock.method(BrowserProtectedStore.prototype, 'read', async () => raw);
  t.mock.method(BrowserProtectedStore.prototype, 'change', async (update: (old: unknown) => unknown, beforeCommit?: () => void) => {
    const next = update(raw);
    if (failing) throw new Error('Durable write refused.');
    if (commit) await commit;
    beforeCommit?.();
    raw = next; committed = true;
  });
  let heard = 0;
  stop = observeBrowserSessionChanges(() => {assert.equal(committed, true); heard++;});
  const store = browserAppSessions();
  commit = new Promise<void>(resolve => {release = resolve;});
  const selecting = store.select('second'); await new Promise(resolve => setImmediate(resolve));
  assert.equal(heard, 0); assert.deepEqual(sent, []);
  release!(); await selecting; commit = undefined;
  assert.equal((await store.read()).selectedServerId, 'second'); assert.equal(heard, 1); assert.deepEqual(sent, ['changed']);
  await store.signOutServer('absent', 'absent-family');
  assert.equal(heard, 2, 'even absent sign-out durably advances the admission fence');
  await store.connectionStorage('second').change(current => current);
  assert.equal(heard, 2, 'a true no-op publishes nothing');
  failing = true; await assert.rejects(store.select('first')); failing = false;
  assert.equal(heard, 2, 'a failed write publishes nothing'); assert.deepEqual(sent, ['changed', 'changed']);
  let current = true;
  commit = new Promise<void>(resolve => {release = resolve;});
  const cancelled = assert.rejects(store.select('first', () => current), {code: 'cancelled'});
  await new Promise(resolve => setImmediate(resolve)); current = false; release!(); await cancelled; commit = undefined;
  assert.equal((await store.read()).selectedServerId, 'second');
  assert.equal(heard, 2, 'a cancelled write publishes no same-tab retirement');
  assert.deepEqual(sent, ['changed', 'changed'], 'a cancelled write publishes no cross-tab retirement');
  const otherTab = new Channel('portico.app-session-change.v1');
  otherTab.postMessage('changed'); assert.equal(heard, 3);
  otherTab.postMessage({changed: true}); assert.equal(heard, 3, 'unknown channel payload is not an authority event');
  page.dispatchEvent(new Event('focus')); page.dispatchEvent(new Event('pageshow')); assert.equal(heard, 5);
  const broken = observeBrowserSessionChanges(() => {throw new Error('Consumer failed.');});
  await store.select('first'); assert.equal(heard, 6, 'a failed observer cannot fail the committed write or another observer'); broken();
  stop(); otherTab.postMessage('changed'); page.dispatchEvent(new Event('focus')); assert.equal(heard, 6);
});
