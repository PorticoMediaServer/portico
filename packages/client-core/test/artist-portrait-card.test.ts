import test from 'node:test';
import assert from 'node:assert/strict';
import {entryFromItem, type Item} from '../src/items.ts';

// 13 Nov art-library walk: every artist circle in a library grid was empty, though the server sends each artist's portrait.
const artist = (images: Record<string, string>) => ({id: 'itm_artist', kind: 'artist', title: 'The Astronomers of the Pale Blue Morning', libraryId: 'lib_music',
  addedAt: '2026-11-13T00:00:00Z', images, state: {watched: false, inList: false, favorite: false, notInterested: false, playCount: 0}}) as Item;

test("an artist's card picture is their portrait; a poster, cover or thumb still comes first", () => {
  assert.equal(entryFromItem(artist({portrait: '/v1/images/portrait', backdrop: '/v1/images/backdrop', logo: '/v1/images/logo'}))?.posterUrl, '/v1/images/portrait');
  assert.equal(entryFromItem(artist({poster: '/v1/images/poster', portrait: '/v1/images/portrait'}))?.posterUrl, '/v1/images/poster');
  assert.equal(entryFromItem(artist({backdrop: '/v1/images/backdrop'}))?.posterUrl, undefined, 'no portrait: the card draws its own fallback');
});
