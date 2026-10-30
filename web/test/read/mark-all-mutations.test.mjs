import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {transformSync} from 'rolldown/utils';
import {createRenderer, tick} from '../helpers/interaction-harness.mjs';
import * as reading from '../../../packages/client-core/src/reading-mark-all.ts';
import * as mutations from '../../../packages/client-core/src/user-mutations.ts';
import {ApiError} from '../../../packages/client-core/src/index.ts';
import {randomId} from '../../../packages/client-core/src/random-id.ts';
import {screenLifetime} from '../../src/screens/detail/context.ts';

async function loadHook(h) {
  const file=new URL('../../src/screens/read/useReadingMarkAll.ts',import.meta.url);
  const result=transformSync(file.pathname,await readFile(file,'utf8'));
  if(result.errors.length)throw new Error(JSON.stringify(result.errors));
  const modules={react:h.react,'@core/index.ts':{ApiError},'@core/reading-mark-all.ts':reading,'@core/user-mutations.ts':mutations,'@core/random-id.ts':{randomId},'../detail/context':{screenLifetime}};
  const code=result.code.replace(/import\s+([\s\S]*?)\s+from\s+["']([^"']+)["'];/g,(_,names,source)=>`const ${names.trim().replace(/\bas\b/g,':')} = modules[${JSON.stringify(source)}];`).replace('export function useReadingMarkAll','function useReadingMarkAll');
  return new Function('modules',`${code}\nreturn useReadingMarkAll;`)(modules);
}
const proof=(viewer=1,count=3)=>({serverId:'server',libraryId:'library',viewerFence:'a'.repeat(64),revision:{catalog:2,viewer},count});
const marker=()=>({key:'p251_1793200000000_0123456789abcdef0123456789abcdef',operation:'post_libraries_libraryId_reading_mark_all',status:'applied',targetId:'library',revision:2,committedMs:1793200000000,expiresMs:1795792000000});
async function fixture({response,current=()=>proof(5,8),ack=async()=>{}}={}) {
  const h=createRenderer(),hook=await loadHook(h),calls=[],acks=[],errors=[];let committed=0,live=true;
  const context={scope:{serverId:'server'},identity:{viewer:{authority:'local',accountId:'account',profileId:'profile',role:'member'}},isCurrent:()=>live,effects:{reportError:error=>errors.push(error)},api:{
    async request(path,method,body){calls.push({path,method,body});if(method==='GET')return calls.length===1?proof():current();return response?response(body):{...proof(2),operationId:body.operationId,expectedRevision:body.expectedRevision,changed:3};},
    async acknowledgeUserMutation(key){acks.push(key);await ack(key);},
  }};
  const render=()=>h.render(()=>hook(context,'library','opening',()=>{committed++;}));
  render();await tick();render();
  return {calls,acks,errors,render,committed:()=>committed,retire:()=>{live=false;},dispose:()=>h.dispose()};
}
test('parsed Mark All success acknowledges before publishing completion',async()=>{
  let release;const held=new Promise(resolve=>{release=resolve;}),f=await fixture({ack:()=>held});
  try{f.render()();await tick();assert.equal(f.acks.length,1);assert.equal(f.committed(),0);release();await tick();assert.equal(f.committed(),1);assert.equal(f.acks[0],f.calls[1].body.operationId);}finally{release();f.dispose();}
});
test('invalid Mark All receipt retains the exact operation and never acknowledges',async()=>{
  const f=await fixture({response:body=>({...proof(2),operationId:'wrong',expectedRevision:body.expectedRevision,changed:3})});
  try{f.render()();await tick();f.render()();await tick();assert.equal(f.acks.length,0);assert.equal(f.committed(),0);assert.equal(f.calls[1].body.operationId,f.calls[2].body.operationId);}finally{f.dispose();}
});
test('compact applied Mark All receipt reads current projection without fabricating old counts',async()=>{
  const f=await fixture({response:()=>({mutationReceipt:marker()})});
  try{f.render()();await tick();assert.equal(f.calls.length,3);assert.equal(f.calls[2].method,'GET');assert.equal(f.acks.length,1);assert.equal(f.committed(),1);assert.deepEqual(f.errors,[]);}finally{f.dispose();}
});
test('wrong compact target or unreadable current projection never acknowledges or completes',async()=>{
  for(const wrongTarget of [false,true]){const m=marker();if(wrongTarget)m.targetId='other';const f=await fixture({response:()=>({mutationReceipt:m}),current:()=>({...proof(5),viewerFence:'b'.repeat(64)})});
    try{f.render()();await tick();assert.equal(f.acks.length,0);assert.equal(f.committed(),0);assert.equal(f.errors.length,1);}finally{f.dispose();}}
});
test('partial compact receipt remains pending for review and cannot claim completion',async()=>{
  const f=await fixture({response:()=>({mutationReceipt:{...marker(),status:'partial'}})});
  try{f.render()();await tick();assert.equal(f.acks.length,0);assert.equal(f.committed(),0);assert.equal(f.render(),undefined);assert.equal(f.errors[0].code,'mutation_needs_review');}finally{f.dispose();}
});
test('covered conflict preserves original command for review without a replacement revision or key',async()=>{
  const f=await fixture({response:()=>{throw new ApiError(409,'personal_state_conflict','conflict');}});
  try{f.render()();await tick();assert.equal(f.calls.length,2);assert.equal(f.render(),undefined);assert.equal(f.acks.length,0);assert.equal(f.calls[1].body.expectedRevision.viewer,1);}finally{f.dispose();}
});
test('retired opening cannot acknowledge a late parsed Mark All success',async()=>{
  let resolve;const held=new Promise(yes=>{resolve=yes;}),f=await fixture({response:()=>held});
  try{f.render()();await tick();f.retire();resolve({...proof(2),operationId:f.calls[1].body.operationId,expectedRevision:f.calls[1].body.expectedRevision,changed:3});await tick();assert.deepEqual(f.acks,[]);assert.equal(f.committed(),0);}finally{f.dispose();}
});


test('Mark All caller ID works on HTTP LAN without randomUUID, subtle or Web Locks',async()=>{
  const descriptor=Object.getOwnPropertyDescriptor(globalThis,'crypto'),crypto=globalThis.crypto;
  Object.defineProperty(globalThis,'crypto',{configurable:true,value:{getRandomValues:crypto.getRandomValues.bind(crypto)}});
  let f;try{f=await fixture();f.render()();await tick();assert.match(f.calls[1].body.operationId,/^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/);assert.equal(f.acks.length,1);}finally{f?.dispose();Object.defineProperty(globalThis,'crypto',descriptor);}
});
