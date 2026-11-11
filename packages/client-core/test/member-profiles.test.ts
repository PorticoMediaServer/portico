import test from 'node:test';
import assert from 'node:assert/strict';
import {MemberProfilesClient} from '../src/member-profiles.ts';

test('an owner reads and limits another person\'s profiles with member= on the profile routes', async () => {
  const sent: string[] = [];
  const api = {request: async <T>(path: string, method = 'GET', body?: unknown) => {
    sent.push(`${method} ${path}${body ? ' ' + JSON.stringify(body) : ''}`);
    if (path.startsWith('/v1/profiles?')) return {items: [{id: 'prf_main', name: 'Sam', color: 'mint', isPrimary: true, pinRequired: false, accessMarker: 'a'},
      {id: 'prf_kid', name: 'Junior', color: 'coral', isPrimary: false, pinRequired: true, accessMarker: 'b'}]} as T;
    if (path.includes('/restrictions')) return {maxRatingAge: method === 'PUT' ? 0 : null, blockedLabels: [], allLibraries: true, libraries: [], accountAllLibraries: true, accountLibraries: []} as T;
    return undefined as T;
  }};
  const client = new MemberProfilesClient(api, 'acc_sam');
  const [, kid] = await client.list();
  assert.equal(kid!.name, 'Junior');
  const limited = await client.restrict(kid!, {maximumAgeRating: 'G'});
  assert.equal(limited.restrictions.maximumAgeRating, 'G');
  const cleared = await client.resetPIN(kid!);
  assert.equal(cleared.pinRequired, false);
  assert.ok(sent.includes('GET /v1/profiles?member=acc_sam'));
  assert.ok(sent.some(line => line.startsWith('PUT /v1/profiles/prf_kid/restrictions?member=acc_sam {"maxRatingAge":0')));
  assert.ok(sent.includes('DELETE /v1/profiles/prf_kid/pin?member=acc_sam'));
});
