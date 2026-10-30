import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {channelTint} from '../src/presentation/kit-model/channels.ts';

/** Shared with native/TV/Roku ports; includes signed-negative, INT_MIN,
 * surrogate-pair and non-normalized Unicode seeds. */
const vectors: readonly {seed: string; hash: number; index: number; tint: string}[] = JSON.parse(
  readFileSync(new URL('./fixtures/channel-tint-vectors.json', import.meta.url), 'utf8'));
test('§33 channel tint matches the shared fixed-order, signed UTF-16 vectors', () => {
  assert.equal(new Set(vectors.map(vector => vector.index)).size, 8);
  assert.ok(vectors.some(vector => vector.hash < 0));
  assert.ok(vectors.some(vector => vector.hash === -2147483648));
  for (const vector of vectors) assert.equal(channelTint(vector.seed), vector.tint, JSON.stringify(vector));
  assert.notEqual(channelTint('source:é'), channelTint('source:e\u0301'));
});
