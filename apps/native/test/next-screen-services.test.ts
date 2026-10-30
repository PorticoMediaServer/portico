import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeScreenServices} from '../src-next/screens/screen-services';
import {NativeShellNavigation} from '../src-next/shell/routes';
import type {ShellAuthority} from '../src-next/shell/model';
import type {ContentEntry} from '../../../packages/client-core/src/library-content';

function fixture(newRequestId: () => Promise<string> = async () => 'operation') {
  let current = true;
  const authority: ShellAuthority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'home'};
  const listeners = new Set<() => void>();
  const navigation = new NativeShellNavigation(authority, () => authority);
  const owner = {authority: () => authority, current: () => current, navigation,
    subscribeAuthority: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}};
  const service = new NativeScreenServices(owner, newRequestId);
  return {service, navigation, listeners, retire: () => {current = false; for (const listener of [...listeners]) listener();}};
}

test('screen feedback ignores cancellation and retiring an authority clears facts and retry commands', () => {
  const f = fixture(); let retried = 0, changes = 0;
  f.service.subscribe(() => changes++);
  f.service.reportError(Object.assign(new Error('private diagnostic'), {code: 'offline'}), 'detail');
  const toast = f.service.getSnapshot(); assert.ok(toast);
  assert.notEqual(toast.message, 'private diagnostic');
  f.service.reportError(Object.assign(new Error('retired'), {name: 'AbortError'}));
  assert.strictEqual(f.service.getSnapshot(), toast);
  const state = f.service.error({code: 'offline'}, () => retried++, 'detail');
  assert.ok(state.primaryAction); state.primaryAction.onPress(); assert.equal(retried, 1);
  f.retire();
  assert.equal(f.service.getSnapshot(), null); assert.equal(f.listeners.size, 0);
  state.primaryAction.onPress(); assert.equal(retried, 1);
  f.service.reportError({code: 'offline'}); assert.equal(changes, 2);
});

test('a native operation id resolving after authority retirement is not returned', async () => {
  let resolve!: (id: string) => void, calls = 0;
  const f = fixture(() => {calls++; return new Promise(done => {resolve = done;});});
  const pending = f.service.requestId();
  f.retire(); resolve('actual-native-id');
  await assert.rejects(pending, {name: 'AbortError'});
  await assert.rejects(f.service.requestId(), {name: 'AbortError'}); assert.equal(calls, 1);
});

test('title navigation uses the actual navigation admission outside Home and stops on authority retirement', () => {
  const f = fixture(), entry: ContentEntry = {id: 'title', kind: 'movie', libraryId: 'movies', title: 'Actual title'};
  f.navigation.open({kind: 'shell', destination: {kind: 'saved'}}, f.navigation.getSnapshot().authority);
  f.service.navigate(entry);
  assert.deepEqual(f.navigation.getSnapshot().route, {kind: 'title', entry});
  f.retire(); assert.throws(() => f.service.navigate(entry), {name: 'AbortError'});
});

// Class 7 (E-tv-43 candidate): error() builds an error row's props during render. After the
// shell's authority retires, a still-mounted row must render its message without a Try again,
// never throw from render and take the whole shell down.
test('an error row rendered after the authority retired shows its message and offers no retry', () => {
  const f = fixture(); let retried = 0;
  f.retire();
  const state = f.service.error({code: 'offline'}, () => retried++, 'detail');
  assert.ok(state.title || state.body, 'the row still says what happened');
  assert.equal(state.primaryAction, undefined, 'a retired screen offers no Try again');
  assert.equal(retried, 0);
});
