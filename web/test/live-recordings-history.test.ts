import assert from 'node:assert/strict';
import test from 'node:test';
import {enterRecordingsPage, leaveRecordingsPage, recordingsEntered, type RecordingsNavigation} from '../src/screens/live/recordings-history.ts';

// A history stack standing in for the browser's: push adds an entry, replace swaps the top, back pops it.
function history(initial: {search: Record<string, unknown>; state?: Record<string, unknown>}) {
  const stack = [{search: initial.search, state: initial.state ?? {}}];
  const nav: RecordingsNavigation = {
    state: () => stack.at(-1)!.state,
    back: () => {stack.pop();},
    navigate: async ({search, replace, state}) => {
      const next = {search, state: state ? state({...stack.at(-1)!.state}) : {}};
      if (replace) stack[stack.length - 1] = next; else stack.push(next);
    },
  };
  return {stack, nav};
}

test('entering Recordings from the guide pushes one marked entry; system Back returns to the guide', async () => {
  const {stack, nav} = history({search: {tab: 'guide'}});
  await enterRecordingsPage(nav, {tab: 'guide'}, 'upcoming');
  assert.equal(stack.length, 2);
  assert.deepEqual(stack[1]!.search, {tab: 'recordings', dvr: 'upcoming'});
  assert.equal(recordingsEntered(stack[1]!.state), true);
  // Switching views inside replaces that entry and keeps the mark.
  await enterRecordingsPage(nav, {tab: 'recordings', dvr: 'upcoming'}, 'recorded');
  assert.equal(stack.length, 2);
  assert.equal(recordingsEntered(stack[1]!.state), true);
  // System or swipe Back pops it: the guide.
  nav.back();
  assert.deepEqual(stack.map(entry => entry.search), [{tab: 'guide'}]);
});

test('the bar\'s "‹ Live TV" goes back to the guide entry rather than adding one', async () => {
  const {stack, nav} = history({search: {tab: 'guide'}});
  await enterRecordingsPage(nav, {tab: 'guide'}, 'upcoming');
  await leaveRecordingsPage(nav, {tab: 'recordings', dvr: 'upcoming'});
  assert.deepEqual(stack.map(entry => entry.search), [{tab: 'guide'}]);
});

test('Recordings opened directly (a link or a reload) leaves by replacing itself with the guide', async () => {
  const {stack, nav} = history({search: {tab: 'recordings', dvr: 'upcoming'}});
  await enterRecordingsPage(nav, {tab: 'recordings', dvr: 'upcoming'}, 'history');
  assert.equal(recordingsEntered(stack[0]!.state), false);
  await leaveRecordingsPage(nav, {tab: 'recordings', dvr: 'history'});
  assert.equal(stack.length, 1);
  assert.deepEqual(stack[0]!.search, {tab: 'guide'});
});
