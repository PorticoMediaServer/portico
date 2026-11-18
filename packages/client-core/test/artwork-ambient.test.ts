import test from 'node:test';
import assert from 'node:assert/strict';
import {ambientArtworkSource, ambientArtworkSourceV2} from '../src/artwork/ambient.ts';
import {artworkWidthBucket, artworkSizeBuckets} from '../src/artwork/buckets.ts';

const identity = {serverId: 'server', viewerId: 'profile', authorityKey: 'session:1'};
const version = 'a'.repeat(64);
const item = '/v1/images/' + version;
const retired = '/v1/items/title/art/backdrop?v=' + version;

test('ambient source refuses external, malformed, non-artwork and candidate identities', () => {
  for (const value of [null, undefined, '', retired, '/v1/metadata/item/title/art/portrait?v=' + version, '//foreign/image', 'https://foreign/image', '/assets/poster.png', item + '#fragment', item + '?v=bad', item + '?v=' + version + '&v=' + version, item + '?candidate=' + version, item + '?unknown=1', item + '?subject=' + 'x'.repeat(161), item + '?subject=%00', item + '?w=1?size=thumbnail', item.replace(version, '%2fsecret'), item.replace(version, '%2e%2e'), item.replace(version, '%zz'), item + ' ', 'x'.repeat(4097)]) {
    assert.equal(ambientArtworkSource(value, identity), null, String(value));
  }
  assert.equal(ambientArtworkSource(item, {...identity, authorityKey: ''}), null);
});

test('foreground width policy retains all existing buckets and never selects ambient', () => {
  assert.deepEqual(artworkSizeBuckets, [400, 800, 1920, 2560]);
  for (const [pixels, expected] of [[1, 400], [32, 400], [64, 400], [400, 400], [500, 800], [1920, 1920], [1921, 2560], [5000, 2560]]) {
    assert.equal(artworkWidthBucket(pixels, {tolerance: 1}), expected);
  }
});


test('hash artwork keeps the v1 default and separates native v2 cache identity', () => {
  // A content-addressed source needs no separate v query.
  const hash = 'f3b328908bf58db21b2a3acd5569e80a548de5cff8755a6fb1c7a984600ebc69';
  const url = '/v1/images/' + hash;
  const ambient = ambientArtworkSource(url, identity)!;
  assert.equal(ambient.url, url + '?size=ambient-v1');
  assert.equal(ambient.version, hash);
  assert.equal(ambient.variant, 'ambient-v1');
  assert.equal(ambient.maxEdge, 64);
  const v2 = ambientArtworkSourceV2(url, identity)!;
  assert.equal(v2.url, url + '?size=ambient-v2');
  assert.equal(v2.version, hash);
  assert.notEqual(v2.cacheKey, ambient.cacheKey);
  for (const suffix of ['?w=800&h=450', '?size=small', '?size=large', '?size=ambient-v1']) {
    assert.deepEqual(ambientArtworkSource(url + suffix, identity), ambient);
  }
  assert(Object.isFrozen(ambient));
  for (const next of [{...identity, serverId: 'other'}, {...identity, viewerId: 'other'}, {...identity, authorityKey: 'session:2'}]) {
    assert.notEqual(ambientArtworkSource(url, next)!.cacheKey, ambient.cacheKey);
  }
  assert.notEqual(ambientArtworkSource('/v1/images/' + 'b'.repeat(64), identity)!.cacheKey, ambient.cacheKey);
  for (const bad of ['/v1/images/mutable', '/v1/images/' + 'A'.repeat(64), url + '/extra', url + '?h=1&h=2', url + '?v=' + version, url + '?subject=other', url + '?candidate=' + version]) {
    assert.equal(ambientArtworkSource(bad, identity), null, bad);
  }
});
