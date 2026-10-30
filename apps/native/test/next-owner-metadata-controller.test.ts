import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import type {MetadataSettings} from '../../../packages/client-core/src/metadata-settings';
import * as values from '../src-next/screens/settings-live/server/metadata-setting-values.ts';

const data = (revision = 1) => ({revision, localArtworkLimitBytes: 2 ** 30, localArtworkUsedBytes: 0, includeAdult: false, artworkMaxEdge: 1920,
  tmdbCredentialConfigured: false, tvdbCredentialConfigured: false, openSubtitlesKeyConfigured: false, openSubtitlesPasswordConfigured: false, openSubtitlesUsername: '',
  librariesComplete: true, libraries: [{id: 'series', name: 'Series', kind: 'tv', revision: 'a'.repeat(64), agent: 'online', agentOptions: ['online', 'local'], lyricsSource: 'files', episodeThumbnailMode: 'provider', scrubPreviews: false, canScrub: true, managed: true}]});

const shared = await screenModule(new URL('../../../packages/client-core/src/metadata-settings.ts', import.meta.url), {
  react: {default: {}}, '../../contracts/src/routes.ts': {routePath: (id: string) => id}, '../../contracts/src/route-paths.ts': {routePath: (id: string) => id}, './server-messages.ts': {unreadableServerResponse: 'unreadable'},
});
const {NativeMetadataController} = await screenModule(new URL('../src-next/screens/settings-live/server/metadata-controller.ts', import.meta.url), {
  react: {default: {}}, '../../../../../../packages/client-core/src/metadata-settings': shared, './metadata-setting-values': values,
});

function fixture() {
  const calls: {path: string; method: string; body: any; signal: AbortSignal}[] = [];
  let reply = async (_call: typeof calls[number]): Promise<unknown> => data();
  let current = true;
  const controller = new NativeMetadataController({request: async (path: string, method = 'GET', body?: unknown, signal?: AbortSignal) => {
    const call = {path, method, body, signal: signal!}; calls.push(call); return reply(call);
  }}, () => current);
  return {controller, calls, setReply: (next: typeof reply) => {reply = next;}, retire: () => {current = false;}};
}

test('one metadata row writes the actual revision and required unchanged values without credential fields', async () => {
  const f = fixture(); await f.controller.load();
  const original = f.controller.getSnapshot().data;
  f.setReply(async () => ({...data(2), includeAdult: true}));
  assert.equal(await f.controller.save(original, {includeAdult: true}), true);
  assert.deepEqual(f.calls[1]!.body, {expectedRevision: 1, localArtworkLimitBytes: 2 ** 30, includeAdult: true});
  assert.equal(f.controller.getSnapshot().data.revision, 2);
  assert.equal(await f.controller.save(original, {includeAdult: false}), false, 'a retained old row cannot dispatch');
  assert.equal(f.calls.length, 2); f.controller.dispose();
});

test('episode thumbnails use the separate complete producer and fence the exact library revision', () => {
  const settings = shared.parseMetadataSettings(data()) as MetadataSettings;
  assert.deepEqual(values.episodeThumbnailChange(settings, 'series', 'off'), {
    expectedRevision: 1, localArtworkLimitBytes: 2 ** 30, includeAdult: false,
    libraries: [{id: 'series', expectedRevision: 'a'.repeat(64), episodeThumbnailMode: 'off'}],
  });
  assert.equal(values.episodeThumbnailChange({...settings, librariesComplete: false}, 'series', 'off'), undefined);
  assert.equal(values.episodeThumbnailChange({...settings, libraries: [{...settings.libraries![0]!, managed: false}]}, 'series', 'off'), undefined);
  assert.equal(values.episodeThumbnailChange(settings, 'other', 'off'), undefined);
  assert.equal(values.episodeThumbnailChange(settings, 'series', 'unknown'), undefined);
});

test('an uncertain or stale save requires a fresh read and does not replay the mutation', async () => {
  const f = fixture(); await f.controller.load(); const original = f.controller.getSnapshot().data;
  f.setReply(async () => {throw {code: 'revision_mismatch'};});
  assert.equal(await f.controller.save(original, {includeAdult: true}), false);
  assert.equal(f.controller.getSnapshot().needsReload, true);
  assert.equal(await f.controller.save(original, {includeAdult: true}), false); assert.equal(f.calls.length, 2);
  f.setReply(async () => data(3)); await f.controller.load();
  assert.equal(f.calls[2]!.method, 'GET'); assert.equal(f.controller.getSnapshot().needsReload, false); f.controller.dispose();
});

test('saving is single-flight and a disposed or retired page cannot adopt or dispatch work', async () => {
  const f = fixture(); await f.controller.load(); const original = f.controller.getSnapshot().data;
  let finish: (reply: unknown) => void = () => {};
  f.setReply(() => new Promise(resolve => {finish = resolve;}));
  const save = f.controller.save(original, {includeAdult: true});
  assert.equal(await f.controller.save(original, {includeAdult: false}), false); assert.equal(f.calls.length, 2);
  f.controller.dispose(); assert.equal(f.calls[1]!.signal.aborted, true);
  finish({...data(2), includeAdult: true}); assert.equal(await save, false);
  assert.equal(f.controller.getSnapshot().data, original); await f.controller.load(); assert.equal(f.calls.length, 2);
  const other = fixture(); await other.controller.load(); other.retire(); await other.controller.load();
  assert.equal(await other.controller.save(other.controller.getSnapshot().data, {includeAdult: true}), false); assert.equal(other.calls.length, 1); other.controller.dispose();
});

test('credential drafts never enter the metadata snapshot and subtitle tests use only saved settings', async () => {
  const f = fixture(); await f.controller.load();
  f.setReply(async () => ({...data(2), tmdbCredentialConfigured: true}));
  await f.controller.save(f.controller.getSnapshot().data, {tmdbReadToken: 'synthetic-new-value'});
  assert.equal(JSON.stringify(f.controller.getSnapshot()).includes('synthetic-new-value'), false);
  assert.equal(f.calls[1]!.body.tmdbReadToken, 'synthetic-new-value');
  f.setReply(async () => ({keySource: 'owner', accountSource: 'owner', account: 'signed_in', downloadsRemaining: 7, downloadsAllowed: 10}));
  await f.controller.checkOpenSubtitles(f.controller.getSnapshot().data);
  assert.equal(f.calls[2]!.method, 'POST'); assert.equal(f.calls[2]!.body, undefined);
  assert.equal(f.controller.getSnapshot().data.openSubtitles.account, 'signed_in'); assert.equal(f.controller.getSnapshot().data.revision, 2); f.controller.dispose();
});

test('Refresh all posts once from the current document, and Advanced choices save as one revisioned change', async () => {
  const advanced = {refreshPolicy: 'regular', fallbackLanguage: 'en', animeTitles: 'language', castPhotos: true, acoustidKeyConfigured: false, acoustidKeySource: 'none', refreshAll: {running: false}};
  const f = fixture(); f.setReply(async () => ({...data(), ...advanced})); await f.controller.load();
  const original = f.controller.getSnapshot().data as MetadataSettings;
  assert.equal(original.advanced?.refreshPolicy, 'regular');
  f.setReply(async () => ({...data(), ...advanced, refreshAll: {running: true, requestedAt: '2026-10-28T10:00:00Z'}}));
  assert.equal(await f.controller.refreshAll(original), true);
  assert.deepEqual([f.calls[1]!.path, f.calls[1]!.method], ['refresh_all_metadata', 'POST']);
  const after = f.controller.getSnapshot().data as MetadataSettings;
  assert.equal(after.advanced?.refreshAll.running, true);
  assert.equal(await f.controller.refreshAll(original), false, 'a retained old document cannot dispatch');
  f.setReply(async () => ({...data(2), ...advanced, castPhotos: false}));
  assert.equal(await f.controller.save(after, {castPhotos: false, acoustidKey: 'owner-key'}), true);
  assert.deepEqual(f.calls.at(-1)!.body, {expectedRevision: 1, localArtworkLimitBytes: 2 ** 30, includeAdult: false, castPhotos: false, acoustidKey: 'owner-key'});
  assert.equal(JSON.stringify(f.controller.getSnapshot()).includes('owner-key'), false, 'the key is never kept');
  f.controller.dispose();
});
