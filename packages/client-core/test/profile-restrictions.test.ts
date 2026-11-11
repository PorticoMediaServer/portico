import test from 'node:test';
import assert from 'node:assert/strict';
import {readRestrictions, saveRestrictions} from '../src/profile-restrictions.ts';

test('a profile\'s highest rating is kept as an age, and a save sends back the libraries it read', async () => {
  const sent: unknown[] = [];
  const api = {request: async <T>(path: string, method?: string, body?: unknown) => {
    sent.push([method, path, body]);
    return (method === 'GET' ? {maxRatingAge: 0, blockedLabels: ['tag_kids'], allLibraries: false, libraries: ['lib_films'], accountAllLibraries: true, accountLibraries: []}
      : {...body as object}) as T;
  }};
  const read = await readRestrictions(api, 'prf_kid', 'acc_member');
  assert.equal(read.maximumAgeRating, 'G', 'an age of 0 is a real limit, not "no limit"');
  const saved = await saveRestrictions(api, read, {maximumAgeRating: 'PG-13'}, 'acc_member');
  assert.equal(saved.maximumAgeRating, 'PG-13');
  assert.deepEqual(sent[1], ['PUT', '/v1/profiles/prf_kid/restrictions?member=acc_member',
    {maxRatingAge: 13, blockedLabels: ['tag_kids'], allLibraries: false, libraries: ['lib_films'], accountAllLibraries: true, accountLibraries: []}]);
  const cleared = await saveRestrictions(api, saved, {maximumAgeRating: ''});
  assert.equal(cleared.maximumAgeRating, '');
  assert.equal((sent[2] as [string, string, {maxRatingAge: unknown}])[2].maxRatingAge, null);
});
