import test from 'node:test';
import assert from 'node:assert/strict';
import {routePath} from '../../contracts/src/routes.ts';
import {ambientArtworkSource} from '../src/artwork/ambient.ts';
import {artworkWidthBucket, artworkSizeBuckets} from '../src/artwork/buckets.ts';

const identity = {serverId: 'server', viewerId: 'profile', authorityKey: 'session:1'};
const version = 'a'.repeat(64);
const item = routePath('get_items_id_art_kind', {id: 'title', kind: 'backdrop'});

test('ambient source preserves selected source identity and leaves foreground URL untouched', () => {
  const foreground = item + '?v=' + version + '&w=800';
  const ambient = ambientArtworkSource(foreground, identity)!;
  assert.equal(ambient.url, item + '?size=ambient-v1&v=' + version);
  assert.equal(foreground, item + '?v=' + version + '&w=800');
  assert.equal(ambient.version, version);
  assert.equal(ambient.maxEdge, 64);
  assert.equal(ambient.variant, 'ambient-v1');
  assert(Object.isFrozen(ambient));
  assert.deepEqual(ambientArtworkSource(ambient.url, identity), ambient);
});

test('metadata subject and source version survive canonical query ordering', () => {
  const path = routePath('get_metadata_kind_id_art_role', {kind: 'item', id: 'with space', role: 'portrait'});
  const a = ambientArtworkSource(path + '?size=thumbnail&w=400&subject=tmdb%3A123&v=' + version, identity)!;
  const b = ambientArtworkSource(path + '?v=' + version + '&subject=tmdb:123', identity)!;
  assert.deepEqual(a, b);
  assert.equal(new URLSearchParams(a.url.split('?')[1]).get('subject'), 'tmdb:123');
  assert(ambientArtworkSource(path + '?v=' + version + '&subject=' + encodeURIComponent('é'.repeat(80)), identity));
  assert.equal(ambientArtworkSource(path + '?v=' + version + '&subject=' + encodeURIComponent('é'.repeat(81)), identity), null, 'the server limits UTF-8 subject bytes');
});

test('cache identity is separated by source revision, variant and current authority', () => {
  const a = ambientArtworkSource(item + '?v=' + version, identity)!;
  for (const next of [{...identity, serverId: 'other'}, {...identity, viewerId: 'other'}, {...identity, authorityKey: 'session:2'}]) {
    assert.notEqual(ambientArtworkSource(item + '?v=' + version, next)!.cacheKey, a.cacheKey);
  }
  assert.notEqual(ambientArtworkSource(item + '?v=' + 'b'.repeat(64), identity)!.cacheKey, a.cacheKey);
  assert.equal(ambientArtworkSource(item, identity), null, 'a mutable URL cannot identify an indefinitely cached source');
});

test('ambient source refuses external, malformed, non-artwork and candidate identities', () => {
  for (const value of [null, undefined, '', '//foreign/image', 'https://foreign/image', '/assets/poster.png', item + '#fragment', item + '?v=bad', item + '?v=' + version + '&v=' + version, item + '?candidate=' + version, item + '?unknown=1', item + '?subject=' + 'x'.repeat(161), item + '?subject=%00', item + '?w=1?size=thumbnail', item.replace('title', '%2fsecret'), item.replace('title', '%2e%2e'), item.replace('title', '%zz'), item + ' ', 'x'.repeat(4097)]) {
    assert.equal(ambientArtworkSource(value, identity), null, String(value));
  }
  assert.equal(ambientArtworkSource(item + '?v=' + version, {...identity, authorityKey: ''}), null);
});

test('foreground width policy retains all existing buckets and never selects ambient', () => {
  assert.deepEqual(artworkSizeBuckets, [400, 800, 1920, 2560]);
  for (const [pixels, expected] of [[1, 400], [32, 400], [64, 400], [400, 400], [500, 800], [1920, 1920], [1921, 2560], [5000, 2560]]) {
    assert.equal(artworkWidthBucket(pixels, {tolerance: 1}), expected);
  }
});
