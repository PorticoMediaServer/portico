import test from 'node:test';
import assert from 'node:assert/strict';
import {ownerPlaybackSwitches} from '../src/presentation/index.ts';

test('ARCH-API-08: the owner switch turns off conversion and burned-in subtitles; nothing else does', () => {
  assert.deepEqual(ownerPlaybackSwitches({transcodingEnabled: false}), {transcoding: false, subtitleBurnIn: false});
  assert.deepEqual(ownerPlaybackSwitches({transcodingEnabled: true}), {transcoding: true, subtitleBurnIn: true});
  assert.deepEqual(ownerPlaybackSwitches({features: {transcoding: 'disabled_by_owner', subtitle_burn_in: 'disabled_by_owner'}}), {transcoding: false, subtitleBurnIn: false});
  assert.deepEqual(ownerPlaybackSwitches({features: {transcoding: 'enabled', subtitle_burn_in: 'disabled_by_owner'}}), {transcoding: true, subtitleBurnIn: false});
  // not_permitted, unavailable and unknown values are not the owner's switch: never hidden here.
  assert.deepEqual(ownerPlaybackSwitches({features: {transcoding: 'unavailable', subtitle_burn_in: 'something_new'}}), {transcoding: true, subtitleBurnIn: true});
  assert.deepEqual(ownerPlaybackSwitches(null), {transcoding: true, subtitleBurnIn: true});
  assert.deepEqual(ownerPlaybackSwitches(undefined), {transcoding: true, subtitleBurnIn: true});
});
