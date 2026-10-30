import assert from 'node:assert/strict';
import {test} from 'node:test';
import {libraryTabs} from '../src/presentation/library-tabs.ts';

/**
 * The owner's Unmatched items tab is an offer, not a kind fact: it exists only when the
 * library's own metadata agent read says online, never for Audiobooks, and never while the
 * answer is unknown. Non-owner Music keeps its public capability-gated browse.
 */
test('an online owner library gets exactly one attention tab', () => {
  for (const kind of ['movie', 'tv', 'anime', 'music']) {
    const tabs = libraryTabs(kind, {owner: true, onlineAttention: true});
    const attention = tabs.filter(tab => tab.shows === 'unmatched');
    assert.equal(attention.length, 1, kind);
    assert.equal(attention[0].id, 'unmatched');
    assert.equal(attention[0].label, 'next.ownerLibraries.tab.attention');
    assert.equal(tabs.at(-1)?.shows, 'unmatched', 'the attention tab is the last pivot');
  }
});

test('unknown, local-only and Audiobooks offer no attention tab', () => {
  for (const kind of ['movie', 'tv', 'anime', 'music', 'audiobook']) {
    for (const options of [{owner: true}, {owner: true, onlineAttention: false}]) {
      const tabs = libraryTabs(kind, options);
      assert.equal(tabs.some(tab => tab.shows === 'unmatched'), false, `${kind} ${JSON.stringify(options)}`);
    }
  }
  assert.equal(libraryTabs('audiobook', {owner: true, onlineAttention: true}).some(tab => tab.shows === 'unmatched'), false);
});

test('non-owner Music keeps its public unmatched browse unchanged', () => {
  const publicTabs = libraryTabs('music', {unmatched: true});
  assert.deepEqual(publicTabs.map(tab => tab.id), ['discover', 'browse', 'releases', 'songs', 'genres', 'playlists', 'unmatched']);
  const publicUnmatched = publicTabs.at(-1)!;
  assert.equal(publicUnmatched.shows, 'titles');
  assert.equal(publicUnmatched.pivot, 'albums');
  assert.deepEqual(publicUnmatched.fixedFilter, [{variant: 'predicate', field: 'match', operator: 'equals', value: 'unmatched'}]);
  const withoutCapability = libraryTabs('music');
  assert.equal(withoutCapability.some(tab => tab.id === 'unmatched'), false);
  assert.equal(libraryTabs('movie', {unmatched: true}).some(tab => tab.id === 'unmatched'), false);
});
