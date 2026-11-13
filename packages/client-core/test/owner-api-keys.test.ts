import test from 'node:test';
import assert from 'node:assert/strict';
import {OwnerApiKeysSource} from '../src/server-admin/owner-api-keys.ts';

const key = {id:'key_one',name:'Tool',scopes:['read'],createdAt:'2026-11-13T12:00:00Z',state:'active'};
function fixture() {
 const calls: {path:string;method?:string;options?:any}[]=[];
 let fail=false, current=true;
 const api={request:async <T>(path:string,method?:string)=>{calls.push({path,method});return {items:[key,{...key,id:'old',state:'revoked',revokedAt:'2026-11-13T13:00:00Z'}]} as T;},
  requestRaw:async(path:string,method:string,options?:any)=>{calls.push({path,method,options});if(fail)throw Error('answer lost');return {status:200,headers:{},body:{key,secret:'issued-only'}};}};
 const source=new OwnerApiKeysSource(api,()=>current,assert.fail,()=> 'same-intent');
 return {source,calls,fail:(value:boolean)=>{fail=value;},retire:()=>{current=false;}};
}

test('the rebuilt key reader uses actual key states; an unknown create outcome retries only its captured idempotency key',async()=>{
 const f=fixture();try{
  await f.source.load();assert.deepEqual(f.source.getSnapshot().page?.items.map(key=>key.state),['active','revoked']);
  assert.equal(f.calls.length,1);assert.equal(f.calls[0].path,'/v1/admin/api-keys');
  f.fail(true);assert.equal(await f.source.create({name:'Tool',scopes:['read']}),undefined);assert.equal(f.source.getSnapshot().pendingCreate,true);
  assert.equal(await f.source.create({name:'Different',scopes:['admin']}),undefined);
  f.fail(false);const issued=await f.source.retryCreate();assert.equal(issued?.secret,'issued-only');
  const creates=f.calls.filter(call=>call.method==='POST');assert.equal(creates.length,2);
  assert.deepEqual(creates[0].options.body,creates[1].options.body);assert.equal(creates[1].options.headers['Idempotency-Key'],'same-intent');
  assert.equal(JSON.stringify(f.source.getSnapshot()).includes('issued-only'),false);
  const selected=f.source.getSnapshot().page!.items[0];await f.source.revoke({...selected});assert.equal(f.calls.some(call=>call.method==='DELETE'),false);
  await f.source.revoke(selected);assert.equal(f.calls.find(call=>call.method==='DELETE')?.path,'/v1/admin/api-keys/key_one');
  const count=f.calls.length;f.retire();await f.source.load();await f.source.create({name:'Tool',scopes:['read']});assert.equal(f.calls.length,count);
 }finally{f.source.dispose();}
});

test('a replaced page cannot publish a late answer or dispatch after retirement',async()=>{
 let finish!:(value:unknown)=>void;
 const source=new OwnerApiKeysSource({request:async <T>(path:string)=>path.includes('cursor')?{items:[{...key,id:'new'}]} as T:new Promise(resolve=>{finish=resolve;}) as Promise<T>,requestRaw:async()=>{throw Error('unexpected write');}},()=>true,assert.fail);
 try{
  const first=source.load();await source.load('2');finish({items:[key]});await first;assert.equal(source.getSnapshot().page?.items[0].id,'new');
 }finally{source.dispose();}
});
