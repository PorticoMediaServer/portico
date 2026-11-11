import assert from 'node:assert/strict';
import test from 'node:test';
import * as timers from 'node:timers';
import {setTimeout as realTimeout} from 'node:timers/promises';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

test('a deliberately captured render failure keeps its own error while the fixture still tears itself down', async () => {
  const {installationIdentity, setInstallationIdentity} = await import(
    '../../../packages/client-core/src/installation.ts'
  );
  const boundary = '__porticoStandardHomeNativeBoundary';
  let resolution = 0;
  // A fresh data URL each call, and the fixture's own Manufacturer marker, so
  // the answer proves which loader resolved react-native rather than replaying
  // an already cached module or matching the host platform's own name.
  const manufacturer = async () =>
    (await import(
      `data:text/javascript,import {Platform} from 'react-native';export const mark = Platform.constants?.Manufacturer ?? 'host';//${++resolution}`
    )).mark;
  const app = await standardHomeApp();
  const installed = installationIdentity();
  const original = Error('deliberately captured render failure');
  let failure: unknown;
  let fired = false;
  try {
    assert.ok(
      installed,
      'the mounted product installed its own installation identity',
    );
    assert.equal(
      typeof (globalThis as Record<string, unknown>)[boundary],
      'object',
      'the fixture loader state is installed',
    );
    assert.equal(
      await manufacturer(),
      'test',
      'the fixture loader answers react-native while it is installed',
    );
    assert.notEqual(
      globalThis.setTimeout,
      timers.setTimeout,
      'the fixture owns the clocks while it is installed',
    );
    let mountFailure: unknown;
    try {
      // The app renders this shell inside its own error boundary, so the real
      // renderer records the thrown value; the test manufactures no success.
      await app.mount({
        phoneShell: () => {
          throw original;
        },
      });
    } catch (error) {
      mountFailure = error;
    }
    assert.equal(
      mountFailure,
      original,
      'the real renderer reports the value it captured',
    );
    assert.ok(
      app.errors.includes(original),
      'the recorded render error is that same value',
    );
    setInstallationIdentity({...installed, name: 'deliberate self-test identity'});
    assert.equal(installationIdentity()?.name, 'deliberate self-test identity');
  } finally {
    try {
      await app.unmount();
    } catch (error) {
      failure = error;
    } finally {
      globalThis.setTimeout(() => {
        fired = true;
      }, 0);
      app.disposeLoader();
    }
  }
  // The captured error is this test's result: it must remain the same value,
  // while the teardown around it is structural rather than a manufactured pass.
  assert.equal(
    failure,
    original,
    'the failed unmount rethrows that same captured error',
  );
  assert.ok(
    app.errors.includes(original),
    'the captured error remains the recorded result',
  );
  assert.equal(
    installationIdentity(),
    undefined,
    'the failed unmount still releases the install identity',
  );
  assert.equal(
    app.subscriptions().frames,
    0,
    "the failed unmount still clears this app's frames",
  );
  assert.notEqual(
    await manufacturer(),
    'test',
    'the fixture loader no longer answers react-native after disposal',
  );
  assert.equal(
    (globalThis as Record<string, unknown>)[boundary],
    undefined,
    'disposeLoader removes the boundary loader state',
  );
  assert.equal(
    globalThis.setTimeout,
    timers.setTimeout,
    'disposeLoader resets the fixture clocks',
  );
  assert.equal(
    globalThis.setInterval,
    timers.setInterval,
    'disposeLoader resets the fixture intervals',
  );
  await realTimeout(30);
  assert.equal(fired, false, 'disposeLoader cleared the timer it owned');
});
