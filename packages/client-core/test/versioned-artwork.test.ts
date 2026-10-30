import test from 'node:test';
import assert from 'node:assert/strict';
import {isVersionedArtwork} from '../src/artwork/versioned.ts';
import {validReadingCover} from '../src/reading-cover.ts';

const source = 'a'.repeat(64), slots = '&sd=533x800.55544.0123456789abcdef&ld=600x900.68221.fedcba9876543210';

test('a versioned artwork route may carry both slot descriptions and nothing else', () => {
  const prefix = '/v1/items/book/art/cover?v=';
  assert.equal(isVersionedArtwork(prefix + source, prefix), true);
  assert.equal(isVersionedArtwork(prefix + source + slots, prefix), true, 'descriptor URLs since P2-17 (artworkref.go)');
  assert.equal(isVersionedArtwork(prefix + source + '&sd=533x800.55544.0123456789abcdef', prefix), false, 'one slot alone is incomplete');
  assert.equal(isVersionedArtwork(prefix + source + '&ld=600x900.68221.fedcba9876543210&sd=533x800.55544.0123456789abcdef', prefix), false, 'the order is fixed');
  assert.equal(isVersionedArtwork(prefix + source + slots + '&next=elsewhere', prefix), false, 'no other query may follow');
  assert.equal(isVersionedArtwork(prefix + source + '&sd=x&ld=y', prefix), false, 'malformed slots');
  assert.equal(isVersionedArtwork(prefix + 'b'.repeat(63) + slots, prefix), false, 'the source is a full digest');
  assert.equal(isVersionedArtwork('/v1/items/other/art/cover?v=' + source, prefix), false, 'another item');
});

test('a Reading cover with slot descriptions is valid for its own item (library pages no longer fail)', () => {
  const url = `/v1/items/book/art/cover?v=${source}${slots}`;
  assert.equal(validReadingCover('book', url), true);
  assert.equal(validReadingCover('book', undefined), true);
  assert.equal(validReadingCover('other', url), false);
  assert.equal(validReadingCover('book', 42), false);
});
