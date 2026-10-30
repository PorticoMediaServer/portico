import test from 'node:test';
import assert from 'node:assert/strict';
import {metadataSettingsService, parseMetadataSettings} from '../src/metadata-settings.ts';
import {decode} from '../../contracts/src/runtime.ts';
import {schemas} from '../../contracts/src/schemas.ts';

const settings = {revision: 4, localArtworkLimitBytes: 2 ** 31, localArtworkUsedBytes: 17, includeAdult: true, artworkMaxEdge: 1280,
  tmdbCredentialConfigured: false, tvdbCredentialConfigured: false, openSubtitlesKeyConfigured: false,
  openSubtitlesUsername: '', openSubtitlesPasswordConfigured: false,
  openSubtitles: {keySource: 'none', accountSource: 'none', account: 'none', downloadsRemaining: -1, downloadsAllowed: -1},
  refreshPolicy: 'regular', fallbackLanguage: 'en', animeTitles: 'language', castPhotos: true, acoustidKeyConfigured: false, acoustidKeySource: 'none', refreshAll: {running: false}};

test('metadata reads require an actual supported edge without a factory fallback', () => {
  for (const artworkMaxEdge of [1280, 1920, 2560]) {
    const parsed = parseMetadataSettings({...settings, artworkMaxEdge, tmdbReadToken: 'private'});
    assert.equal(parsed.artworkMaxEdge, artworkMaxEdge);
    assert.equal(parsed.localArtworkLimitBytes, settings.localArtworkLimitBytes);
    assert.equal(parsed.includeAdult, true);
    assert.equal('tmdbReadToken' in parsed, false);
    assert.ok(Object.isFrozen(parsed));
    assert.doesNotThrow(() => decode('GetMetadataSettingsResponse', {...settings, artworkMaxEdge}));
  }
  for (const artworkMaxEdge of [undefined, null, '1920', true, 0, 1921, 3840, NaN, Infinity]) {
    assert.throws(() => parseMetadataSettings({...settings, artworkMaxEdge}));
    assert.throws(() => decode('GetMetadataSettingsResponse', {...settings, artworkMaxEdge}));
  }
});

test('metadata writes preserve required limit/adult values and admit only an optional supported edge', async () => {
  const calls: Array<{path: string; method?: string; body?: unknown}> = [];
  const service = metadataSettingsService(async <T>(path: string, method?: string, body?: unknown) => {
    calls.push({path, method, body});
    return settings as T;
  });
  const change = {expectedRevision: 4, localArtworkLimitBytes: 2 ** 32, includeAdult: false};
  assert.equal((await service.read()).artworkMaxEdge, 1280);
  await service.save(change);
  assert.deepEqual(calls[1], {path: '/v1/admin/metadata/settings', method: 'PUT', body: change});
  for (const artworkMaxEdge of [1280, 1920, 2560] as const) {
    const input = {...change, artworkMaxEdge};
    assert.equal((await service.save(input)).artworkMaxEdge, 1280, 'use the actual returned snapshot');
    assert.deepEqual(calls.at(-1)?.body, input);
    assert.doesNotThrow(() => decode('PutMetadataSettingsRequest', input));
  }
  const count = calls.length;
  for (const artworkMaxEdge of [null, '1920', true, 0, 1921, 3840, NaN, Infinity]) {
    await assert.rejects(() => Reflect.apply(service.save, service, [{...change, artworkMaxEdge}]));
  }
  assert.equal(calls.length, count, 'invalid edges never reach transport');
});

test('metadata schemas retain required limit/adult fields and the exact edge vocabulary', () => {
  const read = schemas.GetMetadataSettingsResponse;
  const write = schemas.PutMetadataSettingsRequest;
  assert.deepEqual(read.properties.artworkMaxEdge.enum, [1280, 1920, 2560]);
  assert.deepEqual(write.properties.artworkMaxEdge.enum, [1280, 1920, 2560]);
  assert.equal(new Set<string>(read.required).has('artworkMaxEdge'), true);
  assert.equal(new Set<string>(write.required).has('artworkMaxEdge'), false);
  for (const name of ['localArtworkLimitBytes', 'includeAdult']) {
    assert.equal(new Set<string>(write.required).has(name), true);
  }
});
