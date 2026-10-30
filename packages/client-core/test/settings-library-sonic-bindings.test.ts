import test from 'node:test';
import assert from 'node:assert/strict';
import {patchLibrarySonicSettingsBinding,readLibrarySonicSettingsBinding} from '../src/presentation/settings-bindings.ts';
import type {LibrarySonicDocument} from '../src/server-admin/library-sonic.ts';

const library={libraryId:'music',libraryKind:'music',eligibleControls:['libraries.sonicAnalysis','libraries.sonicStatus']};
const sonic:LibrarySonicDocument={libraryId:'music',available:true,enabled:false,defaultEnabled:true,revision:9,policyAllows:false,state:'disabled',done:2,total:7,policyRevision:3,observedAtMs:1000};
test('Sonic reads and reset intents use the real factory default and independent CAS revision',()=>{
 const row=readLibrarySonicSettingsBinding('libraries.sonicAnalysis',library,sonic)!;
 assert.equal(row.current,false);assert.equal(row.effective,false);assert.equal(row.default,true);
 assert.deepEqual(patchLibrarySonicSettingsBinding('libraries.sonicAnalysis',row.default,library,sonic),{enabled:true,expectedRevision:9});
 assert.equal(readLibrarySonicSettingsBinding('libraries.sonicAnalysis',library,{...sonic,enabled:true,defaultEnabled:false})?.default,false);
});
test('Sonic bindings require both the real Library hint and matching available runtime snapshot',()=>{
 for(const [doc,state] of [[{...library,eligibleControls:[]},sonic],[{...library,libraryId:'other'},sonic],[{...library,libraryKind:'movie'},sonic],[library,{...sonic,available:false}]] as const){
  assert.equal(readLibrarySonicSettingsBinding('libraries.sonicAnalysis',doc,state),undefined);
  assert.throws(()=>patchLibrarySonicSettingsBinding('libraries.sonicAnalysis',true,doc,state));
 }
 for(const value of [undefined,null,'true',1])assert.throws(()=>patchLibrarySonicSettingsBinding('libraries.sonicAnalysis',value,library,sonic));
});
test('Sonic status is read-only and contains only approved producer facts',()=>{
 const row=readLibrarySonicSettingsBinding('libraries.sonicStatus',library,{...sonic,model:{id:'model',version:'v1',sha256:'a'.repeat(64)},estimatedRemainingSeconds:12})!;
 assert.deepEqual(row.current,{state:'disabled',done:2,total:7,observedAtMs:1000,estimatedRemainingSeconds:12});
 assert.equal(row.default,undefined);
 assert.throws(()=>patchLibrarySonicSettingsBinding('libraries.sonicStatus',true,library,sonic));
 assert.equal(readLibrarySonicSettingsBinding('unknown',library,sonic),undefined);
});
