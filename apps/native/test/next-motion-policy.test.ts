import assert from 'node:assert/strict';
import test from 'node:test';
import * as design from '@portico/design';
import {effectsPolicy, motionPolicy, shouldUseLiteEffects} from '../src-next/platform/motion-policy.ts';
import {screenModule} from './helpers/screen-harness.mjs';

const GiB = 1024 ** 3;

function setGlobalTier(value: string | undefined) {
  const key = '__PORTICO_EFFECTS_TIER__';
  const previous = Object.getOwnPropertyDescriptor(globalThis, key);
  if (value === undefined) delete (globalThis as Record<string, unknown>)[key];
  else Object.defineProperty(globalThis, key, {configurable: true, writable: true, value});
  return () => {
    if (previous) Object.defineProperty(globalThis, key, previous);
    else delete (globalThis as Record<string, unknown>)[key];
  };
}

async function loadPolicy(options: {
  os?: string;
  isTV?: boolean;
  constants?: Record<string, unknown>;
  nativeModules?: Record<string, unknown>;
  globalTier?: string;
} = {}) {
  const restore = setGlobalTier(options.globalTier);
  try {
    const mod = await screenModule(
      new URL('../src-next/platform/motion-policy.ts', import.meta.url),
      {
        react: {default: {}},
        '@portico/design': design,
        'react-native': {
          Platform: {OS: options.os ?? 'ios', isTV: options.isTV ?? false, constants: options.constants},
          NativeModules: options.nativeModules ?? {},
        },
      },
    );
    return {mod, restore};
  } catch (error) {
    restore();
    throw error;
  }
}

test('shouldUseLiteEffects classifies Fire TV and low-RAM / low-core Android as lite, and keeps Apple full', () => {
  assert.equal(shouldUseLiteEffects(null), false);
  assert.equal(shouldUseLiteEffects(undefined), false);
  assert.equal(shouldUseLiteEffects({}), false);

  // Apple platforms never auto-enable lite effects.
  assert.equal(shouldUseLiteEffects({os: 'ios', isTV: false, memoryBytes: 2 * GiB, processorCount: 2}), false);
  assert.equal(shouldUseLiteEffects({os: 'ios', isTV: true, memoryBytes: 2 * GiB, processorCount: 2}), false);

  // Amazon Fire TV always defaults to lite effects even without RAM/core counts or with higher specs.
  assert.equal(shouldUseLiteEffects({os: 'android', isTV: true, manufacturer: 'Amazon'}), true);
  assert.equal(shouldUseLiteEffects({os: 'android', isTV: true, manufacturer: 'amazon', memoryBytes: 6 * GiB, processorCount: 8}), true);

  // Low-RAM (<= 3 GiB) or low-core (<= 4) Android phones, tablets, and TVs default to lite effects.
  assert.equal(shouldUseLiteEffects({os: 'android', isTV: false, memoryBytes: 3 * GiB, processorCount: 8}), true);
  assert.equal(shouldUseLiteEffects({os: 'android', isTV: false, memoryBytes: 2 * GiB, processorCount: 8}), true);
  assert.equal(shouldUseLiteEffects({os: 'android', isTV: false, memoryBytes: 4 * GiB, processorCount: 4}), true);
  assert.equal(shouldUseLiteEffects({os: 'android', isTV: true, manufacturer: 'Google', memoryBytes: 2 * GiB, processorCount: 4}), true);

  // Higher-spec Android phones and Android TVs stay on full effects.
  assert.equal(shouldUseLiteEffects({os: 'android', isTV: false, memoryBytes: 4 * GiB, processorCount: 6}), false);
  assert.equal(shouldUseLiteEffects({os: 'android', isTV: true, manufacturer: 'NVIDIA', memoryBytes: 4 * GiB, processorCount: 8}), false);

  // Zero, negative, NaN, or absent hardware metrics do not falsely trigger lite.
  assert.equal(shouldUseLiteEffects({os: 'android', isTV: false}), false);
  assert.equal(shouldUseLiteEffects({os: 'android', isTV: false, memoryBytes: 0, processorCount: 0}), false);
  assert.equal(shouldUseLiteEffects({os: 'android', isTV: false, memoryBytes: -1, processorCount: Number.NaN}), false);
});

test('lite effects policy disables blur, glow, shadows, drift, and enterFades', () => {
  assert.deepEqual(effectsPolicy(false, true), {blur: false, glow: false, shadows: false, drift: false, enterFades: false});
  assert.deepEqual(effectsPolicy(true, true), {blur: false, glow: false, shadows: false, drift: false, enterFades: false});
  const lite = motionPolicy(false, true);
  assert.equal(lite.tier, 'lite');
  assert.equal(lite.lite, true);
  assert.deepEqual(lite.effects, {blur: false, glow: false, shadows: false, drift: false, enterFades: false});
});

test('detectLiteEffects enables lite synchronously on Fire TV and asynchronously for low-RAM / low-core Android', async () => {
  // Fire TV fast path via Platform.constants without needing any native module.
  {
    let capCalls = 0;
    const {mod, restore} = await loadPolicy({
      os: 'android',
      isTV: true,
      constants: {Manufacturer: 'Amazon'},
      nativeModules: {PorticoCapabilities: {facts: async () => {capCalls++; return {};}}},
    });
    try {
      let notifications = 0;
      mod.subscribeLiteEffects(() => notifications++);
      const promise = mod.detectLiteEffects();
      assert.equal(mod.getLiteEffects(), true, 'Fire TV enables lite synchronously before first render');
      assert.equal(await promise, true);
      assert.equal(notifications, 1);
      assert.equal(capCalls, 0);
    } finally {
      restore();
    }
  }

  // Low-RAM Android phone detected via PorticoMpvPlaybackModule.facts().
  {
    const {mod, restore} = await loadPolicy({
      os: 'android',
      isTV: false,
      nativeModules: {
        PorticoMpvPlaybackModule: {facts: async () => ({memoryBytes: 3 * GiB, processorCount: 8})},
      },
    });
    try {
      assert.equal(mod.getLiteEffects(), false);
      assert.equal(await mod.detectLiteEffects(), true);
      assert.equal(mod.getLiteEffects(), true);
    } finally {
      restore();
    }
  }

  // Low-core Android TV falling back to PorticoCapabilities.facts() when mpv module is absent.
  {
    const {mod, restore} = await loadPolicy({
      os: 'android',
      isTV: true,
      nativeModules: {
        PorticoCapabilities: {facts: async () => ({manufacturer: 'Google', memoryBytes: 4 * GiB, processorCount: 4})},
      },
    });
    try {
      assert.equal(await mod.detectLiteEffects(), true);
      assert.equal(mod.getLiteEffects(), true);
    } finally {
      restore();
    }
  }

  // High-spec Android phone stays full; iOS skips native module probes; empty/failing modules stay safe.
  {
    let iosCalls = 0;
    const {mod: iosMod, restore: restoreIos} = await loadPolicy({
      os: 'ios',
      isTV: false,
      nativeModules: {PorticoMpvPlaybackModule: {facts: async () => {iosCalls++; return {memoryBytes: 2 * GiB, processorCount: 2};}}},
    });
    try {
      assert.equal(await iosMod.detectLiteEffects(), false);
      assert.equal(iosCalls, 0);
    } finally {
      restoreIos();
    }
  }

  {
    const {mod, restore} = await loadPolicy({
      os: 'android',
      isTV: false,
      nativeModules: {
        PorticoMpvPlaybackModule: {facts: () => Promise.reject(new Error('bridge unavailable'))},
        PorticoCapabilities: {facts: () => {throw new Error('sync failure');}},
      },
    });
    try {
      assert.equal(await mod.detectLiteEffects(), false);
      assert.equal(mod.getLiteEffects(), false);
    } finally {
      restore();
    }
  }
});

test('manual overrides (globalThis.__PORTICO_EFFECTS_TIER__ and setLiteEffects) are never clobbered by detection', async () => {
  // Explicit global 'full' overrides Fire TV auto-detection.
  {
    const {mod, restore} = await loadPolicy({
      os: 'android',
      isTV: true,
      constants: {Manufacturer: 'Amazon'},
      globalTier: 'full',
    });
    try {
      assert.equal(mod.getLiteEffects(), false);
      assert.equal(await mod.detectLiteEffects(), false);
      assert.equal(mod.getLiteEffects(), false);
    } finally {
      restore();
    }
  }

  // Explicit global 'lite' stays lite on iOS.
  {
    const {mod, restore} = await loadPolicy({os: 'ios', isTV: false, globalTier: 'lite'});
    try {
      assert.equal(mod.getLiteEffects(), true);
      assert.equal(await mod.detectLiteEffects(), true);
      assert.equal(mod.getLiteEffects(), true);
    } finally {
      restore();
    }
  }

  // setLiteEffects(false) before detectLiteEffects() prevents Fire TV auto-lite.
  {
    const {mod, restore} = await loadPolicy({
      os: 'android',
      isTV: true,
      constants: {Manufacturer: 'Amazon'},
    });
    try {
      mod.setLiteEffects(false);
      assert.equal(await mod.detectLiteEffects(), false);
      assert.equal(mod.getLiteEffects(), false);
    } finally {
      restore();
    }
  }

  // setLiteEffects(false) while async native detection is in flight is not clobbered on resolution.
  {
    let resolveFacts!: (value: {memoryBytes: number; processorCount: number}) => void;
    const {mod, restore} = await loadPolicy({
      os: 'android',
      isTV: false,
      nativeModules: {
        PorticoMpvPlaybackModule: {facts: () => new Promise(resolve => {resolveFacts = resolve;})},
      },
    });
    try {
      const pending = mod.detectLiteEffects();
      mod.setLiteEffects(false);
      resolveFacts({memoryBytes: 2 * GiB, processorCount: 4});
      assert.equal(await pending, false);
      assert.equal(mod.getLiteEffects(), false);
    } finally {
      restore();
    }
  }
});
