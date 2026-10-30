import test from 'node:test';
import assert from 'node:assert/strict';
import {decode} from '../src/runtime.ts';
import {schemas} from '../src/schemas.ts';

const cover = {album: {id: 'album', name: 'Album'}, posterUrl: '/art/album'};
const card = {
  id: 'opaque-mix',
  nameSource: {kind: 'style', tag: 'classical', model: 'model', version: 'v1'},
  tint: '#28587a', covers: [cover, cover, cover],
  artists: [{id: 'artist', name: 'Artist'}], more: false, songCount: 3,
};
const response = (mixes: unknown[]) => ({
  scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile'},
  data: {mixes},
});
const read = (mixes: unknown[]) => decode('GetMusicMixesResponse', response(mixes));

test('mix cards expose one exact name source and real repeated cover samples', () => {
  assert.doesNotThrow(() => read([card]));
  assert.doesNotThrow(() => read([{...card, nameSource: {kind: 'artist', artistId: 'artist'}}]));
  assert.doesNotThrow(() => read([]));
  for (const nameSource of [
    {}, {kind: 'style', tag: 'classical', model: 'model'},
    {...card.nameSource, version: null}, {...card.nameSource, tag: ''}, {kind: 'artist'},
    {kind: 'artist', artistId: ''},
  ]) assert.throws(() => read([{...card, nameSource}]));
  // Responses are open: members foreign to the chosen variant are dropped, and an unknown kind
  // decodes to the typed unknown variant rather than a throw.
  const nameSourceOf = (nameSource: unknown) => ((read([{...card, nameSource}]) as any).data.mixes[0].nameSource);
  assert.deepEqual({...nameSourceOf({...card.nameSource, artistId: 'artist'})}, card.nameSource);
  assert.deepEqual({...nameSourceOf({kind: 'artist', artistId: 'artist', tag: 'classical'})}, {kind: 'artist', artistId: 'artist'});
  assert.deepEqual({...nameSourceOf({kind: 'other', x: 1})}, {kind: 'other', unknownVariant: true});
});

test('mix cards bound their public summaries and never accept song keys or cluster internals', () => {
  for (const patch of [
    {id: ''}, {tint: 'blue'}, {covers: [cover, cover]},
    {covers: [cover, cover, cover, cover]}, {artists: [card.artists[0], card.artists[0], card.artists[0]]},
    {songCount: 0}, {songCount: 201}, {songCount: 1.5},
  ]) assert.throws(() => read([{...card, ...patch}]));
  // Private members a newer or hostile server might add never reach the decoded card.
  const decoded = (read([{...card, keys: ['private-song'], tracks: [], cluster: 'private', name: 'Invented display name'}]) as any).data.mixes[0];
  for (const key of ['keys', 'tracks', 'cluster', 'name']) assert.equal(key in decoded, false, key);
  const shape = schemas.GetMusicMixesResponse.properties.data.properties.mixes.items;
  assert.equal(shape.additionalProperties, false);
  for (const key of ['keys', 'tracks', 'cluster', 'name']) assert.equal(key in shape.properties, false);
});

test('saving a mix accepts only an operation identity and the localized playlist name', () => {
  const input = {operationId: 'operation', name: 'Classical mix'};
  assert.doesNotThrow(() => decode('PostMusicMixesIdPlaylistRequest', input));
  assert.doesNotThrow(() => decode('PostMusicMixesIdPlaylistRequest', {...input, name: '🎵'.repeat(200)}));
  for (const invalid of [
    {}, {name: input.name}, {operationId: input.operationId},
    {...input, operationId: ''}, {...input, name: ''}, {...input, name: null},
    {...input, name: 1}, {...input, name: '🎵'.repeat(201)},
    {...input, keys: ['private-song']}, {...input, model: '/private/model'},
    {...input, path: '/private/file'}, {...input, expectedRevision: 1},
  ]) assert.throws(() => decode('PostMusicMixesIdPlaylistRequest', invalid));
});
