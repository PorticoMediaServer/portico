import assert from 'node:assert/strict';
import test from 'node:test';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

test('selected-items labels localize genuine single kinds and use honest mixed or unknown totals', async () => {
  const {selectedItemsLabel} = await import(
    '../src-next/shell/selection-display.ts'
  );
  assert.equal(
    selectedItemsLabel({count: 1, types: [{kind: 'movie', count: 1}]}),
    '1 movie',
  );
  assert.equal(
    selectedItemsLabel({count: 4, types: [{kind: 'movie', count: 4}]}),
    '4 movies',
  );
  assert.equal(
    selectedItemsLabel({count: 3, types: [{kind: 'song', count: 3}]}),
    '3 tracks',
  );
  assert.equal(
    selectedItemsLabel({count: 2, types: [{kind: 'episode', count: 2}]}),
    '2 episodes',
  );
  assert.equal(
    selectedItemsLabel({
      count: 4,
      types: [
        {kind: 'movie', count: 2},
        {kind: 'song', count: 2},
      ],
    }),
    '4 titles',
  );
  assert.equal(
    selectedItemsLabel({count: 4, types: [{kind: 'movie', count: 3}]}),
    '4 titles',
    'incomplete kind facts cannot name the whole selection',
  );
  assert.equal(selectedItemsLabel({count: 0, types: []}), '0 titles');
  for (const kind of ['future-kind', '__proto__']) {
    assert.equal(
      selectedItemsLabel(
        JSON.parse(JSON.stringify({count: 2, types: [{kind, count: 2}]})),
      ),
      '2 titles',
    );
  }
});
