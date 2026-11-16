import test from 'node:test';
import assert from 'node:assert/strict';
import {readRestrictions, saveRestrictions} from '../src/profile-restrictions.ts';

test('a profile\'s highest rating is kept as an age, and a save sends back the libraries it read', async () => {
  const sent: unknown[] = [];
  const api = {request: async <T>(path: string, method?: string, body?: unknown) => {
    sent.push([method, path, body]);
    return (method === 'GET' ? {maxRatingAge: 0, allowUnrated: true, blockedLabels: ['tag_kids'], allLibraries: false, libraries: ['lib_films'], accountAllLibraries: true, accountLibraries: [], accountMaxRatingAge: 0, accountAllowUnrated: false, accountBlockedLabels: ['tag_adult'], allowDownloads: false, accountAllowDownloads: false, allowWatchTogether: false, accountAllowWatchTogether: false}
      : {...body as object}) as T;
  }};
  const read = await readRestrictions(api, 'prf_kid', 'acc_member');
  assert.equal(read.maximumAgeRating, 'G', 'an age of 0 is a real limit, not "no limit"');
  assert.equal(read.allowUnrated, true, 'the profile\'s unrated switch is read as the server sends it');
  assert.equal(read.allowDownloads, false, 'the profile\'s download switch is read as the server sends it');
  assert.equal(read.allowWatchTogether, false, 'the profile\'s Watch Together switch is read as the server sends it');
  const saved = await saveRestrictions(api, read, {maximumAgeRating: 'PG-13'}, 'acc_member');
  assert.equal(saved.maximumAgeRating, 'PG-13');
  assert.deepEqual(sent[1], ['PUT', '/v1/profiles/prf_kid/restrictions?member=acc_member',
    {maxRatingAge: 13, allowUnrated: true, blockedLabels: ['tag_kids'], allLibraries: false, libraries: ['lib_films'], accountAllLibraries: true, accountLibraries: [],
      accountMaxRatingAge: 0, accountAllowUnrated: false, accountBlockedLabels: ['tag_adult'],
      allowDownloads: false, allowWatchTogether: false, accountAllowDownloads: false, accountAllowWatchTogether: false}]);
  const cleared = await saveRestrictions(api, saved, {maximumAgeRating: ''});
  assert.equal(cleared.maximumAgeRating, '');
  assert.equal((sent[2] as [string, string, {maxRatingAge: unknown}])[2].maxRatingAge, null);
});

test('download and Watch Together switches are saved and survive readback', async () => {
 const {parseRestrictions} = await import('../src/profile-restrictions.ts');
 let stored = {allowDownloads: true, allowWatchTogether: true, accountAllowDownloads: false, accountAllowWatchTogether: false};
 const api = {request: async <T>(_path: string, _method?: string, body?: unknown) => {stored = {...stored, ...body as object}; return stored as T;}};
 const saved = await saveRestrictions(api, parseRestrictions('p', stored), {allowDownloads: false, allowWatchTogether: false});
 assert.equal(stored.allowDownloads, false);assert.equal(stored.allowWatchTogether, false);
 assert.equal(saved.allowDownloads, false);assert.equal(saved.allowWatchTogether, false);
 const enabled = await saveRestrictions(api, saved, {allowDownloads: true, allowWatchTogether: true});
 assert.equal(enabled.allowDownloads, true);assert.equal(enabled.allowWatchTogether, true);
 assert.equal(stored.accountAllowDownloads, false);assert.equal(stored.accountAllowWatchTogether, false);
});
