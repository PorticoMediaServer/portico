import test from 'node:test';
import assert from 'node:assert/strict';
import {readTVServerPage, type TVReadOnlyPage} from '../src-next/screens/settings-live/server/tv-server-page-data.ts';
import {captureReportContext} from '../../../packages/client-core/src/report-context.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
const report = captureReportContext('UTC');
function fixture(responses: Record<string, unknown>, raw: Record<string, unknown> = {}) {
  const reads: string[] = [];
  const api = {request: async <T,>(path: string, method = 'GET', body?: unknown): Promise<T> => {
    assert.equal(method, 'GET');assert.equal(body, undefined);reads.push(path);
    assert.ok(Object.hasOwn(responses, path), `unexpected read ${path}`);return responses[path] as T;
  }, requestRaw: async (path: string, method: string) => {assert.equal(method, 'GET');reads.push(path);assert.ok(Object.hasOwn(raw, path), `unexpected raw read ${path}`);return {status: 200, headers: {ETag: '"4"'}, body: raw[path]};}};
  return {reads, read: (page: TVReadOnlyPage) => readTVServerPage(page, api, () => true, report, '', 'server', defaultI18n.t, 'en-US', new AbortController().signal)};
}

test('TV Metadata and Intros read rebuilt documents and project values without provider accounts or actions', async () => {
  const f = fixture({'/v1/admin/metadata/settings': {revision: 1, onlineLookups: true, includeAdult: false, defaultLanguage: 'en', defaultRegion: 'US', artworkPreference: 'folders', artworkLanguage: 'textless', artworkMaxEdge: 1920,
    tmdbCredentialConfigured: true, openSubtitlesUsername: 'private-email@example.com', openSubtitlesPasswordConfigured: true},
    '/v1/admin/settings/segments': {etag: '"2"', values: {mode: 'standard', useChapterNames: true}}});
  const metadata = await f.read('metadata');
  assert.ok(metadata.facts.some(row => row.id === 'metadata.online' && row.value === defaultI18n.t('next.settings.on')));
  assert.ok(metadata.facts.some(row => row.id === 'metadata.artworkPrefer'));
  assert.ok(!JSON.stringify(metadata).includes('private-email'));
  assert.ok(!metadata.facts.some(row => /tmdb|osUser|osPass|refreshAll/.test(row.id)));
  const segments = await f.read('segments');assert.ok(segments.facts.some(row => row.id === 'segments.chapters'));
  assert.deepEqual(f.reads, ['/v1/admin/metadata/settings', '/v1/admin/settings/segments']);
});

test('TV Live reads real source/live/DVR documents; source credentials and user agent never reach the view', async () => {
  const f = fixture({'/v1/admin/live/sources': {items: [{id: 'source', name: 'Television', revision: '1', kind: 'm3u', state: 'healthy', paused: true, tunerCount: 1, hasSignIn: true, config: {playlistUrl: 'https://private:secret@example.com/stream'}}]},
    '/v1/admin/settings/live': {etag: '"2"', values: {bufferSeconds: 5, retrySeconds: 10, guideDays: 7, rewindMinutes: 60, importImages: true, discoveryEnabled: false, userAgent: 'private-agent'}},
    '/v1/admin/settings/dvr': {etag: '"3"', values: {folder: '/private/folder', beforeMs: 60000, afterMs: 180000, floorBytes: 10 * 1024 ** 3, capBytes: 0, keepDays: 30, keepNewest: 0, stopViewers: true}}});
  const result = await f.read('live');
  assert.ok(result.facts.some(row => row.id === 'source' && row.label === 'Television'));
  for (const id of ['live.guideDays', 'live.rec.conflict', 'live.rec.early']) assert.ok(result.facts.some(row => row.id === id), id);
  assert.ok(!JSON.stringify(result).includes('private'));
});

test('TV Integrations counts all rebuilt key pages and exposes no key names, notes or webhook addresses', async () => {
  const key = (id: string) => ({id, name: 'private-key-name', scopes: [], state: 'active', createdAt: '2026-11-01T00:00:00Z', note: 'private-note'});
  const f = fixture({'/v1/admin/api-keys': {items: [key('a'), key('b')], nextCursor: 'next'}, '/v1/admin/api-keys?cursor=next': {items: [key('c')]}, '/v1/admin/webhooks': {items: [], events: [], maximum: 10}});
  const result = await f.read('integrations');
  assert.deepEqual(result.facts.map(row => [row.id, row.value]), [['integrations.apiKeys', '3'], ['integrations.webhooks', '0']]);
  assert.ok(!JSON.stringify(result).includes('private'));
});

test('TV Storage reads measurements and notification retention without any generated-files setting', async () => {
  const f = fixture({'/v1/admin/settings/notices': {etag: '"1"', values: {keepDays: 30}}, '/v1/admin/storage': {dataFolder: '/data', diskBytes: 10000, freeBytes: 4000, folders: [{name: 'database', bytes: 100}, {name: 'artwork', bytes: 900}]}});
  const result = await f.read('storage');
  assert.deepEqual(result.facts.map(row => row.id), ['storage.keepNotifications', 'storage.free', 'storage.own', 'storage.database']);
  assert.equal(result.facts.find(row => row.id === 'storage.own')!.value, defaultI18n.bytes(1000));
});


test('TV General uses per-area documents and shows the running version without a configuration request', async () => {
  const documents = {general: {name: 'Home server', backgroundSpeed: 'normal', updateCheck: 'automatic', heavyWork: {hours: 'any'}}, storage: {warnPercent: 10, majorPercent: 5}, notices: {serverNoticesTo: 'owner'}};
  const responses = Object.fromEntries(Object.entries(documents).map(([name, values]) => [`/v1/admin/settings/${name}`, {name, etag: '"3"', values, defaults: values}]));
  const f = fixture({...responses, '/v1/admin/updates': {version: '0.1-test'}}, {'/v1/admin/settings/remote-access': {advertisedInterface: '', interfaces: [], certificateWarningDays: 14}});
  const result = await f.read('general');
  assert.equal(result.facts.find(row => row.id === 'general.name')!.value, 'Home server');
  assert.equal(result.facts.find(row => row.id === 'general.version')!.value, '0.1-test');
  assert.ok(f.reads.includes('/v1/admin/settings/general'));
});

test('TV Remote uses real status and raw GET documents without exposing identity codes, certificate material or notes', async () => {
  const f = fixture({'/v1/admin/remote-access': {thisDevice: {atHome: true, address: '10.0.0.2'}, refused: [], warnings: [{message: 'private-note'}], remoteSecurity: {chosen: 'standard', active: 'standard'}, certificate: {state: 'ok', name: 'private-certificate'}, identityCode: 'private-identity', managedCertificate: {ready: true, state: 'serving', name: 'private-certificate'}}, '/v1/admin/blocked-networks': {items: []}},
    {'/v1/admin/settings/remote-access': {advertisedInterface: '', interfaces: [], remoteAccess: 'on', remoteSecurity: 'standard', secureConnections: 'preferred', signInFromOutside: 'anyone', portMapping: 'automatic', publicPort: 32500, ipv6: false, findAtHome: true, dlna: false, ownAddress: {enabled: true, domain: 'private-domain', certificateFile: '/private/cert', keyFile: '/private/key'}, allowedAddresses: []},
      '/v1/admin/settings/network': {homeNetworks: ['10.0.0.0/24'], trustedProxies: [], warnings: []}});
  const result = await f.read('remote');
  for (const id of ['remote.thisDevice', 'remote.access', 'remote.secureStatus']) assert.ok(result.facts.some(row => row.id === id), id);
  assert.ok(!JSON.stringify(result).includes('private'));
});
