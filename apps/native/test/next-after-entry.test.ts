// The one-shot "open Profiles once the main profile is in" request (Add profile and Manage profiles on Who's watching).
import test from 'node:test';
import assert from 'node:assert/strict';
import {afterEntry} from '../src-next/shell/after-entry.ts';

test('the Profiles request opens only for the main profile it was made for, and only once', () => {
  afterEntry.request({kind: 'profiles'}, 'main');
  assert.equal(afterEntry.take('kids'), undefined, 'another profile never gets it');
  assert.equal(afterEntry.take('main'), undefined, 'and it is used up by that look');
  afterEntry.request({kind: 'profiles'}, 'main');
  assert.deepEqual(afterEntry.take('main'), {kind: 'profiles'});
  assert.equal(afterEntry.take('main'), undefined, 'once');
  afterEntry.request({kind: 'profiles'}, 'main'); afterEntry.clear();
  assert.equal(afterEntry.take('main'), undefined, 'choosing another profile or going back drops it');
});
