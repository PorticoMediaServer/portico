/** Where a chosen subtitle track is, for an engine that draws it itself, and what such an engine
 * reports about the file. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {chapterAt, chaptersOf, deliveriesOf, deviceDrawnTrack, engineSubtitleRender, fontsUrlOf, kindOfFormat, parseSubtitleFonts, planEngineOf, renderingToRequest, requiredSubtitleRendering} from '../src-next/platform/playback/engine-media.ts';

const all = ['text', 'ass', 'pgs', 'vobsub', 'dvb'];
const absolute = (path: string) => 'https://portico.test' + path;
const resource = (more: Record<string, unknown> = {}): any => ({id: 'res-1', sourceId: 'src', revision: 3, scope: 'shared', language: 'en', title: 'English', format: 'ass', origin: 'embedded', rights: '', offsetUs: '0', canManage: false, enabled: true, renderer: 'external_text', default: false, forced: false, ...more});
const plan = (selected: any, more: Record<string, unknown> = {}): any => ({version: 1, sessionId: 's', generation: 1, sourceId: 'src', revision: 1, catalogRevision: 1, renderer: 'external_text', mode: 'track', offAvailable: true, offsetUs: '0', selected, resources: [selected], discovered: [], ...more});
const session = (mode: string, more: Record<string, unknown> = {}): any => ({id: 's', generation: 1, streamUrl: '/v1/media/g1/file', mode, duration: 10, resumeSeconds: 0, ...more});
const base = '/v1/media/subtitles/g1/res-1/3';

const v1 = (more: Record<string, unknown> = {}): any => ({id: 's', revision: '1', kind: 'vod', role: 'local', state: 'playing', lease: {reportEveryMs: 10000}, presentation: {generation: 1, mode: 'direct', url: '/v1/media/g1/file', startPositionMs: 0, engine: 'native', subtitles: [], decision: {}, quality: {mode: 'original'}, ...more}});

test('the engine the plan named is read from the server\'s account of the same presentation', () => {
  assert.deepEqual(planEngineOf(session('direct'), v1({engine: 'mpv'})), {engine: 'mpv', stale: false});
  assert.deepEqual(planEngineOf(session('hls'), v1()), {engine: 'native', stale: false});
  assert.deepEqual(planEngineOf(session('hls'), v1({generation: 2, engine: 'mpv'})), {stale: true}, 'a re-plan the service has not been given yet');
  assert.deepEqual(planEngineOf(session('hls'), undefined), {stale: false}, 'a download: no plan');
  assert.deepEqual(planEngineOf(undefined, v1()), {stale: false});
});

test('a track the server hands to the engine is drawn from the original it lists, or from the file being played', () => {
  const pgs = resource({id: 'res-1', format: 'pgs', origin: 'embedded'});
  const listed = v1({mode: 'stream', engine: 'mpv', subtitle: {trackId: 'res-1', kind: 'pgs', delivery: 'device'}, subtitles: [{trackId: 'res-1', format: 'pgs', url: base + '/original.sup'}]});
  assert.equal(requiredSubtitleRendering(plan(pgs, {renderer: 'client'}), listed), 'pgs');
  assert.deepEqual(engineSubtitleRender(plan(pgs, {renderer: 'client'}), session('hls'), all, absolute, undefined, listed), {kind: 'pgs', url: 'https://portico.test' + base + '/original.sup'});
  // The server turned its own copy off: the engine reads the track from the file it is playing.
  const off = plan(null, {mode: 'off', resources: [pgs]});
  const inFile = v1({engine: 'mpv', subtitle: {trackId: 'res-1', kind: 'pgs', delivery: 'device'}});
  assert.deepEqual(deviceDrawnTrack(off, inFile)?.id, 'res-1');
  assert.equal(requiredSubtitleRendering(off, inFile), 'pgs');
  assert.deepEqual(engineSubtitleRender(off, session('direct'), all, absolute, undefined, inFile), {kind: 'pgs', embedded: {language: 'en', title: 'English'}});
  assert.equal(engineSubtitleRender(off, session('direct'), ['text'], absolute, undefined, inFile), undefined, 'an engine that draws only text is never given it');
  assert.equal(deviceDrawnTrack(off, v1({subtitle: {trackId: 'res-1', kind: 'text', delivery: 'sidecar'}})), undefined, 'plain cues are the app\'s to draw');
  assert.equal(deviceDrawnTrack(off, v1({subtitle: {trackId: 'res-1', kind: 'pgs', delivery: 'burn'}})), undefined, 'burned in: nothing to draw');
  // A styled original lists its fonts.
  const styled = v1({mode: 'stream', engine: 'mpv', subtitle: {trackId: 'res-1', kind: 'ass', delivery: 'device'}, subtitles: [{trackId: 'res-1', format: 'ass', url: base + '/original.ass', fontsUrl: base + '/fonts'}]});
  assert.equal(fontsUrlOf(off, styled), base + '/fonts');
});

test('chapters carry their pictures, and the chapter a position falls in is found', () => {
  const chapters = chaptersOf([{startMs: 0, title: 'Opening', thumbnail: '/v1/items/a/chapters/0.jpg'}, {startMs: 600000, title: 'Two', thumbnail: 'https://elsewhere.test/x.jpg'}]);
  assert.deepEqual(chapters, [{startSeconds: 0, title: 'Opening', thumbnail: '/v1/items/a/chapters/0.jpg'}, {startSeconds: 600, title: 'Two'}]);
  assert.equal(chapterAt(chapters, 599)?.title, 'Opening');
  assert.equal(chapterAt(chapters, 600)?.title, 'Two');
  assert.equal(chapterAt([], 5), undefined);
});

test('deliveries are kept only as addresses under a media grant', () => {
  const r = resource({deliveries: [{format: 'ass', url: base + '/original.ass', fontsUrl: base + '/fonts'}, {format: 'text', url: base + '.vtt'}, {format: 'pgs', url: 'https://elsewhere.test/x.sup'}, {format: 'smell', url: base + '/original.ass'}, 7]});
  assert.deepEqual(deliveriesOf(r), [{format: 'ass', url: base + '/original.ass', fontsUrl: base + '/fonts'}, {format: 'text', url: base + '.vtt'}, {format: 'pgs'}]);
  assert.deepEqual(deliveriesOf(resource()), []);
});

test('the kind to ask the server for is the richest the track and the engine share', () => {
  const styled = resource({deliveries: [{format: 'text'}, {format: 'ass'}]});
  assert.equal(renderingToRequest(styled, all), 'ass');
  assert.equal(renderingToRequest(styled, ['text']), undefined, 'the native player: the ordinary choice');
  assert.equal(renderingToRequest(resource({format: 'pgs', deliveries: [{format: 'pgs'}]}), all), 'pgs');
  assert.equal(renderingToRequest(resource({format: 'srt', deliveries: [{format: 'text'}]}), all), undefined);
  assert.equal(renderingToRequest(resource(), all), undefined, 'a server that offers no deliveries');
});

test('a track delivered for the device to draw in a rich kind says which kind the engine must draw', () => {
  const chosen = resource({format: 'pgs', renderer: 'client', deliveries: [{format: 'pgs', url: base + '/original.sup'}]});
  assert.equal(requiredSubtitleRendering(plan(chosen, {renderer: 'client'})), 'pgs');
  assert.equal(requiredSubtitleRendering(plan(chosen)), undefined, 'not delivered for the device to draw');
  assert.equal(requiredSubtitleRendering(plan(resource({deliveries: [{format: 'text', url: base + '.vtt'}]}), {renderer: 'client'})), undefined, 'plain text needs no special engine');
  assert.equal(requiredSubtitleRendering(plan(chosen, {renderer: 'client', mode: 'off'})), undefined);
  assert.equal(requiredSubtitleRendering(null), undefined);
});

test('the engine is told where the track is: the original file, or the track inside the media itself', () => {
  const pgs = resource({format: 'pgs', origin: 'sidecar', deliveries: [{format: 'pgs', url: base + '/original.sup'}]});
  assert.deepEqual(engineSubtitleRender(plan(pgs, {renderer: 'client'}), session('hls'), all, absolute), {kind: 'pgs', url: 'https://portico.test' + base + '/original.sup'});

  const embedded = resource({format: 'ass', origin: 'embedded'});
  assert.deepEqual(engineSubtitleRender(plan(embedded), session('direct'), all, absolute), {kind: 'ass', embedded: {language: 'en', title: 'English'}}, 'a file played as it is carries its own tracks');
  assert.equal(engineSubtitleRender(plan(embedded), session('hls'), all, absolute), undefined, 'a repackaged stream does not: the cue document is drawn');
  assert.equal(engineSubtitleRender(plan(resource({origin: 'sidecar'})), session('direct'), all, absolute), undefined);
  assert.equal(engineSubtitleRender(plan(embedded), session('direct'), ['text'], absolute), undefined, 'an engine that draws only text');
  assert.equal(engineSubtitleRender(plan(embedded, {mode: 'off'}), session('direct'), all, absolute), undefined);

  const both = resource({format: 'ass', origin: 'embedded', deliveries: [{format: 'ass', url: base + '/original.ass'}]});
  assert.deepEqual(engineSubtitleRender(plan(both, {renderer: 'client'}), session('direct'), all, absolute), {kind: 'ass', embedded: {language: 'en', title: 'English'}, url: 'https://portico.test' + base + '/original.ass'}, 'both are given; the engine tries the file first');
});

test('a styled file\'s attached fonts are fetched before the track is given to the engine', () => {
  const styled = resource({origin: 'sidecar', deliveries: [{format: 'ass', url: base + '/original.ass', fontsUrl: base + '/fonts'}]});
  const p = plan(styled, {renderer: 'client'});
  assert.equal(fontsUrlOf(p), base + '/fonts');
  assert.equal(engineSubtitleRender(p, session('hls'), all, absolute, 'pending'), undefined, 'not yet: the list is still being read');
  const fonts = parseSubtitleFonts({fonts: [{name: 'Gandhi Sans', url: base + '/fonts/1'}, {name: 'x', url: 'https://elsewhere.test/1'}, {name: 'y', url: base + '/fonts/../../secret'}]}, base + '/fonts');
  assert.deepEqual(fonts.fonts, [{name: 'Gandhi Sans', url: base + '/fonts/1'}]);
  assert.deepEqual(engineSubtitleRender(p, session('hls'), all, absolute, fonts), {kind: 'ass', url: 'https://portico.test' + base + '/original.ass', fonts: [{name: 'Gandhi Sans', url: 'https://portico.test' + base + '/fonts/1'}]});
  assert.deepEqual(engineSubtitleRender(p, session('hls'), all, absolute, parseSubtitleFonts(null, base + '/fonts')), {kind: 'ass', url: 'https://portico.test' + base + '/original.ass'}, 'the list could not be read: the track is drawn with system fonts');
  assert.equal(fontsUrlOf(plan(resource({format: 'pgs', deliveries: [{format: 'pgs', url: base + '/original.sup'}]}))), undefined);
});

test('formats map to the kinds of the capability document, and chapters are bounded and ordered', () => {
  assert.deepEqual(['ass', 'ssa', 'pgs', 'sup', 'vobsub', 'idx', 'dvb', 'srt', 'vtt', undefined].map(kindOfFormat), ['ass', 'ass', 'pgs', 'pgs', 'vobsub', 'vobsub', 'dvb', 'text', 'text', 'text']);
  assert.deepEqual(chaptersOf([{startSeconds: 90, title: 'Two'}, {startMs: 0, title: 'One'}, {startSeconds: -1}, {title: 'no time'}, null, {startSeconds: 200}]), [{startSeconds: 0, title: 'One'}, {startSeconds: 90, title: 'Two'}, {startSeconds: 200, title: ''}]);
  assert.deepEqual(chaptersOf('nope'), []);
});
