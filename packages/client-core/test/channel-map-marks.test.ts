import test from 'node:test';
import assert from 'node:assert/strict';
import {parseChannelMapPage} from '../src/administration.ts';

const entry = {channelId: 'channel', name: 'Northwind News', sourceNumber: '101', number: '12', group: 'News', guideChannelId: '', hidden: false, position: 0, overridden: true};
const page = (mark: unknown) => ({sourceId: 'source', generation: 'generation', nextCursor: '', items: [{...entry, ...(mark === undefined ? {} : {mark})}]});

test('channel map preserves authoritative marks without deriving them from names or pages', () => {
  const raw = {text: 'NN 12', seed: 'source:channel'};
  const parsed = parseChannelMapPage(page(raw));
  assert.deepEqual(parsed.items[0].mark, raw);
  assert.ok(Object.isFrozen(parsed.items[0].mark));
  raw.text = 'changed';
  assert.equal(parsed.items[0].mark?.text, 'NN 12');
});

test('channel map keeps readable rows with missing or malformed marks for the neutral fallback', () => {
  for (const mark of [undefined, null, {}, {text: '', seed: 's'}, {text: ' ', seed: 's'}, {text: 'NN', seed: ' '}, {text: 1, seed: 's'}, {text: 'NN', seed: 1}, {text: 'N\u0000N', seed: 's'}, {text: 'x'.repeat(1025), seed: 's'}, {text: 'NN', seed: 's'.repeat(514)}]) {
    const parsed = parseChannelMapPage(page(mark));
    assert.equal(parsed.items.length, 1);
    assert.equal(parsed.items[0].mark, undefined);
  }
});
