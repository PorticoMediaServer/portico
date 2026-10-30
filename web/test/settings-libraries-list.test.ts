import test from 'node:test';
import assert from 'node:assert/strict';
import type {ManagedLibrary} from '@core/library-management.ts';
import {folderRows, isLibraryPanelContext, libraryKindIds, libraryRows, libraryScanAction, localOnlyKind, metadataAgentFor, movedIds, scanning} from '../src/screens/settings/libraries/model.ts';

const library = (id: string, overrides: Partial<ManagedLibrary> = {}): ManagedLibrary => Object.freeze({
  id, name: id.toUpperCase(), kind: 'movie', revision: 1,
  source: Object.freeze({path: '/media/' + id, availability: 'available', basis: 'probe'}),
  lastScan: null, actions: Object.freeze(['rename', 'scan', 'remove'] as const), ...overrides,
});

test('a libraries row carries only the library name, its folder and whether a scan runs', () => {
  const rows = libraryRows([library('movies'), library('shows', {lastScan: {id: 'job', libraryId: 'shows', status: 'running',
    processed: 12, createdAt: '2026-10-24T20:00:00Z', updatedAt: null, finishedAt: null, actions: ['cancel']}})]);
  assert.deepEqual(rows.map(row => [row.name, row.folder, row.scanning]), [
    ['MOVIES', '/media/movies', false], ['SHOWS', '/media/shows', true],
  ]);
  assert.equal(rows[0].canScan, true);
  assert.equal(rows[1].canScan, false, 'a running scan is not offered again');
});

test('a scan the server says is queued is not drawn as scanning', () => {
  assert.equal(scanning(library('movies', {lastScan: {id: 'job', libraryId: 'movies', status: 'queued', processed: 0,
    createdAt: '2026-10-24T20:00:00Z', updatedAt: null, finishedAt: null, actions: ['cancel']}})), false);
});

test('a library that does not allow scanning offers no scan action', () => {
  assert.equal(libraryRows([library('books', {actions: ['rename', 'remove']})])[0].canScan, false);
});

test('the first and last rows cannot move past the list ends', () => {
  const rows = libraryRows([library('a'), library('b'), library('c')]);
  assert.deepEqual(rows.map(row => [row.canMoveUp, row.canMoveDown]), [[false, true], [true, true], [true, false]]);
});

test('moving a row returns the complete stable order and refuses the ends', () => {
  const libraries = [library('a'), library('b'), library('c')];
  assert.deepEqual(movedIds(libraries, 'b', -1), ['b', 'a', 'c']);
  assert.deepEqual(movedIds(libraries, 'b', 1), ['a', 'c', 'b']);
  assert.equal(movedIds(libraries, 'a', -1), undefined);
  assert.equal(movedIds(libraries, 'c', 1), undefined);
  assert.equal(movedIds(libraries, 'missing', 1), undefined);
});

test('the drawn kinds keep their order and the local-only kinds choose no online source', () => {
  assert.deepEqual([...libraryKindIds], ['movie', 'tv', 'anime', 'music', 'audiobook', 'book', 'comic']);
  for (const kind of ['audiobook', 'book', 'comic'] as const) {
    assert.equal(localOnlyKind(kind), true, kind);
    assert.equal(metadataAgentFor(kind), 'local', kind);
  }
  for (const kind of ['movie', 'tv', 'anime', 'music'] as const) {
    assert.equal(localOnlyKind(kind), false, kind);
    assert.equal(metadataAgentFor(kind), 'online', kind);
  }
});

test('scan action rows run the drawn command and belong to one open library page', () => {
  assert.equal(libraryScanAction('libraries.scanNow'), 'incremental');
  assert.equal(libraryScanAction('libraries.scanAll'), 'full');
  assert.equal(libraryScanAction('libraries.remove'), undefined);
  assert.equal(isLibraryPanelContext({panelId: 'library', recordId: 'lib'}), true);
  assert.equal(isLibraryPanelContext({panelId: 'library'}), false);
  assert.equal(isLibraryPanelContext({panelId: 'newLibrary', recordId: 'lib'}), false);
  assert.equal(isLibraryPanelContext({}), false);
});

test('folder rows carry the real path, the disk kind and the server health word', () => {
  const source = (id: string, classification: 'local' | 'network', health: string, enabled = true) => ({
    id, libraryId: 'lib', kind: 'folder', name: id, path: '/' + id, resolvedPath: '/' + id, classification,
    generation: 1, health, enabled, followSymlinks: false, identityConfirmed: true, intervalSeconds: 0,
    nextScanAt: '', lastCompleteAt: '', lastProgressAt: '',
  } as const);
  const rows = folderRows([source('media', 'local', 'healthy'), source('nas', 'network', 'unreachable', false)]);
  assert.deepEqual(rows.map(row => [row.name, row.path, row.network, row.health, row.enabled]), [
    ['media', '/media', false, 'healthy', true], ['nas', '/nas', true, 'unreachable', false],
  ]);
});
