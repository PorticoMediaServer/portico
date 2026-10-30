import assert from 'node:assert/strict';
import test from 'node:test';
import {authorityKey, canOpenLibrary, librariesDestination, phoneBarHeight, phoneHeaderGeometry, phoneTabs, sameAuthority, selectedTab, shellLayout, visibleLibraries} from '../src-next/shell/model';
import {LastLibraryMemory, type LibraryRecordStore} from '../src-next/shell/library-memory';
import {authorizedHome, homePlayTarget, homeSubject, initialHomeFocus, pageAtVisibleEnd, platformRows} from '../src-next/screens/home/model';
import type {ShellAuthority} from '../src-next/shell/model';
import type {HomeDocument, HomeRow} from '../../../packages/client-core/src/home';
import type {ContentEntry} from '../../../packages/client-core/src/library-content';

const authority: ShellAuthority = {serverId: 'server-a', accountId: 'account-a', profileId: 'profile-a', epoch: 'authority-a', viewerFence: 'viewer-a'};
const entry = (id: string, overrides: Partial<ContentEntry> = {}): ContentEntry => ({id, libraryId: 'library-a', kind: 'movie', title: id, ...overrides});
const row = (id: string, overrides: Partial<HomeRow> = {}): HomeRow => ({
  id, title: id, kind: 'recently_added', artworkShape: 'poster', viewerFence: 'viewer-a', entries: [entry(id)],
  privacySensitivity: '', policyState: '', priority: 1, required: false, hideable: true, reorderable: true, critical: false, cursorCapable: true,
  total: 1, start: 0, limit: 20, hasMore: false, nextCursor: '', endpoint: '', revision: {catalog: 1, viewer: 1}, ...overrides,
});
const document = (rows: readonly HomeRow[]): HomeDocument => ({serverId: 'server-a', viewerFence: 'viewer-a', rows, spotlight: [], layout: {revision: 1, rowOrder: [], hiddenRowIds: []}, revision: {catalog: 1, viewer: 1}, generatedAt: '2026-10-22T00:00:00Z'});

test('Libraries opens only an authorized remembered library, including the one-library first-use case', () => {
  const libraries = [{id: 'a', name: 'a', kind: 'movie'}, {id: 'b', name: 'b', kind: 'movie'}];
  assert.deepEqual(librariesDestination(undefined, libraries, false), {kind: 'libraries'});
  assert.deepEqual(librariesDestination('removed', libraries, false), {kind: 'libraries'});
  assert.deepEqual(librariesDestination('a', libraries, false), {kind: 'library', libraryId: 'a'});
  assert.deepEqual(librariesDestination(undefined, [libraries[0]], false), {kind: 'library', libraryId: 'a'});
  assert.equal(selectedTab({kind: 'library', libraryId: 'a'}), 'libraries');
});

test('phone navigation preserves the exact order and removes Live TV when unavailable', () => {
  assert.deepEqual(phoneTabs(true).map(tab => tab.id), ['home', 'search', 'libraries', 'live', 'saved']);
  assert.deepEqual(phoneTabs(false).map(tab => tab.id), ['home', 'search', 'libraries', 'saved']);
  assert.equal(phoneTabs(true).some(tab => String(tab.id) === 'activity'), false);
});

test('TV library order is server-owned and Read never enters the rail', () => {
  const libraries = [{id: 'z', name: 'z', kind: 'music'}, {id: 'r', name: 'r', kind: 'book'}, {id: 'c', name: 'c', kind: 'comic'}, {id: 'old', name: 'old', kind: 'books'}, {id: 'a', name: 'a', kind: 'audiobook'}];
  assert.strictEqual(visibleLibraries(libraries, false), libraries);
  assert.deepEqual(visibleLibraries(libraries, true).map(library => library.id), ['z', 'a']);
  for (const id of ['r', 'c', 'old', 'removed']) assert.equal(canOpenLibrary(libraries, id, true), false, id);
  assert.equal(canOpenLibrary(libraries, 'a', true), true);
  assert.equal(canOpenLibrary(libraries, 'r', false), true);
  assert.equal(canOpenLibrary(libraries, 'c', false), true);
});

test('native breakpoint and safe area geometry retains the drawn bar and scales TV as a unit', () => {
  assert.equal(shellLayout(599, 844, false).panel, 'bottom');
  assert.equal(shellLayout(600, 1024, false).kind, 'tablet');
  assert.equal(shellLayout(1023, 1366, false).tabs, false);
  assert.equal(shellLayout(1023, 1366, false).rail, true);
  assert.equal(shellLayout(1024, 1366, false).kind, 'desktop');
  assert.equal(phoneBarHeight(0), 84);
  assert.equal(phoneBarHeight(34), 84);
  assert.equal(phoneBarHeight(48), 98);
  assert.equal(phoneBarHeight(0, true), 52);
  assert.equal(phoneBarHeight(21, true), 59);
  assert.equal(phoneBarHeight(14, true), 52);
  assert.deepEqual(phoneHeaderGeometry(24, false), {top: 54, height: 104});
  assert.deepEqual(phoneHeaderGeometry(24, true), {top: 46, height: 96});
  assert.deepEqual(phoneHeaderGeometry(60, false), {top: 60, height: 110});
  assert.deepEqual(shellLayout(1280, 800, true), {kind: 'tv', panel: 'side', tabs: false, rail: false, scale: 2 / 3, left: 0, top: 40, width: 1920, height: 1080});
});

test('server/account/profile and permission epoch all fence retained component state', () => {
  for (const key of ['serverId', 'accountId', 'profileId', 'epoch', 'viewerFence'] as const) {
    const other = {...authority, [key]: 'different'};
    assert.equal(sameAuthority(authority, other), false, key);
    assert.notEqual(authorityKey(authority), authorityKey(other), key);
  }
});

class Records implements LibraryRecordStore {
  values = new Map<string, unknown>();
  writes = 0;
  async change<T>(key: string, update: (raw: unknown) => {record: unknown; value: T}) {
    const before = this.values.get(key) ?? null, result = update(before);
    if (before !== result.record) {this.values.set(key, result.record); this.writes++;}
    return result.value;
  }
}

test('last-library memory isolates accounts and profiles even on the same server/device', async () => {
  const records = new Records(), memory = new LastLibraryMemory(records);
  const scopes = [authority, {...authority, profileId: 'profile-b'}, {...authority, accountId: 'account-b'}, {...authority, serverId: 'server-b'}];
  await Promise.all(scopes.map((scope, index) => memory.remember(scope, 'library-' + index)));
  assert.deepEqual(await Promise.all(scopes.map(scope => memory.read(scope))), ['library-0', 'library-1', 'library-2', 'library-3']);
});

test('a read immediately after rapid library choices observes the latest durable choice', async () => {
  const records = new Records(), memory = new LastLibraryMemory(records);
  const first = memory.remember(authority, 'first'), second = memory.remember(authority, 'second');
  assert.equal(await memory.read(authority), 'second');
  await Promise.all([first, second]);
  assert.equal(records.writes, 2);
});

test('malformed records are ignored without destructive repair or guessed first library', async () => {
  const records = new Records(), memory = new LastLibraryMemory(records);
  await memory.remember(authority, 'valid');
  const key = [...records.values.keys()][0], damaged = {libraryId: 'cross-format'};
  records.values.set(key, damaged);
  const writes = records.writes;
  assert.equal(await memory.read(authority), undefined);
  assert.equal(records.writes, writes);
  assert.strictEqual(records.values.get(key), damaged);
  await assert.rejects(memory.remember(authority, '\n'), /invalid_library_identity/);
});

test('a failed memory write does not deadlock the next legitimate library choice', async () => {
  const records = new Records();
  let fail = true;
  const memory = new LastLibraryMemory({change: async (key, update) => {if (fail) {fail = false; throw new Error('storage-unavailable');} return records.change(key, update);}});
  await assert.rejects(memory.remember(authority, 'first'), /storage-unavailable/);
  await memory.remember(authority, 'second');
  assert.equal(await memory.read(authority), 'second');
});

test('retained Home cannot render under a different server or viewer permission fence', () => {
  const home = document([row('a')]);
  assert.strictEqual(authorizedHome(home, authority), home);
  assert.equal(authorizedHome(home, {...authority, serverId: 'other'}), undefined);
  assert.equal(authorizedHome(home, {...authority, viewerFence: 'revoked'}), undefined);
});

test('platform filtering preserves server row objects/order and suppresses TV Read rows', () => {
  const rows = [row('first'), row('read', {libraryId: 'read'}), row('phone-only', {platforms: ['phone', 'web']}), row('last')];
  const shown = platformRows(document(rows), 'tv', new Set(['read']));
  assert.deepEqual(shown.map(value => value.id), ['first', 'last']);
  assert.strictEqual(shown[0], rows[0]);
  assert.strictEqual(shown[1], rows[3]);
});

test('Home plays the explicit server-selected episode target rather than a show id', () => {
  const show = entry('show', {kind: 'show'}), target = entry('episode', {kind: 'episode', playback: {itemId: 'episode', startSeconds: 30}});
  const continued = row('continue', {kind: 'continue', entries: [show], resumes: [{entryId: show.id, target}]});
  assert.strictEqual(homePlayTarget(continued, show), target);
  assert.equal(homePlayTarget(row('unavailable'), entry('missing', {missing: true, playback: {itemId: 'missing'}})), undefined);
  assert.equal(homePlayTarget(row('no-target'), show), undefined);
});

test('Home row fallback and bounded pagination preserve real continuation identity', () => {
  const rows = [row('recent'), row('continue', {kind: 'continue', entries: [entry('resume')]})];
  assert.deepEqual(initialHomeFocus(rows), {rowId: 'continue', entryId: 'resume'});
  assert.equal(initialHomeFocus([]), undefined);
  const paged = row('more', {entries: Array.from({length: 20}, (_, index) => entry(String(index))), nextCursor: 'opaque-server-cursor', hasMore: true});
  assert.equal(pageAtVisibleEnd(paged, 3), false);
  assert.equal(pageAtVisibleEnd(paged, 17), true);
  assert.equal(pageAtVisibleEnd(paged, 9, 10), false);
  assert.equal(pageAtVisibleEnd(paged, 10, 10), true);
  assert.equal(pageAtVisibleEnd({...paged, nextCursor: ''}, 19), false);
});

test('episode presentation receives actual structured show identity and numbering without title parsing', () => {
  const subject = homeSubject(entry('episode', {kind: 'episode', title: 'episode', show: {id: 'show', name: 'show'}, seasonNumber: 2, episodeNumber: 3, progressSeconds: 42, duration: 1800}));
  assert.deepEqual(subject.show, {id: 'show', title: 'show'});
  assert.deepEqual(subject.episode, {mode: 'seasonal', season: 2, episode: 3});
  assert.equal(subject.positionSeconds, 42);
  assert.equal(subject.durationSeconds, 1800);
});


test('sidebar is a bounded device boolean isolated by server/account/profile', async () => {
  const records = new Records(), memory = new LastLibraryMemory(records), current = () => true;
  const other = {...authority, profileId: 'other'};
  assert.equal(await memory.readSidebar(authority, current), true);
  await memory.rememberSidebar(authority, false, current);
  assert.equal(await memory.readSidebar(authority, current), false);
  assert.equal(await memory.readSidebar(other, current), true);
  const key = 'next.sidebar-expanded.v1:' + JSON.stringify([authority.serverId, authority.accountId, authority.profileId]);
  records.values.set(key, 'false');
  assert.equal(await memory.readSidebar(authority, current), true);
  assert.equal(records.values.get(key), 'false', 'invalid records are not destructively repaired');
});

test('retirement prevents queued sidebar writes even inside a delayed store callback', async () => {
  const records = new Records(); let live = true, resume!: () => void;
  const memory = new LastLibraryMemory({change: async (key, update) => {
    await new Promise<void>(resolve => {resume = resolve;});
    return records.change(key, update);
  }});
  const pending = memory.rememberSidebar(authority, false, () => live);
  await Promise.resolve(); await Promise.resolve();
  live = false; resume(); await pending;
  assert.equal(records.writes, 0);
});
