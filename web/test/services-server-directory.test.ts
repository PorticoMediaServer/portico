import test from 'node:test';
import assert from 'node:assert/strict';
import {decodeServersListResponse} from '../../packages/contracts/src/hosted/decoders.ts';
import {servicesServerDirectoryCopy} from '../src/services/server-directory.ts';
import {catalogue,enUS,supportedLocales} from '../../packages/i18n/src/catalog/index.ts';
import {servicesI18n} from '../src/services/i18n.ts';

const i18n=servicesI18n();
function server(overrides:Record<string,unknown>={}) {
  return decodeServersListResponse({items:[{id:'srv_'+ 'A'.repeat(43),name:'Lake house',baseUrl:'https://server.example',publicKey:'A'.repeat(43),
    endpointVerified:true,policyRevision:1,lastSeenAt:'2026-10-18T12:00:00Z',ownedByMe:false,role:'admin',status:'active',secureConnection:'ready',presence:{lastSeenAt:'2026-10-18T12:00:00Z'},...overrides}],version:'v1',watch:'watch'}).items[0]!;
}
test('current Hosted active/admin wire facts render the drawn Online and Administrator labels',()=>{
  const result=servicesServerDirectoryCopy(server(),i18n);
  assert.equal(result.presence,i18n.t('web.hostedAccount.serverOnline'));
  assert.deepEqual(result.badge,{label:i18n.t('web.access.roleAdmin'),tone:'default'});
});
test('the actual owner fact retains its Owner badge independently of other display facts',()=>{
  assert.deepEqual(servicesServerDirectoryCopy(server({ownedByMe:true,role:'owner'}),i18n).badge,{label:i18n.t('web.hostedAccount.serverOwner'),tone:'accent'});
});
test('member and unfamiliar role values never invent an Administrator or Owner badge',()=>{
  for(const role of ['member','owner'])assert.equal(servicesServerDirectoryCopy(server({role}),i18n).badge,undefined);
  for(const role of ['administrator','operator','future'])assert.throws(()=>server({role}),'the canonical decoder refuses unknown membership roles');
});
test('quiet/dormant presence retains the drawn absolute last-seen date',()=>{
  for(const status of ['quiet','dormant'])assert.equal(servicesServerDirectoryCopy(server({status}),i18n).presence,
    i18n.t('web.hostedAccount.serverLastSeen',{when:i18n.date('2026-10-18T12:00:00Z')}));
});
test('an unknown status never claims Online, and missing presence never fabricates a time',()=>{
  assert.notEqual(servicesServerDirectoryCopy(server({status:'online'}),i18n).presence,i18n.t('web.hostedAccount.serverOnline'));
  assert.equal(servicesServerDirectoryCopy(server({status:'quiet',presence:{lastSeenAt:null}}),i18n).presence,undefined);
});
 
test('a pending secure connection says so; offline and pending gives the one next step; ready is normal; no address is ever shown',()=>{
  const pending=servicesServerDirectoryCopy(server({secureConnection:'pending'}),i18n);
  assert.equal(pending.presence,'Getting a secure connection…');
  const offline=servicesServerDirectoryCopy(server({secureConnection:'pending',status:'quiet'}),i18n);
  assert.equal(offline.presence,'Open it on your network from the setup page for now');
  assert.equal(servicesServerDirectoryCopy(server({secureConnection:'ready'}),i18n).presence,i18n.t('web.hostedAccount.serverOnline'));
  for(const copy of [pending,offline])assert.doesNotMatch(JSON.stringify(copy),/server\.example|https?:/);
});
test('secureConnection is required and strictly pending or ready',()=>{
  for(const bad of ['maybe','',undefined,true])assert.throws(()=>server({secureConnection:bad}));
});

test('Account actions is catalogue-backed in every supported locale',()=>{
 for(const locale of supportedLocales)assert.equal(catalogue(locale)['web.hostedAccount.accountActions']??enUS['web.hostedAccount.accountActions'],'Account actions',locale);
});
