import test from 'node:test';
import assert from 'node:assert/strict';
import {servicesServerDirectoryCopy} from '../src/services/server-directory.ts';
import type {ListedServerRow} from '../src/platform/portico-account-holder.ts';
import {catalogue,enUS,supportedLocales} from '../../packages/i18n/src/catalog/index.ts';
import {servicesI18n} from '../src/services/i18n.ts';

// A row from the Portico Account's signed server list (`porticoAccountHolder.servers`).
const i18n=servicesI18n();
const server=(overrides:Partial<ListedServerRow>={}):ListedServerRow=>({id:'srv_'+'A'.repeat(43),name:'Lake house',baseUrl:'https://a1.ns.portico.direct:32400',publicKey:'A'.repeat(43),
  role:'admin',ownedByMe:false,status:'listed',presence:{lastSeenAt:null},...overrides});

test('the account\'s role shows as the drawn Owner or Administrator badge, and a member gets none',()=>{
  assert.deepEqual(servicesServerDirectoryCopy(server(),i18n).badge,{label:i18n.t('web.access.roleAdmin'),tone:'default'});
  assert.deepEqual(servicesServerDirectoryCopy(server({ownedByMe:true,role:'owner'}),i18n).badge,{label:i18n.t('web.hostedAccount.serverOwner'),tone:'accent'});
  assert.equal(servicesServerDirectoryCopy(server({role:'member'}),i18n).badge,undefined);
});
test('a listed server never claims Online or a last-seen time: the signed list says nothing about whether it is up',()=>{
  const copy=servicesServerDirectoryCopy(server(),i18n);
  assert.equal(copy.presence,undefined);assert.equal(copy.online,undefined);
  assert.doesNotMatch(JSON.stringify(copy),/portico\.direct|https?:/,'no address is ever shown');
});
test('Account actions is catalogue-backed in every supported locale',()=>{
 for(const locale of supportedLocales)assert.equal(catalogue(locale)['web.hostedAccount.accountActions']??enUS['web.hostedAccount.accountActions'],'Account actions',locale);
});
