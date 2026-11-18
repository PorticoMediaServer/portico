import {canonicalQualityLadder} from '../../contracts/src/types.ts';
import {wireFixture} from './wire-fixture.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  parseDownloadPreparation as read_parseDownloadPreparation, parseDownloadBatch as read_parseDownloadBatch, parseDownloadOptions as read_parseDownloadOptions,
  parseDownloadUsage, parseDownloadSettings, downloadActionOffered,
  downloadReasonMessage, downloadBytes, parseDownloadTitle as read_parseDownloadTitle,
} from '../src/downloads.ts';
import {resetSkippedParts, skippedParts} from '../src/tolerant.ts';

// Mock server answers below are written in seconds; the wire carries milliseconds.
const DOWNLOAD_WIRE = {durationSeconds: 'durationMs', etaSeconds: 'etaMs', renewAfterSeconds: 'renewAfterMs', positionSeconds: 'positionMs', startSeconds: 'startMs', endSeconds: 'endMs', intervalSeconds: 'intervalMs', sourceOffsetSeconds: 'sourceOffsetMs'};
const onWire = <F extends (value: any, ...rest: any[]) => any>(read: F): F => ((value: unknown, ...rest: any[]) => read(wireFixture(value, DOWNLOAD_WIRE), ...rest)) as F;
const parseDownloadPreparation = onWire(read_parseDownloadPreparation), parseDownloadBatch = onWire(read_parseDownloadBatch);
const parseDownloadTitle = onWire(read_parseDownloadTitle);

const parseDownloadOptions=(value:unknown,...args:any[]) => (read_parseDownloadOptions as any)(wireFixture(value,{'durationSeconds': 'durationMs'},[]),...args);

const sha = 'a'.repeat(64);

/** Every preparation carries the title facts the server last knew. */
function title(over: Record<string, unknown> = {}): any {
  return {kind: 'movie', title: 'Film', onServer: true, ...over};
}

function preparation(): any {
  return {
    id: 'prep', itemId: 'item', libraryId: 'lib', profileId: 'profile', quality: '1080p-low', origin: 'item',
    title: title(), batchId: 'batch', state: 'ready', reason: '',
    artifact: {kind: 'prepared', ref: 'version', sha256: sha, bytes: 4096, estimated: false, container: 'mp4', contentType: 'video/mp4', fileName: 'prep.mp4'},
    progress: {bytesDone: 4096, bytesTotal: 4096, bytesTotalEstimated: false, percent: 100, etaSeconds: null},
    actions: ['remove'], revision: 3,
    createdAt: '2026-09-16T10:00:00Z', updatedAt: '2026-09-16T10:05:00Z', readyAt: '2026-09-16T10:05:00Z', expiresAt: '2026-10-16T10:05:00Z',
  };
}

function options(): any {
  return {
    itemId: 'item', kind: 'movie',
    source: {container: 'mp4', videoCodec: 'h264', audioCodec: 'aac', height: 1080, durationSeconds: 5400, bytes: 8_000_000_000, available: true},
    options: [
      {quality: 'original', label: 'Original', kind: 'source', targetDisplayHeight: 1080, estimatedBytes: 8_000_000_000, estimated: false, available: true, reason: '', requiresPreparation: false},
      {quality: '720p-high', label: '720p-high', kind: 'optimized', targetDisplayHeight: 720, maxVideoBitrateBps: 4_000_000, maxAudioBitrateBps: 128_000, estimatedBytes: 2_800_000_000, estimated: true, available: false, reason: 'optimized_version_unavailable', requiresPreparation: false, preparedProfileId: 'portable-720-v3'},
    ],
    policy: {allowDownloads: true, reason: ''},
    storage: {maxPreparedBytes: 0, committedBytes: 12, remainingBytes: null, retentionDays: 30},
  };
}

test('a preparation is read only when every published invariant holds', () => {
  const parsed = parseDownloadPreparation(preparation());
  assert.equal(parsed.artifact.sha256, sha);
  assert.equal(parsed.title.title, 'Film');
  assert.equal(parsed.title.onServer, true);
  assert.deepEqual([...parsed.actions], ['remove']);
  assert(Object.isFrozen(parsed.actions));
  assert.equal(downloadActionOffered(parsed, 'remove'), true);
  assert.equal(downloadActionOffered(parsed, 'retry'), false);
  // A quality is opaque and id-shaped: any rung the server learns to make is accepted.
  const hevc = preparation();
  hevc.quality = '1080p-hevc';
  assert.equal(parseDownloadPreparation(hevc).quality, '1080p-hevc');
  for (const change of [
    (v: any) => v.state = 'finished',
    (v: any) => v.quality = 'Not A Rung',
    (v: any) => v.reason = 'Free text, not a code',
    (v: any) => v.actions = ['pause', 'pause', 'cancel', 'retry', 'remove', 'resume'],
    (v: any) => v.actions = 'remove',
    (v: any) => v.id = 'not an id',
    (v: any) => v.revision = 0,
    (v: any) => v.artifact.sha256 = 'not-a-digest',
    (v: any) => v.artifact.bytes = -1,
    (v: any) => v.artifact.kind = 'mirrored',
    (v: any) => v.artifact.fileName = 'prep\u0000.mp4',
    // A ready claim without a verification hash is the dangerous case: it would
    // invite a client to trust bytes it cannot check.
    (v: any) => v.artifact.sha256 = '',
    (v: any) => v.artifact.estimated = true,
    (v: any) => v.createdAt = 'yesterday',
  ]) {
    const v = preparation();
    change(v);
    assert.throws(() => parseDownloadPreparation(v), `accepted ${JSON.stringify(v)}`);
  }
});

test('a queued preparation carries an estimate and no hash', () => {
  const v = preparation();
  Object.assign(v, {state: 'queued', actions: ['pause', 'cancel'], readyAt: '', expiresAt: ''});
  v.artifact = {kind: '', ref: '', sha256: '', bytes: 2_800_000_000, estimated: true, container: '', contentType: '', fileName: ''};
  v.progress = {bytesDone: 0, bytesTotal: 2_800_000_000, bytesTotalEstimated: true, percent: 0, etaSeconds: null};
  const parsed = parseDownloadPreparation(v);
  assert.equal(parsed.artifact.estimated, true);
  assert.equal(parsed.expiresAt, '');
});

test('a preparation keeps what verifies its file exact and reads the rest forgivingly', () => {
  resetSkippedParts();
  const v = preparation();
  Object.assign(v, {quality: '4320p', reason: 'device_limit_reached', origin: 'schedule', actions: ['remove', 'archive', 'remove'], shelf: 'new', priority: 4});
  v.artifact.mirror = 'new';
  v.progress.percent = 101;
  const parsed = parseDownloadPreparation(v);
  assert.equal(parsed.quality, '4320p');
  assert.equal(parsed.reason, 'device_limit_reached');
  assert.equal(downloadReasonMessage(parsed.reason), 'device limit reached');
  assert.equal(parsed.origin, 'item');
  assert.deepEqual([...parsed.actions], ['remove']);
  assert.equal(downloadActionOffered(parsed, 'archive' as any), false);
  assert.equal(parsed.progress.percent, 0);
  assert.equal('shelf' in parsed, false);
  assert.deepEqual(parsed.artifact, {kind: 'prepared', ref: 'version', sha256: sha, bytes: 4096, estimated: false, container: 'mp4', contentType: 'video/mp4', fileName: 'prep.mp4'});
  assert.equal(parsed.revision, 3);
  assert.deepEqual(skippedParts().map(p => p.document + ':' + p.part + ':' + p.count).sort(), ['downloads:preparation.actions:2', 'downloads:preparation.origin:1', 'downloads:preparation.progress:1']);
});

test('download options keep every quality the server offers and leave out only what cannot be read', () => {
  resetSkippedParts();
  const v = options();
  v.preview = 'new';
  v.options[1].reason = 'device_limit_reached';
  v.options[1].preparationState = 'verifying';
  v.options[1].badge = 'new';
  v.options.push(
    {...v.options[0], quality: '4320p', label: '4K+'},
    {...v.options[0], quality: '480p', kind: 'remote'},
    // An available option with a reason, or an unavailable one without, leaves nothing to say.
    {...v.options[0], quality: '240p', reason: 'storage_full'},
    {...v.options[1], quality: '1080p-low', reason: ''},
    {...v.options[0], label: 'Original again'},
    {...v.options[0], quality: '1080p-high', estimatedBytes: -1},
  );
  const view = parseDownloadOptions(v, 'item');
  // The server's menu is the list: a quality this app has never heard of is offered as the server
  // names it. What is left out is an option that contradicts itself or cannot be read.
  assert.deepEqual(view.options.map(o => o.quality), ['original', '720p-high', '4320p']);
  assert.equal(view.options[2].label, '4K+');
  assert.equal(view.options[0].label, 'Original');
  assert.equal(view.options[1].reason, 'device_limit_reached');
  assert.equal(view.options[1].preparationState, '');
  assert.equal('badge' in view.options[1], false);
  assert.deepEqual(skippedParts().map(p => p.part + ':' + p.count).sort(), ['option.preparationState:1', 'options:5']);
  // The original need not come first, or be readable, for the other rungs to be offered.
  const reordered = options();
  reordered.options.reverse();
  assert.deepEqual(parseDownloadOptions(reordered, 'item').options.map(o => o.quality), ['720p-high', 'original']);
  const blocked = options();
  blocked.policy = {allowDownloads: false, reason: 'household_limit'};
  assert.deepEqual(parseDownloadOptions(blocked, 'item').policy, {allowDownloads: false, reason: 'household_limit'});
});

test('CD-26: estimated totals may be overtaken; exact totals may not', () => {
  const estimated = preparation();
  estimated.progress = {bytesDone: 3_000_000_000, bytesTotal: 2_800_000_000, bytesTotalEstimated: true, percent: 100, etaSeconds: null};
  assert.equal(parseDownloadPreparation(estimated).progress.bytesDone, 3_000_000_000);
  // Progress is only shown: an impossible figure costs the row its progress, never the row.
  const exact = preparation();
  exact.progress = {bytesDone: 4097, bytesTotal: 4096, bytesTotalEstimated: false, percent: 100, etaSeconds: null};
  const shown = parseDownloadPreparation(exact);
  assert.deepEqual(shown.progress, {bytesDone: 0, bytesTotal: 0, bytesTotalEstimated: true, percent: 0, etaSeconds: null});
  assert.equal(shown.artifact.sha256, sha);
  // Ready artifact digest/size requirements remain enforced.
  const noHash = preparation();
  noHash.artifact = {...noHash.artifact, sha256: ''};
  assert.throws(() => parseDownloadPreparation(noHash));
});

test('a batch bounds its targets and cannot claim more accepted than it returned', () => {
  const batch = parseDownloadBatch({batchId: 'batch', items: [preparation()], rejected: [{itemId: 'other', code: 'source_unavailable'}], accepted: 1, duplicate: false});
  assert.equal(batch.items.length, 1);
  assert.equal(batch.rejected[0].code, 'source_unavailable');
  // More accepted than rows sent reads as the rows sent; a repeated row is read once.
  assert.equal(parseDownloadBatch({batchId: 'batch', items: [preparation()], rejected: [], accepted: 2, duplicate: false}).accepted, 1);
  const repeated = parseDownloadBatch({batchId: 'batch', items: [preparation(), {...preparation(), revision: 9}], rejected: [], accepted: 2, duplicate: false});
  assert.deepEqual(repeated.items.map(v => v.revision), [3]);
  assert.equal(repeated.accepted, 2);
  // A rejection with no code, or for no readable item, is left out; one with a new code is kept.
  const mixed = parseDownloadBatch({batchId: 'batch', items: [], rejected: [{itemId: 'other', code: ''}, {itemId: 'bad id!', code: 'storage_full'}, {itemId: 'third', code: 'device_limit_reached', hint: 'new'}], accepted: 0, duplicate: false});
  assert.deepEqual(mixed.rejected, [{itemId: 'third', code: 'device_limit_reached'}]);
  for (const change of [
    (v: any) => v.batchId = 'batch\u0007',
    (v: any) => v.accepted = -1,
    (v: any) => v.duplicate = 'no',
    (v: any) => v.items = {0: preparation()},
    (v: any) => v.items = Array.from({length: 101}, (_, i) => ({...preparation(), id: 'prep-' + i})),
    (v: any) => v.rejected = Array.from({length: 101}, () => ({itemId: 'other', code: 'storage_full'})),
  ]) {
    const v: any = {batchId: 'batch', items: [preparation()], rejected: [], accepted: 1, duplicate: false};
    change(v);
    assert.throws(() => parseDownloadBatch(v));
  }
});

test('download options keep their item, policy, storage limits and bounds exact', () => {
  const view = parseDownloadOptions(options(), 'item');
  assert.equal(view.options[0].quality, 'original');
  assert.equal(view.options[1].reason, 'optimized_version_unavailable');
  assert.equal(view.storage.remainingBytes, null);
  assert.throws(() => parseDownloadOptions(options(), 'other'));
  for (const change of [
    (v: any) => v.policy = {allowDownloads: false, reason: ''},
    (v: any) => v.policy = {allowDownloads: true, reason: 'storage_full'},
    (v: any) => v.policy = {allowDownloads: 'yes', reason: ''},
    (v: any) => v.policy = {allowDownloads: false, reason: 'Ask the owner.'},
    (v: any) => v.storage.remainingBytes = 10,
    (v: any) => v.storage = {maxPreparedBytes: 100, committedBytes: 0, remainingBytes: null, retentionDays: 30},
    (v: any) => v.storage.retentionDays = 0,
    (v: any) => v.options = Array.from({length: 17}, () => v.options[0]),
    (v: any) => v.options = [],
    (v: any) => v.options = 'original',
    (v: any) => v.kind = 'movie\n',
    (v: any) => v.source.bytes = -1,
  ]) {
    const v = options();
    change(v);
    assert.throws(() => parseDownloadOptions(v, 'item'));
  }
});

test('usage and settings enforce the unlimited-ceiling contract', () => {
  const usage = parseDownloadUsage({profileId: 'profile', profileBytes: 10, profileCount: 1, serverBytes: 20, serverCount: 2, distinctArtifactBytes: 20, maxPreparedBytes: 100, remainingBytes: 80, retentionDays: 30, profiles: [{profileId: 'profile', bytes: 10, count: 1}]});
  assert.equal(usage.remainingBytes, 80);
  // A profile cannot hold more than the server it is part of.
  assert.throws(() => parseDownloadUsage({profileId: 'profile', profileBytes: 30, profileCount: 1, serverBytes: 20, serverCount: 2, distinctArtifactBytes: 20, maxPreparedBytes: 0, remainingBytes: null, retentionDays: 30, profiles: []}));
  assert.throws(() => parseDownloadUsage({profileId: 'profile', profileBytes: 1, profileCount: 1, serverBytes: 20, serverCount: 2, distinctArtifactBytes: 20, maxPreparedBytes: 0, remainingBytes: 5, retentionDays: 30, profiles: []}));
  // One unreadable or repeated profile row leaves the rest of the breakdown; the totals and limits stay exact.
  const breakdown = parseDownloadUsage({profileId: 'profile', profileBytes: 10, profileCount: 1, serverBytes: 20, serverCount: 2, distinctArtifactBytes: 20, maxPreparedBytes: 100, remainingBytes: 80, retentionDays: 30, tier: 'new', profiles: [{profileId: 'profile', bytes: 10, count: 1, label: 'new'}, {profileId: 'profile', bytes: 99, count: 9}, {profileId: 'bad id!', bytes: 1, count: 1}, {profileId: 'other', bytes: -1, count: 1}, {profileId: 'second', bytes: 10, count: 1}]});
  assert.deepEqual(breakdown.profiles, [{profileId: 'profile', bytes: 10, count: 1}, {profileId: 'second', bytes: 10, count: 1}]);
  assert.throws(() => parseDownloadUsage({profileId: 'bad id!', profileBytes: 10, profileCount: 1, serverBytes: 20, serverCount: 2, distinctArtifactBytes: 20, maxPreparedBytes: 100, remainingBytes: 80, retentionDays: 30, profiles: []}));
  assert.throws(() => parseDownloadUsage({profileId: 'profile', profileBytes: 10, profileCount: 1, serverBytes: 20, serverCount: 2, distinctArtifactBytes: 20, maxPreparedBytes: 100, remainingBytes: 80, retentionDays: 30, profiles: Array.from({length: 257}, (_, i) => ({profileId: 'p' + i, bytes: 0, count: 0}))}));
  const qualities=canonicalQualityLadder.map(q=>({quality:q.id,label:q.label,kind:q.height?'optimized':'source',targetDisplayHeight:q.height}));
  assert.deepEqual(parseDownloadSettings({maxPreparedBytes:0,retentionDays:30,revision:1,qualities}).qualities,qualities);
  assert.throws(()=>parseDownloadSettings({maxPreparedBytes:0,retentionDays:30,revision:1}), 'missing ladder cannot invent a short fallback');
  assert.throws(()=>parseDownloadSettings({maxPreparedBytes:0,retentionDays:30,revision:1,qualities:qualities.slice(0,2)}));
  assert.throws(() => parseDownloadSettings({maxPreparedBytes: 0, retentionDays: 30, revision: 0, qualities}));
  assert.throws(() => parseDownloadSettings({maxPreparedBytes: 0, retentionDays: 0, revision: 1, qualities}));
  assert.throws(() => parseDownloadSettings({maxPreparedBytes: 0, retentionDays: 400, revision: 1, qualities}));
});

test('a download title needs the facts a list shows, and onServer must be a boolean', () => {
  const parsed = parseDownloadTitle(title({year: 1999}));
  assert.equal(parsed.title, 'Film');
  assert.equal(parsed.onServer, true);
  assert.equal(parsed.year, 1999);
  // `onServer` decides whether the title is offered as still in the library, so a
  // missing or non-boolean value is refused rather than coerced.
  assert.throws(() => parseDownloadTitle({kind: 'movie', title: 'Film'}));
  assert.throws(() => parseDownloadTitle({kind: 'movie', title: 'Film', onServer: 'true'}));
  assert.throws(() => parseDownloadTitle({kind: 'movie', title: 'Film', onServer: true, year: -1}));
});

test('reason codes render a sentence and sizes render a unit', () => {
  assert.equal(downloadReasonMessage('storage_full'), 'The server has no room left for smaller copies.');
  assert.match(downloadReasonMessage('optimized_version_unavailable'), /original/);
  assert.match(downloadReasonMessage('unknown_preparation'), /no record/);
  assert.match(downloadReasonMessage('several_files'), /several files/);
  assert.equal(downloadReasonMessage(''), '');
  // A code this app has no sentence for is said as its own words.
  assert.equal(downloadReasonMessage('brand_new_code'), 'brand new code');
  assert.equal(downloadBytes(2048), '2 KiB');
  assert.equal(downloadBytes(5_368_709_120), '5.00 GiB');
});
