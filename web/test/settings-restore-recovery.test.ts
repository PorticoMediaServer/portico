import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '@i18n';
import {parseRestoreStaged} from '@core/administration.ts';
import {componentModule, hooks} from './helpers/component-harness.mjs';
import {restoreRecoveryMessage, restoreRefusalMessage} from '../src/screens/settings/server/restore.ts';

const region = {firstLine: 2, lastLine: 3, startByte: 1024, endByte: 2048, reason: 'payload_hash_mismatch'};
const receipt = parseRestoreStaged({staged: true, restartRequired: true, validation: {schemaVersion: 41, integrity: 'recovered', recovery: {
  indexesRebuilt: true, manifestDamage: {count: 24, regions: Array.from({length: 20}, () => region), regionsTruncated: true, missingFiles: 3, unverifiedDatabase: true},
}}});
const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : !value?.props ? [] : [value, ...nodes(value.props.children)];
const text = (value: any): string => Array.isArray(value) ? value.map(text).join(' ') : value?.props ? text(value.props.children) : typeof value === 'string' ? value : '';

async function fixture(state: any) {
  const host = hooks();
  const module = await componentModule(new URL('../src/screens/settings/server/RestoreStatus.tsx', import.meta.url), {
    react: host.react, '@i18n': {defaultI18n}, '../../../kit/controls': {Button: 'button'},
    './restore': {restoreRecoveryMessage, restoreRefusalMessage}, './server.module.css': {default: {}},
  });
  return () => host.render(() => module.RestoreStatus({source: {getSnapshot: () => state}, subscribe: () => () => {}, isCurrent: () => true}));
}

test('web restore status displays precise bounded damage after restart and avoids a lossless repair claim', async () => {
  const render = await fixture({document: {lastRestore: {outcome: 'restored', recovery: receipt.validation.recovery}}});
  const tree = render(), copy = text(tree);
  assert.match(copy, /Portico restored the readable parts of this backup/);
  assert.match(copy, /Backup damage: 24 regions; 3 files could not be restored/);
  assert.match(copy, /Lines 2–3, bytes \[1024, 2048\): Payload hash mismatch/);
  assert.match(copy, /Showing 20 of 24 damaged regions/);
  assert.match(copy, /could not be verified against the backup manifest/);
  assert.equal(nodes(tree).filter((node: any) => node.type === 'li').length, 23);
  assert.doesNotMatch(copy, /Nothing was lost|recovered all of it/);
});

test('web restore status carries staged damage through restart and never attributes rolled-back damage to a restore', async () => {
  const state: any = {restorePhase: 'restarting', staged: receipt, document: {lastRestore: {outcome: 'restored', recovery: {indexesRebuilt: true}}}};
  const render = await fixture(state);
  assert.match(text(render()), /Showing 20 of 24 damaged regions/);
  state.restorePhase = 'preparing';state.restoreJobId = 'restore-job';state.staged = undefined;
  state.document.restore = {jobId: 'restore-job', phase: 'staged', bytesDone: 1, bytesTotal: 1, result: receipt};
  assert.match(text(render()), /bytes \[1024, 2048\)/);
  state.restorePhase = undefined;state.document.lastRestore = {outcome: 'rolled_back', recovery: receipt.validation.recovery};
  assert.doesNotMatch(text(render()), /Backup damage|readable parts|Nothing was lost/);
});
