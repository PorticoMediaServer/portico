import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

// Each load is a fresh process as far as the module's once-only state is concerned.
const load = (runtime: Record<string, unknown> | undefined) =>
  screenModule(new URL('../src-next/platform/boot-host.ts', import.meta.url), {react: {default: {}}, 'react-native': {NativeModules: {PorticoRuntime: runtime}}});

test('the first opening starts at the platform launch time; later openings start when asked', async () => {
  const host = await load({launchStartedAt: 9_000});
  assert.equal(host.takeLaunchStartedAt(10_000), 9_000);
  assert.equal(host.takeLaunchStartedAt(12_000), 12_000);
});

test('a launch time that is missing, in the future or from a long-running process is not used', async () => {
  for (const launchStartedAt of [undefined, 'soon', Number.NaN, 10_001, 10_000 - 60_000]) {
    const host = await load({launchStartedAt});
    assert.equal(host.takeLaunchStartedAt(10_000), 10_000, String(launchStartedAt));
  }
  assert.equal((await load(undefined)).takeLaunchStartedAt(10_000), 10_000);
});

test('React reports its first frame to the native boot layer once', async () => {
  let calls = 0;
  const host = await load({bootDrawn: () => {calls++;}});
  host.reportFirstFrame(); host.reportFirstFrame();
  assert.equal(calls, 1);
  (await load({})).reportFirstFrame();
  (await load(undefined)).reportFirstFrame();
});
