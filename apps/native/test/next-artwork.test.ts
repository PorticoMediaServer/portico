import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import * as artwork from '../../../packages/client-core/src/artwork/index.ts';
import * as artworkArrival from '../src-next/platform/artwork-arrival.ts';

test('copied image scheduler gates mounting, remembers decodes and cancels offscreen work', async () => {
  (globalThis as {__DEV__?: boolean}).__DEV__ = false;
  const effects: (() => (() => void) | void)[] = [];
  const react = {createContext: (value: unknown) => ({value}), useContext: (context: {value: unknown}) => context.value, useState: (value: unknown) => [value, () => {}], useEffect: (effect: () => (() => void) | void) => {effects.push(effect);}};
  const pipeline = await screenModule(new URL('../src-next/platform/ArtworkPipeline.ts', import.meta.url), {react: {...react, default: react}, '../../../../packages/client-core/src/artwork/index': artwork, './artwork-arrival': artworkArrival});
  // A box picks the slot whose actual pixels cover it, by name and token; the descriptor and any
  // legacy width are dropped so one slot is one URL. A deliberate representation is left alone.
  const described = 'https://server.test/art?v=' + 'a'.repeat(64) + '&sd=533x800.9.' + '1'.repeat(16) + '&ld=1280x1920.40.' + '2'.repeat(16) + '&w=400';
  assert.equal(pipeline.sizedUri(described, 130, 195, 3), 'https://server.test/art?r=' + '1'.repeat(16) + '&size=small&v=' + 'a'.repeat(64));
  assert.equal(pipeline.sizedUri(described, 400, 600, 3), 'https://server.test/art?r=' + '2'.repeat(16) + '&size=large&v=' + 'a'.repeat(64));
  assert.equal(pipeline.sizedUri('https://server.test/art', 200, 300, 2), 'https://server.test/art?size=small');
  assert.equal(pipeline.sizedUri('https://server.test/art?size=avatar', 200, 200, 2), 'https://server.test/art?size=avatar');
  pipeline.beginArtworkScreen('next-image-proof');
  const first = pipeline.useArtworkSlot('image-proof', {row: 0, column: 0}, true);
  assert.equal(first.active, false);
  const cleanups = effects.splice(0).map(effect => effect());
  await new Promise(resolve => setTimeout(resolve, 0));
  const loading = pipeline.useArtworkSlot('image-proof', {row: 0, column: 0}, true);
  assert.equal(loading.active, true); loading.loaded();
  await new Promise(resolve => setTimeout(resolve, 0));
  const cached = pipeline.useArtworkSlot('image-proof', null, false);
  assert.equal(cached.cached, true); assert.equal(cached.active, true);
  cleanups.forEach(cleanup => cleanup?.()); effects.length = 0;
  pipeline.useArtworkSlot('image-cancelled', {row: 1}, false);
  const offscreen = effects.splice(0).map(effect => effect());
  offscreen.forEach(cleanup => cleanup?.());
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.equal(pipeline.shown.has('image-cancelled'), false);
});
