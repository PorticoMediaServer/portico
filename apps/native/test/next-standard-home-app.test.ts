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

test('standard index/App Home actions use restored authority, current documents and real registered controller lifetimes', async t => {
  const app = await standardHomeApp();
  // Sessions the server started: a resumed Play starts one inside its queue
  // request, an explicit start posts its own.
  const sessions = () =>
    app.receipts.filter(
      receipt =>
        receipt.method === 'POST' &&
        receipt.status === 201 &&
        typeof receipt.sessionId === 'string',
    );
  const genericToast = () =>
    app
      .hosts()
      .some(
        host =>
          host.name === 'Text' &&
          host.children === 'Something went wrong. Try again.',
      );
  try {
    await t.test(
      'standard App reaches real Home after secure-record and verified-route restore',
      () => {
        assert.ok(
          app.hosts().some(host => host.props.testID === 'native-home'),
        );
        assert.ok(
          app.receipts.some(
            receipt =>
              receipt.path === '/v1/networking/identity-proof' &&
              receipt.status === 200,
          ),
        );
        assert.ok(
          app.receipts.some(
            receipt => receipt.path === '/v1/me' && receipt.status === 200,
          ),
        );
        assert.ok(
          app.receipts.some(
            receipt =>
              receipt.path === '/v1/me/devices/current/capabilities' &&
              receipt.status === 204,
          ),
        );
        assert.equal(sessions().length, 0);
      },
    );
    await t.test(
      'an HTTP boundary failure reaches the real Home toast and creates no session',
      async () => {
        app.fault({status: 500, code: 'r25_boundary_options_failure'});
        const warning = console.warn;
        try {
          console.warn = () => {throw Error('throwing Home diagnostic sink');};
          await app.tap();
        } finally {console.warn = warning;}
        assert.ok(
          app.receipts.some(
            receipt =>
              receipt.path === '/v1/items/movie/playback-options' &&
              receipt.status === 500 &&
              receipt.errorCode === 'r25_boundary_options_failure',
          ),
        );
        assert.equal(sessions().length, 0);
        assert.ok(
          genericToast(),
          'the same generic text alone cannot identify the failed call',
        );
        app.fault(undefined);
      },
    );
    await t.test(
      'real Home subscription refresh replaces callbacks and refuses an unoffered retained entry',
      async () => {
        const previous = app.card().props.onPress;
        await app.refresh();
        assert.notEqual(app.card().props.onPress, previous);
        const count = app.requests.length;
        previous();
        await app.settle();
        assert.equal(
          app.requests.length,
          count,
          'a deliberately retained stale callback cannot dispatch playback',
        );
        assert.equal(sessions().length, 0);
        assert.ok(
          genericToast(),
          'a second distinct guarded failure also has generic text',
        );
      },
    );
    await t.test(
      'current native Continue onPress traverses real playback options and creates a protocol session',
      async () => {
        const warning = console.warn;
        try {
          console.warn = () => {throw Error('throwing Home diagnostic sink');};
          await app.tap();
        } finally {console.warn = warning;}
        assert.ok(
          app.receipts.some(
            receipt =>
              receipt.path === '/v1/items/movie/playback-options' &&
              receipt.status === 200,
          ),
        );
        assert.equal(sessions().length, 1);
        assert.equal(
          app.errors.length,
          0,
          'real production Fabric commits have no rendering or nested-update error',
        );
      },
    );
    await t.test(
      'App unmount/restoration registers a genuine phone More menu whose Play resumes into a second session',
      async () => {
        await app.unmount();
        // server-connections intentionally keeps one app-global native monitor. Runtime subscriptions must retire.
        assert.deepEqual(app.subscriptions(), {
          listeners: 1,
          appState: 1,
          pending: 0,
          frames: 0,
        });
        await app.mount();
        assert.notEqual(app.card().props.accessibilityState?.expanded, true);
        await app.more();
        assert.equal(app.card().props.accessibilityState?.expanded, true, 'the real native card reflects the live controller menu');
        assert.ok(
          app
            .hosts()
            .some(
              host =>
                host.name === 'Pressable' &&
                host.props.accessibilityLabel === 'Play',
            ),
        );
        await app.menuPlay();
        assert.notEqual(app.card().props.accessibilityState?.expanded, true, 'accepted Play closes only the menu binding');
        assert.equal(
          sessions().length,
          2,
          'actual HomeWindow membership, controller menu and guarded playback port succeed',
        );
      },
    );
    await t.test(
      'changed server Home fence retires the real authority and removes Home once Home is shown again',
      async () => {
        // The second session's player covers Home. A hidden Home waits rather
        // than revalidating, so the fence change cannot retire it yet.
        const reads = () => app.receipts.filter(receipt => receipt.path === '/v1/home').length;
        const before = reads();
        await app.retireHome();
        assert.equal(reads(), before, 'a Home covered by the player is not read');
        assert.ok(app.hosts().some(host => host.props.testID === 'native-home'));
        await app.tapLabel('Minimize player');
        // The shown Home's owed read waits out the freshness gather (400 ms, plus jitter).
        await realTimeout(700);
        await app.settle();
        assert.ok(reads() > before, 'collapsing the player revalidates the waiting Home');
        assert.equal(
          app.hosts().some(host => host.props.testID === 'native-home'),
          false,
        );
        assert.equal(sessions().length, 2);
      },
    );
  } finally {
    try {
      await app.unmount();
    } finally {
      app.disposeLoader();
    }
  }
});
