import assert from 'node:assert/strict';
import test from 'node:test';
import {createI18n} from '../../i18n/src/index.ts';
import {importSkipped, tunerStatus, tunerUseLine} from '../src/server-admin/live-words.ts';
import {memberOffLine} from '../src/server-admin/panel-logic.ts';

const i18n = createI18n();
const t = i18n.t;
const has = (id: string) => i18n.has(id);

test('an import that left nothing out says nothing', () => {
  assert.equal(importSkipped(t, has, {skippedChannels: 0, skippedProgrammes: 0, adjustedProgrammes: 0, examples: []}), undefined);
  assert.equal(importSkipped(t, has, undefined), undefined);
  assert.equal(importSkipped(t, has, 'nonsense'), undefined);
});

test('what an import left out is counted in a sentence, with each reason once', () => {
  const both = importSkipped(t, has, {skippedChannels: 12, skippedProgrammes: 1, adjustedProgrammes: 5, examples: ['live.import.channelAddressMissing', 'live.import.channelAddressMissing', 'live.import.programmeInvalid', 'made.up.key', 7]})!;
  assert.equal(both.summary, '12 channels and 1 program were left out. 5 programs’ times were adjusted.');
  assert.deepEqual(both.reasons, ['A channel had no stream address.', 'A program had no usable title or time.']);
  assert.equal(importSkipped(t, has, {skippedChannels: 1})!.summary, '1 channel was left out.');
  assert.equal(importSkipped(t, has, {skippedProgrammes: 40})!.summary, '40 programs were left out.');
  assert.equal(importSkipped(t, has, {adjustedProgrammes: 1})!.summary, '1 program’s time was adjusted.');
  // A count that is not a count is nothing, never a guess.
  assert.equal(importSkipped(t, has, {skippedChannels: -4, skippedProgrammes: 'many'}), undefined);
});

test('a tuner says what is using it', () => {
  const when = () => 'Tue 20:00';
  assert.equal(tunerUseLine(t, {kind: 'live', state: 'active', title: 'BBC One', startsAt: ''}, when), 'Being watched · BBC One');
  assert.equal(tunerUseLine(t, {kind: 'recording', state: 'recording', title: 'Match of the Day', startsAt: '2026-10-13T20:00:00Z'}, when), 'Recording now · Match of the Day');
  assert.equal(tunerUseLine(t, {kind: 'recording', state: 'scheduled', title: 'Newsnight', startsAt: '2026-10-13T20:00:00Z'}, when), 'Tue 20:00 · Newsnight');
  assert.equal(tunerUseLine(t, {kind: 'live', state: 'retiring', title: 'BBC Two', startsAt: ''}, when), 'Being watched · BBC Two · finishing');
  // Whose it is, when the server names them: the profile, with the account when they differ.
  assert.equal(tunerUseLine(t, {kind: 'live', state: 'active', title: 'BBC One', startsAt: '', accountName: 'sam', profileName: 'Kids'}, when), 'Being watched · BBC One · Kids (sam)');
  assert.equal(tunerUseLine(t, {kind: 'live', state: 'active', title: 'BBC One', startsAt: '', accountName: 'sam', profileName: 'sam'}, when), 'Being watched · BBC One · sam');
  assert.deepEqual(tunerStatus(t, {conflicts: 2, inUse: 1, tunerCount: 2}), {tone: 'danger', text: '2 clashes'});
  assert.deepEqual(tunerStatus(t, {conflicts: 0, inUse: 2, tunerCount: 2}), {tone: 'warning', text: '2 of 2 in use'});
  assert.deepEqual(tunerStatus(t, {conflicts: 0, inUse: 0, tunerCount: 2}), {tone: 'healthy', text: '0 of 2 in use'});
});

test('an account that is off says who turned it off and when', () => {
  const date = () => 'Oct 3';
  assert.equal(memberOffLine({disabled: false}, t, date), '');
  assert.equal(memberOffLine({disabled: true}, t, date), '');
  assert.equal(memberOffLine({disabled: true, disabledAt: 'not a date', disabledReason: 'owner'}, t, date), '');
  assert.equal(memberOffLine({disabled: true, disabledAt: '2026-10-03T10:00:00Z', disabledReason: 'owner'}, t, date), 'Turned off Oct 3');
  assert.equal(memberOffLine({disabled: true, disabledAt: '2026-10-03T10:00:00Z', disabledReason: 'left'}, t, date), 'Left Oct 3');
});
