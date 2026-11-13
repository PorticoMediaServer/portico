import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {nativeFileInfoFile, nativeFileInfoShowVersion, nativeFileInfoView, nativeSplitCandidates} from '../src-next/screens/watch/file-info.ts';
import {fakeSession} from './helpers/file-info-windows.ts';
const text = {t: defaultI18n.t};
const settle = () => new Promise(resolve => setImmediate(resolve));

test('phone and TV project the real stored versions and tracks, with no legacy File info routes', async () => {
  const {session, calls} = fakeSession('member');
  await session.groups();
  for (const group of session.getSnapshot().groups.rows!.rows) nativeFileInfoShowVersion(session, group);
  await settle();
  for (const tv of [false, true]) {
    const view = nativeFileInfoView(session.getSnapshot(), 'movie', text, tv)!;
    assert.equal(view.versionCount, 2); assert.equal(view.model.files[0].audio[0].name.text, 'English');
    assert.equal(view.model.files[1].subtitles.length, 1, 'a sidecar is offered even when the physical file has no embedded subtitle track');
    assert.equal(view.model.owner, undefined); assert.equal(view.locations, undefined);
  }
  session.retire(); assert.deepEqual(calls, ['GET /v1/items/movie', 'GET /v1/items/movie/playback-options']);
});

test('owner and administrator see every path; split targets whole versions, including multipart versions', async () => {
  for (const role of ['owner', 'admin']) {
    const {session, calls} = fakeSession(role);
    await session.groups();
    for (const group of session.getSnapshot().groups.rows!.rows) nativeFileInfoShowVersion(session, group);
    await settle();
    const view = nativeFileInfoView(session.getSnapshot(), 'movie', text, false)!;
    assert.deepEqual(view.locations?.map(l => [l.versionId, l.path]), [['g1', '/private/g1-0.mkv'], ['g1', '/private/g1-1.mkv'], ['g2', '/private/g2-0.mkv']]);
    assert.deepEqual(nativeSplitCandidates(session.getSnapshot()).map(v => [v.id, v.occurrence]), [['g1', 'g1'], ['g2', 'g2']]);
    assert.equal(nativeFileInfoFile(session.getSnapshot(), 'g1:1')?.file.id, 'g1');
    assert.deepEqual(view.model.owner!.actions.map(a => a.id), ['split-versions', 'merge-title']);
    assert.equal(view.locations!.some(l => l.actions.length > 0), false, 'no Delete until the server can preview this version');
    session.retire(); assert.deepEqual(calls, ['GET /v1/items/movie', 'GET /v1/items/movie/files', 'GET /v1/items/movie/playback-options']);
  }
});
