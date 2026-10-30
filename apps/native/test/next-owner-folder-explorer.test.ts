import test from 'node:test';
import assert from 'node:assert/strict';
import type {FilesystemEntry, FilesystemPage} from '../../../packages/client-core/src/administration';
import {appendExplorerPage, explorerEntries, toggleExplorerSelection} from '../src-next/screens/settings-live/owner-libraries/folder-explorer-model.ts';

const entry = (path: string, kind: FilesystemEntry['kind'] = 'directory'): FilesystemEntry => ({path, name: path.split('/').at(-1)!, kind, readable: true, symlink: false, bytes: 0, hidden: false});
const page = (entries: readonly FilesystemEntry[], nextCursor = ''): FilesystemPage => ({path: '/media', parent: '/', separator: '/', platform: 'linux', writable: false, writableChecked: false, suggestions: [], truncated: false, roots: [], entries, nextCursor});

test('library folder selection excludes files while restore admits files and directories', () => {
  const listing = page([entry('/media/Movies'), entry('/media/copy.db', 'file'), entry('/media/unreadable', 'unknown')]);
  assert.deepEqual(explorerEntries(listing, 'folders').map(row => row.path), ['/media/Movies']);
  assert.deepEqual(explorerEntries(listing, 'file-or-folder').map(row => row.path), ['/media/Movies', '/media/copy.db']);
  assert.deepEqual(explorerEntries(undefined, 'folders'), []);
});

test('selection survives paging past a folder whose host is no longer mounted', () => {
  const original = page(Array.from({length: 200}, (_, index) => entry(`/media/${index}`)), 'page-2');
  const selected = toggleExplorerSelection([], original.entries[0]!, true);
  const next = appendExplorerPage(original, page(Array.from({length: 200}, (_, index) => entry(`/media/${200 + index}`)), 'page-3'));
  const both = toggleExplorerSelection(selected, next.entries[399]!, true);
  assert.deepEqual(both.map(row => row.path), ['/media/0', '/media/399']);
  assert.equal(original.entries.length, 200);
  assert.equal(next.entries.length, 400);
  assert.deepEqual(toggleExplorerSelection(both, next.entries[0]!, true).map(row => row.path), ['/media/399']);
  assert.deepEqual(toggleExplorerSelection(both, entry('/media/backup.db', 'file'), false).map(row => row.path), ['/media/backup.db']);
});

test('pagination deduplicates entries and refuses another directory or a stalled cursor', () => {
  const first = page([entry('/media/Movies')], 'next');
  assert.equal(appendExplorerPage(first, page([entry('/media/Movies'), entry('/media/Music')])).entries.length, 2);
  assert.throws(() => appendExplorerPage(first, {...page([]), path: '/other'}));
  assert.throws(() => appendExplorerPage(first, page([], 'next')));
});
