import test from 'node:test';
import assert from 'node:assert/strict';
import {MANIFEST_DAMAGE_REASONS, parseBackupsDocument, parseRestoreStaged, type RestoreRecovery} from '../src/administration.ts';
import {restoreBackup, restoreRecoveryDetails, restoreRecoveryNotice, type BackupTransport} from '../src/backups.ts';
import {defaultI18n} from '../../i18n/src/index.ts';

const staged = (recovery: unknown) => ({staged: true, restartRequired: true, validation: {schemaVersion: 41, integrity: 'recovered', recovery}});
const region = {firstLine: 2, lastLine: 2, startByte: 100, endByte: 200, reason: 'unreadable_record'} as const;
const manifestDamage = {count: 1, regions: [region], missingFiles: 0};

test('recovery notices distinguish an index repair, full salvage and either kind of loss', () => {
  const cases: readonly [RestoreRecovery | undefined, ReturnType<typeof restoreRecoveryNotice>][] = [
    [undefined, undefined], [{}, undefined], [{indexesRebuilt: false, salvaged: false}, undefined],
    [{indexesRebuilt: true}, 'repaired'], [{indexesRebuilt: true, salvaged: false}, 'repaired'],
    [{salvaged: true}, 'recovered'], [{indexesRebuilt: true, salvaged: true, damagedTables: [], removedRows: 0}, 'recovered'],
    [{salvaged: true, damagedTables: ['media']}, 'partial'], [{salvaged: true, removedRows: 1}, 'partial'],
    [{indexesRebuilt: true, salvaged: true, damagedTables: ['media'], removedRows: 2}, 'partial'],
    [{manifestDamage}, 'partial'], [{indexesRebuilt: true, manifestDamage}, 'partial'],
    [{salvaged: true, manifestDamage: {...manifestDamage, unverifiedDatabase: true}}, 'partial'],
  ];
  for (const [recovery, notice] of cases) assert.equal(restoreRecoveryNotice(recovery), notice);
});

test('the backups document carries manual and scheduled kinds and both recovery reports', () => {
  const recovery = {indexesRebuilt: true, salvaged: true, damagedTables: ['media'], removedRows: 2};
  const running = {jobId: 'backup-1', phase: 'copy', bytesDone: 1, bytesTotal: 2};
  for (const kind of ['manual', 'scheduled'] as const) {
    const doc = parseBackupsDocument({backups: [], running: {...running, kind},
      lastBackup: {jobId: 'backup-0', outcome: 'failed', errorCode: 'backup_busy'},
      restore: {...running, jobId: 'restore-1', phase: 'staged', result: staged(recovery)},
      lastRestore: {at: '2026-10-28', outcome: 'restored', recovery}});
    assert.equal(doc.running?.kind, kind);
    assert.equal(doc.lastBackup?.errorCode, 'backup_busy');
    assert.deepEqual(doc.restore?.result?.validation.recovery, recovery);
    assert.deepEqual(doc.lastRestore?.recovery, recovery);
    assert(Object.isFrozen(doc.lastRestore?.recovery));
    assert(Object.isFrozen(doc.restore?.result?.validation.recovery?.damagedTables));
  }
  for (const kind of [undefined, 'future']) assert.equal(parseBackupsDocument({backups: [], running: {...running, kind}}).running?.kind, undefined);
});

test('recovered receipts retain validated recovery details and reject unreadable reports', () => {
  const recovery = {indexesRebuilt: true};
  assert.deepEqual(parseRestoreStaged(staged(recovery)).validation, {schemaVersion: 41, integrity: 'recovered', recovery});
  assert.equal(parseRestoreStaged({staged: true, restartRequired: true, validation: {schemaVersion: 41, integrity: 'recovered'}}).validation.integrity, 'recovered');
  for (const invalid of [null, [], {indexesRebuilt: 1}, {salvaged: 'true'}, {damagedTables: 'media'}, {damagedTables: [1]}, {removedRows: -1}, {removedRows: 0.5}]) {
    assert.throws(() => parseRestoreStaged(staged(invalid)), {code: 'invalid_administration_response'});
  }
});

test('missing the staged response during restart preserves the completed recovery report', async () => {
  for (const recovery of [{indexesRebuilt: true}, {salvaged: true}, {salvaged: true, removedRows: 2}, {manifestDamage}]) {
    const api: BackupTransport = {request: async <T,>(_path: string, method?: string) => ({protocolVersion: '1.0', serverId: 'server-1',
      result: method === 'POST' ? {jobId: 'restore-1'} : {backups: [], lastRestore: {jobId: 'restore-1', schemaVersion: 41, at: '2026-10-28', outcome: 'restored', recovery}}}) as T};
    const result = await restoreBackup(api, 'server-1', 'operation-1', {path: '/backup/portico.db'});
    assert.equal(result.validation.integrity, 'recovered');
    assert.deepEqual(result.validation.recovery, recovery);
  }
});

test('manifest damage preserves bounded exact reports in staged and post-restart documents', () => {
  const regions = MANIFEST_DAMAGE_REASONS.map(reason => ({...region, reason}));
  const recovery = {manifestDamage: {...manifestDamage, count: regions.length, regions, missingFiles: 3, unverifiedDatabase: true}};
  const receipt = parseRestoreStaged(staged(recovery));
  const doc = parseBackupsDocument({backups: [], lastRestore: {at: '2026-10-28', outcome: 'restored', recovery},
    restore: {jobId: 'restore-1', phase: 'staged', bytesDone: 0, bytesTotal: 0, result: staged(recovery)}});
  for (const parsed of [receipt.validation.recovery, doc.lastRestore?.recovery, doc.restore?.result?.validation.recovery]) {
    assert.deepEqual(parsed, recovery);
    assert(Object.isFrozen(parsed?.manifestDamage));
    assert(Object.isFrozen(parsed?.manifestDamage?.regions));
    assert(parsed?.manifestDamage?.regions.every(Object.isFrozen));
  }
  const capped = {...manifestDamage, count: 200_000, regions: Array.from({length: 20}, () => region), regionsTruncated: true};
  assert.deepEqual(parseRestoreStaged(staged({manifestDamage: capped})).validation.recovery?.manifestDamage, capped);
  const eof = {...manifestDamage, regions: [{firstLine: 5, lastLine: 5, startByte: 888, endByte: 888, reason: 'missing_end_record'}]};
  assert.deepEqual(parseRestoreStaged(staged({manifestDamage: eof})).validation.recovery?.manifestDamage, eof);
});

test('manifest damage refuses unsafe offsets, unknown reasons, excessive lists and dishonest counts', () => {
  const badRegions = [null, [], {...region, firstLine: 0}, {...region, firstLine: 3}, {...region, firstLine: 1.5},
    {...region, lastLine: Number.MAX_SAFE_INTEGER + 1}, {...region, startByte: -1}, {...region, endByte: 99},
    {...region, endByte: Infinity}, {...region, startByte: '100'}, {...region, reason: 'future_reason'}];
  const invalid = [null, [], {}, {...manifestDamage, count: 0}, {...manifestDamage, count: 2},
    {...manifestDamage, count: -1}, {...manifestDamage, count: 1.5}, {...manifestDamage, count: Number.MAX_SAFE_INTEGER + 1},
    {...manifestDamage, missingFiles: -1}, {...manifestDamage, missingFiles: '0'}, {...manifestDamage, unverifiedDatabase: 1},
    {...manifestDamage, regionsTruncated: 1}, {...manifestDamage, regionsTruncated: true},
    {...manifestDamage, count: 21, regions: Array.from({length: 20}, () => region)},
    {...manifestDamage, count: 21, regions: Array.from({length: 21}, () => region), regionsTruncated: true},
    {...manifestDamage, regions: null}, ...badRegions.map(value => ({...manifestDamage, regions: [value]}))];
  for (const value of invalid) {
    assert.throws(() => parseRestoreStaged(staged({manifestDamage: value})), {code: 'invalid_administration_response'});
    assert.throws(() => parseBackupsDocument({backups: [], lastRestore: {at: '2026-10-28', outcome: 'restored', recovery: {manifestDamage: value}}}), {code: 'invalid_administration_response'});
  }
});

test('owner damage details keep exact byte bounds, missing file counts and truncation honest', () => {
  assert.deepEqual(restoreRecoveryDetails(undefined, defaultI18n.t), []);
  const report = parseRestoreStaged(staged({indexesRebuilt: true, manifestDamage: {
    count: 31, missingFiles: 4, regionsTruncated: true, unverifiedDatabase: true,
    regions: Array.from({length: 20}, (_, index) => ({...region, firstLine: index + 2, lastLine: index + 2,
      startByte: index * 100, endByte: index === 19 ? index * 100 : (index + 1) * 100,
      reason: index === 19 ? 'missing_end_record' : 'payload_hash_mismatch'})),
  }})).validation.recovery;
  const details = restoreRecoveryDetails(report, defaultI18n.t);
  assert.equal(details.length, 23);
  assert.equal(details[0], 'Backup damage: 31 regions; 4 files could not be restored.');
  assert.equal(details[1], 'Lines 2–2, bytes [0, 100): Payload hash mismatch.');
  assert.equal(details[20], 'Lines 21–21, bytes [1900, 1900): Missing end record.');
  assert.equal(details[21], 'Showing 20 of 31 damaged regions.');
  assert.match(details[22], /could not be verified against the backup manifest/);
  assert(Object.isFrozen(details));
});
