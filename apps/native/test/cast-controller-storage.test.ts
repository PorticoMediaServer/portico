/** Original scoped proofs only; storage custody is not adoption authority. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {readControllerProofs} from '../src-next/platform/play-on/cast-controller-storage.ts';
import {castProofKey} from '../../../packages/client-core/src/cast/controller-proof.ts';
const scope={serverId:'server',authority:'local',accountId:'account',profileId:'profile',senderDeviceId:'phone',senderFamilyId:'family'};
const context={...scope,publicKey:'a'.repeat(43),origin:'https://server.test'};
const proof=(receiverId:string,token='a'):[string,string]=>[castProofKey(context,receiverId),JSON.stringify({version:2,context,binding:{id:'cct_proof',bootstrapId:'cbs_pair',receiverId,targetId:'cdv_target',targetFamilyId:'receiver-family',targetGeneration:'1',scope},controlToken:token.repeat(48)})];
test('scoped records retain newest original proof without relabelling their context',()=>{
 const a=proof('a'),b=proof('b'),latest=proof('a','b');assert.deepEqual(readControllerProofs({items:[a,b,latest]}),[b,latest]);
});
test('legacy tokens, wrong keys, malformed and oversized scope records are ignored',()=>{
 const valid=proof('tv');
 assert.deepEqual(readControllerProofs({items:[['old-origin-and-name','a'.repeat(48)],[valid[0],'a'.repeat(48)],['other-key',valid[1]],[valid[0],'x'.repeat(4097)],valid]}),[valid]);
 for(const raw of [null,undefined,'text',[],{},{items:'no'}])assert.deepEqual(readControllerProofs(raw),[]);
});
test('only the newest sixteen bounded installation-private records are kept',()=>{
 const many=Array.from({length:40},(_,i)=>proof('tv'+i)),kept=readControllerProofs({items:many});assert.equal(kept.length,16);assert.deepEqual(kept[0],many[24]);assert.deepEqual(kept.at(-1),many[39]);
});
