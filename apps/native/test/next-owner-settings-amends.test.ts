import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {channelStatus, previewMatches, previewDuration} from '../src-next/screens/settings-live/server/channels/presentation.ts';

const t = defaultI18n.t;
test('channel states use the five approved labels and never present a problem as on air', () => {
  assert.deepEqual(channelStatus({state: 'healthy', healthCode: ''}), {label: 'server.channels.state.ready', tone: 'healthy'});
  assert.deepEqual(channelStatus({state: 'generating', healthCode: ''}), {label: 'server.channels.state.generating', tone: 'building'});
  assert.deepEqual(channelStatus({state: 'stale', healthCode: ''}), {label: 'server.channels.state.stale', tone: 'warning'});
  assert.deepEqual(channelStatus({state: 'disabled', healthCode: ''}), {label: 'server.channels.state.disabled', tone: 'neutral'});
  for (const state of ['no-eligible-media', 'unresolved-duration', 'new-server-token', '']) assert.deepEqual(channelStatus({state, healthCode: ''}), {label: 'server.channels.state.degraded', tone: 'danger'}, state);
  assert.deepEqual(channelStatus({state: 'healthy', healthCode: 'too_few_titles'}), {label: 'server.channels.state.degraded', tone: 'danger'});
});

test('budget-limited counts and total duration stay lower bounds, including a partially counted rule', () => {
  assert.equal(previewMatches(2, true, t), '2 titles match');
  assert.equal(previewMatches(2, false, t), 'At least 2 titles match');
  assert.equal(previewMatches(1, false, t), 'At least 1 title matches');
  const result = {durationMs: 5400000, complete: true, rules: [{complete: true}]} as any;
  assert.equal(previewDuration(result), '≈ 1.5 h');
  assert.equal(previewDuration({...result, complete: false}), '≥ 1.5 h');
  assert.equal(previewDuration({...result, rules: [{complete: false}]}), '≥ 1.5 h');
});
