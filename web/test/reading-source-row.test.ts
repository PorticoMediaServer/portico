import assert from 'node:assert/strict';
import test from 'node:test';
import type {RemoteSource, SourceReadSettings} from '../../packages/client-core/src/remote-sources.ts';
import {acceptedReadingSource, configuredReadingSource} from '../src/screens/settings/remote-sources/reading-source-row.ts';

// Authored UNRUN. These are the actual production identity checks; the original
// form/route/SDK receipt guards remain in the wizard and are not simulated here.
const original: RemoteSource = Object.freeze({id: 'source', kind: 'webdav', name: 'Source', rootPath: '/dav', generation: 7,
  state: 'ready', rangeSupport: 'conditional', insecureLocal: false, credentialPresent: true});
const profile = {sourceId: 'source', generation: '8'} as SourceReadSettings;
test('configuration adopts only its exact generation and preserves the actual source object', () => {
  assert.equal(configuredReadingSource([original], {sourceId: 'source', generation: 7}), original);
  assert.equal(configuredReadingSource([{...original, generation: 8}], {sourceId: 'source', generation: 7}), undefined);
  assert.equal(configuredReadingSource([{...original, id: 'other'}], {sourceId: 'source', generation: 7}), undefined);
});
test('only the SDK-accepted child raw generation can enter the folder step', () => {
  const child = Object.freeze({...original, generation: 8});
  assert.equal(acceptedReadingSource([original, child], profile), child);
  assert.equal(acceptedReadingSource([original], profile), undefined);
  assert.equal(acceptedReadingSource([{...child, generation: 9}], profile), undefined);
  assert.equal(acceptedReadingSource([{...child, id: 'other'}], profile), undefined);
});
test('missing readback never fabricates a source or accepts a coerced generation spelling', () => {
  assert.equal(acceptedReadingSource([], profile), undefined);
  assert.equal(acceptedReadingSource([original], {...profile, generation: '07'}), undefined);
  assert.equal(configuredReadingSource([], {sourceId: 'source', generation: 7}), undefined);
});
