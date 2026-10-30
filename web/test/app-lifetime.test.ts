import test from 'node:test';
import assert from 'node:assert/strict';
import {AppLifetime} from '../src/AppLifetime.ts';

function deferred() {let resolve!: () => void, reject!: (error: Error) => void;const promise = new Promise<void>((yes, no) => {resolve = yes;reject = no;});return {promise, resolve, reject};}

test('teardown while installation is loading aborts startup and never mounts after resolution', async () => {
  const loading = deferred(), app = new AppLifetime();let mounts = 0, signal: AbortSignal | undefined;
  const startup = app.start(value => {signal = value;return loading.promise;}, () => {mounts++;return () => {};});
  app.dispose();app.dispose();assert.equal(signal?.aborted, true);
  loading.resolve();await startup;
  await app.start(async () => {assert.fail('a disposed app cannot initialize again');}, () => {mounts++;return () => {};});
  assert.equal(mounts, 0);
});

test('an initializer rejection after teardown is contained, while an active failure remains a failure', async () => {
  const loading = deferred(), app = new AppLifetime();
  const startup = app.start(() => loading.promise, () => {assert.fail('failed initialization cannot mount');});
  app.dispose();loading.reject(new Error('late-native-installation'));await startup;
  const active = new AppLifetime();
  await assert.rejects(active.start(async () => {throw new Error('native-unavailable');}, () => {assert.fail('no browser fallback');}), /native-unavailable/);
  active.dispose();
});

test('one mounted app retires captured owners before the host removes its native platform ports', async () => {
  const app = new AppLifetime(), order: string[] = [];let mounts = 0;
  const mount = () => {mounts++;return () => {order.push('bootstrap:retire-playback', 'root:unmount-saved-and-watch');};};
  await Promise.all([app.start(async () => {}, mount), app.start(async () => {assert.fail('only one startup');}, mount)]);
  app.dispose();order.push('native:uninstall');app.dispose();
  assert.equal(mounts, 1);assert.deepEqual(order, ['bootstrap:retire-playback', 'root:unmount-saved-and-watch', 'native:uninstall']);
});

test('a close during mounting immediately retires the returned owner instead of retaining it', async () => {
  const app = new AppLifetime();let retired = 0;
  await app.start(async () => {}, () => {app.dispose();return () => {retired++;};});
  app.dispose();assert.equal(retired, 1);
});
