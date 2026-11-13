import test from 'node:test';
import assert from 'node:assert/strict';
import {FileInfoSession} from '../src/file-info-session.ts';
import {fileInfoPanelModel} from '../src/presentation/file-info-versions.ts';
import {defaultI18n} from '../../i18n/src/index.ts';

const streams = [{index: 0, kind: 'video', codec: 'hevc', profile: 'Main 10', width: 3840, height: 1608, hdr: 'hdr10', bitDepth: 10, frameRate: 23.976},
  {index: 1, kind: 'audio', codec: 'truehd', language: 'en', channels: 8, channelLayout: '7.1', default: true},
  {index: 2, kind: 'subtitle', codec: 'srt', language: 'fr', forced: true, hearingImpaired: true}];
const version = {id: 'ver_1', name: 'Original', edition: 'Cinema', container: 'mkv', size: 2000, durationMs: 30000,
  parts: [{size: 1000, durationMs: 15000, streams}, {size: 1000, durationMs: 15000, streams: []}]};
const item = {id: 'movie/1', kind: 'movie', versions: [version, {...version, id: 'ver_2', parts: [version.parts[0]]}]};
const places = {versions: item.versions.map(v => ({id: v.id, files: v.parts.map((p, i) => ({...p, path: `/private/${v.id}-${i}.mkv`, container: 'mkv', missingSinceAt: i === 1 ? '2026-11-12T00:00:00Z' : undefined}))}))};
function fixture(role = 'member') {
  const calls: string[] = [];
  const session = new FileInfoSession({itemId: item.id, role, isCurrent: () => true, api: {async request<T>(path: string) {
    calls.push(path); return (path.endsWith('/files') ? places : path.endsWith('/playback-options') ? {itemId: item.id, versions: [{id: 'ver_1', audio: [{id: 'audio', codec: 'truehd', channels: 8}], subtitles: []}]} : item) as T;
  }}});
  return {session, calls};
}

test('File info reads existing title versions and streams without an opening or any owner request for a viewer', async () => {
  const {session, calls} = fixture();
  await session.groups();
  const first = session.getSnapshot().groups.rows!.rows[0];
  await session.tracks(first.headOccurrence, 'audio'); await session.tracks(first.headOccurrence, 'subtitles'); await session.members(first.id);
  const snapshot = session.getSnapshot(), model = fileInfoPanelModel(snapshot, {tracks: 'raw', context: 'desktop', manageSurface: 'desktop', split: true, merge: true}, {t: defaultI18n.t})!;
  assert.deepEqual(calls, ['/v1/items/movie%2F1']);
  assert.deepEqual(snapshot.groups.rows!.rows.map(v => [v.id, v.ordinal, v.memberCount]), [['ver_1', 1, 2], ['ver_2', 2, 1]]);
  assert.equal(model.files[0].audio[0].name.text, 'English');
  assert.equal(model.files[0].subtitles[0].detail.some(p => p.id === 'forced'), true);
  assert.equal(model.owner, undefined); assert.equal(snapshot.members.size, 0); assert.equal(JSON.stringify(snapshot).includes('/private'), false);
  session.retire();
});

test('owner and administrators get recorded paths and missing status; only the owner may delete a version', async () => {
  for (const role of ['owner', 'admin']) {
    const {session, calls} = fixture(role);
    await session.groups(); await session.members('ver_1');
    const snapshot = session.getSnapshot(), files = snapshot.members.get('ver_1')!.rows!.rows;
    assert.deepEqual(calls, ['/v1/items/movie%2F1', '/v1/items/movie%2F1/files']);
    assert.deepEqual(files.map(f => [f.id, f.path, f.available]), [['ver_1', '/private/ver_1-0.mkv', true], ['ver_1', '/private/ver_1-1.mkv', false]]);
    assert.equal(files[1].missingSinceAt, '2026-11-12T00:00:00Z');
    assert.deepEqual(snapshot.surface!.manage, {delete: role === 'owner', split: true, merge: true});
    assert.equal(snapshot.groups.rows!.more, false); session.retire();
  }
});

test('Delete this version is offered once per whole version, on its first file, and only where the surface asks for it', async () => {
  const {session} = fixture('owner');
  await session.groups(); await session.members('ver_1'); await session.members('ver_2');
  const snapshot = session.getSnapshot(), policy = {tracks: 'raw', context: 'desktop', manageSurface: 'desktop', split: true, merge: true} as const;
  const actions = (model: ReturnType<typeof fileInfoPanelModel>) => model!.owner!.locations.map(location => [location.versionId, location.actions.map(action => action.id)]);
  assert.deepEqual(actions(fileInfoPanelModel(snapshot, {...policy, delete: true}, {t: defaultI18n.t})), [['ver_1', ['delete-version']], ['ver_1', []], ['ver_2', ['delete-version']]], 'a two-file version is deleted whole, never one part');
  assert.deepEqual(actions(fileInfoPanelModel(snapshot, policy, {t: defaultI18n.t})), [['ver_1', []], ['ver_1', []], ['ver_2', []]], 'a surface without the confirm (native today) offers none');
  const admin = fixture('admin'); await admin.session.groups(); await admin.session.members('ver_1');
  assert.deepEqual(actions(fileInfoPanelModel(admin.session.getSnapshot(), {...policy, delete: true}, {t: defaultI18n.t})), [['ver_1', []], ['ver_1', []]], 'an administrator is never offered it');
  session.retire(); admin.session.retire();
});

test('native offered tracks use playback options while flat parts use the stored streams, with no lease cleanup', async () => {
  const {session, calls} = fixture();
  await session.files(); await session.headTracks('ver_1', 'audio'); await session.tracks('ver_1:0', 'audio');
  const snapshot = session.getSnapshot();
  assert.equal(snapshot.headTracks.get('audio:ver_1')!.rows!.rows[0].codec, 'truehd');
  assert.equal(snapshot.tracks.get('audio:ver_1:0')!.rows!.rows[0].channels, 8);
  assert.equal(snapshot.files!.rows!.rows.length, 3); session.retire();
  assert.deepEqual(calls, ['/v1/items/movie%2F1', '/v1/items/movie%2F1/playback-options']);
});

test('retiring or changing authority during the read cannot publish the late files', async () => {
  let finish!: (value: unknown) => void, current = true;
  const session = new FileInfoSession({itemId: item.id, role: 'member', isCurrent: () => current,
    api: {request: <T>() => new Promise<T>(resolve => {finish = resolve as typeof finish;})}});
  const read = session.groups(), held = session.getSnapshot(); current = false; session.retire(); finish(item); await read;
  assert.equal(session.getSnapshot(), held); assert.equal(session.getSnapshot().surface, undefined);
});

test('a failed title read retries the real route, not an obsolete preparation handle', async () => {
  let tries = 0;
  const session = new FileInfoSession({itemId: item.id, role: 'member', isCurrent: () => true,
    api: {async request<T>() {if (++tries === 1) throw new TypeError('offline'); return item as T;}}});
  await session.groups(); assert.equal(session.getSnapshot().groups.status, 'failed');
  await session.groups('restart'); assert.equal(session.getSnapshot().groups.status, 'ready'); assert.equal(tries, 2); session.retire();
});
