import {test} from 'node:test';
import assert from 'node:assert/strict';
import {matchesBuildPin, buildStability} from './build.mjs';
const commit = '4dadbd216320b69f9b0268c1e63bc37bdf0ec5ab';
const stamp = {version: 'server-c4dde3e3', id: 'server-a', build: {commit}};
test('the complete supplied pin is compared, including after character 12', () => {
  assert.equal(matchesBuildPin(commit, commit), true);
  assert.equal(matchesBuildPin(commit, commit.slice(0, 9)), true);
  assert.equal(matchesBuildPin(commit, commit.slice(0, 12) + '0'.repeat(28)), false);
  for (const pin of ['', '4dad', 'not-a-sha']) assert.equal(matchesBuildPin(commit, pin), false);
});
test('a separate static and server pin is stable when both reads agree', () => {
  assert.equal(buildStability(stamp, structuredClone(stamp)).valid, true);
});
test('missing or unreadable deployment evidence invalidates the run', () => {
  for (const bad of [null, {}, {...stamp, version: null}, {...stamp, id: ''}, {...stamp, build: null}, {...stamp, build: {commit: 'bad'}}]) {
    assert.equal(buildStability(stamp, bad).valid, false);
    assert.equal(buildStability(bad, stamp).valid, false);
  }
});
test('server replacement, static drift and server version drift invalidate the run', () => {
  for (const changed of [{...stamp, id: 'server-b'}, {...stamp, version: 'new'}, {...stamp, build: {commit: 'a'.repeat(40)}}]) {
    assert.equal(buildStability(stamp, changed).valid, false);
  }
});
