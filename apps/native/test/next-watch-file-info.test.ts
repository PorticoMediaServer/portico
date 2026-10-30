import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {nativeFileInfoFile, nativeFileInfoShowFile, nativeFileInfoShowVersion, nativeFileInfoView, nativeSplitCandidates} from '../src-next/screens/watch/file-info.ts';
import {envelope, fakeSession, groupRow, groupsPage, headTracksPage, memberRow, membersPage, ownerSurface, viewerSurface} from './helpers/file-info-windows.ts';

const text = {t: defaultI18n.t};
const settle = () => new Promise(resolve => setImmediate(resolve));
const audio = [{id: 'a1', ordinal: 1, codec: 'truehd', language: 'en', channels: 8}, {id: 'a2', ordinal: 2, codec: 'ac3', language: 'en', channels: 2, commentary: true, title: 'Director commentary'}];
const subtitles = [{id: 's1', ordinal: 1, codec: 'srt', language: 'en'}, {id: 's2', ordinal: 2, codec: 'srt', language: 'en', hearingImpaired: true}];
/** The server's answer per path: two versions, the first with two files. */
function server(surface: Record<string, unknown>, first: Record<string, unknown> = {}) {
  return (path: string) => {
    const url = new URL(path, 'https://portico.test'), at = url.searchParams, group = at.get('group') ?? '';
    if (url.pathname.endsWith('/groups/head-tracks')) return headTracksPage('q-' + group + at.get('lane'), group, at.get('lane') as 'audio', at.get('lane') === 'audio' ? audio : subtitles);
    if (url.pathname.endsWith('/groups/members')) return membersPage('q-m' + group, group, group === 'g1'
      ? [memberRow('occ-g1', 'a1', {path: '/private/a1.mkv'}), memberRow('occ-g1-part2', 'a2', {path: '/private/a2.mkv', partIndex: 1})]
      : [memberRow('occ-g2', 'b1', {path: '/private/b1.mkv'})]);
    return groupsPage('q-groups', surface, [groupRow(1, {memberCount: 2, audioCount: 2, subtitleCount: 2, ...first}), groupRow(2, {audioCount: 0})]);
  };
}
async function open(surface: Record<string, unknown>, kind = 'movie', isTV = false) {
  const data = fakeSession(server(surface));
  await data.session.groups();
  for (const group of data.session.getSnapshot().groups.rows!.rows) nativeFileInfoShowVersion(data.session, group);
  await settle();
  return {...data, view: () => nativeFileInfoView(data.session.getSnapshot(), kind, text, isTV)!};
}

test('versions come from the server groups with their ordinals, and offered head tracks from the lane', async () => {
  const d = await open(viewerSurface);
  try {
    const {model, versionCount} = d.view();
    assert.deepEqual(model.files.map(file => file.id), ['g1', 'g2']);
    assert.deepEqual(model.files.map(file => file.display.heading.text), ['Version 1', 'Version 2']);
    assert.equal(versionCount, 2);
    assert.ok(model.files[0]!.display.video.some(part => part.text === 'HEVC'));
    // Version 1's offered tracks are read; version 2 has none to offer and is never asked.
    assert.equal(model.files[0]!.audio.length, 2);
    assert.equal(model.files[0]!.subtitles.length, 2);
    assert.ok(model.files[0]!.display.audio[0]!.parts.some(part => part.text === '7.1'));
    assert.ok(model.files[0]!.display.audio[1]!.parts.some(part => part.id === 'commentary' || part.text === 'Director commentary'));
    assert.ok(model.files[0]!.display.subtitles.some(part => part.text === defaultI18n.t('next.kit.fileInfo.sdh')), 'the SDH track is marked');
    assert.equal(model.files[1]!.audio.length, 0);
    assert.equal(d.calls.filter(call => call.includes('/groups/head-tracks')).length, 2);
    assert.ok(d.calls.some(call => call === 'GET /v1/items/movie/file-info/groups/head-tracks?opening=o1&group=g1&lane=audio&limit=40'));
  } finally {d.session.retire();}
});

test('a viewer gets no owner part, no paths and never reads the owner files', async () => {
  const d = await open(viewerSurface);
  try {
    const view = d.view();
    assert.equal(view.model.owner, undefined);
    assert.equal(view.locations, undefined);
    assert.equal(d.calls.some(call => call.includes('/groups/members')), false);
    assert.equal(nativeFileInfoFile(d.session.getSnapshot(), 'occ-g1'), undefined);
  } finally {d.session.retire();}
});

test('the owner sees every file of every version, by exact occurrence, with its path', async () => {
  const d = await open(ownerSurface);
  try {
    const {locations, model} = d.view();
    assert.deepEqual(locations?.map(location => [location.id, location.versionId, location.path]),
      [['occ-g1', 'g1', '/private/a1.mkv'], ['occ-g1-part2', 'g1', '/private/a2.mkv'], ['occ-g2', 'g2', '/private/b1.mkv']]);
    assert.deepEqual(locations?.map(location => location.actions.map(action => action.id)), [['delete-version'], ['delete-version'], ['delete-version']]);
    assert.equal(model.owner?.locations.length, 3);
    const listed = nativeFileInfoFile(d.session.getSnapshot(), 'occ-g1-part2');
    assert.equal(listed?.group?.id, 'g1'); assert.equal(listed?.file.id, 'a2');
    assert.equal(nativeFileInfoFile(d.session.getSnapshot(), 'unlisted'), undefined);
  } finally {d.session.retire();}
});

test('delete is the server\'s manage.delete, per file, for phone and TV alike', async () => {
  const surface = {...ownerSurface, manage: {delete: false, split: true, merge: false}};
  const d = await open(surface);
  try {
    assert.deepEqual(d.view().locations?.map(location => location.actions.length), [0, 0, 0]);
    assert.deepEqual(nativeFileInfoView(d.session.getSnapshot(), 'movie', text, true)?.locations?.map(location => location.actions.length), [0, 0, 0]);
  } finally {d.session.retire();}
});

test('split is offered only on the server\'s own canSplit for a splittable kind; merge only for a mergeable one', async () => {
  const ids = (view: ReturnType<typeof nativeFileInfoView>) => view?.model.owner?.actions.map(action => action.id);
  const split = await open(ownerSurface);
  try {
    assert.deepEqual(ids(split.view()), ['split-versions', 'merge-title']);
    assert.deepEqual(ids(nativeFileInfoView(split.session.getSnapshot(), 'movie', text, true)), ['split-versions', 'merge-title']);
    assert.deepEqual(ids(nativeFileInfoView(split.session.getSnapshot(), 'season', text, false)), [], 'a kind that cannot be split or merged');
  } finally {split.session.retire();}
  const refused = await open({...ownerSurface, canSplit: false});
  try { assert.deepEqual(ids(refused.view()), ['merge-title'], 'the server said no, so not even a splittable kind offers it'); } finally {refused.session.retire();}
  const withheld = await open({...ownerSurface, manage: {delete: true, split: false, merge: false}});
  try { assert.deepEqual(ids(withheld.view()), []); } finally {withheld.session.retire();}
});

test('the version count is the surface\'s, not what is resident', async () => {
  const data = fakeSession(path => path.includes('cursor=')
    ? groupsPage('q2', viewerSurface, [groupRow(2)]) : groupsPage('q1', viewerSurface, [groupRow(1)], {nextCursor: 'n1'}));
  await data.session.groups();
  try {
    const first = nativeFileInfoView(data.session.getSnapshot(), 'movie', text, false)!;
    assert.equal(first.model.files.length, 1); assert.equal(first.versionCount, 2);
    assert.equal(data.session.getSnapshot().groups.rows?.more, true);
  } finally {data.session.retire();}
});

test('a "more" window appends the next versions and keeps their server ordinals', async () => {
  const data = fakeSession(path => path.includes('cursor=n1')
    ? groupsPage('q2', viewerSurface, [groupRow(2)]) : groupsPage('q1', viewerSurface, [groupRow(1)], {nextCursor: 'n1'}));
  await data.session.groups();
  try {
    await data.session.groups('next');
    const view = nativeFileInfoView(data.session.getSnapshot(), 'movie', text, false)!;
    assert.deepEqual(view.model.files.map(file => file.display.heading.text), ['Version 1', 'Version 2']);
    assert.equal(data.session.getSnapshot().groups.rows?.more, false);
    assert.deepEqual(data.calls, ['GET /v1/items/movie/file-info/groups?opening=o1&limit=40', 'GET /v1/items/movie/file-info/groups?opening=o1&cursor=n1', 'DELETE /v1/file-info-queries/q1', 'DELETE /v1/file-info-queries/q2']);
  } finally {data.session.retire();}
});

test('a later window of one version\'s tracks appends to the ones already shown', async () => {
  const data = fakeSession(path => {
    const at = new URL(path, 'https://portico.test').searchParams;
    if (path.includes('/head-tracks')) return at.get('cursor') ? headTracksPage('t2', 'g1', 'audio', [audio[1]], {}, 2)
      : headTracksPage('t1', 'g1', 'audio', [audio[0]], {nextCursor: 'tn'}, 2);
    return groupsPage('q1', ownerSurface, [groupRow(1, {audioCount: 2})]);
  });
  await data.session.groups();
  try {
    const group = data.session.getSnapshot().groups.rows!.rows[0]!;
    nativeFileInfoShowVersion(data.session, group); await settle();
    assert.equal(nativeFileInfoView(data.session.getSnapshot(), 'movie', text, false)!.model.files[0]!.audio.length, 1);
    await data.session.headTracks('g1', 'audio', 'next');
    assert.equal(nativeFileInfoView(data.session.getSnapshot(), 'movie', text, false)!.model.files[0]!.audio.length, 2);
  } finally {data.session.retire();}
});

test('only a version that is one file on its own can be split, named by its listed file or its head', async () => {
  const d = await open(ownerSurface);
  try {
    const candidates = nativeSplitCandidates(d.session.getSnapshot());
    assert.deepEqual(candidates.map(candidate => [candidate.id, candidate.occurrence, candidate.file?.path]), [['g2', 'occ-g2', '/private/b1.mkv']]);
  } finally {d.session.retire();}
  const unread = fakeSession(server(ownerSurface));
  await unread.session.groups();
  try {
    assert.deepEqual(nativeSplitCandidates(unread.session.getSnapshot()).map(candidate => [candidate.id, candidate.occurrence, candidate.file]), [['g2', 'occ-g2', undefined]],
      'files not read yet: the head names the version');
  } finally {unread.session.retire();}
});

test('a disc version carries its measured title', async () => {
  const disc = {kind: 'bluray', packaging: 'iso', titleNumber: 3, chapterCount: 21};
  const data = fakeSession(() => groupsPage('q1', ownerSurface, [groupRow(1, {disc}), groupRow(2)]));
  await data.session.groups();
  try {
    const view = nativeFileInfoView(data.session.getSnapshot(), 'movie', text, true)!;
    assert.deepEqual(view.disc.map(title => [title.id, title.titleNumber, title.chapterCount]), [['g1', 3, 21]]);
  } finally {data.session.retire();}
});

test('an album reads its flat files: each listed with its own raw tracks, every file splittable, owner paths per file', async () => {
  const files = [memberRow('occ-t1', 't1', {path: '/music/1.flac', audioCount: 1}), memberRow('occ-t2', 't2', {path: '/music/2.flac', audioCount: 0})];
  const {session, calls} = fakeSession(path => path.includes('/file-info/tracks')
    ? envelope('file-info-tracks', 'q2', {lane: 'audio', occurrence: 'occ-t1', total: 1, tracks: [{ordinal: 1, codec: 'flac', channels: 2}]})
    : envelope('file-info-files', 'q1', {itemKind: 'album', surface: ownerSurface, files}, {returned: 2}));
  await session.files();
  nativeFileInfoShowFile(session, session.getSnapshot().files!.rows!.rows[0]!);
  nativeFileInfoShowFile(session, session.getSnapshot().files!.rows!.rows[1]!);
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.deepEqual(calls.filter(call => call.includes('/tracks')), ['GET /v1/items/movie/file-info/tracks?opening=o1&occurrence=occ-t1&lane=audio&limit=40'], 'only a file with audio reads its raw tracks');
  const view = nativeFileInfoView(session.getSnapshot(), 'album', {t: defaultI18n.t}, false)!;
  assert.deepEqual(view.model.files.map(file => file.id), ['occ-t1', 'occ-t2']);
  assert.equal(view.versionCount, 3, 'a flat surface counts its files');
  assert.deepEqual(view.locations?.map(location => [location.id, location.path]), [['occ-t1', '/music/1.flac'], ['occ-t2', '/music/2.flac']]);
  assert.deepEqual(nativeSplitCandidates(session.getSnapshot()).map(candidate => [candidate.id, candidate.ordinal]), [['occ-t1', 1], ['occ-t2', 2]]);
  assert.equal(nativeFileInfoFile(session.getSnapshot(), 'occ-t2')?.file.path, '/music/2.flac');
  session.retire();
});
