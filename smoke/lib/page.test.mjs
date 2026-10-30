import {test} from 'node:test';
import assert from 'node:assert/strict';
import {playbackEvidence, seekEvidence, transcodeEvidence, projectSession, watchSessions} from './page.mjs';

// Checkpoint-only classifier fixtures. Importing the helper never loads a browser.
const frame = {present: true, videoWidth: 640, videoHeight: 360, readyState: 4,
  paused: false, currentTime: 1, duration: 10, error: null};
const samples = patch => [frame, {...frame, currentTime: 2}].map(value => ({...value, ...patch}));

for (const [name, readings, reason] of [
  ['absent video', [{present: false}], /no <video>/],
  ['undecoded response', samples({videoWidth: 0, videoHeight: 0}), /no decoded frame/],
  ['unready video', samples({readyState: 1}), /HAVE_CURRENT_DATA/],
  ['paused video', samples({paused: true}), /stayed paused/],
  ['stalled clock', samples({currentTime: 1}), /did not advance/],
  ['media error', samples({error: {code: 3, message: 'fixture decode error'}}), /video element error 3/],
]) {
  test(`checkpoint playback refuses ${name}`, () => {
    const verdict = playbackEvidence(readings);
    assert.equal(verdict.ok, false);
    assert.match(verdict.reason, reason);
  });
}

test('decoded, ready, unpaused advancing video retains measured playback values', () => {
  const readings = samples({});
  const verdict = playbackEvidence(readings);
  assert.equal(verdict.ok, true);
  assert.equal(verdict.advanced, 1);
  assert.equal(verdict.width, 640);
  assert.equal(verdict.height, 360);
  assert.equal(verdict.duration, 10);
  assert.deepEqual(verdict.readings, readings);
});

for (const [name, readings, ok] of [
  ['no samples', [], false],
  ['one decoded instant', [{...frame}], false],
  ['below half-second advance', [{...frame, currentTime: 0}, {...frame, currentTime: 0.49}], false],
  ['half-second advance', [{...frame, currentTime: 0}, {...frame, currentTime: 0.5}], true],
]) {
  test(`checkpoint playback threshold: ${name}`, () => {
    const verdict = playbackEvidence(readings);
    assert.equal(verdict.ok, ok);
    if (ok) assert.equal(verdict.advanced, 0.5);
  });
}

for (const value of [NaN, Infinity, -Infinity]) {
  test(`checkpoint playback refuses a present nonfinite clock: ${String(value)}`, () => {
    const verdict = playbackEvidence([{...frame, currentTime: value}, {...frame, currentTime: 2}]);
    assert.equal(verdict.ok, false);
    assert.equal(verdict.reason, 'currentTime contains a nonfinite measurement');
  });
}

test('checkpoint playback refuses advancement overflow between finite clocks', () => {
  const verdict = playbackEvidence([
    {...frame, currentTime: -Number.MAX_VALUE},
    {...frame, currentTime: Number.MAX_VALUE},
  ]);
  assert.equal(verdict.ok, false);
  assert.equal(verdict.reason, 'currentTime advancement is nonfinite');
});

test('an absent video sample contributes no measured clock', () => {
  const verdict = playbackEvidence([{present: false, currentTime: NaN}, ...samples({})]);
  assert.equal(verdict.ok, true);
  assert.equal(verdict.advanced, 1);
});

test('paused seek rejects a no-op and normal elapsed playback', () => {
  const before = {currentTime: 10, duration: 120, paused: true};
  assert.equal(seekEvidence(before, {currentTime: 40, paused: true}, 30).ok, true);
  assert.equal(seekEvidence(before, {currentTime: 10, paused: true}, 30).ok, false);
  assert.equal(seekEvidence(before, {currentTime: 12.3, paused: false}, 30).ok, false);
  assert.equal(seekEvidence(before, {currentTime: 40, paused: false}, 30).ok, false);
  assert.equal(seekEvidence({...before, currentTime: 115}, {currentTime: 120, paused: true}, 30).ok, true);
});

test('transcode requires a new 240p conversion generation on the same session', () => {
  const before = {id: 'session', presentation: {generation: 1}};
  const presentation = {generation: 2, mode: 'stream', decision: {video: {action: 'transcode'}}, quality: {id: '240p'}, videoHeight: 240};
  assert.equal(transcodeEvidence(before, {id: 'session', presentation}).ok, true);
  for (const change of [
    {generation: 1}, {mode: 'direct'}, {decision: {video: {action: 'copy'}}},
    {quality: {id: 'original'}}, {videoHeight: 360}, {videoHeight: undefined},
  ]) assert.equal(transcodeEvidence(before, {id: 'session', presentation: {...presentation, ...change}}).ok, false);
  assert.equal(transcodeEvidence(before, {id: 'other', presentation}).ok, false);
});

test('session evidence includes created responses and safe conversion facts', async () => {
  let respond;
  const sink = watchSessions({on(event, callback) { assert.equal(event, 'response'); respond = callback; }});
  for (const status of [201, 202]) {
    await respond({url: () => 'http://fixture/v1/playback/sessions', status: () => status,
      request: () => ({method: () => 'POST'}), json: async () => ({id: 'session', presentation: {
        generation: 2, mode: 'stream', url: '/v1/media/private-grant/master.m3u8',
        quality: {id: '240p'}, videoHeight: 240, decision: {video: {action: 'transcode'}},
      }})});
  }
  assert.equal(sink.length, 2);
  assert.equal(sink[0].session.videoAction, 'transcode');
  assert.equal(sink[0].session.qualityId, '240p');
  assert.equal(sink[0].session.presentationGeneration, 2);
  assert.equal(projectSession({presentation: {url: '/v1/media/grant/master.m3u8'}}).urlPresent, true);
});
