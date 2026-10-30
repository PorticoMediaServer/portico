import assert from 'node:assert/strict';
import test from 'node:test';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer} from './helpers/interaction-harness.mjs';

// A-05: a panel its owner closes keeps painting its last content for the exit, inert, and
// unmounts after the longer of the exit (120) and the reduced (160) durations.
test('a closed panel leaves with its last content, then unmounts; reopening cancels the leave', async () => {
  const h = createRenderer(), timers = new Map();
  const saved = {setTimeout: globalThis.window?.setTimeout, clearTimeout: globalThis.window?.clearTimeout};
  let next = 1;
  globalThis.window = {...(globalThis.window ?? {}), setTimeout: (run, ms) => {const id = next++; timers.set(id, {run, ms}); return id;}, clearTimeout: id => timers.delete(id)};
  try {
    const shared = await componentModule(new URL('../src/kit/overlays/shared.ts', import.meta.url), {react: h.react});
    const mod = await componentModule(new URL('../src/kit/overlays/PanelPresence.tsx', import.meta.url), {
      react: h.react, '@design': {motion: {quick: 120}, reducedMotion: {durationMs: 160}}, './shared': shared,
    });
    let value = 'options';
    const render = () => h.render(() => ({type: 'probe', props: {state: mod.useLeavingPresence(value)}})).props.state;
    assert.deepEqual(render(), {shown: 'options', leaving: false});
    value = null;
    assert.deepEqual(render(), {shown: 'options', leaving: true}, 'the last panel stays for its exit');
    assert.deepEqual([...timers.values()].map(timer => timer.ms), [160], 'held for the longer of 120 and the reduced 160');
    value = 'episodes';
    assert.deepEqual(render(), {shown: 'episodes', leaving: false}, 'reopening mid-exit shows the new panel at once');
    value = null; render();
    for (const [id, timer] of [...timers]) {timers.delete(id); timer.run();}
    assert.deepEqual(render(), {shown: undefined, leaving: false}, 'after the exit nothing is painted');
  } finally {h.dispose(); if (saved.setTimeout) Object.assign(globalThis.window, saved);}
});
