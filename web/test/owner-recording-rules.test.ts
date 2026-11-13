import test from 'node:test';
import assert from 'node:assert/strict';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {RecordingRulesController} from '../src/screens/settings/server-livetv/recording/rules-controller.ts';
import {RecordingRules} from '../src/screens/settings/server-livetv/recording/RecordingRules.tsx';
const defaults={folder:'',beforeMs:60000,afterMs:180000,floorBytes:10*1024**3,capBytes:0,keepDays:0,keepNewest:0,stopViewers:false};
const rule={id:'rul_1',profileId:'pro_one',profileName:'One',title:'News',source:'lsr_1',channel:'lch_1',channelName:'News channel',byTitle:true,newOnly:false,priority:0,paused:false};
const permissions={getSnapshot:()=>({owners:[{profileId:'pro_one',profileName:'One',accountName:'Member',canRecord:true}],loading:false}),subscribe:()=>()=>{}} as any;
function fixture(fail=false){let rules:any[]=[rule];const calls:any[]=[];
 const api={async request<T>(path:string,method='GET',body?:any){calls.push({path,method,body});if(path==='/v1/admin/settings/dvr')return {values:defaults,etag:'"7"'} as T;if(method==='GET'&&path.startsWith('/v1/dvr/rules'))return {items:rules} as T;
 if(path.includes('/guide'))return {items:[{channel:{kind:'source',name:'News channel'},programmes:[{id:'program',title:'News',startsAt:'2026-11-13T12:00:00Z',endsAt:'2026-11-13T13:00:00Z'}]}]} as T;
 if(path.includes('preview=true'))return {...rule,id:body?.programme?'':rule.id,preview:{matches:2,airings:[],newOnlyAvailable:true}} as T;
 if(method==='DELETE'){rules=[];return undefined as T;}if(fail)throw {status:503};return {...rule,...body} as T;}};
 const controller=new RecordingRulesController(api,'server',permissions,()=>true,()=>{});return {controller,calls};
}
test('owner rule edits use modern partial PATCH and preserve implicit defaults and unrelated choices',async()=>{
 const {controller,calls}=fixture();await controller.load();controller.open(controller.getSnapshot().rules[0]);await new Promise(resolve=>setImmediate(resolve));const draft=controller.getSnapshot().draft!;controller.edit(draft,{paused:true});assert.equal(await controller.save(controller.getSnapshot().draft!),true);
 const write=calls.find(call=>call.method==='PATCH'&&!call.path.includes('preview'));assert.equal(write.path,'/v1/dvr/rules/rul_1');assert.deepEqual(write.body,{paused:true});controller.dispose();
});
test('creating a rule sends the permitted target profile and actual programme without legacy generations',async()=>{
 const {controller,calls}=fixture();await controller.load();controller.open();controller.edit(controller.getSnapshot().draft!,{profileId:'pro_one'});await controller.guide(controller.getSnapshot().draft!,'2026-11-13','lsr_1');controller.choose(controller.getSnapshot().draft!,controller.getSnapshot().programmes[0]!);await new Promise(resolve=>setImmediate(resolve));assert.equal(await controller.save(controller.getSnapshot().draft!),true);
 const write=calls.find(call=>call.method==='POST'&&!call.path.includes('preview'));assert.equal(write.path,'/v1/dvr/rules');assert.equal(write.body.profileId,'pro_one');assert.equal(write.body.programme,'program');assert.equal(write.body.padBeforeMs,60000);assert.equal(write.body.padAfterMs,180000);assert.equal('anchor'in write.body,false);assert.equal('operationId'in write.body,false);controller.dispose();
});
test('unknown saves read the real list without replaying, and the editor renders backed fields',async()=>{
 const {controller,calls}=fixture(true);await controller.load();controller.open(controller.getSnapshot().rules[0]);await new Promise(resolve=>setImmediate(resolve));
 const html=renderToStaticMarkup(createElement(RecordingRules,{controller,permissions,sources:[{id:'lsr_1',name:'Antenna'}]}));assert.match(html,/News channel/);assert.match(html,/One/);assert.match(html,/Antenna/);assert.match(html,/Matches by title/);
 controller.edit(controller.getSnapshot().draft!,{paused:true});await controller.save(controller.getSnapshot().draft!);assert.equal(calls.filter(call=>call.method==='PATCH'&&!call.path.includes('preview')).length,1);assert.equal(controller.getSnapshot().draft,undefined);assert.ok(controller.getSnapshot().error);controller.dispose();
});
