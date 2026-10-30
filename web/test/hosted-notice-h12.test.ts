import test from 'node:test';
import assert from 'node:assert/strict';
import {ServicesDirectoryFlow, type DirectoryItem} from '../src/services/directory-flow.ts';
import {directoryNotice} from '../src/screens/accounts/hosted-notice.ts';
import {RouteError} from '@core/route-identity.ts';
import {ApiError} from '@core/index.ts';

const server: DirectoryItem = {id: 'srv_' + 'a'.repeat(43), name: 'Synthetic Test Server', baseUrl: 'https://c-x.ptc-y.direct.getportico.tv:32500', publicKey: 'p'.repeat(43), policyRevision: 1, secureConnection: 'ready', endpointVerified: true, lastSeenAt: null, presence: {lastSeenAt: null}, ownedByMe: true, role: 'owner', status: 'quiet'};
const directory = (items: DirectoryItem[]) => ({items, version: '1', watch: '1', membershipEnding: undefined});

// H12 (29 Oct): a server whose old name no longer resolves (ERR_NAME_NOT_RESOLVED on /identity-proof)
// was reported as "Portico can't reach your Portico Account". A server's failure is the server's.
test('H12: a server DNS or identity-proof failure never shows an account notice', async () => {
  for (const failure of [new TypeError('Failed to fetch'), new RouteError('route_unavailable', 'The server did not answer.'), new RouteError('invalid_signature', 'proof'), new RouteError('identity_mismatch', 'proof')]) {
    const flow = new ServicesDirectoryFlow(directory([server]), {current: () => true, read: async () => directory([server]), admit: async () => {throw failure;}, empty: () => {}});
    await flow.open(server, flow.getSnapshot());
    const state = flow.getSnapshot();
    assert.equal(state.errorSource, 'server', String(failure));
    for (const place of ['frame', 'list'] as const) assert.notEqual(directoryNotice(state, place, server.name)?.source, 'account', `${place}: ${String(failure)}`);
    assert.equal(directoryNotice(state, 'frame', server.name), undefined, 'the state frame already says what is happening');
    flow.dispose();
  }
});

test('only a failure of Portico Accounts itself is an account notice', async () => {
  const outage = new ServicesDirectoryFlow(directory([server]), {current: () => true, read: async () => {throw new TypeError('Failed to fetch');}, admit: async () => ({dispose: () => {}}), empty: () => {}});
  await outage.refresh(outage.getSnapshot());
  assert.deepEqual(directoryNotice(outage.getSnapshot(), 'list')?.source, 'account');
  outage.dispose();
  const refused = new ServicesDirectoryFlow(directory([server]), {current: () => true, read: async () => directory([server]), admit: async () => {throw new ApiError(403, 'access_refused', 'access_refused', false);}, empty: () => {}});
  await refused.open(server, refused.getSnapshot());
  assert.equal(directoryNotice(refused.getSnapshot(), 'list', server.name)?.source, 'server', 'a server refusing is the server speaking');
  refused.dispose();
});
