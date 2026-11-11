import test from 'node:test';
import assert from 'node:assert/strict';
import {foregroundArtwork} from '../src/artwork/media-sources.ts';

test('a picture address from the rebuilt server is fetched at a size that covers the card, in steps so it is reused', () => {
  const identity = {serverId: 'srv', viewerId: 'prf', authorityKey: 'key'};
  const poster = foregroundArtwork('/v1/images/1349d1964a87981e', 380, 2 / 3, identity);
  assert.equal(poster?.url, '/v1/images/1349d1964a87981e?w=480&h=720');
  assert.equal(foregroundArtwork('/v1/images/1349d1964a87981e', 400, 2 / 3, identity)?.url, poster?.url, 'a slightly larger card asks for the same picture');
  // The server names its pictures; an address of any other shape is not fetched.
  assert.equal(foregroundArtwork('/v1/images/../admin', 380, 1, identity), undefined);
  assert.equal(foregroundArtwork('https://elsewhere.test/v1/images/abc', 380, 1, identity), undefined);
});
