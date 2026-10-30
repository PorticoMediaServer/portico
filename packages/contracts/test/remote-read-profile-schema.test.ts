import test from 'node:test';
import assert from 'node:assert/strict';
import {decode} from '../src/runtime.ts';

const scope = {serverId: 'server', viewerFence: 'owner'};
const values = {scanDepth: 'file_list_only', wholeFileReads: false, connections: 1, requestsPerSecond: 1, bytesPerSecond: 0};
const settings = {sourceId: 'source', generation: '1', revision: 0, saved: false, values};
const change = {operationId: '1760000000000-11111111-1111-4111-8111-111111111111', expectedGeneration: '1', expectedRevision: 0, values};
const source = {id: 'source', kind: 'rclone', name: 'Media', rootPath: 'media:', generation: 1, state: 'ready', rangeSupport: 'unknown', insecureLocal: false, credentialPresent: false};
const receipt = {operationId: change.operationId, accepted: true, removed: false, source};

test('read-profile requests preserve complete false and zero values with real profile bounds', () => {
  const decoded = decode('PutStorageSourceReadProfileRequest', change) as typeof change;
  assert.equal(decoded.expectedRevision, 0);
  assert.equal(decoded.values.wholeFileReads, false);
  assert.equal(decoded.values.bytesPerSecond, 0);
  for (const scanDepth of ['file_list_only', 'basic', 'complete']) {
    assert.doesNotThrow(() => decode('PutStorageSourceReadProfileRequest', {...change, values: {...values, scanDepth}}));
  }
  for (const bytesPerSecond of [0, 1_000_000, 2_000_000, 1_000_000_000]) {
    assert.doesNotThrow(() => decode('PutStorageSourceReadProfileRequest', {...change, values: {...values, bytesPerSecond}}));
  }
  for (const key of Object.keys(change)) {
    const missing = {...change} as Record<string, unknown>;
    delete missing[key];
    assert.throws(() => decode('PutStorageSourceReadProfileRequest', missing));
  }
  for (const key of Object.keys(values)) {
    const missing = {...values} as Record<string, unknown>;
    delete missing[key];
    assert.throws(() => decode('PutStorageSourceReadProfileRequest', {...change, values: missing}));
    assert.throws(() => decode('PutStorageSourceReadProfileRequest', {...change, values: {...values, [key]: null}}));
  }
  for (const patch of [
    {scanDepth: 'deep'}, {wholeFileReads: 0}, {connections: 0}, {connections: 33},
    {requestsPerSecond: 0}, {requestsPerSecond: 51}, {bytesPerSecond: -1},
    {bytesPerSecond: 999_999}, {bytesPerSecond: 1_000_001}, {bytesPerSecond: 1_500_000},
    {bytesPerSecond: 1_001_000_000}, {privatePath: '/not-a-wire-field'},
  ]) assert.throws(() => decode('PutStorageSourceReadProfileRequest', {...change, values: {...values, ...patch}}));
  for (const patch of [{expectedGeneration: ''}, {expectedRevision: -1}, {values: null}, {operationId: 'short'}, {privateKey: 'not-a-field'}]) {
    assert.throws(() => decode('PutStorageSourceReadProfileRequest', {...change, ...patch}));
  }
});

test('read-profile GET requires captured settings without inventing saved values', () => {
  const decoded = decode('GetStorageSourceReadProfileResponse', {scope, settings}) as {settings: typeof settings};
  assert.equal(decoded.settings.saved, false);
  assert.equal(decoded.settings.revision, 0);
  assert.equal(decoded.settings.values.wholeFileReads, false);
  assert.equal(decoded.settings.values.bytesPerSecond, 0);
  for (const patch of [{sourceId: ''}, {generation: ''}, {revision: -1}, {saved: null}, {values: null}]) {
    assert.throws(() => decode('GetStorageSourceReadProfileResponse', {scope, settings: {...settings, ...patch}}));
  }
  // Responses are open: a member that is not a wire field is dropped.
  const extended = decode('GetStorageSourceReadProfileResponse', {scope, settings: {...settings, path: '/not-a-wire-field'}}) as {settings: Record<string, unknown>};
  assert.equal(Object.hasOwn(extended.settings, 'path'), false);
});

test('receipts preserve additive response projection and validate present read-profile facts', () => {
  const names = [
    'GetStorageSourceOperationsIdResponse', 'PostStorageSourcesWebdavResponse',
    'PutStorageSourcesWebdavIdResponse', 'DeleteStorageSourcesWebdavIdResponse',
    'PutStorageSourceReadProfileResponse',
  ] as const;
  for (const name of names) {
    assert.doesNotThrow(() => decode(name, {scope, receipt}));
    const decoded = decode(name, {scope, receipt: {...receipt, readSettings: settings}}) as {receipt: {readSettings: typeof settings}};
    assert.equal(decoded.receipt.readSettings.values.bytesPerSecond, 0);
    const absent = decode(name, {scope, receipt: {...receipt, readSettings: null, futureFact: true}}) as {receipt: Record<string, unknown>};
    assert.equal(Object.hasOwn(absent.receipt, 'readSettings'), false);
    assert.equal(Object.hasOwn(absent.receipt, 'futureFact'), false);
    assert.throws(() => decode(name, {scope, receipt: {...receipt, readSettings: {sourceId: 'source'}}}));
    assert.throws(() => decode(name, {scope, receipt: {...receipt, readSettings: {...settings, values: {...values, wholeFileReads: 'false'}}}}));
  }
});
