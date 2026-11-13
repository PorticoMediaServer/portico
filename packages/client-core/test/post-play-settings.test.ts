import assert from 'node:assert/strict';
import test from 'node:test';
import {upNextCountdown, upNextOffer} from '../../player-core/src/up-next.ts';
import {defaultPostPlay, postPlayFor, postPlaySettings} from '../src/playback-v1/post-play.ts';
import {segmentMarkers} from '../src/playback-v1/offers.ts';
import {parseEntry, parseOptions} from '../src/playback-v1/types.ts';
import type {PreferenceSnapshot} from '../src/preferences.ts';

/** A profile's settings as `GET /v1/preferences` gives them: only `effective` is read here. */
const saved = (values: Record<string, unknown>) => ({deviceClass: 'web', deviceBound: true, registry: {revision: '1', fields: []}, documents: [], effective: values, effectiveSource: {}}) as unknown as PreferenceSnapshot;
const episode = (entryId: string, show: string, season: number, number: number) =>
  parseEntry({entryId, position: 0, itemId: 'itm_' + entryId, kind: 'episode', title: entryId, available: true, index: number, parentIndex: season, parentId: `sea_${show}${season}`, rootId: show, images: {}});
const s1e9 = episode('e9', 'shw_a', 1, 9), s1e10 = episode('e10', 'shw_a', 1, 10), s2e1 = episode('e11', 'shw_a', 2, 1);
// A 45-minute episode with trusted closing credits from 43:00 to its end.
const options = parseOptions({itemId: 'itm_e9', kind: 'episode', versions: [{id: 'med_e9', label: '', durationMs: 2_700_000, video: [], audio: [], subtitles: []}],
  chapters: [], markers: [{id: 'credits-2580000', type: 'credits', startMs: 2_580_000, endMs: 2_700_000, confidence: 'embedded', automaticSafe: true}], qualities: [],
  preferred: {versionId: 'med_e9', quality: {mode: 'original'}}, plan: {versionId: 'med_e9', mode: 'direct', engine: 'native', route: 'original', decision: {}, quality: {mode: 'original'}}, alternatives: []});
const markers = segmentMarkers(options, 'med_e9', 1).markers;

test('an owner-reviewed outro survives the rebuilt playback response', () => {
  const withOutro = parseOptions({...options, markers: [{id: 'outro-2600000', type: 'outro', startMs: 2_600_000, endMs: 2_700_000, confidence: 'manual', automaticSafe: true}]});
  assert.deepEqual(segmentMarkers(withOutro, 'med_e9', 1).markers, [{id: 'outro-2600000', kind: 'outro', startSeconds: 2600, endSeconds: 2700, automaticSafe: true}]);
});
/** What the Up next card does 30 seconds into the credits, with these settings. */
function card(values: Record<string, unknown>, next = s1e10) {
  const decided = postPlayFor(postPlaySettings(saved(values)), s1e9, next);
  const offer = upNextOffer({markers, positionSeconds: 2610, durationSeconds: 2700, kind: 'episode', mode: 'ask', nextAvailable: true, passoutDue: false});
  assert.ok(offer, 'the credits offer the next episode');
  return upNextCountdown({offer, positionSeconds: 2610, durationSeconds: 2700, rate: 1, elapsedSeconds: 0, ...decided});
}

test('with nothing saved the next episode follows after a ten-second countdown at the end', () => {
  assert.deepEqual(postPlaySettings(saved({})), defaultPostPlay);
  assert.deepEqual(card({}), {automatic: false, seconds: 90 + 10});
});

test('Autoplay next off: no countdown, nothing moves on by itself', () => {
  assert.equal(card({'playback.autoplayNext': false}), null);
  assert.equal(postPlayFor(postPlaySettings(saved({'playback.autoplayNext': false})), s1e9, s1e10).autoplay, false);
});

test('Countdown: the chosen number of seconds is what the card counts after the title', () => {
  assert.deepEqual(card({'playback.upNextCountdownMs': 5000}), {automatic: false, seconds: 95});
  assert.deepEqual(card({'playback.upNextCountdownMs': 15000}), {automatic: false, seconds: 105});
  assert.deepEqual(card({'playback.upNextCountdownMs': 0}), {automatic: false, seconds: 90});
});

test('Start during credits: with trusted closing credits the next episode starts after the countdown, during the credits', () => {
  assert.deepEqual(card({'playback.startNextDuringCredits': true}), {automatic: true, seconds: 10});
  assert.deepEqual(card({'playback.startNextDuringCredits': true, 'playback.autoplayNext': false}), null);
});

test('Stop at season end: the last episode of a season does not move on by itself; within a season it does', () => {
  const on = postPlaySettings(saved({'playback.stopAtSeasonEnd': true}));
  assert.equal(postPlayFor(on, s1e10, s2e1).autoplay, false);
  assert.equal(postPlayFor(on, s1e9, s1e10).autoplay, true);
  assert.equal(postPlayFor(on, s1e10, episode('x1', 'shw_b', 1, 1)).autoplay, false, 'another show');
  assert.equal(postPlayFor(postPlaySettings(saved({})), s1e10, s2e1).autoplay, true, 'off by default');
  assert.equal(card({'playback.stopAtSeasonEnd': true}, s2e1), null);
});
