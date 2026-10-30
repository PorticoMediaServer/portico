import test from 'node:test';
import assert from 'node:assert/strict';
import {parseActivitySummary, ActivitySummaryContractError} from '../src/admin/activity-summary/parser.ts';

const stamp = '2026-10-22T07:00:00.000000001Z';
const fence = 'a'.repeat(64);
const session = (id = 'session') => ({
  id, user: {id: 'account', username: 'account name'}, profile: {id: 'profile', name: 'profile name'}, device: {id: 'device', name: 'device name'},
  kind: 'vod', state: 'playing', startedAt: stamp, converting: true, positionMs: 1234, durationMs: 5000,
  item: {id: 'episode', title: 'Episode title', kind: 'episode', showId: 'show', showTitle: 'Show title', seasonNumber: 1, episodeNumber: 3, episodeNumbering: 'aired', airDate: '2023-01-29', year: 2023, art: [{kind: 'show', id: 'show', role: 'poster', revision: 'digest'}]},
  decision: {video: {action: 'transcode', reasons: ['quality_requested'], to: 'h264'}}, quality: {mode: 'limit', maxHeight: 1080}, videoHeight: 720,
});
const scan = () => ({libraryId: 'movies', libraryName: 'Movies', kind: 'movie', discovered: 80, analyzed: 51, total: 80, current: 'Film (2001)/Film.mkv', phase: 'analysis'});
const task = (id = 'task') => ({id, kind: 'checksum', state: 'running', phase: 'checksum', libraryId: 'movies', libraryName: 'Movies', processed: 128, total: 1024, unit: 'bytes'});
const document = (): any => ({scope: {serverId: 'server', viewerFence: fence}, observedAt: stamp, serverName: 'Home server', streams: {count: 1, convertingCount: 1, overflow: {count: 0, convertingCount: 0}, items: [session()]}, scans: [scan()], tasks: {count: 1, items: [task()]}, update: {available: true, state: 'available', current: '0.1.0-dev', latest: '0.2.0'}, majorIssues:[], health: {degraded: false}, lastScanFinishedAt: '2026-10-21T07:00:00Z'});
function rejects(change: (raw: any) => void): void { const raw = document(); change(raw); assert.throws(() => parseActivitySummary(raw), ActivitySummaryContractError); }

test('busy summary retains independent source identities, episode facts, actual height and all owner facts', () => {
  const raw = document(), parsed = parseActivitySummary(raw);
  assert.deepEqual(parsed, raw);
  assert.deepEqual(parsed.streams.items[0].profile, {id: 'profile', name: 'profile name'});
  assert.equal(parsed.streams.items[0].quality?.maxHeight, 1080);
  assert.equal(parsed.streams.items[0].videoHeight, 720);
  assert.equal(parsed.streams.items[0].item?.showId, 'show');
  assert.equal(parsed.observedAt, stamp);
  assert.ok(Object.isFrozen(parsed)); assert.ok(Object.isFrozen(parsed.scans[0])); assert.ok(Object.isFrozen(parsed.streams.items[0].item?.art));
  raw.streams.items[0].profile.name = 'overwritten';
  assert.equal(parsed.streams.items[0].profile.name, 'profile name');
});
test('quiet, update-only and degraded documents retain real cached update and last scan facts', () => {
  const raw = document(); raw.streams = {count: 0, convertingCount: 0, overflow: {count: 0, convertingCount: 0}, items: []}; raw.scans = []; raw.tasks = {count: 0, items: []};
  for (const [state, available] of [['unconfigured', false], ['unavailable', false], ['current', false], ['available', true]] as const) {
    raw.update = {state, available, current: '0.1.0-dev', ...(available ? {latest: '0.2.0'} : {})}; raw.health.degraded = true;
    assert.deepEqual(parseActivitySummary(raw), raw);
  }
});
test('exact totals survive three visible sessions and three visible tasks without client pagination', () => {
  const raw = document(); raw.streams = {count: 23, convertingCount: 7, overflow: {count: 20, convertingCount: 4}, items: Array.from({length: 3}, (_, n) => session('s' + n))}; raw.tasks = {count: 11, items: Array.from({length: 3}, (_, n) => task('t' + n))};
  const parsed = parseActivitySummary(raw);
  assert.equal(parsed.streams.count, 23); assert.equal(parsed.tasks.count, 11);
  assert.equal(parsed.streams.items.length, 3); assert.equal(parsed.tasks.items.length, 3);
});
test('unknown denominators remain absent, including STRM bytes larger than its descriptor', () => {
  const raw = document(); delete raw.scans[0].total; delete raw.scans[0].current;
  raw.tasks.items[0] = {...task(), processed: 2_000_000}; delete raw.tasks.items[0].total;
  delete raw.streams.items[0].videoHeight;
  const parsed = parseActivitySummary(raw);
  assert.equal(parsed.scans[0].discovered, 80); assert.equal(parsed.scans[0].analyzed, 51);
  assert.equal(parsed.scans[0].total, undefined); assert.equal(parsed.scans[0].current, undefined);
  assert.equal(parsed.tasks.items[0].processed, 2_000_000); assert.equal(parsed.tasks.items[0].total, undefined);
  assert.equal(parsed.streams.items[0].videoHeight, undefined);
  assert.equal(Object.hasOwn(parsed.scans[0], 'percentage'), false);
});
test('known zero denominator remains zero and unmeasured tasks remain absent, without fabricated progress', () => {
  const raw = document(); raw.scans[0] = {...scan(), discovered: 0, analyzed: 0, total: 0};
  raw.tasks.items[0] = {id: 'segments:movies', kind: 'segments', phase: 'segment_evidence', state: 'queued', unit: 'files'};
  const parsed = parseActivitySummary(raw);
  assert.equal(parsed.scans[0].total, 0); assert.equal(parsed.tasks.items[0].processed, undefined);
});
test('music facts are independent and channel identity needs no synthetic item or duration', () => {
  const raw = document(); raw.streams.items[0].item = {id: 'song', kind: 'song', title: 'Song name', artist: 'Artist name', album: 'Album name', art: []}; raw.streams.items[0].kind = 'audio';
  const parsed = parseActivitySummary(raw); assert.equal(parsed.streams.items[0].item?.artist, 'Artist name'); assert.equal(parsed.streams.items[0].item?.album, 'Album name');
  raw.streams.convertingCount = 0; raw.streams.items[0] = {id: 'channel-session', user: {id: 'a'}, startedAt: stamp, converting: false, profile: {id: 'p', name: ''}, device: {id: 'd', name: ''}, kind: 'channel', state: 'starting', positionMs: 0, decision: {}};
  assert.equal(parseActivitySummary(raw).streams.items[0].item, undefined);
});
test('nonfinite, negative, fractional and unsafe counters never become clamped rings', () => {
  for (const value of [NaN, Infinity, -Infinity, -1, 1.5, Number.MAX_SAFE_INTEGER + 1, '1', null]) {
    for (const field of ['discovered', 'analyzed', 'total']) rejects(raw => {raw.scans[0][field] = value;});
    for (const field of ['processed', 'total']) rejects(raw => {raw.tasks.items[0][field] = value;});
    rejects(raw => {raw.streams.count = value;}); rejects(raw => {raw.tasks.count = value;});
    rejects(raw => {raw.streams.items[0].positionMs = value;});
  }
});
test('contradictory totals, counts, caps and duplicate identities fail closed', () => {
  for (const change of [
    (raw: any) => {raw.streams.items = Array.from({length: 4}, (_, n) => session('s' + n)); raw.streams.count = 4;},
    (raw: any) => {raw.tasks.items = Array.from({length: 4}, (_, n) => task('t' + n)); raw.tasks.count = 4;},
    (raw: any) => {raw.streams.count = 0;}, (raw: any) => {raw.tasks.count = 0;},
    (raw: any) => {raw.streams.items = [session(), session()]; raw.streams.count = 2;},
    (raw: any) => {raw.tasks.items = [task(), task()]; raw.tasks.count = 2;},
    (raw: any) => {raw.scans = [scan(), scan()];}, (raw: any) => {raw.scans[0].analyzed = 81;},
    (raw: any) => {raw.scans[0].total = 79;}, (raw: any) => {raw.tasks.items[0].processed = 1025;},
    (raw: any) => {delete raw.tasks.items[0].processed;},
  ]) rejects(change);
});
test('no unexpected private fields, capability URLs, absolute files, invalid scope or malformed timestamps survive', () => {
  for (const change of [
    (raw: any) => {raw.token = 'credential';}, (raw: any) => {raw.streams.items[0].url = 'capability';},
    (raw: any) => {raw.streams.items[0].item.path = '/private/media';},
    (raw: any) => {raw.scope.viewerFence = '';}, (raw: any) => {raw.scope.serverId = '';},
    (raw: any) => {raw.scans[0].current = '/media/Film.mkv';}, (raw: any) => {raw.scans[0].current = '../Film.mkv';},
    (raw: any) => {raw.scans[0].current = 'C:\\Media\\Film.mkv';}, (raw: any) => {raw.scans[0].current = '\\\\server\\share';},
    (raw: any) => {raw.scans[0].current = 'Film\n.mkv';}, (raw: any) => {raw.observedAt = '2026-02-30T00:00:00Z';},
    (raw: any) => {raw.lastScanFinishedAt = 'yesterday';}, (raw: any) => {raw.observedAt = '2026-10-22T07:00:00-03:00';},
    (raw: any) => {raw.streams.items[0].item.airDate = '2026-02-30';},
    (raw: any) => {raw.scans = null;}, (raw: any) => {raw.tasks.items = {};},
  ]) rejects(change);
});
test('quality request is validated independently of committed conversion height', () => {
  const raw = document();
  for (const quality of [{mode: 'original'}, {mode: 'fixed', id: '720p-high'}, {mode: 'automatic'}, {mode: 'automatic', id: '1080p-low'}, {mode: 'limit', maxHeight: 1080}]) {
    raw.streams.items[0].quality = quality; assert.equal(parseActivitySummary(raw).streams.items[0].videoHeight, 720);
  }
  for (const change of [
    (raw: any) => {raw.streams.items[0].quality = {mode: 'fixed', maxHeight: 720};},
    (raw: any) => {raw.streams.items[0].quality = {mode: 'original', id: '1080p-low'};},
    (raw: any) => {raw.streams.items[0].quality = {mode: 'automatic', step: 'down'};},
    (raw: any) => {raw.streams.items[0].videoHeight = NaN;},
    (raw: any) => {raw.update.available = false;}, (raw: any) => {delete raw.update.latest;},
  ]) rejects(change);
});

test('username provenance, actual start and complete overflow facts survive without invented identity', () => {
  const raw = document(); delete raw.streams.items[0].user.username;
  const parsed = parseActivitySummary(raw);
  assert.deepEqual(parsed.streams.items[0].user, {id: 'account'});
  assert.equal(parsed.streams.items[0].startedAt, stamp);
  assert.equal(parsed.streams.convertingCount, 1);
  assert.deepEqual(parsed.streams.overflow, {count: 0, convertingCount: 0});
  for (const change of [
    (raw: any) => {raw.streams.items[0].user.name = 'profile substitute';},
    (raw: any) => {raw.streams.items[0].user.username = '';},
    (raw: any) => {raw.streams.items[0].user.username = '   ';},
    (raw: any) => {delete raw.streams.items[0].startedAt;},
    (raw: any) => {raw.streams.items[0].startedAt = 'yesterday';},
    (raw: any) => {raw.streams.items[0].problem = true;},
    (raw: any) => {raw.streams.items[0].converting = false;},
    (raw: any) => {raw.streams.convertingCount = 0;},
    (raw: any) => {raw.streams.convertingCount = 2;},
    (raw: any) => {raw.streams.overflow.count = 1;},
    (raw: any) => {raw.streams.overflow.convertingCount = 1;},
    (raw: any) => {delete raw.streams.overflow;},
  ]) rejects(change);
});
test('global priority then nanosecond recency and stable id order are validated without client reslicing', () => {
  const raw = document();
  const problem = {...session('problem'), state: 'buffering', problem: 'buffering', bufferingSince:'2026-10-22T06:59:40Z', converting: false, decision: {}, startedAt: '2026-10-20T00:00:00Z'};
  const converter = {...session('converter'), startedAt: '2026-10-21T00:00:00.000000001Z'};
  const recent = {...session('recent'), converting: false, decision: {video: {action: 'copy', reasons: []}}, quality: {mode: 'limit', maxHeight: 2160}};
  raw.streams = {count: 7, convertingCount: 3, overflow: {count: 4, convertingCount: 2}, items: [problem, converter, recent]};
  const parsed = parseActivitySummary(raw);
  assert.deepEqual(parsed.streams.items.map(item => item.id), ['problem', 'converter', 'recent']);
  assert.equal(parsed.streams.items[2].converting, false);
  assert.equal(parsed.streams.overflow.convertingCount, 2);
  for (const items of [[recent, converter, problem], [converter, problem, recent]]) {
    assert.throws(() => parseActivitySummary({...raw, streams: {...raw.streams, items}}), ActivitySummaryContractError);
  }
  const a = {...session('a'), startedAt: '2026-10-22T07:00:00.000000002Z'}, b = {...session('b'), startedAt: stamp};
  raw.streams = {count: 2, convertingCount: 2, overflow: {count: 0, convertingCount: 0}, items: [a, b]};
  assert.equal(parseActivitySummary(raw).streams.items[0].id, 'a');
  raw.streams.items = [b, a]; assert.throws(() => parseActivitySummary(raw), ActivitySummaryContractError);
  b.startedAt = a.startedAt; raw.streams.items = [a, b]; assert.equal(parseActivitySummary(raw).streams.items[0].id, 'a');
  raw.streams.items = [b, a]; assert.throws(() => parseActivitySummary(raw), ActivitySummaryContractError);
});

test('qualified problems require current producer evidence and sustained buffering; health alone is independent',()=>{
 const raw=document();const item=raw.streams.items[0];
 item.transcode={speed:0.5,observedAt:'2026-10-22T06:59:59Z',cadenceMs:1000};item.problem='behind';
 assert.equal(parseActivitySummary(raw).streams.items[0].problem,'behind');
 for(const speed of [NaN,Infinity,-1])assert.throws(()=>parseActivitySummary({...raw,streams:{...raw.streams,items:[{...item,transcode:{...item.transcode,speed}}]}}),ActivitySummaryContractError);
 item.transcode.observedAt='2026-10-22T06:59:54Z';assert.throws(()=>parseActivitySummary(raw),ActivitySummaryContractError);
 delete item.transcode;delete item.problem;item.state='buffering';item.bufferingSince='2026-10-22T06:59:59Z';
 assert.equal(parseActivitySummary(raw).streams.items[0].problem,undefined);
 item.problem='buffering';assert.throws(()=>parseActivitySummary(raw),ActivitySummaryContractError);
 item.bufferingSince='2026-10-22T06:59:40Z';assert.equal(parseActivitySummary(raw).streams.items[0].problem,'buffering');
 raw.health.degraded=true;assert.deepEqual(parseActivitySummary(raw).majorIssues,[]);
 raw.majorIssues=[{kind:'remote-source',code:'remote_unreachable',severity:'playback_stop',subject:'Archive',destination:{kind:'remote-source',target:'archive'}}];
 assert.equal(parseActivitySummary(raw).majorIssues[0].subject,'Archive');
 raw.majorIssues[0].token='private';assert.throws(()=>parseActivitySummary(raw),ActivitySummaryContractError);
});

test('qualified issue code, effect and destination agree instead of inferring a dot from a count',()=>{
 const raw=document();const issue={kind:'remote-source',code:'remote_unreachable',severity:'playback_stop',subject:'Archive',destination:{kind:'remote-source',target:'archive'}};
 raw.majorIssues=[issue];assert.equal(parseActivitySummary(raw).majorIssues.length,1);
 for(const change of [{...issue,code:'scan_failed'},{...issue,severity:'warning'},{...issue,destination:{kind:'settings',target:'/private'}},{...issue,kind:'library'},{...issue,destination:{kind:'remote-source',target:''}}]){
  assert.throws(()=>parseActivitySummary({...raw,majorIssues:[change]}),ActivitySummaryContractError);
 }
});
