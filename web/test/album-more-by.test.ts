import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {defaultI18n} from '@i18n';

// listen-2: an album page ends with More by its artist: the artist's other albums, or a note when this is the only one.
const album = readFileSync(new URL('../src/screens/detail/Album.tsx', import.meta.url), 'utf8');

test('the album page reads its artist\'s releases and leaves this album out', () => {
  assert.match(album, /artists\.select\(\{libraryId, view: 'artist', entityId: artistId\}\)/);
  assert.match(album, /\.filter\(entry => entry\.kind === 'album' && entry\.id !== albumId\)/);
  assert.match(album, /<TitleRow heading=\{t\('music\.moreBy', \{name: album\.artist\.name\}\)\} entries=\{artistMore\.albums\}/);
});

test('the only-album note names the album and the artist, with Go to the artist', () => {
  assert.equal(defaultI18n.t('music.onlyAlbumBy', {title: 'After Hours', name: 'The Weeknd'}), 'After Hours is the only album by The Weeknd on your server.');
  assert.equal(defaultI18n.t('music.goToArtist', {name: 'The Weeknd'}), 'Go to The Weeknd');
  assert.equal(defaultI18n.t('music.moreBy', {name: 'The Weeknd'}), 'More by The Weeknd');
});
