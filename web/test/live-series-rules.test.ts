import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {RULE_KEEP_OPTIONS, RULE_PADDING_OPTIONS, createRulesState, matchingAirings, matchingCount, rulesReducer} from '../src/screens/live/series-rules-model';
import {seriesRulesFixture as view} from '../src/dev/families/live-rules.fixture';

test('the frame starts with the Clockmakers draft and three matching airings', () => {
  const state = createRulesState(view);
  assert.equal(state.selectedId, 'clockmakers');
  assert.deepEqual(state.draft, view.rules[0]!.settings);
  assert.notEqual(state.draft, view.rules[0]!.settings);
  assert.equal(matchingAirings(state.rules[0]!, state.draft).length, 3);
  assert.equal(createRulesState({...view, selectedRuleId: 'missing'}).draft, undefined);
});
test('new episodes, channel choice and pause filter actual airing facts without mutating the view', () => {
  const rule = {...view.rules[0]!, airings: [...view.rules[0]!.airings!, {...view.rules[0]!.airings![2]!, id: 'other-channel', channelId: 'other'}]};
  assert.equal(matchingAirings(rule, {...rule.settings, episodes: 'new'}).length, 2);
  assert.equal(matchingAirings(rule, {...rule.settings, episodes: 'new', channel: 'this'}).length, 1);
  assert.equal(matchingAirings(rule, {...rule.settings, enabled: false}).length, 0);
  assert.equal(view.rules[0]!.airings!.length, 3);
  assert.equal(matchingCount(view.rules[1]!), 2);
  assert.equal(matchingAirings(view.rules[1]!).length, 0);
  assert.equal(matchingCount(view.rules[3]!), 0);
});
test('all editor controls change only the draft; Cancel closes and discards; reopening uses saved settings', () => {
  let state = createRulesState(view);
  const patch = {enabled: false, episodes: 'new', channel: 'this', keep: '3', startEarly: 0, endLate: 2} as const;
  state = rulesReducer(state, {type: 'change', patch});
  assert.deepEqual(state.draft, patch);
  assert.equal(state.rules[0]!.settings.enabled, true);
  state = rulesReducer(state, {type: 'cancel'});
  assert.equal(state.selectedId, undefined);assert.equal(state.draft, undefined);
  state = rulesReducer(state, {type: 'select', id: 'clockmakers'});
  assert.deepEqual(state.draft, view.rules[0]!.settings);
});
test('selecting another card replaces unsaved edits with that rule and Save commits locally', () => {
  let state = rulesReducer(createRulesState(view), {type: 'change', patch: {enabled: false}});
  state = rulesReducer(state, {type: 'select', id: 'world'});
  assert.deepEqual(state.draft, view.rules[1]!.settings);
  assert.equal(state.selectedId, 'world');
  state = rulesReducer(state, {type: 'change', patch: {keep: '3', startEarly: 0}});
  state = rulesReducer(state, {type: 'save'});
  assert.equal(state.selectedId, undefined);
  assert.equal(state.rules[1]!.settings.keep, '3');
  assert.equal(view.rules[1]!.settings.keep, '1');
  assert.equal(rulesReducer(state, {type: 'select', id: 'unknown'}), state);
});
test('delete removes only the selected rule and an inactive editor ignores changes', () => {
  const state = rulesReducer(createRulesState(view), {type: 'delete'});
  assert.deepEqual(state.rules.map(rule => rule.id), ['world', 'football', 'wild']);
  assert.equal(rulesReducer(state, {type: 'change', patch: {enabled: false}}), state);
  assert.equal(view.rules.length, 4);
});

test('SeriesRules and ProgrammePanel share keep/padding choices, omit the no-op switcher, use leaveRecordingsPage, and deduplicate error banners', () => {
  const rulesSource = readFileSync(new URL('../src/screens/live/SeriesRules.tsx', import.meta.url), 'utf8');
  const entrySource = readFileSync(new URL('../src/screens/live/SeriesRulesEntry.tsx', import.meta.url), 'utf8');
  const panelSource = readFileSync(new URL('../src/screens/live/ProgrammePanel.tsx', import.meta.url), 'utf8');
  assert.deepEqual([...RULE_KEEP_OPTIONS], ['all', '1', '3', '5', '10', '20']);
  assert.deepEqual([...RULE_PADDING_OPTIONS], [0, 1, 2, 5, 10, 15]);
  assert.match(rulesSource, /RULE_KEEP_OPTIONS/);
  assert.match(rulesSource, /RULE_PADDING_OPTIONS/);
  assert.match(panelSource, /RULE_KEEP_OPTIONS/);
  assert.match(panelSource, /RULE_PADDING_OPTIONS/);
  assert.doesNotMatch(rulesSource, /switcher=/, 'no-op source switcher removed from RecordingsFrame');
  assert.match(entrySource, /leaveRecordingsPage/);
  assert.match(entrySource, /!deleteOpening && \(state\.error \|\| state\.previewError\)/, 'error banner suppressed outside delete dialog while delete dialog is open');
  assert.match(rulesSource, /\{error && !selected && <Banner/, 'page error banner suppressed while rule editor side panel is open');
});
