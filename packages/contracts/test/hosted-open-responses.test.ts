import test from 'node:test';
import assert from 'node:assert/strict';
import {decodeServerClaimLookupResponse, decodeServersListResponse} from '../src/hosted/index.ts';
import {decode} from '../src/hosted/runtime.ts';

// R-09b for Hosted: a field a newer Hosted adds to a response never breaks this client, even
// inside an object Hosted's schema closes. Requests that are closed stay closed.

const time = '2030-01-02T03:04:05Z';
const added = {addedByANewerHosted: {nested: true}};

test('a Hosted claim lookup with fields this client does not know still decodes', () => {
  const lookup = {purpose: 'setup', code: 'c'.repeat(32), serverId: 'server', name: 'Living room', address: 'https://192.0.2.1:8443', requestedAt: time, expiresAt: time, status: 'pending', phase: {variant: 'review'}};
  assert.equal(decodeServerClaimLookupResponse({...lookup, ...added}).status, 'pending');
  assert.equal(decodeServerClaimLookupResponse({...lookup, phase: {variant: 'review', ...added}}).phase?.variant, 'review');
  assert.equal(Object.hasOwn(decodeServerClaimLookupResponse({...lookup, ...added}), 'addedByANewerHosted'), false);
  assert.throws(() => decodeServerClaimLookupResponse({...lookup, status: 1}), 'a known field still has to be right');
});

test('a Hosted server list whose membership ending gains a field still decodes', () => {
  const membershipEnding = {serverId: `srv_${'a'.repeat(43)}`, membershipId: 'b'.repeat(32), sequence: 1, cause: 'owner_removed', expiresAt: time};
  assert.equal(decodeServersListResponse({items: [], version: '0', watch: 'watch', membershipEnding: {...membershipEnding, ...added}}).membershipEnding?.sequence, 1);
});

test('a closed Hosted request still refuses a field it does not know', () => {
  const ending = {sequence: 1, cause: 'owner_removed', endedAt: time, expiresAt: time};
  const push = {after: 0, through: 1, full: true, members: [{accountId: 'account', role: 'member', active: false, ending}]};
  decode('ServerMembersPushRequest', push);
  assert.throws(() => decode('ServerMembersPushRequest', {...push, members: [{...push.members[0], ending: {...ending, ...added}}]}));
});
