import test from 'node:test';
import assert from 'node:assert/strict';
import {routePath} from '../../contracts/src/routes.ts';
import {ambientArtworkSource} from '../src/artwork/ambient.ts';
import {ARTWORK_SMALL_EDGE, artworkDescriptor, artworkRenditionFor, artworkSlotFor} from '../src/artwork/renditions.ts';

const source = 'a'.repeat(64);
const smallToken = '1'.repeat(16), largeToken = '2'.repeat(16);
const item = routePath('get_items_id_art_kind', {id: 'title', kind: 'poster'});
// A 2:3 poster: small 533x800, large 1280x1920.
const poster = item + '?v=' + source + '&sd=533x800.91234.' + smallToken + '&ld=1280x1920.402000.' + largeToken;

test('the page descriptor is parsed only when both slots are complete', () => {
  assert.deepEqual(artworkDescriptor(poster), {
    small: {width: 533, height: 800, bytes: 91234, token: smallToken},
    large: {width: 1280, height: 1920, bytes: 402000, token: largeToken},
  });
  for (const bad of [item, item + '?v=' + source, item + '?sd=533x800.1.' + smallToken, poster.replace(smallToken, 'XYZ'), poster.replace('533x800', '0x800')]) {
    assert.equal(artworkDescriptor(bad), undefined, bad);
  }
});

test('small paints a card its actual pixels cover; anything larger takes large', () => {
  // Phone poster 130x195 pt at 3x needs 585 px tall: small.
  assert.equal(artworkSlotFor(artworkDescriptor(poster), {width: 130, height: 195, dpr: 3}), 'small');
  // Desktop medium grid 170x255 at 2x: small.
  assert.equal(artworkSlotFor(artworkDescriptor(poster), {width: 170, height: 255, dpr: 2}), 'small');
  // Web Large card at 3x: 400 wide needs 1800 tall: large.
  assert.equal(artworkSlotFor(artworkDescriptor(poster), {width: 400, height: 600, dpr: 3}), 'large');
  // Within the 10% tolerance (880 tall) small is kept; past it, large.
  assert.equal(artworkSlotFor(artworkDescriptor(poster), {width: 293, height: 440, dpr: 2}), 'small');
  assert.equal(artworkSlotFor(artworkDescriptor(poster), {width: 300, height: 450, dpr: 2}), 'large');
  // A crop needs more pixels: a poster covering a 16:9 hero box.
  assert.equal(artworkSlotFor(artworkDescriptor(poster), {width: 640, height: 360, dpr: 1}), 'large');
  assert.equal(artworkSlotFor(artworkDescriptor(poster), {width: 640, height: 360, dpr: 1, fit: 'contain'}), 'small');
  // Authored zoom counts.
  assert.equal(artworkSlotFor(artworkDescriptor(poster), {width: 170, height: 255, dpr: 2, zoom: 2}), 'large');
});

test('a source smaller than the small edge is chosen by its actual size, not the nominal one', () => {
  const tiny = item + '?v=' + source + '&sd=200x300.9000.' + smallToken + '&ld=200x300.12000.' + largeToken;
  assert.equal(artworkSlotFor(artworkDescriptor(tiny), {width: 100, height: 150, dpr: 2}), 'small');
  assert.equal(artworkSlotFor(artworkDescriptor(tiny), {width: 200, height: 300, dpr: 2}), 'large');
});

test('without a descriptor the nominal small edge decides', () => {
  assert.equal(artworkSlotFor(undefined, {width: 400, height: 400, dpr: 2}), 'small');
  assert.equal(artworkSlotFor(undefined, {width: ARTWORK_SMALL_EDGE, height: 10, dpr: 1.2}), 'large');
  assert.equal(artworkSlotFor(undefined, {width: 0, height: 0, dpr: 3}), 'small');
});

test('the requested URL names one slot and its token, and drops the descriptor', () => {
  const small = artworkRenditionFor(poster, {width: 130, height: 195, dpr: 3});
  assert.deepEqual(small, {url: item + '?r=' + smallToken + '&size=small&v=' + source, slot: 'small', width: 533, height: 800, bytes: 91234});
  const large = artworkRenditionFor(poster + '&w=400', {width: 400, height: 600, dpr: 3});
  assert.equal(large.url, item + '?r=' + largeToken + '&size=large&v=' + source);
  // One picture at one slot has one URL, whatever box asked for it.
  assert.equal(artworkRenditionFor(poster, {width: 100, height: 150, dpr: 2}).url, small.url);
  // A subject survives; a bare route gets a slot but no token.
  const metadata = routePath('get_metadata_kind_id_art_role', {kind: 'item', id: 'title', role: 'portrait'});
  assert.equal(artworkRenditionFor(metadata + '?v=' + source + '&sd=533x800.1.' + smallToken + '&ld=533x800.1.' + largeToken + '&subject=tmdb%3A1', {width: 80, height: 80, dpr: 2}).url, metadata + '?r=' + smallToken + '&size=small&subject=tmdb%3A1&v=' + source);
  assert.deepEqual(artworkRenditionFor(item, {width: 2000, height: 1000, dpr: 1}), {url: item + '?size=large', slot: 'large'});
  // Programme images describe their slots but carry no source version: no token.
  const programme = '/v1/guide/programme-images/g/p?sd=533x300.1.' + smallToken + '&ld=1920x1080.2.' + largeToken;
  assert.equal(artworkRenditionFor(programme, {width: 110, height: 62, dpr: 2}).url, '/v1/guide/programme-images/g/p?size=small');
});

test('ambient derivation accepts a described URL and keeps only the source', () => {
  const identity = {serverId: 'server', viewerId: 'profile', authorityKey: 'session:1'};
  assert.equal(ambientArtworkSource(poster, identity)!.url, item + '?size=ambient-v1&v=' + source);
  assert.equal(ambientArtworkSource(artworkRenditionFor(poster, {width: 1, height: 1, dpr: 1}).url, identity)!.url, item + '?size=ambient-v1&v=' + source);
});
