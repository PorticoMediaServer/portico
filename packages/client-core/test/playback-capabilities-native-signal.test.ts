import test from 'node:test';
import assert from 'node:assert/strict';
import {readPlaybackCapabilities} from '../src/query/playback.ts';

// React Native's AbortSignal has no throwIfAborted. Every Play in the phone and TV apps reads the
// capabilities first, so calling that method there stopped all playback with a TypeError.
function nativeSignal() {
  const controller = new AbortController();
  Object.defineProperty(controller.signal, 'throwIfAborted', {configurable: true, value: undefined});
  return controller;
}
const api = {request: () => Promise.reject(new Error('no request expected'))};

test('playback capabilities read with a React Native signal', async () => {
  assert.deepEqual(await readPlaybackCapabilities(api, nativeSignal().signal), {features: {}});
});

test('playback capabilities read with a cancelled React Native signal is cancelled', () => {
  const controller = nativeSignal(); controller.abort();
  assert.throws(() => readPlaybackCapabilities(api, controller.signal), (error: unknown) => error instanceof Error && error.name === 'AbortError');
});
