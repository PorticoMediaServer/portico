import test from 'node:test';
import assert from 'node:assert/strict';
import type {ItemResponse} from '../../packages/client-core/src/media-item.ts';
import {castCoverPath, castCoverTarget} from '../src/screens/watch/cast-cover.ts';

const item = (fields: Partial<ItemResponse>): ItemResponse => ({
  addedAt: null, available: true, durationMs: 180_000, id: 'song-1', kind: 'song',
  libraryId: 'music', missing: false, progressMs: 0, title: 'DNA.', ...fields,
} as ItemResponse);

const song = (albumId: string): NonNullable<ItemResponse['song']> => ({
  albumArtist: 'Kendrick Lamar', albumId, albumTitle: 'DAMN.', artist: 'Kendrick Lamar',
  providerMatchStatus: 'matched', providerReleaseStatus: 'matched',
});

const bookFile = (bookId: string): NonNullable<ItemResponse['bookFile']> => ({
  bookId, bookTitle: 'Project Hail Mary', sourceBoundary: 'file',
});

const relatedAlbum = (fields: Partial<ItemResponse> = {}): ItemResponse => item({
  id: 'album-1', kind: 'album', title: 'DAMN.', posterUrl: '/v1/items/album-1/art/poster', ...fields,
});

const relatedBook = (fields: Partial<ItemResponse> = {}): ItemResponse => item({
  id: 'book-1', kind: 'book', title: 'Project Hail Mary', libraryId: 'books', posterUrl: '/v1/items/book-1/art/poster', ...fields,
});

const albumTarget = Object.freeze({id: 'album-1', kind: 'album' as const});
const bookTarget = Object.freeze({id: 'book-1', kind: 'book' as const});

test('a song or track with no image of its own targets its album', () => {
  assert.deepEqual(castCoverTarget(item({song: song('album-1')})), {id: 'album-1', kind: 'album'});
  assert.deepEqual(castCoverTarget(item({kind: 'track', song: song('album-1')})), {id: 'album-1', kind: 'album'});
});

test('an audiobook file or chapter targets its book container', () => {
  for (const kind of ['audiobook_file', 'chapter']) {
    assert.deepEqual(castCoverTarget(item({id: 'file-1', kind, libraryId: 'books', bookFile: bookFile('book-1')})), {id: 'book-1', kind: 'book'}, kind);
  }
});

test('a non-audio kind never reads a malformed song or bookFile relation', () => {
  for (const kind of ['movie', 'episode', 'show', 'season', 'album', 'book', 'audiobook', 'collection']) {
    assert.equal(castCoverTarget(item({id: 'other-1', kind, song: song('album-1')})), undefined, `${kind} song`);
    assert.equal(castCoverTarget(item({id: 'other-1', kind, bookFile: bookFile('book-1')})), undefined, `${kind} book`);
  }
});

test('each related kind is read only for its own item kind', () => {
  assert.equal(castCoverTarget(item({id: 'file-1', kind: 'audiobook_file', libraryId: 'books', song: song('album-1')})), undefined);
  assert.equal(castCoverTarget(item({bookFile: bookFile('book-1')})), undefined);
});

test('an item with its own poster or backdrop is not re-read', () => {
  assert.equal(castCoverTarget(item({song: song('album-1'), posterUrl: '/v1/items/song-1/art/poster'})), undefined);
  assert.equal(castCoverTarget(item({song: song('album-1'), backdropUrl: '/v1/items/song-1/art/backdrop'})), undefined);
  assert.equal(castCoverTarget(item({id: 'file-1', kind: 'audiobook_file', libraryId: 'books', bookFile: bookFile('book-1'), posterUrl: '/v1/items/file-1/art/poster'})), undefined);
});

test('an item without a related id has no target', () => {
  assert.equal(castCoverTarget(item({song: song('')})), undefined);
  assert.equal(castCoverTarget(item({song: song('album 1')})), undefined);
  assert.equal(castCoverTarget(item({id: 'file-1', kind: 'audiobook_file', libraryId: 'books', bookFile: bookFile('')})), undefined);
});

test('the related id is complete and bounded', () => {
  assert(castCoverTarget(item({song: song('a'.repeat(128))})));
  assert.equal(castCoverTarget(item({song: song('a'.repeat(129))})), undefined);
  for (const id of ['.', '..', '/album-1', 'album-1/art', 'album\\1', 'album\n', 'album\r', 'album\r\n', 'album\u2028', 'album\u2029', 'album\t', 'album\u000b', 'album\u000c', 'album ', 'album\u00a0', 'album\u1680', 'album\u0000', 'album\u007f', 'album\u0080']) {
    assert.equal(castCoverTarget(item({song: song(id)})), undefined, JSON.stringify(id));
  }
});

test('a related cover is selected only for the exact expected entity', () => {
  assert.equal(castCoverPath(albumTarget, relatedAlbum()), '/v1/items/album-1/art/poster');
  assert.equal(castCoverPath(bookTarget, relatedBook()), '/v1/items/book-1/art/poster');
  assert.equal(castCoverPath(albumTarget, relatedAlbum({id: 'album-2'})), undefined);
  assert.equal(castCoverPath(albumTarget, relatedAlbum({kind: 'audiobook_file'})), undefined);
  assert.equal(castCoverPath(albumTarget, relatedAlbum({kind: 'audiobook'})), undefined);
  assert.equal(castCoverPath(albumTarget, relatedBook()), undefined);
});

test('the related read must prove missing === false', () => {
  assert.equal(castCoverPath(albumTarget, {...relatedAlbum(), missing: undefined} as unknown as ItemResponse), undefined);
  assert.equal(castCoverPath(albumTarget, {...relatedAlbum(), missing: 'false'} as unknown as ItemResponse), undefined);
  assert.equal(castCoverPath(albumTarget, relatedAlbum({missing: true})), undefined);
});

test('a related cover never falls back to its backdrop or to another image field', () => {
  assert.equal(castCoverPath(albumTarget, relatedAlbum({posterUrl: undefined, backdropUrl: '/v1/items/album-1/art/backdrop'})), undefined);
  assert.equal(castCoverPath(albumTarget, relatedAlbum({posterUrl: undefined, stillUrl: '/v1/items/album-1/art/still'})), undefined);
});

test('only a complete same-origin printable catalogue path is accepted', () => {
  assert.equal(castCoverPath(albumTarget, relatedAlbum({posterUrl: '/' + 'a'.repeat(500)})), '/' + 'a'.repeat(500));
  for (const poster of [
    'https://server.example/v1/items/album-1/art/poster',
    '//server.example/v1/items/album-1/art/poster',
    'v1/items/album-1/art/poster',
    '/v1/items/album 1/art/poster',
    '/v1/items/album\\1/art/poster',
    '/v1/items/album-1/art/poster\n',
    '/v1/items/album-1/art/poster\r',
    '/v1/items/album-1/art/poster\r\n',
    '/v1/items/album-1/art/poster\u2028',
    '/v1/items/album-1/art/poster\u2029',
    '/v1/items/album-1/art/poster\u0000',
    '/v1/items/album-1/art/poster\u007f',
    '/v1/items/album-1/art/poster\u0080',
    '/v1/items/album-1/art/poster\t',
    '/v1/items/album-1/art/poster\u000b',
    '/v1/items/album-1/art/poster\u000c',
    '/v1/items/album-1/art/poster\u00a0',
    '',
    '/' + 'a'.repeat(501),
  ]) assert.equal(castCoverPath(albumTarget, relatedAlbum({posterUrl: poster})), undefined, JSON.stringify(poster));
  assert.equal(castCoverPath(albumTarget, relatedAlbum({posterUrl: '/v1/items/album-1/art/poster?v=2'})), '/v1/items/album-1/art/poster?v=2');
});

test('no target or no related read selects no path', () => {
  assert.equal(castCoverPath(undefined, relatedAlbum()), undefined);
  assert.equal(castCoverPath(albumTarget, undefined), undefined);
});
