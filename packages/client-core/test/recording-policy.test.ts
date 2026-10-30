import test from 'node:test';
import assert from 'node:assert/strict';
import {parseRecordingGrants, parseRecordingOwners, recordingAllowed} from '../src/server-admin/recording-policy.ts';
import {resetSkippedParts, skippedParts} from '../src/tolerant.ts';

const owner = {authority: 'local' as const, accountId: 'account', profileId: 'primary'};
const child = {...owner, profileId: 'child'};
const choice = {owner, accountName: 'Account', profileName: 'Primary', canRecord: true, revision: 1};
const grant = {owner, enabled: true, revision: 1};

test('the recording profile list keeps its readable profiles and ignores fields it does not know', () => {
  resetSkippedParts();
  const page = parseRecordingOwners({items: [
    {...choice, avatar: 'newer field', owner: {...owner, region: 'newer field'}},
    {owner: child, accountName: null, profileName: 'Child', canRecord: false, revision: 1},
    {owner: child, accountName: 'Account', canRecord: false, revision: 1},
    {owner: child, accountName: 'Account', profileName: 'Child', revision: 1},
    'not a row',
    {owner: child, accountName: 'Account', profileName: 'Child', canRecord: false, revision: 3},
  ], nextCursor: 'page-two', total: 5});
  assert.deepEqual(page, {items: [choice, {owner: child, accountName: 'Account', profileName: 'Child', canRecord: false, revision: 3}], nextCursor: 'page-two'});
  assert.deepEqual(skippedParts().map(p => [p.document, p.part, p.count]), [['recording-policy', 'owners', 4]]);
});

test('the recording profile list still refuses an unreadable identity, cursor or shape', () => {
  for (const bad of [{...owner, profileId: ''}, {...owner, accountId: 7}, {...owner, authority: 'federated'}, null]) {
    assert.throws(() => parseRecordingOwners({items: [choice, {owner: bad, accountName: 'Account', profileName: 'Other', canRecord: false, revision: 1}]}));
  }
  assert.throws(() => parseRecordingOwners({items: [choice], nextCursor: 7}));
  assert.throws(() => parseRecordingOwners({items: {}}));
  assert.throws(() => parseRecordingOwners([choice]));
});

test('recording permissions stay exact: each profile is its own answer, and one unreadable row refuses them all', () => {
  const denial = {owner: child, enabled: false, revision: 2};
  assert.equal(recordingAllowed(child, parseRecordingGrants([grant, denial])), false);
  // The account's main profile recording never reaches its other profiles.
  assert.equal(recordingAllowed(child, [grant]), false);
  assert.equal(recordingAllowed(owner, [grant]), true);
  for (const bad of [{...denial, enabled: 'false'}, {...denial, revision: 0}, {...denial, owner: {...child, authority: 'federated'}}, {...denial, owner: {...child, profileId: ''}}, null]) {
    assert.throws(() => parseRecordingGrants([grant, bad]));
  }
  assert.throws(() => parseRecordingGrants({items: [grant]}));
  // A field this app does not know never changes what is allowed.
  assert.deepEqual(parseRecordingGrants([{...grant, grantedBy: 'newer field'}]), [grant]);
});
