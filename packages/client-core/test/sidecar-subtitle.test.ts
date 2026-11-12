import test from 'node:test';
import assert from 'node:assert/strict';
import {presentedSidecarSubtitle} from '../src/playback-v1/sidecar-subtitle.ts';
import type {Presentation} from '../src/playback-v1/types.ts';

const api = {mediaUrl: (path: string) => 'https://server.test' + path};
const presentation = (over: Partial<Presentation>) => ({generation: 1, mode: 'direct', url: '/v1/media/g/file', startPositionMs: 0, engine: 'native',
  subtitles: [{trackId: 'f3-0', format: 'vtt', url: '/v1/media/subtitles/grant/f3-0/2.vtt', offsetMs: 250}], decision: {}, quality: {mode: 'original'}, ...over}) as Presentation;

test('the sidecar subtitle in force names its WebVTT file, revision, offset and resolved address', () => {
  assert.deepEqual(presentedSidecarSubtitle(api, presentation({subtitle: {trackId: 'f3-0', kind: 'text', delivery: 'sidecar'}})),
    {trackId: 'f3-0', fileRevision: 2, documentUrl: 'https://server.test/v1/media/subtitles/grant/f3-0/2.vtt', offsetMs: 250});
});

test('nothing is drawn by the app when subtitles are off, burned in, drawn by the engine or not listed as a .vtt file', () => {
  assert.equal(presentedSidecarSubtitle(api, presentation({})), undefined);
  assert.equal(presentedSidecarSubtitle(api, presentation({subtitle: {trackId: 'f3-0', kind: 'text', delivery: 'burn'}})), undefined);
  assert.equal(presentedSidecarSubtitle(api, presentation({subtitle: {trackId: 'f3-0', kind: 'ass', delivery: 'device'}})), undefined);
  assert.equal(presentedSidecarSubtitle(api, presentation({subtitle: {trackId: 'other', kind: 'text', delivery: 'sidecar'}})), undefined);
  assert.equal(presentedSidecarSubtitle(api, presentation({subtitle: {trackId: 'f3-0', kind: 'text', delivery: 'sidecar'},
    subtitles: [{trackId: 'f3-0', format: 'srt', url: '/v1/media/subtitles/grant/f3-0/original.srt'}]})), undefined);
});
