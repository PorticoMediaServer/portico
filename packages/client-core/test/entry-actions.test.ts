import test from 'node:test';
import assert from 'node:assert/strict';
import {entryActions, entryActionList} from '../src/presentation/index.ts';
import {enUS} from '../../i18n/src/index.ts';
import {isIconId, componentSpec} from '../../design/src/index.ts';

const everything = {playable: true, progressSeconds: 120, queue: true, group: true, watchlisted: true, favorite: false, watched: false, reaction: 'like' as const, addToPlaylist: true, addToCollection: true, download: 'media' as const, details: true, editMetadata: true, refreshMetadata: true, delete: true};

test('entry actions follow the shared group order, destructive last (CON-24, menuSpec)', () => {
  const groups = entryActions(everything);
  assert.deepEqual(groups.map(g => g.id), componentSpec.menu.entryGroups);
  const list = entryActionList(groups);
  assert.equal(list.at(-1)!.id, 'delete');
  assert.ok(list.at(-1)!.destructive);
  assert.equal(list.filter(a => a.destructive).length, 1);
  assert.deepEqual(list.slice(0, 4).map(a => a.id), ['resume', 'playFromStart', 'playNext', 'addToQueue']);
});

test('labels are catalogue IDs and glyphs are registry IDs with the right meanings (CON-16)', () => {
  for (const a of entryActionList(entryActions(everything))) {
    assert.ok(a.label in enUS, a.label);
    assert.ok(isIconId(a.icon), a.icon);
  }
  const byId = Object.fromEntries(entryActionList(entryActions({...everything, watched: false})).map(a => [a.id, a]));
  assert.equal(byId.playFromStart!.icon, 'restart', 'never refresh for play from start');
  assert.equal(byId.watched!.icon, 'watched', 'never a check for mark watched');
  assert.equal(byId.addToPlaylist!.icon, 'listPlus', 'never the queue glyph for playlists');
  assert.ok(enUS[byId.addToPlaylist!.label].endsWith('…'), 'further steps carry an ellipsis (CON-22)');
  assert.ok(!enUS[byId.refreshMetadata!.label].endsWith('…'));
});

test('nothing is invented: absent capabilities give no action and empty groups vanish', () => {
  assert.deepEqual(entryActions({}), []);
  const groups = entryActions({playable: true, details: true});
  assert.deepEqual(groups.map(g => g.id), ['play', 'open']);
  assert.deepEqual(entryActionList(groups).map(a => a.id), ['play', 'details']);
  assert.equal(entryActionList(entryActions({download: 'container'}))[0]!.label, 'entry.downloadAll');
});

test('X-12: Remove from Continue Watching sits after Mark watched, only when offered', () => {
  const ids = (c: Parameters<typeof entryActions>[0]) => entryActionList(entryActions(c)).map(a => a.id);
  assert.deepEqual(ids({playable: true, watched: false, continueWatching: true}).slice(-2), ['watched', 'removeFromContinueWatching']);
  assert.equal(ids({playable: true, watched: false}).includes('removeFromContinueWatching'), false);
  const action = entryActionList(entryActions({continueWatching: true})).find(a => a.id === 'removeFromContinueWatching')!;
  assert.equal(action.label, 'entry.removeFromContinueWatching');
  assert.equal(action.destructive, undefined, 'undoable, so not shown as destructive');
});

test('Refresh metadata is offered only where the server can do it: one playable title, never a whole show, album or book', async () => {
  const {canRefreshMetadata} = await import('../src/presentation/index.ts');
  for (const kind of ['movie', 'episode', 'song', 'audiobook_file', 'extra']) assert.equal(canRefreshMetadata(kind), true, kind);
  for (const kind of ['show', 'season', 'album', 'artist', 'book', 'collection', 'author', undefined]) assert.equal(canRefreshMetadata(kind), false, String(kind));
});
