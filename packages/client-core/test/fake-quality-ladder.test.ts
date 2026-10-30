import test from 'node:test';
import assert from 'node:assert/strict';
import {FakePlaybackServer,fakeHttp} from '../src/playback-v1/testing/fake-server.ts';
import {SessionsClient} from '../src/playback-v1/sessions.ts';
import {qualityLadder} from '../src/playback-v1/quality.ts';
import type {V1Http,V1Request} from '../src/playback-v1/http.ts';

function setup(){
 const server=new FakePlaybackServer(),inner=fakeHttp(server),requests:V1Request[]=[];
 const http:V1Http={send:request=>{requests.push(request);return inner.send(request as never) as never;}};
 return {server,requests,client:new SessionsClient(http)};
}

test('fake PATCH retains every canonical measured automatic quality ID on the same session',async()=>{
 const {client,requests}=setup();let session=await client.start({itemId:'movie'},{state:'playing'},'quality-ladder');const id=session.id;
 for(const rung of qualityLadder){
  const choice={mode:'automatic' as const,id:rung.id};
  session=await client.patch(id,session.revision,{quality:choice,throughputKbps:3000});
  assert.equal(session.id,id);assert.equal(session.presentation.quality.mode,'automatic');assert.equal(session.presentation.quality.id,rung.id);
  assert.deepEqual(requests.at(-1)!.body,{quality:choice,throughputKbps:3000});
 }
 session=await client.patch(id,session.revision,{quality:{mode:'automatic',id:'720p-low'}});
 assert.equal(session.presentation.quality.id,'720p-low');assert.deepEqual((requests.at(-1)!.body as {quality:unknown}).quality,{mode:'automatic',id:'720p-low'});
});

test('fake automatic stepping uses the canonical ladder and clamps at Original and the last rung',async()=>{
 const {client}=setup();let session=await client.start({itemId:'movie'},{state:'playing'},'quality-step');const id=session.id;
 session=await client.patch(id,session.revision,{quality:{mode:'automatic',id:'original'}});assert.equal(session.presentation.quality.id,undefined);
 session=await client.patch(id,session.revision,{quality:{mode:'automatic',step:'up'}});assert.equal(session.presentation.quality.id,undefined);
 for(const rung of qualityLadder){session=await client.patch(id,session.revision,{quality:{mode:'automatic',step:'down'}});assert.equal(session.presentation.quality.id,rung.id);assert.equal(session.id,id);}
 session=await client.patch(id,session.revision,{quality:{mode:'automatic',step:'down'}});assert.equal(session.presentation.quality.id,qualityLadder.at(-1)!.id);
 for(const rung of [...qualityLadder].reverse().slice(1)){session=await client.patch(id,session.revision,{quality:{mode:'automatic',step:'up'}});assert.equal(session.presentation.quality.id,rung.id);}
 session=await client.patch(id,session.revision,{quality:{mode:'automatic',step:'up'}});assert.equal(session.presentation.quality.id,undefined);
 session=await client.patch(id,session.revision,{quality:{mode:'automatic',step:'up'}});assert.equal(session.presentation.quality.id,undefined);
});

test('fake numeric ceilings and explicit Original remain independent of automatic ladder stepping',async()=>{
 const {client,requests}=setup();let session=await client.start({itemId:'movie'},{state:'playing'},'quality-ceiling');const id=session.id;
 const ceiling={mode:'limit' as const,maxHeight:720,maxVideoBitrateKbps:2000,maxAudioBitrateKbps:128};
 session=await client.patch(id,session.revision,{quality:ceiling});assert.deepEqual(session.presentation.quality,ceiling);assert.deepEqual(requests.at(-1)!.body,{quality:ceiling});
 session=await client.patch(id,session.revision,{quality:{mode:'original'}});assert.deepEqual(session.presentation.quality,{mode:'original'});assert.equal(session.id,id);
 session=await client.patch(id,session.revision,{quality:{mode:'automatic',step:'down'}});assert.equal(session.presentation.quality.id,qualityLadder[0]!.id);
});
