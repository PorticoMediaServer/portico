import test from 'node:test';
import assert from 'node:assert/strict';
import type {RemoteSource, RemoteState} from '../../../packages/client-core/src/remote-sources';
import {currentSourceGeneration, sourceBackMode, sourceWizardStep, type SourceMode} from '../src-next/screens/settings/remote-sources/source-flow.ts';
import {connectionTestField, connectionTestTone, readingDiffers, sourceLimitValue} from '../src-next/screens/settings/remote-sources/reading-presentation.ts';

const source: RemoteSource = {id: 'archive', generation: 2, kind: 'webdav', name: 'Archive', rootPath: '/media', state: 'ready', rangeSupport: 'conditional', insecureLocal: false, credentialPresent: true};
const folder = {name: 'Movies', path: '/media/Movies', relative: 'Movies'};

test('the creation wizard has five stages and leaves saved-source management unnumbered', () => {
  const stages: SourceMode[] = [{kind: 'choose', wizard: true}, {kind: 'connection', wizard: true}, {kind: 'reading', source, wizard: true},
    {kind: 'folders', source, chosen: [], from: 'reading', wizard: true}, {kind: 'complete', source, wizard: true}];
  assert.deepEqual(stages.map(sourceWizardStep), [1, 2, 3, 4, 5]);
  assert.equal(sourceWizardStep({kind: 'reading', source}), undefined);
  assert.equal(sourceWizardStep({kind: 'assignment', source, folders: [folder], wizard: true}), 4);
});

test('Back preserves the saved source and chosen folders, and never reopens a completed assignment', () => {
  const assignment: SourceMode = {kind: 'assignment', source, folders: [folder], wizard: true, connection: 'connection'};
  const previous = sourceBackMode(assignment)!;
  assert.equal(previous.kind, 'folders');
  assert.deepEqual('chosen' in previous && previous.chosen, [folder]);
  assert.equal('source' in previous && previous.source, source);
  assert.deepEqual(sourceBackMode(assignment, true), {kind: 'detail', source});
  assert.deepEqual(sourceBackMode({kind: 'reading', source, wizard: true, connection: 'connection'}), {kind: 'connection', source, wizard: true, connection: 'connection'});
  assert.equal(sourceBackMode({kind: 'choose', wizard: true}), undefined);
});

test('a confirmed write generation can advance the wizard before the directory refresh', () => {
  const state: Pick<RemoteState, 'sources' | 'receipt'> = {sources: [{...source, generation: 1}], receipt: {operationId: 'source-operation', accepted: true, source, removed: false}};
  assert.equal(currentSourceGeneration(source, state), true);
  assert.equal(currentSourceGeneration({...source, generation: 1}, state), false);
  assert.equal(currentSourceGeneration(source, {...state, sources: [{...source, generation: 3}]}), false);
  assert.equal(currentSourceGeneration(source, {...state, receipt: {...state.receipt!, removed: true}}), false);
});

test('limit sheets accept only the service bounds and convert MB per second once', () => {
  assert.equal(sourceLimitValue('connections', '32'), 32);
  assert.equal(sourceLimitValue('requestsPerSecond', '50'), 50);
  assert.equal(sourceLimitValue('bytesPerSecond', '25'), 25_000_000);
  assert.equal(sourceLimitValue('bytesPerSecond', ''), 0);
  for (const input of ['0', '33', '-1', '1.5', '', '1e2']) assert.equal(sourceLimitValue('connections', input), undefined, input);
  assert.equal(sourceLimitValue('requestsPerSecond', '51'), undefined);
  assert.equal(sourceLimitValue('bytesPerSecond', '1001'), undefined);
});

test('reading reset comparisons never treat analysis consent as a reading limit', () => {
  const baseline = {scanDepth: 'basic' as const, wholeFileReads: false, connections: 4, requestsPerSecond: 10, bytesPerSecond: 0};
  assert.equal(readingDiffers({...baseline, allowAdvancedMediaAnalysis: true}, baseline), false);
  assert.equal(readingDiffers({...baseline, connections: 5}, baseline), true);
  assert.equal(readingDiffers({...baseline, scanDepth: 'complete'}, baseline), true);
});

test('connection test presentation distinguishes unknown range support and only names a rejected credential field', () => {
  assert.equal(connectionTestTone({origin: 'https://storage.example', rangeSupport: 'conditional'}), 'success');
  assert.equal(connectionTestTone({origin: 'https://storage.example', rangeSupport: 'unsupported'}), 'limited');
  assert.equal(connectionTestTone({origin: 'https://storage.example', rangeSupport: 'unknown'}), 'unknown');
  assert.equal(connectionTestField({code: 'remote_credentials_required'}), 'password', 'the service normalizes provider refusal to this code');
  assert.equal(connectionTestField({code: 'source_credentials_required'}), 'password');
  assert.equal(connectionTestField({code: 'remote_invalid_configuration'}), undefined);
  assert.equal(connectionTestField({status: 401}), undefined, 'owner authentication failures are not blamed on storage credentials');
});
