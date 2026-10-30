import test from 'node:test';
import assert from 'node:assert/strict';
import {FileInfoWindowError, FileInfoWindowSession, fileInfoRows, type FileInfoWindowFacts, type FileInfoWindowSessionOptions} from '../src/file-info-windows.ts';
import type {PreparationResponse} from '../src/read-preparation.ts';

type Read = Readonly<{itemId: string}>;
type Page = Readonly<{rows: readonly string[]; window: FileInfoWindowFacts}>;
const response = (status: number, body?: unknown, headers: Record<string, string> = {}): PreparationResponse => ({status, body, headers});
const page = (rows: string[], window: Partial<FileInfoWindowFacts> = {}) => ({rows, window: {queryId: 'q1', basis: 'b1', limit: 2, returned: rows.length, ...window}});
const preparing = (queryId = 'q1') => ({kind: 'file-preparing', state: 'preparing', queryId, retryAfterMs: 1000});
async function flush() { for (let i = 0; i < 40; i++) await Promise.resolve(); }

function fixture(replies: PreparationResponse[]) {
  const calls: {path: string; method: string}[] = [], timers: (() => void)[] = [];
  let time = 0;
  const options: FileInfoWindowSessionOptions<Read, Page> = {
    api: {requestRaw(path, method) {
      calls.push({path, method});
      if (method === 'DELETE') return Promise.resolve(response(204));
      assert.ok(replies.length, `unexpected request ${path}`);
      return Promise.resolve(replies.shift()!);
    }},
    path: (read, cursor) => `/v1/items/${read.itemId}/groups${cursor ? `?cursor=${cursor}` : ''}`,
    poll: id => `/v1/file-info-preparations/${id}`, cancel: id => `/v1/file-info-preparations/${id}`,
    parse: body => body as Page, window: value => value.window,
    parsePreparing: body => ({queryId: (body as {queryId: string}).queryId, retryAfterMs: (body as {retryAfterMs: number}).retryAfterMs}),
    readyQueryId: body => (body as Page).window.queryId,
    errorFromResponse: raw => new Error(`status ${raw.status}`),
    isCurrent: () => true,
    runtime: {now: () => time, random: () => 0, setTimeout: (callback, ms) => { timers.push(() => { time += ms; callback(); }); return timers.length; }, clearTimeout: () => {}},
  };
  return {session: new FileInfoWindowSession(options), calls, timers};
}

test('a window session reads one window at a time and keeps only the latest lease', async () => {
  const {session, calls} = fixture([response(200, page(['a', 'b'], {next: 'n1'})), response(200, page(['c'], {queryId: 'q2', previous: 'p2'}))]);
  const first = await session.open({itemId: 'i1'});
  assert.deepEqual(first.page.rows, ['a', 'b']);assert.equal(first.reopened, false);
  const second = await session.next();
  assert.deepEqual(second.page.rows, ['c']);
  assert.deepEqual(calls.map(call => `${call.method} ${call.path}`), ['GET /v1/items/i1/groups', 'GET /v1/items/i1/groups?cursor=n1', 'DELETE /v1/file-info-preparations/q1'],
    'the first window is released only after the server consumed its token');
  await assert.rejects(session.next(), (error: unknown) => error instanceof FileInfoWindowError && error.code === 'file_info_continuation_unavailable');
  session.retire();
  assert.equal(calls.at(-1)?.method, 'DELETE');assert.equal(calls.at(-1)?.path, '/v1/file-info-preparations/q2');
});

test('a preparing window polls the handle, then publishes the ready window', async () => {
  const {session, calls, timers} = fixture([response(202, preparing(), {Location: '/v1/file-info-preparations/q1', 'Retry-After': '1'}), response(200, page(['a']))]);
  const reading = session.open({itemId: 'i1'});
  await flush();assert.equal(timers.length, 1);timers[0]!();
  assert.deepEqual((await reading).page.rows, ['a']);
  assert.deepEqual(calls.map(call => call.path), ['/v1/items/i1/groups', '/v1/file-info-preparations/q1']);
});

test('a continuation onto a different opening is stale, and a stale reply reopens the same lane from its start', async () => {
  const changed = fixture([response(200, page(['a'], {next: 'n1'})), response(200, page(['b'], {basis: 'b2'}))]);
  await changed.session.open({itemId: 'i1'});
  await assert.rejects(changed.session.next(), (error: unknown) => error instanceof FileInfoWindowError && error.code === 'stale_continuation');
  const stale = fixture([response(200, page(['a'], {next: 'n1'})), response(409, {code: 'stale_continuation'}), response(200, page(['a2'], {basis: 'b2', queryId: 'q3'}))]);
  await stale.session.open({itemId: 'i1'});
  const again = await stale.session.next();
  assert.equal(again.reopened, true);assert.deepEqual(again.page.rows, ['a2']);
  assert.ok(stale.calls.some(call => call.method === 'GET' && call.path === '/v1/items/i1/groups' && stale.calls.indexOf(call) > 0), 'reopened without the old token');
});

test('resident rows are bounded: appending past the bound drops the earliest and says so', () => {
  const facts = (over: Partial<FileInfoWindowFacts> = {}): FileInfoWindowFacts => ({queryId: 'q', basis: 'b', limit: 2, returned: 2, ...over});
  let rows = fileInfoRows<number>(undefined, [1, 2], facts({next: 'n'}), 'replace', 3);
  assert.deepEqual(rows, {rows: [1, 2], more: true, earlier: false});
  rows = fileInfoRows(rows, [3, 4], facts({next: 'n2', previous: 'p'}), 'append', 3);
  assert.deepEqual(rows, {rows: [2, 3, 4], more: true, earlier: true});
  rows = fileInfoRows(rows, [5], facts({previous: 'p'}), 'append', 3);
  assert.deepEqual(rows, {rows: [3, 4, 5], more: false, earlier: true});
});

import {FileInfoSession, fileInfoTrackKey} from '../src/file-info-session.ts';
import type {FileInfoOpening} from '../src/file-info-pages.ts';

const opening: FileInfoOpening = {token: 'o1', scope: {serverId: 's1', viewerId: 'v1'}, target: {libraryId: 'l1', itemId: 'm1'}, revision: {catalog: 4, viewer: 2}};
const envelope = (kind: string, queryId: string, over: Record<string, unknown> = {}, window: Record<string, unknown> = {}) => ({
  scope: {serverId: 's1', libraryId: 'l1', itemId: 'm1', viewerFence: 'f1'}, detail: {scope: {serverId: 's1', libraryId: 'l1', itemId: 'm1', viewerFence: 'f1'}, revision: {catalog: 4, viewer: 2}},
  basis: 'b1', kind, window: {queryId, limit: 40, returned: 1, ...window}, ...over});
const surface = {fileCount: 2, eligibleFileCount: 2, groupCount: 2, canSplit: false, hasDiscColumns: false};
const groupRow = (ordinal: number) => ({id: `g${ordinal}`, headOccurrence: `m1.a${ordinal}`, ordinal, memberCount: 1, container: 'mkv', sizeBytes: 10, durationMs: 1000, audioCount: 1, subtitleCount: 0});

test('a File info session reads versions window by window and a finished lane gives up its lease', async () => {
  const replies = [
    response(200, envelope('file-info-groups', 'q1', {surface, groups: [groupRow(1)]}, {nextCursor: 'n1'})),
    response(200, envelope('file-info-groups', 'q2', {surface, groups: [groupRow(2)]})),
    response(200, envelope('file-info-tracks', 'q3', {lane: 'audio', occurrence: 'm1.a1', total: 1, tracks: [{ordinal: 1, codec: 'aac', language: 'en'}]})),
  ];
  const calls: string[] = [];
  const session = new FileInfoSession({opening, isCurrent: () => true, errorFromResponse: raw => new Error(String(raw.status)),
    api: {requestRaw(path, method) { calls.push(`${method} ${path}`); return Promise.resolve(method === 'DELETE' ? response(204) : replies.shift()!); }}});
  await session.groups();
  assert.equal(session.getSnapshot().groups.status, 'ready');assert.equal(session.getSnapshot().groups.rows?.more, true);
  await session.groups('next');
  assert.deepEqual(session.getSnapshot().groups.rows?.rows.map(row => row.ordinal), [1, 2]);assert.equal(session.getSnapshot().groups.rows?.more, false);
  assert.equal(session.getSnapshot().surface?.groupCount, 2);
  await session.tracks('m1.a1', 'audio');
  assert.equal(session.getSnapshot().tracks.get(fileInfoTrackKey('m1.a1', 'audio'))?.rows?.rows[0]?.language, 'en');
  assert.deepEqual(calls, ['GET /v1/items/m1/file-info/groups?opening=o1&limit=40', 'GET /v1/items/m1/file-info/groups?opening=o1&cursor=n1', 'DELETE /v1/file-info-queries/q1', 'DELETE /v1/file-info-queries/q2',
    'GET /v1/items/m1/file-info/tracks?opening=o1&occurrence=m1.a1&lane=audio&limit=40', 'DELETE /v1/file-info-queries/q3']);
  session.retire();
});

test('a window from another Detail revision is refused', async () => {
  const session = new FileInfoSession({opening, isCurrent: () => true, errorFromResponse: raw => new Error(String(raw.status)),
    api: {requestRaw: (_path, method) => Promise.resolve(method === 'DELETE' ? response(204) : response(200, {...envelope('file-info-groups', 'q1', {surface, groups: [groupRow(1)]}), detail: {scope: {serverId: 's1', libraryId: 'l1', itemId: 'm1', viewerFence: 'f1'}, revision: {catalog: 5, viewer: 2}}}))}});
  await session.groups();
  assert.equal(session.getSnapshot().groups.status, 'failed');
});

import {parseFileInfoGroupsPage, parseFileInfoMembersPage} from '../src/file-info-pages.ts';

test('frozen numerics: an exact decimal size stays lossless, and an unrepresentable length is absent, never zero', () => {
  const page = parseFileInfoGroupsPage(envelope('file-info-groups', 'q1', {surface, groups: [{...groupRow(1), sizeBytes: undefined, sizeBytesExact: '18446744073709551615', durationMs: undefined, durationState: 'unrepresentable'}]}), opening);
  assert.deepEqual(page.groups[0]!.size, {exact: '18446744073709551615'});
  assert.equal(page.groups[0]!.durationMs, undefined);assert.equal(page.groups[0]!.durationUnrepresentable, true);
  assert.throws(() => parseFileInfoGroupsPage(envelope('file-info-groups', 'q1', {surface, groups: [{...groupRow(1), sizeBytesExact: '10'}]}), opening), 'never both forms');
  assert.throws(() => parseFileInfoGroupsPage(envelope('file-info-groups', 'q1', {surface, groups: [{...groupRow(1), durationState: 'unrepresentable'}]}), opening), 'an unrepresentable length is never sent');
});

test('paths are owner-only, and legal long text arrives whole', () => {
  const path = '/media/' + 'x'.repeat(9000) + '.mkv';
  const member = {occurrence: 'opaque-occurrence-1', itemId: 'm1', id: 'a1', path, size: 1, container: 'mkv', duration: 1.5, available: true, partIndex: 0, audioCount: 0, subtitleCount: 0};
  const owned = parseFileInfoMembersPage(envelope('file-info-members', 'q1', {groupId: 'g1', total: 1, members: [member]}), opening, 'g1', true);
  assert.equal(owned.members[0]!.path, path);assert.equal(owned.members[0]!.duration, 1.5);
  assert.throws(() => parseFileInfoMembersPage(envelope('file-info-members', 'q1', {groupId: 'g1', total: 1, members: [member]}), opening, 'g1', false));
});

import {fileInfoPanelModel} from '../src/presentation/file-info-versions.ts';
import {defaultI18n} from '../../i18n/src/index.ts';

function panelSession(replies: Record<string, unknown>) {
  const calls: string[] = [];
  const session = new FileInfoSession({opening, isCurrent: () => true, api: {requestRaw(path, method) {
    calls.push(`${method} ${path}`);
    if (method === 'DELETE') return Promise.resolve(response(204));
    const key = Object.keys(replies).find(prefix => path.startsWith(prefix));
    assert.ok(key, `unexpected ${path}`);
    return Promise.resolve(response(200, replies[key!]));
  }}});
  return {session, calls};
}
const owner = {...surface, groupCount: 2, canSplit: true, manage: {delete: true, split: true, merge: true}};

test('the panel model numbers versions by the server, lists raw tracks on web and offered tracks on native, and splits only on the server\'s word', async () => {
  const groupsPage = envelope('file-info-groups', 'q1', {surface: owner, groups: [groupRow(1), {...groupRow(2), audioCount: 0}]}, {returned: 2});
  const {session} = panelSession({
    '/v1/items/m1/file-info/groups/head-tracks': envelope('file-info-head-tracks', 'q3', {lane: 'audio', groupId: 'g1', occurrence: 'm1.a1', total: 1, tracks: [{ordinal: 1, id: 'h1', codec: 'eac3', language: 'de', commentary: true}]}),
    '/v1/items/m1/file-info/groups/members': envelope('file-info-members', 'q4', {groupId: 'g1', total: 1, members: [{occurrence: 'm1.a1', itemId: 'm1', id: 'a1', path: '/m/one.mkv', size: 10, container: 'mkv', duration: 1, available: true, partIndex: 0, audioCount: 1, subtitleCount: 0}]}),
    '/v1/items/m1/file-info/groups': groupsPage,
    '/v1/items/m1/file-info/tracks': envelope('file-info-tracks', 'q2', {lane: 'audio', occurrence: 'm1.a1', total: 1, tracks: [{ordinal: 1, codec: 'aac', language: 'en'}]}),
  });
  await session.groups();
  await session.tracks('m1.a1', 'audio'); await session.headTracks('g1', 'audio'); await session.members('g1');
  const env = {t: defaultI18n.t};
  const web = fileInfoPanelModel(session.getSnapshot(), {tracks: 'raw', context: 'desktop', manageSurface: 'desktop', split: true, merge: false}, env)!;
  assert.deepEqual(web.files.map(file => file.display.heading.text), ['Version 1', 'Version 2']);
  assert.match(web.files[0]!.display.audio[0]!.parts.map(part => part.text).join(' '), /English/);
  assert.deepEqual(web.owner?.locations.map(location => [location.id, location.versionId, location.path]), [['m1.a1', 'g1', '/m/one.mkv']]);
  assert.deepEqual(web.owner?.actions.map(action => action.id), ['split-versions']);
  const native = fileInfoPanelModel(session.getSnapshot(), {tracks: 'offered', context: 'phone', manageSurface: 'phone', split: false, merge: true}, env)!;
  assert.match(native.files[0]!.display.audio[0]!.parts.map(part => part.text).join(' '), /German/);
  assert.deepEqual(native.owner?.actions.map(action => action.id), ['merge-title'], 'a kind that cannot be split offers no Split, whatever the server says');
  const member = panelSession({'/v1/items/m1/file-info/groups': envelope('file-info-groups', 'q1', {surface, groups: [groupRow(1)]})});
  await member.session.groups();
  assert.equal(fileInfoPanelModel(member.session.getSnapshot(), {tracks: 'raw', context: 'desktop', manageSurface: 'desktop', split: true, merge: true}, env)!.owner, undefined);
  session.retire(); member.session.retire();
});

import {fileInfoUsesGroups} from '../src/file-info-session.ts';

test('an album or a book reads its flat files: each listed with its raw tracks, its owner sees each path, and groups are only for playable kinds', async () => {
  assert.deepEqual(['movie', 'episode', 'extra', 'song', 'audiobook_file', 'album', 'book', 'show'].map(fileInfoUsesGroups), [true, true, true, true, true, false, false, false]);
  const file = (n: number) => ({occurrence: `album.t${n}`, itemId: `song${n}`, id: `t${n}`, path: `/music/${n}.flac`, size: 30, container: 'flac', duration: 200, available: true, partIndex: 0, audioCount: 1, subtitleCount: 0});
  const {session, calls} = panelSession({
    '/v1/items/m1/file-info/tracks': envelope('file-info-tracks', 'q2', {lane: 'audio', occurrence: 'album.t1', total: 1, tracks: [{ordinal: 1, codec: 'flac', channels: 2}]}),
    '/v1/items/m1/file-info': envelope('file-info-files', 'q1', {itemKind: 'album', surface: owner, files: [file(1), file(2)]}, {returned: 2}),
  });
  await session.files(); await session.tracks('album.t1', 'audio');
  const model = fileInfoPanelModel(session.getSnapshot(), {tracks: 'raw', context: 'desktop', manageSurface: 'desktop', split: true, merge: false}, {t: defaultI18n.t})!;
  assert.deepEqual(model.files.map(row => [row.id, row.display.heading.text]), [['album.t1', 'Version 1'], ['album.t2', 'Version 2']]);
  assert.equal(model.files[0]!.display.audio.length, 1);
  assert.deepEqual(model.owner?.locations.map(location => [location.id, location.versionId, location.path]), [['album.t1', 'album.t1', '/music/1.flac'], ['album.t2', 'album.t2', '/music/2.flac']]);
  assert.equal(calls[0], 'GET /v1/items/m1/file-info?opening=o1&limit=40');
  session.retire();
});

test('P2-29 amendment: only the opening preparation may omit its Detail; once a window is read, every preparation names it', async () => {
  const bare = {kind: 'file-info-preparing', state: 'preparing', queryId: 'p1', retryAfterMs: 10};
  const replies = [
    response(202, bare, {Location: '/v1/file-info-queries/p1', 'Retry-After': '1'}),
    response(200, envelope('file-info-groups', 'p1', {surface, groups: [groupRow(1)]}, {nextCursor: 'n1'})),
    response(202, bare, {Location: '/v1/file-info-queries/p2', 'Retry-After': '1'}),
  ];
  const timers: (() => void)[] = [];let time = 0;
  const session = new FileInfoSession({opening, isCurrent: () => true,
    runtime: {now: () => time, random: () => 0, setTimeout: (callback, ms) => {timers.push(() => {time += ms; callback();}); return timers.length;}, clearTimeout: () => {}},
    api: {requestRaw: (_path, method) => Promise.resolve(method === 'DELETE' ? response(204) : replies.shift()!)}});
  const first = session.groups();
  for (let i = 0; i < 40 && !timers.length; i++) await Promise.resolve();
  assert.equal(session.getSnapshot().groups.status, 'preparing', 'the opening preparation is shown as preparing, not as an error');
  timers.shift()!();await first;
  assert.equal(session.getSnapshot().groups.status, 'ready');
  await session.groups('next');
  assert.equal(session.getSnapshot().groups.status, 'failed', 'after a window was read, a preparation without its Detail is refused');
  session.retire();
});
