import test from 'node:test';
import assert from 'node:assert/strict';
import {ChannelMapController} from '../src/screens/settings/server-livetv/channel-map-controller.ts';
const source='lsr_guide';
const row={id:'chn_news',name:'News',number:'2.1',sourceNumber:'2.1',broughtIn:true,hidden:false,guideSource:source,guideKey:'news',edited:[]};
test('the rebuilt map searches/pages and sends only edited fields, preserving the paired listings source',async()=>{
 const calls:any[]=[];let patched=false;
 const controller=new ChannelMapController({async request<T>(path,method,body){calls.push({path,method,body});if(method==='PATCH'){patched=true;return undefined as T;}if(path==='/v1/admin/live/sources')return {items:[{id:source,name:'Guide'}]} as T;return {items:[{...row,hidden:patched}],nextCursor:patched?undefined:'cursor'} as T;}},'server','lsr_channels',()=>true,()=>{});
 await controller.load(false,'News');assert.equal(calls[0].path,'/v1/admin/live/sources/lsr_channels/channels?q=News');
 await controller.load(true);assert.equal(calls[2].path,'/v1/admin/live/sources/lsr_channels/channels?q=News&cursor=cursor');assert.equal(controller.getSnapshot().rows.length,1);
 controller.edit(controller.getSnapshot().rows[0],{guideKey:'renamed',hidden:true});assert.equal(await controller.save(),true);
 assert.deepEqual(calls.find(call=>call.method==='PATCH').body,{channels:[{id:row.id,hidden:true,guideSource:source,guideKey:'renamed'}]});
 assert.equal(controller.getSnapshot().rows[0].hidden,true);assert.deepEqual(controller.getSnapshot().edits,{});controller.dispose();
});
test('a missing save answer reads back without replay; retirement prevents retained commands',async()=>{
 let reads=0,writes=0,current=true;
 const controller=new ChannelMapController({async request<T>(path,method){if(method==='PATCH'){writes++;throw {status:503};}if(path==='/v1/admin/live/sources')return {items:[]} as T;reads++;return {items:[{...row,number:reads>1?'40':'2.1'}]} as T;}},'server','lsr_channels',()=>current,()=>{});
 await controller.load();controller.edit(controller.getSnapshot().rows[0],{number:'40'});assert.equal(await controller.save(),false);assert.equal(reads,2);assert.equal(writes,1);assert.equal(controller.getSnapshot().rows[0].number,'40');assert.deepEqual(controller.getSnapshot().edits,{});
 current=false;assert.equal(await controller.save(),false);assert.equal(writes,1);assert.equal(controller.getSnapshot().rows.length,0);
});
