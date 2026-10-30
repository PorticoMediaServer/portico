import test from 'node:test';
import assert from 'node:assert/strict';
import {RouteError, routeOrigin} from '../../../packages/client-core/src/route-identity';
import {screenModule} from './helpers/screen-harness.mjs';

async function fixture() {
  const opened: string[] = [], writes = new Set<() => void>(), accountChanges = new Set<() => void>(), cancellations = new Set<() => void>();
  let active = true, state = {revision: 7, selectedServerId: 'original-selection'}, live: any = {account: {id: 'account'}, familyId: 'family'};
  let read = async () => state;
  const credentials = {getSnapshot: () => ({session: live}), subscribe: (fn: () => void) => {accountChanges.add(fn); return () => {accountChanges.delete(fn);};}};
  const owner = {accountCredentials: () => credentials, onCancel: (fn: () => void) => {cancellations.add(fn); return () => {cancellations.delete(fn);};}};
  const {NativeOwnerRecoveryContact} = await screenModule(new URL('../src-next/platform/owner-recovery-contact.ts', import.meta.url), {
    react: {default: {}}, 'react-native': {Platform: {isTV: false}, Linking: {openURL: async (url: string) => {opened.push(url);}}},
    '../../../../packages/client-core/src/route-identity': {RouteError, routeOrigin},
    '../../../../packages/client-core/src/owner-recovery-contact': {contactKnownServerForOwnerRecovery: async () => {throw Error('explicit purpose fixture required');}},
    './accounts': {nativeAccounts: owner},
    './app-session-storage': {nativeAppSessions: {read: () => read()}, subscribeNativeAppSessions: (fn: () => void) => {writes.add(fn); return () => {writes.delete(fn);};}},
    './server-connections': {nativeOwnerRecoveryFetch: () => async () => {throw Error('purpose transport only');}, nativeRouteCrypto: {}},
  });
  const captured = {baseUrl: 'https://original.example', serverId: 'original-server', publicKey: 'original-key'};
  const open = (contact: (...args: any[]) => Promise<any>) => new NativeOwnerRecoveryContact(captured, {accountId: 'account', familyId: 'family'}, () => active, contact, owner);
  return {open, captured, opened, writes, accountChanges, cancellations,
    stale: () => {active = false;},
    changeSelection: (notify = true) => {state = {revision: 8, selectedServerId: 'replacement'}; if (notify) for (const fn of [...writes]) fn();},
    changeAccount: () => {live = {account: {id: 'account'}, familyId: 'replacement-family'}; for (const fn of [...accountChanges]) fn();},
    setRead: (value: typeof read) => {read = value;}};
}
const answer = () => ({purpose: 'owner-recovery', origin: 'https://original.example', serverId: 'original-server', signInURL: 'https://original.example/sign-in'});

test('phone recovery preserves the original immutable directory capture and opens only its proven page', async () => {
  const f = await fixture(); let seen: any;
  const source = f.open(async (captured, options) => {
    seen = captured; assert.equal(Object.isFrozen(captured), true);
    assert.equal(options.isCurrent(), true); assert.equal(options.signal.aborted, false);
    return answer();
  });
  f.captured.baseUrl = 'https://replacement.example'; f.captured.publicKey = 'replacement-key';
  await source.open();
  assert.deepEqual(seen, {baseUrl: 'https://original.example', serverId: 'original-server', publicKey: 'original-key'});
  assert.deepEqual(f.opened, ['https://original.example/sign-in']);
  assert.equal(f.writes.size + f.accountChanges.size + f.cancellations.size, 0);
});

test('retired phone context after its protected read cannot start a public contact', async () => {
  const f = await fixture(); let calls = 0;
  f.setRead(async () => {f.stale(); return {revision: 7, selectedServerId: 'original-selection'};});
  const source = f.open(async () => {calls++; return answer();});
  await assert.rejects(source.open(), {code: 'cancelled'});
  assert.equal(calls, 0); assert.deepEqual(f.opened, []);
});

for (const change of ['changeSelection', 'changeAccount'] as const) test(`phone recovery aborts ${change} during the purpose await`, async () => {
  const f = await fixture(); let release!: () => void, entered!: () => void, signal: AbortSignal | undefined;
  const waiting = new Promise<void>(resolve => {entered = resolve;}), held = new Promise<void>(resolve => {release = resolve;});
  const source = f.open(async (_capture, options) => {signal = options.signal; entered(); await held; return answer();});
  const opening = source.open(); await waiting;
  f[change](); assert.equal(signal?.aborted, true); release();
  await assert.rejects(opening, {code: 'cancelled'}); assert.deepEqual(f.opened, []);
});

test('a silently replaced protected selection is refused at the final read too', async () => {
  const f = await fixture();
  const source = f.open(async () => {f.changeSelection(false); return answer();});
  await assert.rejects(source.open(), {code: 'cancelled'}); assert.deepEqual(f.opened, []);
});

for (const signInURL of ['https://replacement.example/sign-in', 'https://original.example/sign-in?claim=not-authority', 'http://original.example/sign-in', 'https://original.example/sign-in#code=not-authority']) {
  test('phone browser seam refuses an altered recovery destination: ' + signInURL, async () => {
    const f = await fixture(), source = f.open(async () => ({...answer(), signInURL}));
    await assert.rejects(source.open(), {code: 'identity_mismatch'}); assert.deepEqual(f.opened, []);
  });
}

test('cancellation settles even while a protected native read has not returned', async () => {
  const f = await fixture(); let release!: () => void, entered!: () => void, calls = 0;
  const waiting = new Promise<void>(resolve => {entered = resolve;}), held = new Promise<void>(resolve => {release = resolve;});
  f.setRead(async () => {entered(); await held; return {revision: 7, selectedServerId: 'original-selection'};});
  const source = f.open(async () => {calls++; return answer();});
  const opening = source.open(); await waiting; source.dispose();
  await assert.rejects(opening, {code: 'cancelled'});
  release(); await Promise.resolve();
  assert.equal(calls, 0); assert.deepEqual(f.opened, []);
});

test('an internal public-contact deadline remains an honest timeout in a current phone context', async () => {
  const f = await fixture(), source = f.open(async () => {throw new RouteError('cancelled', 'Canonical deadline');});
  await assert.rejects(source.open(), {code: 'timeout'}); assert.deepEqual(f.opened, []);
});
