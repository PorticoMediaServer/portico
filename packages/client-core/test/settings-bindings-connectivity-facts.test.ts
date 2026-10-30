import test from 'node:test';
import assert from 'node:assert/strict';
import {settingsBinding,readSettingsBinding,settingsBindingPatch} from '../src/presentation/settings-bindings.ts';

test('identity code is an actual readonly snapshot fact, independent of own-address toggle',()=>{
 const binding=settingsBinding('remote.identityCode')!;
 assert.deepEqual(binding,{form:'connectivityStatus',key:'identityCode',readOnly:true});
 assert.equal(readSettingsBinding(binding,{forms:{connectivityStatus:{identityCode:'JJWY-C8J8-J26F'},connectivity:{ownAddressEnabled:false}}})?.current,'JJWY-C8J8-J26F');
 assert.equal(readSettingsBinding(binding,{forms:{connectivityStatus:{}}}),undefined);
 assert.equal(readSettingsBinding(binding,{forms:{runtime:{identityCode:'configured-historical-value'}}}),undefined);
 assert.throws(()=>settingsBindingPatch(binding,'JJWY-C8J8-J26F'));
});
