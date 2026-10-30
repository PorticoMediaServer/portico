import test from 'node:test';
import assert from 'node:assert/strict';
import {activityIndicator} from '../src/shell/activity-indicator.ts';
import type {ActivityPanelView} from '../src/kit/shell';

const quiet: ActivityPanelView = {serverName: 'Server', phase: 'ready', streamCount: 0, taskCount: 0,
  streams: [], scans: [], tasks: [], onDashboard() {}};
test('rail shows update and working without requiring a stream', () => {
  const update = activityIndicator({...quiet, update: {available: true, onOpen() {}}});
  assert.equal(update.dot, 'update'); assert.equal(update.count, 0);
  assert.equal(activityIndicator({...quiet, taskCount: 1}).busy, true);
  assert.equal(activityIndicator({...quiet, scans: [{id: 'scan', title: 'Library', icon: 'i-refresh', onOpen() {}}]}).busy, true);
});
test('major issue owns the red dot while streams and update retain their independent facts', () => {
  const issue = {title: 'Storage', description: 'Storage issue', onOpen() {}};
  const state = activityIndicator({...quiet, streamCount: 3, update: {available: true, onOpen() {}}, majorIssue: issue});
  assert.equal(state.dot, 'major'); assert.equal(state.majorIssue, issue);
  assert.equal(state.count, 3); assert.equal(state.update, true); assert.equal(state.busy, true);
});
test('loading and failed summaries do not expose stale indicators', () => {
  for (const phase of ['loading', 'error'] as const) {
    assert.deepEqual(activityIndicator({...quiet, phase, streamCount: 3, update: {available: true, onOpen() {}}, majorIssue: {title: 'Old', description: '', onOpen() {}}}),
      {count: undefined, update: false, majorIssue: undefined, busy: false, dot: undefined});
  }
});
