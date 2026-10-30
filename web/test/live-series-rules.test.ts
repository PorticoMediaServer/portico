import test from 'node:test';
import assert from 'node:assert/strict';
import {createRulesState, matchingAirings, matchingCount, rulesReducer} from '../src/screens/live/series-rules-model';
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
