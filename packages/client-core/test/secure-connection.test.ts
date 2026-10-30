import test from 'node:test';
import assert from 'node:assert/strict';
import {certificateClaimed, secureConnectionView} from '../src/server-admin/secure-connection.ts';
import type {CertificateStatus} from '../src/certificates.ts';
import type {ClaimStatus} from '../src/claim-onboarding.ts';

const cert = (over: Partial<CertificateStatus> & {enabled?: boolean} = {}): CertificateStatus => {
  const {enabled = true, ...rest} = over;
  return {configured: true, authorityId: 'a'.repeat(64), config: {enabled, publicPort: 32500, publicAddress: '', revision: '1'}, state: 'waiting', environment: 'production', tlsReady: false, publiclyTrusted: false, listenerBound: false, listenPort: 0, reachability: 'probe_required', ...rest} as CertificateStatus;
};
const claim = (state: string, extra: Partial<ClaimStatus> = {}): ClaimStatus => ({state, identity: {serverId: 's', localGeneration: '0'}, installationAcknowledged: false, approvalRequired: false, actions: [], ...extra});

test('a server without a Portico Account connection says how to turn it on and offers the step', () => {
  const v = secureConnectionView({certificate: cert({state: 'claim_required'}), claim: claim('unconfigured', {actions: ['prepare']})});
  assert.equal(v.state, 'notSetUp');
  assert.equal(v.body, 'settings.server.secure.notSetUp');
  assert.equal(v.canConnect, true);
  assert.equal(certificateClaimed(cert({state: 'claim_required'})), false);
});

test('without a certificate service only the own certificate is offered', () => {
  const v = secureConnectionView({});
  assert.equal(v.state, 'notSetUp');
  assert.equal(v.body, 'settings.server.secure.notSetUpOwnOnly');
  assert.equal(v.canConnect, false);
});

test('an approval in progress waits, and offers to open the approval again', () => {
  const v = secureConnectionView({certificate: cert({state: 'claim_required'}), claim: claim('prepared', {approvalRequired: true, operation: {operationId: 'op', localGeneration: '0', revision: '1'}})});
  assert.equal(v.state, 'waiting');
});

test('a connected server is starting until its certificate is ready, then on with its address', () => {
  assert.equal(secureConnectionView({certificate: cert(), claim: claim('installed')}).state, 'starting');
  assert.equal(secureConnectionView({certificate: cert({state: 'claim_pending'}), claim: claim('installed')}).state, 'starting');
  const on = secureConnectionView({certificate: cert({state: 'ready', tlsReady: true, publiclyTrusted: true, routeUrl: 'https://current.ptc-abc.direct.getportico.tv:32500/'})});
  assert.equal(on.state, 'on');
  assert.equal(on.body, 'settings.server.secure.onAt');
  assert.equal(on.address, 'current.ptc-abc.direct.getportico.tv:32500');
});

test('turned off, failing and own certificate are said as such', () => {
  assert.equal(secureConnectionView({certificate: cert({enabled: false})}).state, 'off');
  assert.equal(secureConnectionView({certificate: cert({state: 'retrying', errorCode: 'issuer_unavailable'})}).state, 'problem');
  const own = secureConnectionView({certificate: cert({state: 'claim_required'}), own: {serving: true, state: 'ready', domain: 'media.example.com'}});
  assert.equal(own.state, 'ownCertificate');
  assert.equal(own.address, 'media.example.com');
  assert.equal(own.canConnect, false);
});
