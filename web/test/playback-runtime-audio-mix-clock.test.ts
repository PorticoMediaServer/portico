import test from 'node:test';
import assert from 'node:assert/strict';
import {MixClock} from '../src/platform/playback/audio/mix-clock.ts';

test('late suspension maps the silent gap to the joint frontier', () => {
  const clock = new MixClock();
  clock.start(10, 0); clock.admit(100);
  assert.equal(clock.at(60), 50);
  assert.equal(clock.at(510), 100);
  assert.equal(clock.rebase(510, 518), 100);
  clock.admit(250);
  assert.equal(clock.contextAt(150), 568);
  assert.equal(clock.frontier, 250);
  assert.equal(clock.at(60), 50, 'the previously audible prefix retains its old epoch');
  assert.equal(clock.at(515), 100);
  assert.equal(clock.at(568), 150);
});

test('early suspension freezes at the heard prefix, not the old frontier', () => {
  const clock = new MixClock();
  clock.start(10, 0); clock.admit(100);
  assert.equal(clock.rebase(60, 68), 50);
  clock.admit(150);
  assert.equal(clock.at(55), 45);
  assert.equal(clock.at(65), 50);
  assert.equal(clock.at(78), 60);
  clock.trimBefore(68);
  assert.equal(clock.at(78), 60);
});

test('a suspension before the future lead does not advance the logical start', () => {
  const clock = new MixClock();
  clock.start(100, 0); clock.admit(10);
  assert.equal(clock.rebase(50, 60), 0);
  clock.admit(10);
  assert.equal(clock.at(55), 0);
  assert.equal(clock.at(65), 5);
});
