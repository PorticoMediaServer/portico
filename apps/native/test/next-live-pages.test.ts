import assert from 'node:assert/strict';
import test from 'node:test';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

test('an upright guide reserves a live grid beneath the controls and retains the programme sheet', async () => {
  const h = hooks();
  const {GuidePage} = await screenModule(new URL('../src-next/pages/touch/live/GuidePage.tsx', import.meta.url), {
    react: h.react, '../../../kit/frames': {LiveFrame: 'LiveFrame'},
  });
  const props = {title: 'Live TV', actions: 'List or Guide', tabs: 'Filters', toolbar: 'Today', list: 'Grid', overlay: 'Programme'};
  const frame = h.render(() => GuidePage(props));
  for (const [name, value] of Object.entries(props)) assert.equal(frame.props[name], value);
});

test('an upright recordings page preserves Back and changes the view without disturbing the list', async () => {
  const h = hooks(), actions: string[] = [];
  const {RecordingsPage} = await screenModule(new URL('../src-next/pages/touch/live/RecordingsPage.tsx', import.meta.url), {
    react: h.react, '../../../kit/frames': {LiveFrame: 'LiveFrame'}, '../../../kit/controls': {Tabs: 'Tabs'},
  });
  const recorded = {value: 'recorded', label: 'Recorded'};
  const frame = h.render(() => RecordingsPage({title: 'Recordings', back: {label: 'Live TV', onPress: () => actions.push('guide')},
    choices: {items: [recorded], value: 'upcoming', onValueChange: (value: string) => actions.push(value)},
    list: 'Recordings list', footer: 'Storage', overlay: 'Recording details'}));
  assert.equal(frame.props.list, 'Recordings list');
  assert.equal(frame.props.footer, 'Storage');
  assert.equal(frame.props.overlay, 'Recording details');
  assert.equal(frame.props.tabs.props.itemProps(recorded).testID, 'recordings-view-recorded');
  frame.props.tabs.props.onValueChange('recorded');
  frame.props.back.onPress();
  assert.deepEqual(actions, ['recorded', 'guide']);
});

test('the TV guide keeps its focus controls outside the full-width grid', async () => {
  const h = hooks();
  const {TVGuidePage} = await screenModule(new URL('../src-next/pages/tv/live/GuidePage.tsx', import.meta.url), {
    react: h.react, '../../../kit/frames': {LiveFrame: 'LiveFrame'},
  });
  const frame = h.render(() => TVGuidePage({title: 'Live TV', actions: 'Guide controls', tabs: 'Filters', list: 'TV grid'}));
  assert.equal(frame.props.flushList, true);
  assert.equal(frame.props.actions, 'Guide controls');
  assert.equal(frame.props.list, 'TV grid');
});

test('TV Recordings keeps storage facts, view choices and the recorded list in one frame', async () => {
  const h = hooks();
  const {TVRecordingsPage} = await screenModule(new URL('../src-next/pages/tv/live/RecordingsPage.tsx', import.meta.url), {
    react: h.react, '../../../kit/frames': {LiveFrame: 'LiveFrame'},
  });
  const frame = h.render(() => TVRecordingsPage({title: 'Recordings', facts: '19 GB free', tabs: 'Upcoming', list: 'Recorded list'}));
  assert.equal(frame.props.facts, '19 GB free');
  assert.equal(frame.props.tabs, 'Upcoming');
  assert.equal(frame.props.list, 'Recorded list');
});
