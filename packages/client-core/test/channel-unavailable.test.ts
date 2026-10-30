import test from 'node:test';
import assert from 'node:assert/strict';
import {unavailableIsServerWide, unavailableMessage, type LinearUnavailableReason} from '../src/channel-guide.ts';

const reasons: LinearUnavailableReason[] = ['', 'delivery-unavailable', 'recording-unavailable', 'not-recordable', 'source-disabled', 'source-unavailable', 'capacity-unavailable', 'permission-denied', 'no-schedule', 'decoder_confinement_unavailable', 'ffmpeg_not_configured', 'decoder_dependencies_unavailable', 'schedule_preparing', 'delivery_unavailable', 'ffprobe_not_configured', 'channel_runtime_unavailable'];

test('channel reasons read as plain words, never engineering text, in US English', () => {
  assert.equal(unavailableMessage(''), '');
  for (const r of reasons.filter(Boolean)) {
    const text = unavailableMessage(r);
    assert.ok(text.length > 0, r);
    assert.doesNotMatch(text, /confinement|ffmpeg|ffprobe|decoder|delivery|runtime|dependenc|programme|authoriz/i, `${r}: ${text}`);
  }
  assert.equal(unavailableMessage('decoder_confinement_unavailable'), 'Live TV isn’t available on this server yet.');
  assert.equal(unavailableMessage('permission-denied'), 'You don’t have access to this channel.');
});

test('server-wide reasons are told apart from a channel’s own', () => {
  for (const r of ['decoder_confinement_unavailable', 'ffmpeg_not_configured', 'ffprobe_not_configured', 'decoder_dependencies_unavailable', 'channel_runtime_unavailable', 'delivery_unavailable', 'delivery-unavailable', 'recording-unavailable'] as const) assert.equal(unavailableIsServerWide(r), true, r);
  for (const r of ['', 'source-disabled', 'source-unavailable', 'capacity-unavailable', 'permission-denied', 'no-schedule', 'schedule_preparing', 'not-recordable'] as const) assert.equal(unavailableIsServerWide(r), false, r);
});
