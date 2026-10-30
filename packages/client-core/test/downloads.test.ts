import {canonicalQualityLadder} from '../../contracts/src/types.ts';
import {wireFixture} from './wire-fixture.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  parseDownloadPreparation as read_parseDownloadPreparation, parseDownloadBatch as read_parseDownloadBatch, parseDownloadPage as read_parseDownloadPage, parseDownloadGrant as read_parseDownloadGrant, parseDownloadOptions as read_parseDownloadOptions,
  decodeDownloadReceipt, parseReceiptOutcomes as read_parseReceiptOutcomes, parseReceiptKeys, parseRevocationPage, parseDeferredProgressReceipt as readProgressReceipt,
  parseDownloadUsage, parseDownloadSettings, validDeferredProgressEntry, downloadActionOffered, receiptUsable,
  downloadReasonMessage, downloadBytes, parseDownloadBundle as read_parseDownloadBundle, parseDownloadTitle as read_parseDownloadTitle,
} from '../src/downloads.ts';
import {resetSkippedParts, skippedParts} from '../src/tolerant.ts';

// Mock server answers below are written in seconds; the wire carries milliseconds.
const DOWNLOAD_WIRE = {durationSeconds: 'durationMs', etaSeconds: 'etaMs', renewAfterSeconds: 'renewAfterMs', positionSeconds: 'positionMs', startSeconds: 'startMs', endSeconds: 'endMs', intervalSeconds: 'intervalMs', sourceOffsetSeconds: 'sourceOffsetMs'};
const onWire = <F extends (value: any, ...rest: any[]) => any>(read: F): F => ((value: unknown, ...rest: any[]) => read(wireFixture(value, DOWNLOAD_WIRE), ...rest)) as F;
const parseDownloadPreparation = onWire(read_parseDownloadPreparation), parseDownloadBatch = onWire(read_parseDownloadBatch), parseDownloadPage = onWire(read_parseDownloadPage);
const parseDownloadGrant = onWire(read_parseDownloadGrant), parseReceiptOutcomes = onWire(read_parseReceiptOutcomes), parseDownloadBundle = onWire(read_parseDownloadBundle), parseDownloadTitle = onWire(read_parseDownloadTitle);
const parseDeferredProgressReceipt=(v:unknown)=>readProgressReceipt(wireFixture(v,{positionSeconds:'positionMs'}));

const parseDownloadOptions=(value:unknown,...args:any[]) => (read_parseDownloadOptions as any)(wireFixture(value,{'durationSeconds': 'durationMs'},[]),...args);

const sha = 'a'.repeat(64);
const encode = (v: unknown) => Buffer.from(JSON.stringify(v), 'utf8').toString('base64url');
const signature = 'A'.repeat(86);
const publicKey = 'B'.repeat(43);

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

function claims(): any {
  return {
    kind: 'portico.download-receipt', version: '1', receiptId: 'receipt', keyId: 'key',
    viewer: {authority: 'local', accountId: 'account', profileId: 'profile', serverId: 'server'},
    itemId: 'item', preparationId: 'prep', quality: '1080p-low', qualityLabel: '1080p-low',
    artifact: {sha256: sha, bytes: 4096}, issuedAt: '2026-09-16T10:00:00Z', expiresAt: '2026-10-16T10:00:00Z',
  };
}
const envelope = (c = claims()) => ({receiptId: c.receiptId, algorithm: 'ed25519', keyId: c.keyId, payload: encode(c), signature, claims: c, revision: 1});

function bundle(): any {
  return {
    preparationId: 'prep', itemId: 'item', quality: '1080p-low', qualityLabel: '1080p-low', overview: 'An overview.',
    title: title({kind: 'episode', title: 'Pilot', parentTitle: 'Show', groupNumber: 1, number: 2}),
    media: {container: 'mp4', videoCodec: 'h264', audioCodec: 'aac', width: 1920, height: 1080},
    artwork: [{role: 'poster', path: 'artwork/poster.jpg', contentType: 'image/jpeg'}],
    chapters: [], markers: [], audio: [], subtitles: [],
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

test('one unreadable download leaves the rest of the list, and the list itself stays bounded and exact', () => {
  resetSkippedParts();
  const row = (id: string, change: (v: any) => void = () => {}) => { const v = preparation(); v.id = id; change(v); return v; };
  const page = parseDownloadPage({nextCursor: 'cursor-2', total: 7, items: [
    row('a', v => v.badge = 'new'),
    row('b', v => v.state = 'verifying'),
    row('c', v => v.artifact.sha256 = 'not-a-digest'),
    row('d', v => v.artifact.sha256 = ''),
    row('e', v => v.revision = 'three'),
    row('bad id!'),
    'not a row',
    row('a', v => v.revision = 8),
    row('f', v => v.reason = 'brand_new_code'),
  ]});
  assert.deepEqual(page.items.map(v => v.id), ['a', 'f']);
  assert.equal(page.items[0].revision, 3);
  assert.equal(page.nextCursor, 'cursor-2');
  assert.deepEqual(skippedParts().map(p => p.part + ':' + p.count), ['items:7']);
  for (const bad of [
    {items: Array.from({length: 201}, (_, i) => row('p' + i)), nextCursor: ''},
    {items: {0: row('a')}, nextCursor: ''},
    {items: [row('a')], nextCursor: 7},
    {items: [row('a')], nextCursor: 'cursor\u0000'},
    {items: [row('a')], nextCursor: 'c'.repeat(513)},
    [row('a')],
  ]) assert.throws(() => parseDownloadPage(bad));
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
  const page = parseDownloadPage({items: [preparation()], nextCursor: ''});
  assert.equal(page.nextCursor, '');
});

test('a grant URL is a server path and never an absolute redirect', () => {
  const good = {preparationId: 'prep', itemId: 'item', url: '/v1/downloads/artifacts/tok-1', token: 'tok-1', issuedAt: '2026-09-16T10:00:00Z', expiresAt: '2026-09-16T10:10:00Z', renewAfterSeconds: 600, renewed: false, artifact: preparation().artifact};
  const parsed = parseDownloadGrant(good);
  assert.equal(parsed.renewAfterSeconds, 600);
  assert.equal(parsed.renewed, false);
  for (const url of ['https://evil.example/v1/downloads/artifacts/tok-1', '/v1/downloads/artifacts/../../etc', '/v1/media/tok-1', '/v1/downloads/artifacts/tok-2', '']) {
    assert.throws(() => parseDownloadGrant({...good, url}), `accepted ${url}`);
  }
  assert.throws(() => parseDownloadGrant({...good, expiresAt: good.issuedAt}));
  assert.throws(() => parseDownloadGrant({...good, artifact: {...good.artifact, sha256: ''}}));
  assert.throws(() => parseDownloadGrant({...good, renewAfterSeconds: 0}));
  assert.throws(() => parseDownloadGrant({...good, renewed: undefined}));
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

test('a receipt envelope may not disagree with the document it signed', () => {
  const {receipt, signedBytes, signatureBytes} = decodeDownloadReceipt(envelope());
  assert.equal(receipt.claims.artifact.sha256, sha);
  assert.equal(signatureBytes.length, 64);
  assert.equal(new TextDecoder().decode(signedBytes), JSON.stringify(claims()));
  // The signature covers `payload`; a client that read `claims` instead would
  // be reading an unsigned copy, so a disagreement is refused outright.
  assert.throws(() => decodeDownloadReceipt({...envelope(), claims: {...claims(), itemId: 'other'}}));
  assert.throws(() => decodeDownloadReceipt({...envelope(), receiptId: 'different'}));
  assert.throws(() => decodeDownloadReceipt({...envelope(), keyId: 'different'}));
  assert.throws(() => decodeDownloadReceipt({...envelope(), algorithm: 'none'}));
  assert.throws(() => decodeDownloadReceipt({...envelope(), signature: 'AAAA'}));
  const backwards = claims();
  backwards.expiresAt = backwards.issuedAt;
  assert.throws(() => decodeDownloadReceipt(envelope(backwards)));
});

test('a receipt is usable only for its own viewer, in date, and not revoked', () => {
  const {receipt} = decodeDownloadReceipt(envelope());
  const viewer = {authority: 'local', accountId: 'account', profileId: 'profile', serverId: 'server'};
  assert.equal(receiptUsable(receipt.claims, viewer, new Date('2026-09-20T00:00:00Z')), true);
  assert.equal(receiptUsable(receipt.claims, viewer, new Date('2026-11-20T00:00:00Z')), false);
  assert.equal(receiptUsable(receipt.claims, {...viewer, profileId: 'other'}, new Date('2026-09-20T00:00:00Z')), false);
  assert.equal(receiptUsable(receipt.claims, {...viewer, serverId: 'other'}, new Date('2026-09-20T00:00:00Z')), false);
  assert.equal(receiptUsable(receipt.claims, viewer, new Date('2026-09-20T00:00:00Z'), new Set(['receipt'])), false);
});

test('receipt outcomes pair a receipt with issue and renewal, and a reason with refusal', () => {
  const results = parseReceiptOutcomes({results: [
    {preparationId: 'prep', receiptId: 'receipt', outcome: 'issued', receipt: envelope(), titleOnServer: true},
    {receiptId: 'gone', outcome: 'refused', code: 'item_deleted', titleOnServer: true},
  ]});
  assert.equal(results[0].receipt?.receiptId, 'receipt');
  assert.equal(results[0].titleOnServer, true);
  assert.equal(results[1].receipt, null);
  // A refusal says nothing about the server's library, whatever the wire carried.
  assert.equal(results[1].titleOnServer, null);
  assert.throws(() => parseReceiptOutcomes({results: [{receiptId: 'gone', outcome: 'refused', code: 'item_deleted', receipt: envelope()}]}));
  assert.throws(() => parseReceiptOutcomes({results: [{receiptId: 'gone', outcome: 'refused', code: ''}]}));
  assert.throws(() => parseReceiptOutcomes({results: [{receiptId: 'a', outcome: 'renewed'}]}));
  // An absent `titleOnServer` (or a non-boolean) is not silently treated as a fact.
  assert.equal(parseReceiptOutcomes({results: [{receiptId: 'receipt', outcome: 'renewed', receipt: envelope()}]})[0].titleOnServer, null);
  assert.throws(() => parseReceiptOutcomes({results: [{receiptId: 'receipt', outcome: 'issued', receipt: envelope(), titleOnServer: 'yes'}]}));
  // A renewal may carry where the viewer stands; a refusal carries nothing.
  const viewing = parseReceiptOutcomes({results: [{receiptId: 'receipt', outcome: 'renewed', receipt: envelope(), viewing: {positionSeconds: 120, watched: false}}]});
  assert.deepEqual(viewing[0].viewing, {positionSeconds: 120, watched: false});
  assert.equal(parseReceiptOutcomes({results: [{receiptId: 'receipt', outcome: 'renewed', receipt: envelope()}]})[0].viewing, null);
  assert.equal(parseReceiptOutcomes({results: [{receiptId: 'gone', outcome: 'refused', code: 'item_deleted', viewing: {positionSeconds: 1, watched: true}}]})[0].viewing, null);
  assert.throws(() => parseReceiptOutcomes({results: [{receiptId: 'receipt', outcome: 'issued', receipt: envelope(), viewing: {positionSeconds: -1, watched: false}}]}));
  // A refusal can cost a device its file, so one this app cannot read in full is never acted on in part.
  assert.throws(() => parseReceiptOutcomes({results: [{preparationId: 'prep', receiptId: 'receipt', outcome: 'issued', receipt: envelope()}, {receiptId: 'gone', outcome: 'refused', code: 'brand_new_code'}]}));
  assert.throws(() => parseReceiptOutcomes({results: [{receiptId: 'gone', outcome: 'postponed', code: 'item_deleted'}]}));
  // A receipt is permission to play a file already on the device. A quality name this app has
  // never heard of does not make that file any less the viewer's to play.
  const newRung = claims();
  newRung.quality = '4320p';
  assert.equal(decodeDownloadReceipt(envelope(newRung)).receipt.claims.quality, '4320p');
});

test('verification keys are 32 bytes and unique', () => {
  const keys = parseReceiptKeys({keys: [{id: 'key', algorithm: 'ed25519', publicKey, createdAt: '2026-09-01T00:00:00Z', retiredAt: ''}]});
  assert.equal(keys[0].id, 'key');
  assert.throws(() => parseReceiptKeys({keys: [{id: 'key', algorithm: 'ed25519', publicKey: 'AAAA', createdAt: '2026-09-01T00:00:00Z'}]}));
  assert.throws(() => parseReceiptKeys({keys: [{id: 'key', algorithm: 'hmac', publicKey, createdAt: '2026-09-01T00:00:00Z'}]}));
  const key = {id: 'key', algorithm: 'ed25519', publicKey, createdAt: '2026-09-01T00:00:00Z', retiredAt: ''};
  assert.throws(() => parseReceiptKeys({keys: [key, key]}));
  assert.throws(() => parseReceiptKeys({keys: [key, {...key, id: 'bad id!'}]}));
});

test('a revocation page must be ordered, because its cursor is a sequence', () => {
  const page = parseRevocationPage({items: [
    {receiptId: 'a', itemId: 'item', profileId: 'profile', reason: 'removed', revokedAt: '2026-09-16T10:00:00Z', sequence: 4},
    {receiptId: 'b', itemId: 'item', profileId: 'profile', reason: 'retention_expired', revokedAt: '2026-09-16T11:00:00Z', sequence: 9},
  ], nextCursor: '9', asOf: '2026-09-16T12:00:00Z'});
  assert.equal(page.items.length, 2);
  assert.equal(page.nextCursor, '9');
  assert.throws(() => parseRevocationPage({items: [
    {receiptId: 'b', itemId: 'item', profileId: 'profile', reason: 'removed', revokedAt: '2026-09-16T11:00:00Z', sequence: 9},
    {receiptId: 'a', itemId: 'item', profileId: 'profile', reason: 'removed', revokedAt: '2026-09-16T10:00:00Z', sequence: 4},
  ], nextCursor: '4', asOf: '2026-09-16T12:00:00Z'}));
  assert.throws(() => parseRevocationPage({items: [
    {receiptId: 'a', itemId: 'item', profileId: 'profile', reason: 'removed', revokedAt: '2026-09-16T10:00:00Z', sequence: 4},
  ], nextCursor: '99', asOf: '2026-09-16T12:00:00Z'}));
});

test('a withdrawal with a reason this app does not know is still a withdrawal, and none is ever left out', () => {
  const row = {receiptId: 'a', itemId: 'item', profileId: 'profile', reason: 'owner_policy_changed', revokedAt: '2026-09-16T10:00:00Z', sequence: 4, actor: 'owner'};
  const page = parseRevocationPage({items: [row], nextCursor: '4', asOf: '2026-09-16T12:00:00Z', region: 'new'});
  assert.deepEqual(page.items, [{receiptId: 'a', itemId: 'item', profileId: 'profile', reason: 'owner_policy_changed', revokedAt: '2026-09-16T10:00:00Z', sequence: 4}]);
  // Dropping an unreadable withdrawal would leave a receipt usable, so the whole page is refused instead.
  const good = {receiptId: 'b', itemId: 'item', profileId: 'profile', reason: 'removed', revokedAt: '2026-09-16T11:00:00Z', sequence: 9};
  for (const bad of [{...row, receiptId: 'bad id!'}, {...row, reason: ''}, {...row, reason: 'Free text'}, {...row, sequence: 0}, {...row, revokedAt: 'today'}]) {
    assert.throws(() => parseRevocationPage({items: [bad, good], nextCursor: '9', asOf: '2026-09-16T12:00:00Z'}));
  }
  assert.throws(() => parseRevocationPage({items: [good], nextCursor: 9, asOf: '2026-09-16T12:00:00Z'}));
  assert.throws(() => parseRevocationPage({items: Array.from({length: 501}, (_, i) => ({...good, sequence: i + 1})), nextCursor: '', asOf: '2026-09-16T12:00:00Z'}));
});

test('deferred progress reports every entry and its counts agree', () => {
  const receipt = parseDeferredProgressReceipt({applied: 1, conflicts: 1, entries: [
    {playId: 'play-1', itemId: 'item', outcome: 'applied', positionSeconds: 600, watched: false, observedAt: '2026-09-14T09:12:00Z'},
    {playId: 'play-2', itemId: 'other', outcome: 'stale_observation', positionSeconds: 42, watched: true, observedAt: '2026-09-13T09:12:00Z'},
  ]});
  assert.equal(receipt.entries.length, 2);
  assert.equal(receipt.entries[0].watched, false);
  assert.equal(receipt.entries[0].playId, 'play-1');
  // A play recorded while the viewer has paused their history is neither applied nor a conflict.
  const paused = parseDeferredProgressReceipt({applied: 1, conflicts: 0, entries: [
    {playId: 'play-1', itemId: 'item', outcome: 'applied', positionSeconds: 1, watched: true, observedAt: '2026-09-14T09:12:00Z'},
    {playId: 'play-2', itemId: 'other', outcome: 'history_paused', positionSeconds: 1, watched: false, observedAt: '2026-09-14T09:12:00Z'},
  ]});
  assert.deepEqual([paused.applied, paused.conflicts, paused.entries.length], [1, 0, 2]);
  // Counts that disagree with the entries are read off the entries.
  const miscounted = parseDeferredProgressReceipt({applied: 2, conflicts: 0, entries: [
    {playId: 'play-1', itemId: 'item', outcome: 'applied', positionSeconds: 1, watched: false},
  ]});
  assert.deepEqual([miscounted.applied, miscounted.conflicts, miscounted.entries.length], [1, 0, 1]);
  const onlyPaused = parseDeferredProgressReceipt({applied: 1, conflicts: 0, entries: [
    {playId: 'play-1', itemId: 'item', outcome: 'history_paused', positionSeconds: 1, watched: false},
  ]});
  assert.deepEqual([onlyPaused.applied, onlyPaused.conflicts], [0, 0]);
  // An outcome this app does not know, or an entry it cannot read, leaves out that entry alone.
  const newer = parseDeferredProgressReceipt({applied: 1, conflicts: 2, later: true, entries: [
    {playId: 'play-1', itemId: 'item', outcome: 'applied', positionSeconds: 1, watched: false, device: 'tv'},
    {playId: 'play-2', itemId: 'other', outcome: 'no_such_outcome', positionSeconds: 1, watched: false},
    {playId: 'play-3', itemId: 'third', outcome: 'stale_observation', positionSeconds: -4, watched: false},
  ]});
  assert.deepEqual(newer, {applied: 1, conflicts: 0, entries: [{playId: 'play-1', itemId: 'item', outcome: 'applied', positionSeconds: 1, watched: false, observedAt: ''}]});
  assert.throws(() => parseDeferredProgressReceipt({applied: 1, conflicts: 0, entries: {0: {}}}));
  assert.throws(() => parseDeferredProgressReceipt({applied: 0, conflicts: 101, entries: Array.from({length: 101}, () => ({playId: 'p', itemId: 'item', outcome: 'invalid_entry', positionSeconds: 1, watched: false}))}));
  assert.throws(() => parseDeferredProgressReceipt({applied: 'one', conflicts: 0, entries: []}));
  assert.equal(validDeferredProgressEntry({playId: 'play-1', itemId: 'item', positionSeconds: 12, observedAt: '2026-09-14T09:12:00Z'}), true);
  assert.equal(validDeferredProgressEntry({playId: 'play-1', itemId: 'item', positionSeconds: 1, observedAt: '2026-09-14T09:12:00Z', ended: true}), true);
  assert.equal(validDeferredProgressEntry({playId: 'play-1', itemId: 'item', positionSeconds: 1, observedAt: '2026-09-14T09:12:00Z', ended: 'yes' as any}), false);
  assert.equal(validDeferredProgressEntry({playId: 'play-1', itemId: 'item', positionSeconds: -1, observedAt: '2026-09-14T09:12:00Z'}), false);
  assert.equal(validDeferredProgressEntry({playId: 'play-1', itemId: 'item', positionSeconds: Number.NaN, observedAt: '2026-09-14T09:12:00Z'}), false);
  assert.equal(validDeferredProgressEntry({itemId: 'item', positionSeconds: 1, observedAt: '2026-09-14T09:12:00Z'} as any), false);
  assert.equal(validDeferredProgressEntry({playId: 'play-1', itemId: 'item', positionSeconds: 1, observedAt: 'soon'}), false);
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

test('a bundle names only files under the grant, never a path out of it', () => {
  const parsed = parseDownloadBundle(bundle(), 'prep');
  assert.equal(parsed.artwork[0].path, 'artwork/poster.jpg');
  assert.equal(parsed.title.title, 'Pilot');
  assert.throws(() => parseDownloadBundle({...bundle(), preparationId: 'other'}, 'prep'));
  for (const path of ['artwork/../poster.jpg', '/artwork/poster.jpg']) {
    assert.throws(() => parseDownloadBundle({...bundle(), artwork: [{role: 'poster', path, contentType: 'image/jpeg'}]}, 'prep'), `accepted ${path}`);
  }
});

test('unreadable descriptive bundle rows cost only themselves while authority and bounds stay exact', () => {
  resetSkippedParts();
  const chapter = {index: 0, title: 'Opening', startSeconds: 0, endSeconds: 30};
  const marker = {kind: 'intro', startSeconds: 0, endSeconds: 10, automatic: true};
  const audio = {index: 1, language: 'en', title: 'English', codec: 'aac', channels: 2, default: true};
  const raw = {...bundle(), chapters: [chapter, {...chapter, endSeconds: -1}], markers: [{...marker, automatic: 'yes'}, marker], audio: [audio, {...audio, channels: -1}]};
  const parsed = parseDownloadBundle(raw, 'prep');
  assert.deepEqual(parsed.chapters, [chapter]);
  assert.deepEqual(parsed.markers, [marker]);
  assert.deepEqual(parsed.audio, [audio]);
  assert.equal(parsed.artwork[0].path, 'artwork/poster.jpg');
  assert.deepEqual(skippedParts().map(p => [p.part, p.count]), [['bundle.chapters', 1], ['bundle.markers', 1], ['bundle.audio', 1]]);
  for (const [field, bound] of [['chapters', 2000], ['markers', 256], ['audio', 64]] as const) {
    assert.throws(() => parseDownloadBundle({...bundle(), [field]: Array(bound + 1).fill(null)}, 'prep'));
  }
  assert.throws(() => parseDownloadBundle({...raw, preparationId: 'other'}, 'prep'));
  assert.throws(() => parseDownloadBundle({...raw, artwork: [{role: 'poster', path: 'artwork/../escape', contentType: 'image/jpeg'}]}, 'prep'));
});

test('downloaded subtitle originals and fonts retain server bundle paths without live grants', () => {
  const files = {
    ass: {path: 'subtitles/track/3.ass', contentType: 'text/x-ssa'},
    vobsub: {path: 'subtitles/track/3.idx', contentType: 'text/plain'},
    sub: {path: 'subtitles/track/3.sub', contentType: 'application/octet-stream'},
    font0: {path: 'subtitles/track/3.font0', contentType: 'font/ttf'},
  };
  const subtitle = {id: 'track', revision: 3, language: 'en', title: 'English', default: false, forced: false, files};
  const parsed = parseDownloadBundle({...bundle(), subtitles: [subtitle]}, 'prep');
  assert.deepEqual(parsed.subtitles[0].files, files);
  assert.throws(() => parseDownloadBundle({...bundle(), subtitles: [{...subtitle, files: {...files, ass: {...files.ass, path: '/v1/media/subtitles/live/track/3/original.ass'}}}]}, 'prep'));
  // An image track carries its forced-line count; a count that cannot be true is left out.
  assert.deepEqual(parseDownloadBundle({...bundle(), subtitles: [{...subtitle, lines: {forced: 2, total: 40}}]}, 'prep').subtitles[0].lines, {forced: 2, total: 40});
  assert.equal(parseDownloadBundle({...bundle(), subtitles: [{...subtitle, lines: {forced: 41, total: 40}}]}, 'prep').subtitles[0].lines, undefined);
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

test('N13: chapter thumbnails and scrubbing previews travel in the bundle; a broken one is left out', () => {
  const tiles = [{path: 'trickplay/set1/tiles/0.jpg', contentType: 'image/jpeg'}, {path: 'trickplay/set1/tiles/1.jpg', contentType: 'image/jpeg'}];
  const trickplay = {id: 'set1', tileWidth: 320, tileHeight: 180, columns: 10, rows: 10, intervalSeconds: 10, frameCount: 150, durationSeconds: 1500, sourceOffsetSeconds: 0, thumbnails: {path: 'trickplay/set1/thumbnails.vtt', contentType: 'text/vtt'}, tiles};
  const raw = bundle() as Record<string, unknown>;
  const chapters = [{index: 1, title: 'One', startSeconds: 0, endSeconds: 60, image: {path: 'chapters/asset1/1/image', contentType: 'image/jpeg'}}, {index: 2, title: 'Two', startSeconds: 60, endSeconds: 120, image: {path: '../x', contentType: 'image/jpeg'}}];
  const parsed = parseDownloadBundle({...raw, chapters, trickplay}, 'prep');
  assert.equal(parsed.chapters[0]?.image?.path, 'chapters/asset1/1/image');
  assert.equal(parsed.chapters[1]?.image, undefined);
  assert.equal(parsed.trickplay?.tiles.length, 2);
  // Too few sheets for its frames: the previews are left out, the bundle stands.
  const short = parseDownloadBundle({...raw, trickplay: {...trickplay, tiles: tiles.slice(0, 1)}}, 'prep');
  assert.equal(short.trickplay, undefined);
  assert.equal(short.preparationId, 'prep');
});
