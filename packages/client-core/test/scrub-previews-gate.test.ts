import test from 'node:test';
import assert from 'node:assert/strict';
import {readScrubPreviewsEnabled, readWebPlaybackCapabilities, scrubPreviewsEnabled} from '../src/query/playback.ts';

// Lead, 16 Nov: no preview read at all until the server says it makes scrub previews.
test('players ask for scrub previews only when GET /v1/system lists them; a failed read is no', async () => {
  const answering = (capabilities: string[]) => ({request: async <T>(path: string) => {assert.equal(path, '/v1/system'); return {capabilities} as T;}});
  assert.equal(scrubPreviewsEnabled(await readWebPlaybackCapabilities(answering(['queue-transitions']))), false);
  assert.equal(scrubPreviewsEnabled(await readWebPlaybackCapabilities(answering(['queue-transitions', 'scrub-previews']))), true);
  assert.equal(await readScrubPreviewsEnabled(answering(['scrub-previews'])), true);
  assert.equal(await readScrubPreviewsEnabled({request: async () => {throw new Error('offline');}}), false);
});
