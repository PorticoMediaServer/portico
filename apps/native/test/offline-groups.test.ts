/**
 * The Downloads list is built from what is kept on the device alone: an episode sits under its
 * show in season and episode order, a song under its album in disc and track order, and a film
 * stands by itself. No server is involved.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import './react-native-stub.mjs';
import type * as OfflineModule from '../src-next/platform/offline-downloads.ts';

const title = (over: Record<string, unknown>) => ({kind: 'movie', title: '', year: 0, durationSeconds: 0, parentId: '', parentTitle: '', groupId: '', groupTitle: '', groupNumber: null, number: null, artist: '', onServer: true, ...over});
const entry = (id: string, addedAt: number, info: ReturnType<typeof title>, artworkUrl = '') => ({
  id, itemId: 'item-' + id, title: String(info.title), subtitle: '', quality: 'original', artworkUrl, state: 'ready', error: '', waiting: '', held: false,
  bytes: 1, bytesDone: 1, addedAt, kind: String(info.kind), info, onServer: true, changedOnServer: false,
}) as unknown as OfflineModule.OfflineEntry;

test('downloads are grouped under their show, album or book and ordered within it', async () => {
  const {groupOfflineEntries} = await import('../src-next/platform/offline-downloads.ts');
  const show = {kind: 'episode', parentId: 'show-1', parentTitle: 'The Show'};
  const groups = groupOfflineEntries([
    entry('e3', 30, title({...show, title: 'Third', groupNumber: 2, number: 1})),
    entry('film', 20, title({kind: 'movie', title: 'Film', year: 1999})),
    entry('e2', 10, title({...show, title: 'Second', groupNumber: 1, number: 2}), 'file:///poster'),
    entry('e1', 5, title({...show, title: 'First', groupNumber: 1, number: 1})),
    entry('s2', 4, title({kind: 'song', title: 'B', parentId: 'album-1', parentTitle: 'Album', groupNumber: 1, number: 2})),
    entry('s1', 3, title({kind: 'song', title: 'A', parentId: 'album-1', parentTitle: 'Album', groupNumber: 1, number: 1})),
  ]);
  // Newest activity first: the show (an episode added at 30), the film (20), the album (4).
  assert.deepEqual(groups.map(g => g.title), ['The Show', '', 'Album']);
  assert.deepEqual(groups[0].entries.map(e => e.id), ['e1', 'e2', 'e3'], 'season and episode order, not the order they arrived in');
  assert.equal(groups[0].artworkUrl, 'file:///poster', 'a group shows artwork kept with one of its members');
  assert.deepEqual(groups[1].entries.map(e => e.id), ['film']);
  assert.deepEqual(groups[2].entries.map(e => e.id), ['s1', 's2']);
});

test('a download with no stored title facts stands by itself', async () => {
  const {groupOfflineEntries} = await import('../src-next/platform/offline-downloads.ts');
  const bare = {...entry('x', 1, title({})), info: undefined} as unknown as OfflineModule.OfflineEntry;
  const groups = groupOfflineEntries([bare, entry('y', 2, title({kind: 'episode', title: 'Loose'}))]);
  assert.equal(groups.length, 2, 'an episode whose show is unknown is not put into a nameless group');
  assert.ok(groups.every(g => g.title === '' && g.entries.length === 1));
});
