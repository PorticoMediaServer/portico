import test from 'node:test';
import assert from 'node:assert/strict';
import {decode} from '../src/runtime.ts';
import {schemas} from '../src/schemas.ts';

test('Sonic settings require both the explicit choice and its CAS revision', () => {
  assert.doesNotThrow(() => decode('PutAdminLibrariesIdSonicAnalysisRequest', {enabled: false, expectedRevision: 1}));
  for (const input of [{enabled: true}, {expectedRevision: 1}, {enabled: null, expectedRevision: 1}, {enabled: 'true', expectedRevision: 1}, {enabled: true, expectedRevision: '1'}]) {
    assert.throws(() => decode('PutAdminLibrariesIdSonicAnalysisRequest', input));
  }
});

test('the owner Sonic projection publishes bounded state codes and no raw runtime facts', () => {
  const status = {libraryId: 'library', available: true, defaultEnabled: true, enabled: true, revision: 1, policyAllows: true, state: 'complete', done: 1, total: 1, policyRevision: 1, observedAtMs: 1};
  assert.doesNotThrow(() => decode('GetAdminLibrariesIdSonicAnalysisResponse', {...status, available: true, enabled: false, policyAllows: false, state: 'disabled'}));
  assert.doesNotThrow(() => decode('GetAdminLibrariesIdSonicAnalysisResponse', {...status, available: false, state: 'unavailable'}));
  for (const state of ['disabled', 'unavailable', 'complete', 'paused', 'analyzing']) {
    assert.doesNotThrow(() => decode('GetAdminLibrariesIdSonicAnalysisResponse', {...status, state}));
  }
  for (const reasonCode of ['inference_failed', 'inference_not_verified', 'runtime_or_model_missing', 'scan_policy_disallows']) {
    assert.doesNotThrow(() => decode('GetAdminLibrariesIdSonicAnalysisResponse', {...status, reasonCode}));
  }
  for (const input of [{...status, available: undefined}, {...status, available: null}, {...status, available: 'true'}, {...status, available: 1}, {...status, state: 'ready'}, {...status, reasonCode: '/private/runtime-error'}, {...status, estimatedRemainingSeconds: '1'}]) {
    assert.throws(() => decode('GetAdminLibrariesIdSonicAnalysisResponse', input));
  }
  const properties = schemas.GetAdminLibrariesIdSonicAnalysisResponse.properties;
  assert.ok(schemas.GetAdminLibrariesIdSonicAnalysisResponse.required.includes('available'));
  assert.equal(properties.available.type, 'boolean');
  for (const key of ['work', 'runtime', 'path', 'vector', 'error']) assert.equal(key in properties, false);
});

test('every Sonic status response requires the factory default independently of saved choice and runtime availability', () => {
  const status = {libraryId: 'library', available: false, defaultEnabled: true, enabled: false, revision: 2, policyAllows: false, state: 'disabled', done: 0, total: 1, policyRevision: 1, observedAtMs: 1};
  for (const name of ['GetAdminLibrariesIdSonicAnalysisResponse', 'PutAdminLibrariesIdSonicAnalysisResponse', 'PostAdminLibrariesIdSonicAnalysisRetryResponse'] as const) {
    for (const available of [true, false]) {
      assert.doesNotThrow(() => decode(name, {...status, available}));
    }
    for (const defaultEnabled of [undefined, null, 'true', 1]) {
      assert.throws(() => decode(name, {...status, defaultEnabled}));
    }
    assert.ok(schemas[name].required.includes('defaultEnabled'));
    assert.equal(schemas[name].properties.defaultEnabled.type, 'boolean');
    assert.ok(schemas[name].required.includes('available'));
    assert.equal(schemas[name].properties.available.type, 'boolean');
  }
});

test('the existing similar payload declares its actual sound selection basis', () => {
  const data = schemas.GetItemsIdSimilarResponse.properties.data;
  assert.ok(data.required.includes('bySound'));
  assert.equal(data.properties.bySound.type, 'boolean');
});
