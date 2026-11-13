import test from 'node:test';
import assert from 'node:assert/strict';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {OwnerChannelSource} from '../src/screens/settings/server/channels/owner-channel-source.ts';
import {OwnerSelection} from '../src/screens/settings/server/channels/OwnerSelection.tsx';
import {copyBlockPreset,blockOverlap,validBuilder} from '../src/screens/settings/server/channels/owner-channel-draft.ts';
import {channelConditionNames,findChannelTitles,uploadOwnerChannelLogo} from '../../packages/client-core/src/server-admin/owner-library-channels.ts';
const builder={libraries:['films'],kinds:['movie'],quality:{},logoOnScreen:{},conditions:[{field:'futureField',mode:'any',values:['keep me']}]};
const row={id:'chn_one',revision:'3',name:'Films',number:1,on:true,summary:'Movies',timeZone:'America/Halifax',libraries:['films'],state:'onAir',titles:20,builder};
function fixture(){let document=structuredClone(row),fail=false,current=true;const calls:{path:string;method:string;body?:any;headers?:Record<string,string>}[]=[];
 const api={async request<T>(path:string,method='GET',body?:unknown){calls.push({path,method,body});if(path.includes('/templates'))return {items:[],timeZone:'America/Halifax',moodAvailable:false} as T;if(path==='/v1/admin/library-channels')return {items:[document]} as T;if(path.includes('/preview'))return {titles:20,unplaceable:0,hours:30,sample:[],today:[]} as T;return document as T;},async requestRaw(path:string,method:string,options:any){calls.push({path,method,body:options.body,headers:options.headers});if(fail)throw {status:503};document={...document,...options.body,revision:String(Number(document.revision)+1)};return {status:200,body:document,headers:{}};}};
 const source=new OwnerChannelSource(api,()=>current,()=>{});return {source,api,calls,fail:(v:boolean)=>{fail=v;},set:(value:Partial<typeof row>)=>{document={...document,...value};},retire:()=>{current=false;}};
}
test('modern channel edits carry a real revision, preserve opaque builders, and never replay an unknown patch',async()=>{
 const f=fixture();await f.source.load();await f.source.open(f.source.getSnapshot().channels[0]!);const editor=f.source.getSnapshot().editor!;f.source.edit(editor,draft=>({...draft,name:'Cinema'}));await f.source.save(f.source.getSnapshot().editor!);
 const write=f.calls.find(call=>call.method==='PATCH')!;assert.deepEqual(write.body,{name:'Cinema'});assert.equal(write.headers?.['If-Match'],'"3"');assert.deepEqual(f.source.getSnapshot().channels[0]!.builder,builder);
 await f.source.open(f.source.getSnapshot().channels[0]!);f.source.edit(f.source.getSnapshot().editor!,draft=>({...draft,description:'Evenings'}));f.fail(true);await f.source.save(f.source.getSnapshot().editor!);assert.equal(f.calls.filter(call=>call.method==='PATCH').length,2);assert.equal(f.source.getSnapshot().editor?.draft.description,'Evenings');assert.equal(f.source.getSnapshot().uncertain,false);assert.ok(f.source.getSnapshot().error);f.source.dispose();
});
test('an unknown create retries its immutable body and key, and a retired owner publishes nothing',async()=>{
 const f=fixture();await f.source.load();f.source.blank();f.source.edit(f.source.getSnapshot().editor!,draft=>({...draft,name:'New',builder}));f.fail(true);await f.source.save(f.source.getSnapshot().editor!);const first=f.calls.find(call=>call.method==='POST')!;assert.equal(f.source.getSnapshot().pendingCreate,true);
 const editor=f.source.getSnapshot().editor!;f.source.edit(editor,draft=>({...draft,name:'Changed'}));assert.equal(f.source.getSnapshot().editor,editor);f.fail(false);await f.source.save(editor);const writes=f.calls.filter(call=>call.method==='POST');assert.equal(writes.length,2);assert.deepEqual(writes[1]!.body,first.body);assert.equal(writes[1]!.headers?.['Idempotency-Key'],first.headers?.['Idempotency-Key']);
 f.retire();assert.equal(f.source.getSnapshot().channels.length,0);await f.source.load();assert.equal(f.source.isActive(),false);
});
test('a copied preset owns its selection and overnight blocks refuse weekly overlaps',()=>{
 const preset={id:'pre_one',revision:'1',name:'Night',library:'shows',recipe:{clauses:[{field:'genre',values:['Drama']}],order:'recent',minutes:120,startsAt:'23:00',days:['sat']}};
 const copied=copyBlockPreset(preset,'tv','block');assert.equal(copied.to,'01:00');assert.equal(copied.fromPreset,undefined);assert.equal(copied.selection?.sort,'recent');preset.recipe.clauses[0]!.values[0]='Comedy';assert.deepEqual(copied.selection?.conditions?.[0]?.values,['Drama']);
 const next={id:'next',name:'Early',days:['sun'],from:'00:30',to:'02:00'};assert.deepEqual(blockOverlap([copied,next]),[copied,next]);assert.equal(validBuilder({...builder,blocks:[copied,next]}),false);assert.equal(blockOverlap([copied,{...next,from:'01:00'}]),undefined);
});
test('title search is scoped, selected conditions have labels, and logo upload uses authenticated raw PUT',async()=>{
 const calls:any[]=[];const api={async request<T>(path:string){calls.push(path);return path.includes('/search')?{groups:[{kind:'movie',items:[{id:'mov',title:'Film'}],nextCursor:'next'}]} as T:{items:[{id:'tag_one',name:'Drama',kind:'genre'}]} as T;}};
 assert.equal((await findChannelTitles(api,'lib_one','a b','movie')).items[0]?.title,'Film');assert.match(calls[0],/library=lib_one/);assert.match(calls[0],/q=a\+b/);assert.deepEqual(await channelConditionNames(api,'genre',['tag_one','Thriller']),{tag_one:'Drama',Thriller:'Thriller'});
 const file=new File(['picture'],'logo.gif',{type:'image/gif'}),signal=new AbortController().signal;
 await uploadOwnerChannelLogo({baseUrl:'https://server.invalid',routeFetch:async(url:string,options:RequestInit)=>{assert.equal(url,'https://server.invalid/v1/admin/library-channels/chn_one/logo');assert.equal(options.method,'PUT');assert.equal(options.body,file);assert.equal(options.signal,signal);assert.equal((options.headers as Record<string,string>).Authorization,'Bearer captured');return new Response('{}');}},'chn_one','captured',file,signal);
 const html=renderToStaticMarkup(createElement(OwnerSelection,{lookup:{api,current:()=>true,report:()=>{}},libraries:[{id:'films',name:'Films',kind:'movie'}],value:builder,locked:false,change:()=>{}}));assert.match(html,/Play order/);assert.match(html,/Always include/);assert.match(html,/conditions the builder can/);assert.doesNotMatch(html,/futureField|keep me/);
});
