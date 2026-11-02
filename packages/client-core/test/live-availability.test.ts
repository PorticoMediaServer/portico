import test from 'node:test';
import assert from 'node:assert/strict';
import {channelUnavailableMessage, liveGuideStale, liveServerWideNotice} from '../src/presentation/live-availability.ts';

test('a channel says why it cannot play, unless the reason is the server\'s own setup (LT-4)', () => {
  assert.equal(channelUnavailableMessage('capacity-unavailable'), 'web.live.reason.tunersBusy');
  assert.equal(channelUnavailableMessage('schedule_preparing'), 'web.live.reason.preparing');
  assert.equal(channelUnavailableMessage('source-disabled'), 'web.live.reason.sourceOff');
  assert.equal(channelUnavailableMessage('some-new-reason'), 'web.live.reason.cantPlay');
  assert.equal(channelUnavailableMessage(''), undefined);
  assert.equal(channelUnavailableMessage('channel_runtime_unavailable'), undefined, 'said once for the guide, not per row');
});

const source = (refreshState: string, unavailableReasons: Record<string, number> = {}) => ({channelCount: 4, refreshState, unavailableReasons});
test('the guide says once when Live TV is unavailable on the server, with the cause for owners only (LT-4)', () => {
  assert.equal(liveServerWideNotice([source('healthy', {'capacity-unavailable': 2})], true), undefined);
  assert.deepEqual(liveServerWideNotice([source('healthy', {channel_runtime_unavailable: 4})], false), {title: 'web.live.serverWide.title', body: 'web.live.serverWide.memberBody'});
  assert.equal(liveServerWideNotice([source('healthy', {ffmpeg_not_configured: 4})], true)?.body, 'web.live.serverWide.toolsBody');
  assert.equal(liveServerWideNotice([source('healthy', {decoder_confinement_unavailable: 4})], true)?.body, 'web.live.serverWide.platformBody');
  assert.equal(liveServerWideNotice([source('healthy', {channel_runtime_unavailable: 0})], true), undefined, 'a zero count is not a reason');
});

test('a stale guide is the server\'s refresh state, never a count of empty days (LT-3)', () => {
  assert.equal(liveGuideStale([source('healthy'), source('degraded')]), true);
  assert.equal(liveGuideStale([source('credentials-required')]), true);
  assert.equal(liveGuideStale([source('refreshing'), source('healthy')]), false);
});
