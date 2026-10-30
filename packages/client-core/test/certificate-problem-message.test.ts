import test from 'node:test';
import assert from 'node:assert/strict';
import {certificateProblemMessage, parseCertificateStatus, type CertificateStatus} from '../src/certificates.ts';
import {defaultI18n} from '../../i18n/src/index.ts';

function status(over: Partial<CertificateStatus> = {}): CertificateStatus {
 return parseCertificateStatus({configured:true,authorityId:'a'.repeat(64),config:{enabled:true,publicPort:32500,publicAddress:'',revision:'1'},state:'waiting',environment:'production',tlsReady:false,publiclyTrusted:false,listenerBound:false,listenPort:0,reachability:'probe_required',...over});
}
const reason = (over: Partial<CertificateStatus>) => {
 const id=certificateProblemMessage(status(over));
 return id ? defaultI18n.t(id) : undefined;
};

test('healthy, starting and disabled certificates have no problem reason',()=>{
 assert.equal(certificateProblemMessage(),undefined);
 for(const state of ['waiting','requesting','ready','renewing','recovering'])assert.equal(reason({state}),undefined);
 const current=status();
 assert.equal(certificateProblemMessage(status({config:{...current.config,enabled:false},state:'invalid',errorCode:'local_certificate_invalid',routeErrorCode:'route_name_retry'})),undefined);
});

test('expired and invalid material take precedence over order and route failures',()=>{
 assert.equal(reason({state:'expired',errorCode:'provider_retry',routeErrorCode:'route_name_retry'}),'The certificate has expired.');
 assert.equal(reason({state:'invalid',errorCode:'local_tls_key_unavailable'}),'The certificate is invalid.');
 for(const errorCode of ['local_certificate_invalid','certificate_response_invalid','public_trust_store_unavailable','local_tls_key_unavailable'])assert.equal(reason({errorCode}),'The certificate is invalid.');
});

test('the server certificate/ACME vocabulary maps to acquisition or renewal failure',()=>{
 const codes=['certificate_not_found','certificate_not_due','certificate_conflict','certificate_configuration_required','invalid_certificate_request',
  'acceptance_uncertain','authority_changed','creation_started','provider_rejected','provider_response_invalid','provider_order_invalid','dns_validation_failed',
  'coordinator_retry','cleanup_retry','revocation_retry','provider_retry','acme_account_configuration_or_retry','dns_or_issuer_configuration',
  'certificate_retry','issuer_environment_mismatch'];
 for(const errorCode of codes){
  assert.equal(reason({state:'retrying',errorCode,routeErrorCode:'route_name_retry'}),'The server could not obtain a certificate.',errorCode);
  assert.equal(reason({state:'renewing',errorCode,tlsReady:true}),'The certificate is still working, but its renewal failed.',errorCode);
 }
 // Status can serve previous material after failing to load the active generation.
 assert.equal(reason({state:'recovering',errorCode:'local_certificate_invalid',tlsReady:true}),'The certificate is still working, but its renewal failed.');
});

test('route publication has its own reason and unknown codes have a bounded fallback',()=>{
 assert.equal(reason({state:'ready',tlsReady:true,routeErrorCode:'route_name_retry'}),'The secure address could not be published.');
 for(const over of [{errorCode:'future_certificate_failure'},{errorCode:'future_certificate_failure',tlsReady:true},{routeErrorCode:'future_route_failure'}]){
  assert.equal(reason(over),'The secure connection needs attention. Check the server logs for details.');
 }
 assert.equal(reason({state:'expired',errorCode:'future_certificate_failure'}),'The certificate has expired.');
});
