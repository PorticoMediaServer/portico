import test from 'node:test';
import assert from 'node:assert/strict';
import {isBitmapSubtitleFormat, isBitmapResource, isTextResource, textTrackForLanguage, sourceFactsOf, sourceIs4kOrHdr, bitmapWarningNeeded} from '../src/presentation/bitmap-subtitles.ts';

test('MU4 COMPAT-01: bitmap formats are PGS, VobSub/DVD and DVB spellings', () => {
  for (const f of ['pgs', 'PGS', 'sup', 'vobsub', 'VobSub', 'idx', 'dvb', 'DVBSub', ' dvb ']) assert.equal(isBitmapSubtitleFormat(f), true, f);
  for (const f of ['srt', 'vtt', 'ass', 'ssa', '', undefined, null, 42]) assert.equal(isBitmapSubtitleFormat(f), false, String(f));
});

test('MU4 COMPAT-01: a bitmap format or a burn_in renderer counts as bitmap', () => {
  assert.equal(isBitmapResource({format: 'pgs'}), true);
  assert.equal(isBitmapResource({format: 'srt', renderer: 'burn_in'}), true);
  assert.equal(isBitmapResource({format: 'srt', renderer: 'external_text'}), false);
  assert.equal(isBitmapResource({format: 'ass'}), false);
  assert.equal(isBitmapResource(null), false);
  assert.equal(isTextResource({format: 'srt'}), true);
  assert.equal(isTextResource({format: 'pgs'}), false);
});

test('MU4 COMPAT-01: a language with both picks the text track', () => {
  const text = {id: 't1', format: 'srt', language: 'en', renderer: 'external_text'};
  const bitmap = {id: 'b1', format: 'pgs', language: 'en', renderer: 'burn_in'};
  const other = {id: 't2', format: 'srt', language: 'fr', renderer: 'external_text'};
  assert.equal(textTrackForLanguage([bitmap, text, other], 'en'), text);
  assert.equal(textTrackForLanguage([bitmap, text, other], 'EN'), text);
  assert.equal(textTrackForLanguage([bitmap], 'en'), undefined);
  assert.equal(textTrackForLanguage([bitmap, text], 'de'), undefined);
  assert.equal(textTrackForLanguage([], 'en'), undefined);
  assert.equal(textTrackForLanguage(null, 'en'), undefined);
});

test('MU4 COMPAT-01: warning condition is 4K/HDR by bitmap', () => {
  const bitmap = {format: 'pgs'};
  const text = {format: 'srt'};
  const uhd = {is4k: true, isHdr: false}, hdr = {is4k: false, isHdr: true}, sdr = {is4k: false, isHdr: false};
  assert.equal(bitmapWarningNeeded(bitmap, uhd, false), true);
  assert.equal(bitmapWarningNeeded(bitmap, hdr, false), true);
  assert.equal(bitmapWarningNeeded(bitmap, sdr, false), false);
  assert.equal(bitmapWarningNeeded(text, uhd, false), false);
  assert.equal(bitmapWarningNeeded(bitmap, uhd, true), false);
  assert.equal(bitmapWarningNeeded(bitmap, null, false), false);
});

test('MU4 COMPAT-01: 4K/HDR facts from width, height and hdr markers', () => {
  assert.equal(sourceIs4kOrHdr({height: 2160}), true);
  assert.equal(sourceIs4kOrHdr({width: 3840, height: 2160}), true);
  assert.equal(sourceIs4kOrHdr({height: 1080}), false);
  assert.equal(sourceIs4kOrHdr({height: 1080, hdr: 'hdr10'}), true);
  assert.equal(sourceIs4kOrHdr({height: 1080, hdr: 'dolbyvision:8.1'}), true);
  assert.equal(sourceIs4kOrHdr({height: 1080, hdr: 'sdr'}), false);
  assert.equal(sourceIs4kOrHdr({}), false);
  assert.equal(sourceIs4kOrHdr(null), false);
});

test('MU4 COMPAT-01: source facts parse out of the playback-options answer, fail open', () => {
  const uhd = {versions: [{id: 'v1', video: [{id: 'v0', codec: 'hevc', width: 3840, height: 2160}]}]};
  assert.deepEqual(sourceFactsOf(uhd), {is4k: true, isHdr: false});
  const hdr = {versions: [{id: 'v1', video: [{id: 'v0', codec: 'hevc', width: 1920, height: 1080, hdr: 'hdr10'}]}]};
  assert.deepEqual(sourceFactsOf(hdr), {is4k: true, isHdr: true});
  assert.deepEqual(sourceFactsOf({versions: [{id: 'v1', video: [{id: 'v0', width: 1920, height: 1080}]}]}), {is4k: false, isHdr: false});
  assert.deepEqual(sourceFactsOf({}), {is4k: false, isHdr: false});
  assert.deepEqual(sourceFactsOf(null), {is4k: false, isHdr: false});
});
