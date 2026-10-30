import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {defaultRecordingOptions, type RecordingChannel, type RecordingOptions, type RecordingRule, type RuleSummary} from '../../../packages/client-core/src/dvr';
import {keepChoiceList, keepChoiceValue, keepLabel, ruleFacts, ruleKeep, ruleNext, ruleScope, seriesConfig, seriesFormDefaults, withKeep} from '../src-next/screens/settings-live/recording-rule-presentation';

const channel: RecordingChannel = {id: 'channel', name: 'Atlas Documentaries', number: '501'};
const options = (patch: Partial<RecordingOptions> = {}): RecordingOptions => ({...defaultRecordingOptions, ...patch});
const rule = (patch: Partial<RecordingRule['config']> = {}): RecordingRule => ({id: 'a'.repeat(48), revision: 3, reconcileState: 'synced', diagnostic: '', updatedAt: '2026-10-21T12:00:00Z',
  config: {name: 'The Clockmakers', sourceId: 'source', seriesId: 'series', enabled: true, episodes: 'all', allowedChannels: [], blockedChannels: [], keywords: [], blockedKeywords: [], options: options(), ...patch}});
const summary = (patch: Partial<RuleSummary> = {}): RuleSummary => ({upcoming: 3, recorded: 6, conflicts: 0, matchingTonight: 2, channel, ...patch});

test('the real keep policy reads the two DVR limits the administration document maps onto', () => {
  assert.deepEqual(ruleKeep(options()), {mode: 'all', count: 0, days: 0});
  assert.deepEqual(ruleKeep(options({episodeLimit: 3})), {mode: 'count', count: 3, days: 0});
  assert.deepEqual(ruleKeep(options({retentionDays: 30})), {mode: 'days', count: 0, days: 30});
  // A rule written straight through the DVR API can carry both; both are reported, not hidden.
  assert.deepEqual(ruleKeep(options({episodeLimit: 5, retentionDays: 14})), {mode: 'both', count: 5, days: 14});
});

test('the drawn keep values print the policy a rule actually stores', () => {
  assert.equal(keepLabel(defaultI18n, ruleKeep(options())), 'Keep every episode');
  assert.equal(keepLabel(defaultI18n, ruleKeep(options({episodeLimit: 1}))), 'Keep the latest episode');
  assert.equal(keepLabel(defaultI18n, ruleKeep(options({episodeLimit: 5}))), 'Keep the latest 5');
  assert.equal(keepLabel(defaultI18n, ruleKeep(options({retentionDays: 30}))), 'Keep for 30 days');
  assert.equal(keepLabel(defaultI18n, ruleKeep(options({episodeLimit: 5, retentionDays: 14}))), 'Keep the latest 5 · Keep for 14 days');
});

test('frame 30 lists the drawn count choices and adds the current days policy only when it is real', () => {
  assert.deepEqual(keepChoiceList(defaultI18n, options()).map(choice => choice.value), ['count:0', 'count:1', 'count:3', 'count:5', 'count:10', 'count:20']);
  assert.deepEqual(keepChoiceList(defaultI18n, options()).map(choice => choice.label), ['Keep every episode', 'Keep the latest episode', 'Keep the latest 3', 'Keep the latest 5', 'Keep the latest 10', 'Keep the latest 20']);
  const days = keepChoiceList(defaultI18n, options({retentionDays: 30}));
  assert.deepEqual(days.at(-1), {value: 'days:30', label: 'Keep for 30 days'});
  assert.equal(keepChoiceValue(options({episodeLimit: 5})), 'count:5');
  assert.equal(keepChoiceValue(options({retentionDays: 30})), 'days:30');
  assert.equal(keepChoiceValue(options({episodeLimit: 5, retentionDays: 14})), 'days:14');
});

test('a chosen keep row writes exactly one limit and keeps the rest of the options', () => {
  const base = options({beforeSeconds: 120, afterSeconds: 180, priority: 4, episodeLimit: 10, retentionDays: 30});
  assert.deepEqual(withKeep(base, 'count:5'), {...base, episodeLimit: 5, retentionDays: 0});
  assert.deepEqual(withKeep(base, 'days:14'), {...base, episodeLimit: 0, retentionDays: 14});
  assert.deepEqual(withKeep(base, 'count:0'), {...base, episodeLimit: 0, retentionDays: 0});
  assert.deepEqual(withKeep(base, 'nonsense'), base);
});

test('the channel fact names the tied channel and says any channel for a series rule that follows all', () => {
  assert.equal(ruleScope(rule(), 'Northwind News 24', defaultI18n), 'Any channel');
  assert.equal(ruleScope(rule({allowedChannels: ['channel']}), 'Northwind News 24', defaultI18n), 'Northwind News 24');
  assert.equal(ruleScope(rule({matchKind: 'title', title: 'World Tonight'}), 'Northwind News 24', defaultI18n), 'Northwind News 24');
  assert.equal(ruleScope(rule({channelId: 'channel'}), undefined, defaultI18n), 'This channel');
});

test('the next airing line prints today with its channel, another day with its date, and the conflict in red', () => {
  const now = new Date(2026, 9, 21, 16, 5).getTime();
  const today = new Date(2026, 9, 21, 16, 15).toISOString(), later = new Date(2026, 9, 23, 20, 0).toISOString();
  const next = (start: string) => summary({channel, next: {id: 'b'.repeat(48), channelId: 'channel', title: 'The Clockmakers', start, end: new Date(Date.parse(start) + 45 * 60000).toISOString(), description: '', lineage: 'interval-only', seriesId: '', episodeId: '', newEvidence: 'unknown', facts: []} as unknown as RuleSummary['next']});
  assert.deepEqual(ruleNext(next(today), defaultI18n, now), {text: 'Next: today, 4:15 PM on Atlas Documentaries', conflict: false});
  assert.deepEqual(ruleNext(next(later), defaultI18n, now), {text: 'Next: Oct 23, 8:00 PM on Atlas Documentaries', conflict: false});
  assert.deepEqual(ruleNext(summary({conflicts: 1, next: next(today).next}), defaultI18n, now), {text: 'Next: today, 4:15 PM, tuner conflict', conflict: true});
  assert.equal(ruleNext(summary(), defaultI18n, now), undefined);
});

test('the rule facts come straight from the server summary', () => {
  assert.deepEqual(ruleFacts(undefined), {upcoming: 0, recorded: 0, tonight: 0});
  assert.deepEqual(ruleFacts(summary()), {upcoming: 3, recorded: 6, tonight: 2});
});

test('the Record series form starts from the real Recording defaults', () => {
  const document = {settings: {prePaddingMs: 60000, postPaddingMs: 180000, keepPolicy: {mode: 'keep-all', keepCount: 0, keepDays: 0}}};
  assert.deepEqual(seriesFormDefaults(document), {episodes: 'new', anyChannel: false, keep: 'count:0', beforeSeconds: 60, afterSeconds: 180});
  assert.equal(seriesFormDefaults({settings: {...document.settings, keepPolicy: {mode: 'keep-count', keepCount: 5, keepDays: 0}}}).keep, 'count:5');
  assert.equal(seriesFormDefaults({settings: {...document.settings, keepPolicy: {mode: 'keep-days', keepCount: 0, keepDays: 30}}}).keep, 'days:30');
  assert.deepEqual(seriesFormDefaults(undefined), {episodes: 'new', anyChannel: false, keep: 'count:0', beforeSeconds: 0, afterSeconds: 0});
});

test('the guide form builds the same rule the shared series mapping builds', () => {
  const source = 'a'.repeat(48), channel = 'b'.repeat(64), programmeId = 'c'.repeat(64);
  const guideChannel = {id: channel, sourceId: source, provenance: 'live-source' as const, generation: 'd'.repeat(48), name: 'Cinema', number: '1', group: '', programmes: [], tuneAvailable: true, recordAvailable: true, tuneUnavailableReason: '', recordUnavailableReason: '', favorite: false, hidden: false, preferenceRevision: 1};
  const programme = {id: programmeId, channelId: channel, title: 'Synthetic Showcase', start: '2026-10-24T22:00:00Z', end: '2026-10-24T22:30:00Z', description: '', lineage: 'interval-only' as const, seriesId: '', episodeId: '', newEvidence: 'unknown' as const, facts: []};
  const draft = {channel: guideChannel as never, programme: programme as never, series: true};
  const built = seriesConfig(draft, {episodes: 'all', anyChannel: true, keep: 'days:30', beforeSeconds: 60, afterSeconds: 180});
  assert.equal(built.config.matchKind, 'title');
  assert.equal(built.config.title, 'Synthetic Showcase');
  assert.equal(built.config.episodes, 'all');
  // A title match follows this channel by contract, even when the any-channel chip is chosen.
  assert.deepEqual(built.config.allowedChannels, [channel]);
  assert.deepEqual(built.config.options, {beforeSeconds: 60, afterSeconds: 180, priority: 0, retentionDays: 30, episodeLimit: 0});
  assert.equal(built.anchor.channelId, channel);
  const counted = seriesConfig(draft, {episodes: 'new', anyChannel: false, keep: 'count:3', beforeSeconds: 0, afterSeconds: 0});
  assert.deepEqual(counted.config.allowedChannels, [channel]);
  assert.equal(counted.config.options.episodeLimit, 3);
  assert.equal(counted.config.options.retentionDays, 0);
});
