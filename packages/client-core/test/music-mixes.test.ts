import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../i18n/src/index.ts';
import {musicMixDisplayName} from '../src/music-mixes.ts';

test('mix row and playlist names use genuine source facts and catalogue labels', () => {
  const t = defaultI18n.t;
  assert.equal(musicMixDisplayName({kind: 'style', tag: 'new age', model: 'musicnn-mtt', version: 'checked-model'}, [], t), 'New age mix');
  assert.equal(musicMixDisplayName({kind: 'artist', artistId: 'artist'}, [{id: 'artist', name: 'Fleetwood Mac'}], t), 'Fleetwood Mac mix');
  assert.equal(musicMixDisplayName({kind: 'artist', artistId: 'missing'}, [{id: 'artist', name: 'Fleetwood Mac'}], t), undefined);
  for (const tag of ['guitar', 'female', 'loud', 'fast', 'Mellow', 'classic rock', '__proto__']) {
    assert.equal(musicMixDisplayName({kind: 'style', tag, model: 'musicnn-mtt', version: 'checked-model'}, [], t), undefined, tag);
  }
  assert.equal(musicMixDisplayName({kind: 'style', tag: 'rock', model: 'other', version: 'v'}, [], t), undefined);
  assert.equal(musicMixDisplayName({kind: 'style', tag: 'rock', model: 'musicnn-mtt', version: 'v', artistId: 'artist'}, [], t), undefined);
  assert.equal(musicMixDisplayName({kind: 'artist', artistId: 'artist', tag: 'rock'}, [{id: 'artist', name: 'Artist'}], t), undefined);
  assert.equal(musicMixDisplayName(null, [], t), undefined);
});
