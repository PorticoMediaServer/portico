import {test} from 'node:test';
import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';
import vm from 'node:vm';
import {fmtDur, fmtHrs, fmtMbps, fmtPct, formatAudioSource, formatDeliveryTarget, formatHistoryDuration, formatHistoryStarted, formatQuietHours, formatReportBucketTitle, formatStreamRate, formatSubtitleSource, formatTrackRoute, formatVideoSource, loadScale, niceScale} from '../src/screens/dashboard/format.ts';
import {curveSegments, loadAxisTicks, historyAxisTicks, plotLine, thinTicks, trackRoute, sessionTracks, clock, mediaKinds} from '../src/screens/dashboard/model.ts';
import type {DashboardSession} from '../src/screens/dashboard/bindings.ts';
const referenceRepo = process.env.PORTICO_DASHBOARD_DESIGN_REPO;
const reference = referenceRepo && execFileSync('git', ['-C', referenceRepo, 'show', '812dacacc70e296ac870ec36d1a37a485eaeb8ed:design/ui-v1/settings/dashboard.js'], {encoding: 'utf8'});
const session: DashboardSession = {id: 'source', user: 'person', profile: 'person', device: 'device', title: 'title', kind: 'vod', state: 'playing', positionMs: 0, location: 'local', decision: {}};
test('drawn source and delivery strings use actual selected and delivered facts', () => {
  assert.equal(formatVideoSource({id: 'v', height: 2160, codec: 'hevc', profile: 'Main 10', hdr: 'hdr10'}), '4K HEVC HDR10');
  assert.equal(formatVideoSource({id: 'v', height: 1080, codec: 'hevc', profile: 'Main 10'}), 'HEVC Main 10');
  assert.equal(formatVideoSource({id: 'v', height: 1080, codec: 'h264'}), 'H.264');
  const audio = {id: 'a', codec: 'truehd', language: 'en', layout: '7.1', channels: 8, atmos: false, commentary: false, default: true, visualImpaired: false};
  assert.equal(formatAudioSource(audio, false), 'English TrueHD 7.1');
  assert.equal(formatAudioSource({...audio, codec: 'aac', language: 'ja', layout: 'stereo', channels: 2}, false), 'Japanese AAC 2.0');
  assert.equal(formatAudioSource({...audio, codec: 'flac', bitDepth: 24, sampleRate: 96000}, true), 'FLAC 24-bit 96 kHz');
  assert.equal(formatSubtitleSource({id: 's', format: 'srt', language: 'en'}), 'English SRT');
  assert.equal(formatDeliveryTarget({...session, delivery: {streams: [], videoHeight: 1080}}, 'video', 'h264'), undefined);
  assert.equal(formatDeliveryTarget({...session, delivery: {streams: [], audioChannels: 2}}, 'audio', 'aac'), 'AAC stereo');
});
test('absent source facts never become guessed language, quality, or multichannel layout', () => {
  assert.equal(formatVideoSource({id: 'v', codec: 'hevc'}), 'HEVC');
  assert.equal(formatSubtitleSource({id: 's', format: 'srt'}), 'SRT');
  assert.equal(formatAudioSource({id: 'a', codec: 'truehd', channels: 8, atmos: false, commentary: false, default: true, visualImpaired: false}, false), 'TrueHD');
  assert.equal(formatDeliveryTarget(session, 'video'), undefined);
  assert.equal(formatDeliveryTarget(session, 'video', 'h264'), undefined);
});
test('niceScale uses the drawing steps instead of fractional quarters of the data max', () => {
  assert.deepEqual(niceScale(20 * 1.06), {top: 30, ticks: [0, 10, 20, 30]});
  assert.deepEqual(niceScale(8), {top: 8, ticks: [0, 2, 4, 6, 8]});
  assert.deepEqual(niceScale(9), {top: 10, ticks: [0, 2.5, 5, 7.5, 10]});
  assert.deepEqual(loadScale('conversions', 3.2), {top: 4, ticks: [0, 1, 2, 3, 4]});
});
test('frame numeric formatter boundaries and cardinality match', () => {
  assert.equal(fmtMbps(8), '8.0 Mbps'); assert.equal(fmtMbps(100.4), '100 Mbps'); assert.equal(fmtPct(7), '7.0%');
  assert.equal(fmtDur(2 + 5 / 60), '2h 05m'); assert.equal(fmtDur(24 / 60), '24m'); assert.equal(fmtDur(100.4), '100 h');
  assert.equal(fmtHrs(10.4), '10 h'); assert.equal(fmtHrs(9.44), '9.4 h');
  assert.equal(formatStreamRate(8000), '8 Mbps'); assert.equal(formatStreamRate(9400), '9.4 Mbps'); assert.equal(formatStreamRate(128), '128 kbps');
  assert.equal(clock(184000), '3:04'); assert.equal(clock(7500000), '2:05:00');
});
test('VS-10 maps only known per-track decisions and preserves protocol identifiers', () => {
  const raw = {...session, media: {video: {id: 'v', codec: 'hevc', height: 2160}}, delivery: {videoHeight: 1080, streams: [{kind: 'video', action: 'convert', inputCodec: 'hevc', outputCodec: 'h264'}]}};
  const track = sessionTracks(raw)[0]; assert.equal(track.reportedAction, 'convert'); assert.equal(track.route, 'converted'); assert.equal(raw.delivery.streams[0].action, 'convert');
  for (const [protocol, display] of [['direct', 'original'], ['original', 'original'], ['copy', 'repackaged'], ['convert', 'converted'], ['transcode', 'converted'], ['burn', 'converted'], ['burn_in', 'converted']]) assert.equal(trackRoute(protocol), display);
  for (const action of [undefined, 'drop', 'unknown', 'future', 'sidecar', 'device']) assert.equal(trackRoute(action), undefined);
  assert.equal(formatTrackRoute(track), 'Converted');
  assert.equal(formatTrackRoute({...track, route: 'original'}), 'Original');
  assert.equal(formatTrackRoute({...track, route: 'repackaged'}), 'Repackaged');
  assert.equal(formatTrackRoute({...track, kind: 'audio', output: 'AAC stereo'}), 'Converted to AAC stereo');
  assert.equal(formatTrackRoute({...track, kind: 'subtitles', action: 'burn'}), 'Burned in');
  assert.equal(formatTrackRoute({...track, route: undefined, reportedAction: 'drop', action: 'off'}), undefined);
  assert.equal(formatTrackRoute({...track, output: undefined}), 'Converted');
  assert.equal('route' in raw, false);
});
test('Top people retains the five drawn kinds in their drawn order', () => {
  assert.deepEqual(mediaKinds, ['movies', 'tv', 'music', 'audiobooks', 'live']);
});
test('History lengths and local date presentation match the drawn cells without resolving intervals', () => {
  assert.equal(formatHistoryDuration(180), '3 min');
  assert.equal(formatHistoryDuration(274, true), '4 min 34 s');
  assert.equal(formatHistoryDuration(7500), '2 h 5 min');
  assert.equal(formatHistoryDuration(1439.9), '24 min');
  assert.equal(formatHistoryStarted('2026-10-22T18:26:00Z', 'America/Halifax', '2026-10-22T19:15:00Z'), 'Today, 3:26 PM');
  assert.equal(formatHistoryStarted('2026-10-22T17:26:00Z', 'Asia/Kathmandu', '2026-10-22T19:15:00Z').startsWith('Today'), false);
});
test('axis labels use returned bucket starts in the viewer zone, and quiet hours copy the drawn meridiem suppression', () => {
  const buckets = [{start: '2026-10-18T18:15:00Z', end: '2026-10-19T18:15:00Z', partial: false, kinds: []}, {start: '2026-10-19T18:15:00Z', end: '2026-10-20T18:15:00Z', partial: false, kinds: []}];
  assert.deepEqual(historyAxisTicks({period: '30d', timeZone: 'Asia/Kathmandu', buckets}), [{f: 0, text: 'Oct 19'}]);
  assert.deepEqual(historyAxisTicks({period: '7d', timeZone: 'Asia/Kathmandu', buckets}), [{f: 0, text: 'Mon 19'}, {f: 1, text: 'Tue 20'}]);
  assert.equal(buckets[0].start, '2026-10-18T18:15:00Z');
  assert.equal(formatQuietHours(3), '3 to 6 AM'); assert.equal(formatQuietHours(10), '10 AM to 1 PM'); assert.equal(formatQuietHours(21), '9 PM to 12 AM');
});
test('formatter functions agree with the checked drawing over boundary values', {skip: !reference}, () => {
  const scaleSource = reference!.slice(reference!.indexOf('  function niceScale('), reference!.indexOf('  const f1 ='));
  const referenceScale = vm.runInNewContext(scaleSource + '; niceScale');
  for (const max of [0, .001, .2, 1, 2, 2.5, 5, 9.9, 10, 20, 21.2, 22, 99, 100, 1234]) assert.equal(JSON.stringify(niceScale(max)), JSON.stringify(referenceScale(max)));
  const a = reference!.indexOf('  const fmtMbps ='), b = reference!.indexOf('  const clamp =');
  const formatters = vm.runInNewContext(reference!.slice(a, b) + '; ({fmtMbps,fmtPct,fmtDur,fmtHrs})');
  for (const value of [0, .01, .1, .5, 1, 2.08333333333333, 9.44, 10.4, 99.9, 100, 1234]) for (const [name, format] of Object.entries({fmtMbps, fmtPct, fmtDur, fmtHrs})) assert.equal(format(value), formatters[name](value), `${name}(${value})`);
});
test('chart curves copy the checked monotone helper including descending stack baselines', {skip: !reference}, () => {
  const a = reference!.indexOf('  const f1 ='), b = reference!.indexOf('  const thin =');
  const drawing = vm.runInNewContext(reference!.slice(a, b) + '; ({segs,smooth,stepped})');
  const points: [number, number][] = [[64, 220], [110, 100], [160, 100], [230, 210], [350, 20]];
  for (const input of [points, [...points].reverse(), points.slice(0, 1)]) {
    assert.equal(curveSegments(input), drawing.segs(input));
    assert.equal(plotLine(input), drawing.smooth(input));
    assert.equal(plotLine(input, true), drawing.stepped(input));
  }
});
test('axis label thinning matches the checked drawing', {skip: !reference}, () => {
  const a = reference!.indexOf('  const thin ='), b = reference!.indexOf('  const MODELS =');
  const drawing = vm.runInNewContext(reference!.slice(a, b) + '; thin');
  const ticks = Array.from({length: 30}, (_, i) => ({f: i / 29, text: `Oct ${i + 1}`}));
  assert.equal(JSON.stringify(thinTicks(ticks, 854)), JSON.stringify(drawing(ticks, 854)));
});

test('load axes keep returned bucket identities, viewer zone, and exact Live labels', () => {
  const live = {period: 'live', timeZone: 'Asia/Kathmandu', from: '2026-10-22T12:13:00Z', until: '2026-10-22T12:15:00Z', buckets: []};
  assert.deepEqual(loadAxisTicks(live, 0, 120000, 840).map(t => t.text), ['2m', '1m 40s', '1m 20s', '1m', '40s', '20s', 'Now']);
  const daily = {period: '7d', timeZone: 'Asia/Kathmandu', from: '2026-10-18T18:15:00Z', until: '2026-10-20T12:15:00Z', buckets: [{start: '2026-10-18T18:15:00Z', end: '2026-10-19T18:15:00Z', partial: false}, {start: '2026-10-19T18:15:00Z', end: '2026-10-20T12:15:00Z', partial: true}]};
  assert.deepEqual(loadAxisTicks(daily, Date.parse(daily.buckets[0].start), Date.parse(daily.buckets[1].start), 840), [{f: 0, text: 'Mon'}, {f: 1, text: 'Tue'}]);
  const repeated = {period: '12h', timeZone: 'America/Halifax', from: '2026-11-01T02:00:00Z', until: '2026-11-01T08:00:00Z', buckets: ['2026-11-01T04:00:00Z', '2026-11-01T05:00:00Z'].map(start => ({start, end: '2026-11-01T05:00:00Z', partial: false}))};
  assert.equal(repeated.buckets.length, 2); // repeated 1 AM instants stay distinct, even though neither is a drawn even-hour tick.
  assert.deepEqual(loadAxisTicks(repeated, Date.parse(repeated.from), Date.parse(repeated.until), 840), []);
  assert.equal(repeated.buckets[0].start, '2026-11-01T04:00:00Z');
});

test('tooltip date copy uses drawn punctuation and actual viewer-local bucket bounds', () => {
 const bounds = {period: '7d', timeZone: 'America/Halifax', from: '2026-10-22T03:00:00Z', until: '2026-10-22T19:15:00Z', buckets: [{start: '2026-10-22T03:00:00Z', end: '2026-10-22T19:15:00Z', partial: true}]};
 assert.equal(formatReportBucketTitle(bounds, Date.parse(bounds.from), true), 'Thu Oct 22');
 assert.equal(formatReportBucketTitle(bounds, Date.parse(bounds.from)), 'Thu Oct 22, 12 AM');
 assert.equal(formatReportBucketTitle({...bounds, period: '12h'}, Date.parse(bounds.from)), 'Thu 12:00 AM');
 assert.equal(formatReportBucketTitle({...bounds, period: '1y'}, Date.parse(bounds.from)), 'Oct 22 to Oct 22');
 assert.equal(formatVideoSource({id: 'v', codec: 'unknown', hdr: 'unknown'}), '');
});
