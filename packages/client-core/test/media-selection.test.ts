import {test} from 'node:test';
import assert from 'node:assert/strict';
import {MediaSelectionService,mediaSelectionKey} from '../src/media-selection.ts';
import type {ContentEntry} from '../src/library-content.ts';
const entries:ContentEntry[]=['one','two','three'].map(id=>({id,title:id,kind:'movie',libraryId:'library',navigation:{view:'item',entityId:id}}));
const scope={serverId:'server',viewerId:'viewer'};
const state=(revision=0,favorite=false)=>({watchlisted:false,favorite,rating:null,revision});
function detail(itemId:string){return {scope:{serverId:'server',libraryId:'library',itemId,viewerFence:'fence'},revision:{catalog:1,viewer:0},item:{id:itemId,libraryId:'library',title:itemId,kind:'movie',duration:10,progressSeconds:0,available:true},personal:state(),actions:[{id:'favorite',labelKey:'favorite',enabled:true}],metadata:{status:'unavailable',ratings:[],genres:[],credits:[]}};}
const tick=()=>new Promise(resolve=>setTimeout(resolve,0));
test('range selection is bounded to loaded, deduplicated entries and cannot mutate aggregate entities',()=>{
 const service=new MediaSelectionService({scope,api:{async request(){throw Error('unused');}},requestId:async()=>crypto.randomUUID()});
 service.setEntries([...entries,entries[0]]);service.toggle(mediaSelectionKey(entries[0]));service.toggle(mediaSelectionKey(entries[2]),true);assert.equal(service.getSnapshot().selected.length,3);
 service.setEntries([entries[1]]);assert.equal(service.getSnapshot().selected.length,1);
 service.clear();service.setEntries([{id:'show',title:'Show',kind:'show',libraryId:'library',playback:{itemId:'first-episode'}}]);service.selectLoaded();assert.equal(service.getSnapshot().canApply,false);service.dispose();
});
test('select loaded and range selection retain every item beyond the old 100-item cap',()=>{
 const many:ContentEntry[]=Array.from({length:151},(_,i)=>({id:`item-${i}`,title:`Item ${i}`,kind:'movie',libraryId:'library'}));
 const service=new MediaSelectionService({scope,api:{async request(){throw Error('unused');}},requestId:async()=>crypto.randomUUID()});
 service.setEntries(many);service.selectLoaded();assert.equal(service.getSnapshot().selected.length,151);
 service.clear();service.toggle(mediaSelectionKey(many[0]));service.toggle(mediaSelectionKey(many[150]),true);
 assert.equal(service.getSnapshot().selected.length,151);assert.equal(service.getSnapshot().canApply,true);service.dispose();
});
test('applying a loaded selection writes and reports every selected item',async()=>{
 const many:ContentEntry[]=Array.from({length:101},(_,i)=>({id:`item-${i}`,title:`Item ${i}`,kind:'movie',libraryId:'library'}));
 const written:string[]=[];
 const api={async request<T>(path:string,method='GET',body?:any):Promise<T>{const id=path.split('/')[3];if(method==='GET')return detail(id) as T;written.push(id);return {operationId:body.operationId,serverId:'server',libraryId:'library',itemId:id,viewerFence:'fence',personal:state(1,true)} as T;}};
 const service=new MediaSelectionService({scope,api,requestId:async()=>crypto.randomUUID()});service.setEntries(many);service.selectLoaded();await service.apply('favorite',true);
 assert.equal(written.length,101);assert.equal(service.getSnapshot().results.length,101);assert(service.getSnapshot().results.every(result=>result.status==='saved'));service.dispose();
});
test('partial failures keep receipt identity, retry only failed items, and do not replay successful writes',async()=>{
 const bodies:{id:string;body:any}[]=[];let fail=true;
 const api={async request<T>(path:string,method='GET',body?:any):Promise<T>{const id=path.split('/')[3];if(method==='GET')return detail(id) as T;bodies.push({id,body});if(id==='two'&&fail){fail=false;throw new TypeError('Connection lost');}return {operationId:body.operationId,serverId:'server',libraryId:'library',itemId:id,viewerFence:'fence',personal:state(1,true)} as T;}};
 const service=new MediaSelectionService({scope,api,requestId:async()=>crypto.randomUUID()});service.setEntries(entries);service.selectLoaded();await service.apply('favorite',true);await tick();
 assert.deepEqual(service.getSnapshot().results.map(r=>r.status),['saved','failed','saved']);const operation=bodies.find(b=>b.id==='two')!.body.operationId;
 await service.retry();assert.deepEqual(service.getSnapshot().results.map(r=>r.status),['saved','saved','saved']);assert.equal(bodies.length,4);assert.equal(bodies.at(-1)!.body.operationId,operation);service.dispose();
});
test('disposal fences late reads and prevents any write for the previous viewer',async()=>{
 let finish!:(v:any)=>void,writes=0;const api={async request<T>(_path:string,method='GET'):Promise<T>{if(method==='PUT'){writes++;throw Error('must not write');}return new Promise(resolve=>{finish=resolve;});}};
 const service=new MediaSelectionService({scope,api,requestId:async()=>crypto.randomUUID()});service.setEntries(entries);service.selectLoaded();const task=service.apply('favorite',true);service.dispose();finish(detail('one'));await task;assert.equal(writes,0);
});
