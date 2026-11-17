import test from 'node:test';
import assert from 'node:assert/strict';
import {sessionSubtitlePlan} from '../src/playback-v1/offers.ts';
import {parseOptions, parseSession} from '../src/playback-v1/types.ts';
import {drawnRenderingFor, selectedSubtitleOriginal} from '../src/subtitle-originals.ts';
import type {SubtitleDelivery} from '../src/subtitles.ts';

const root = '/v1/media/subtitles/grant/1/1';
const version = (format: string, deliveries: readonly SubtitleDelivery[]) => parseOptions({itemId: 'item', versions: [{id: 'source',
  subtitles: [{id: '1', format, source: 'sidecar', deliveries}]}]}).versions[0]!;
const session = (kind: string, delivery: string, files: readonly object[]) => parseSession({id: 'session', itemId: 'item', versionId: 'source', kind: 'vod', state: 'playing',
  presentation: {generation: 2, mode: 'direct', url: '/v1/media/grant/video', startPositionMs: 10_000, decision: {},
    subtitle: {trackId: '1', kind, delivery}, subtitles: files}});
const text = {trackId: '1', format: 'vtt', url: root + '.vtt', offsetMs: -1725, timeDomain: 'source-relative'};

for (const format of ['ass', 'ssa']) test(`${format} retains authored script, fonts and timing for the existing drawn renderer`, () => {
  const file = {trackId: '1', format, url: root + '/original.' + format, fontsUrl: root + '/fonts', offsetMs: -1725, timeDomain: 'source-relative'};
  const plan = sessionSubtitlePlan(session('ass', 'device', [{...text, offsetMs: undefined}, file]), version(format, [{format: 'text'}, {format: 'ass'}]));
  assert.equal(plan.renderer, 'client');assert.equal(plan.mode, 'track');assert.equal(plan.selected?.id, '1');
  assert.equal(plan.offsetUs, '-1725000');assert.equal(plan.documentUrl, text.url, 'plain text remains the existing renderer fallback');
  assert.deepEqual(selectedSubtitleOriginal(plan), {format: 'ass', url: file.url, fontsUrl: file.fontsUrl});
  assert.deepEqual(plan.selected?.deliveries?.map(delivery => delivery.format), ['text', 'ass'], 'one delivery per kind');
});

test('served PGS, VobSub and DVB originals enable the selected track with its companion and forced-line addresses', () => {
  for (const [format, extension] of [['pgs', 'sup'], ['vobsub', 'idx'], ['dvb', 'mks']] as const) {
    const original = {format, url: root + '/original.' + extension,
      ...(format === 'vobsub' ? {companionUrl: root + '/original.sub'} : {}), ...(format !== 'dvb' ? {linesUrl: root + '/lines'} : {})};
    const plan = sessionSubtitlePlan(session(format, 'device', [{trackId: '1', ...original, offsetMs: 0, timeDomain: 'source-relative'}]), version(format, [{format}]));
    assert.equal(plan.renderer, 'client');assert.equal(plan.mode, 'track');assert.equal(plan.selected?.enabled, true);
    assert.equal(plan.offsetUs, '0');assert.equal(plan.documentUrl, undefined, 'packet files are never handed to the text reader');
    assert.deepEqual(selectedSubtitleOriginal(plan), original);
  }
});

test('an unselected track retains advertised delivery kinds without inventing a grant address', () => {
  const v = version('pgs', [{format: 'pgs'}]);
  const off = session('pgs', 'device', []);
  const plan = sessionSubtitlePlan({...off, presentation: {...off.presentation, subtitle: undefined}}, v);
  assert.equal(plan.mode, 'off');assert.equal(plan.selected, null);
  assert.equal(plan.resources[0]!.enabled, true);assert.deepEqual(plan.resources[0]!.deliveries, [{format: 'pgs'}]);
  assert.equal(drawnRenderingFor(plan.resources[0], ['pgs']), 'pgs');
  const unavailable = sessionSubtitlePlan(session('pgs', 'device', [{trackId: '2', format: 'pgs', url: '/v1/media/subtitles/grant/2/1/original.sup'}]), version('pgs', []));
  assert.equal(unavailable.resources[0]!.enabled, false);assert.equal(unavailable.selected, null);
});

test('a subtitle burned into video remains selected without also starting the app renderer', () => {
  const plan = sessionSubtitlePlan(session('ass', 'burn', [text]), version('ass', [{format: 'text'}, {format: 'ass'}]));
  assert.equal(plan.renderer, 'burn_in');assert.equal(plan.mode, 'track');assert.equal(plan.selected?.id, '1');
  assert.equal(selectedSubtitleOriginal(plan), null);
});

test('plain text keeps its WebVTT document and the current signed offset', () => {
  const plan = sessionSubtitlePlan(session('text', 'sidecar', [text]), version('srt', [{format: 'text'}]));
  assert.equal(plan.renderer, 'external_text');assert.equal(plan.mode, 'track');assert.equal(plan.documentUrl, text.url);
  assert.equal(plan.offsetUs, '-1725000');assert.equal(selectedSubtitleOriginal(plan), null);
});
