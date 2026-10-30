import {strict as assert} from 'node:assert';
import {test} from 'node:test';
import {channelDaySegments} from '../src/screens/settings/server/channels/channel-hours-layout.ts';

test('a daytime block occupies its exact minutes only on selected weekdays', () => {
  const block = {id: 'day', weekdays: [1, 3], startMinute: 360, endMinute: 540};
  assert.deepEqual(channelDaySegments([block], 1), [{block, start: 360, end: 540, continuation: false}]);
  assert.deepEqual(channelDaySegments([block], 2), []);
});

test('overnight blocks carry into the next day including the end of the week', () => {
  const block = {id: 'night', weekdays: [0, 6], startMinute: 1380, endMinute: 120};
  assert.deepEqual(channelDaySegments([block], 0), [{block, start: 1380, end: 1440, continuation: false}, {block, start: 0, end: 120, continuation: true}]);
  assert.deepEqual(channelDaySegments([block], 1), [{block, start: 0, end: 120, continuation: true}]);
  assert.deepEqual(channelDaySegments([block], 2), []);
});

test('midnight endings do not create zero-height blocks and a full day stays 24 hours', () => {
  const night = {id: 'night', weekdays: [1], startMinute: 1200, endMinute: 0};
  assert.deepEqual(channelDaySegments([night], 2), []);
  const allDay = {id: 'all', weekdays: [1], startMinute: 0, endMinute: 0};
  const segments = channelDaySegments([allDay], 1);
  assert.equal(segments.length, 1);assert.equal(segments[0]!.end - segments[0]!.start, 1440);
});
