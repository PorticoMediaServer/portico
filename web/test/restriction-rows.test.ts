import test from 'node:test';
import assert from 'node:assert/strict';
import {changeRestriction, restrictionRow} from '../src/screens/settings/account-profile/restriction-rows.ts';

const doc = {profileId: 'p1', ratingSystem: '', maximumAgeRating: '', maximumAge: -1, allowUnrated: false, blockedLabels: ['t-gone', 't-horror'],
  allLibraries: true, libraries: [], accountAllLibraries: true, accountLibraries: [], allowDownloads: true, allowLiveTv: true, allowDvr: true, allowWatchTogether: true};
const source = (saved: unknown[], refused: string[] = []) => ({getSnapshot: () => ({loading: false, busy: false, document: doc, systems: [], tags: [{id: 't-horror', name: 'Horror'}, {id: 't-war', name: 'War'}]}),
  save: async (changes: unknown) => {saved.push(changes);}, refuse: (problem: string) => {refused.push(problem);}}) as never;
const profiles = {getSnapshot: () => ({document: {canManage: true}})} as never;

test('a blocked Tag the picker does not list stays blocked when another Tag is added', async () => {
  const saved: unknown[] = [];
  const shown = restrictionRow(source(saved), profiles, 'profile.hiddenLabels')!.value as string[];
  await changeRestriction(source(saved), 'profile.hiddenLabels', [...shown, 'War']);
  assert.deepEqual(saved, [{blockedLabels: ['t-gone', 't-horror', 't-war']}]);
});

test('a Tag name nobody has is told in place and nothing is saved', async () => {
  const saved: unknown[] = [], refused: string[] = [];
  await assert.rejects(changeRestriction(source(saved, refused), 'profile.hiddenLabels', ['Horror', 'zzz-no-such-tag'])!, {code: 'unknown_tag'});
  assert.deepEqual(saved, []);
  assert.deepEqual(refused, ['There is no Tag called zzz-no-such-tag.']);
});

test('limits that could not be read say so on the Highest rating row', () => {
  const failed = {getSnapshot: () => ({loading: false, busy: false, error: new Error('down')})} as never;
  assert.equal(restrictionRow(failed, profiles, 'profile.maxRating')?.error, 'This profile’s limits didn’t load.');
  assert.equal(restrictionRow(failed, profiles, 'profile.hiddenLabels')?.present, false);
});
