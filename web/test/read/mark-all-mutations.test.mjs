import * as readingCommands from '../../../packages/client-core/src/query/reading-commands.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {transformSync} from 'rolldown/utils';
import {createRenderer, tick} from '../helpers/interaction-harness.mjs';
import * as reading from '../../../packages/client-core/src/reading-mark-all.ts';
import {ApiError} from '../../../packages/client-core/src/index.ts';
import {randomId} from '../../../packages/client-core/src/random-id.ts';
import {screenLifetime} from '../../src/screens/detail/context.ts';

async function loadHook(h, commands = readingCommands) {
  const file=new URL('../../src/screens/read/useReadingMarkAll.ts',import.meta.url);
  const result=transformSync(file.pathname,await readFile(file,'utf8'));
  if(result.errors.length)throw new Error(JSON.stringify(result.errors));
  const modules={'@core/query/reading-commands.ts':commands,react:h.react,'@core/index.ts':{ApiError},'@core/reading-mark-all.ts':reading,'@core/random-id.ts':{randomId},'../detail/context':{screenLifetime}};
  const code=result.code.replace(/import\s+([\s\S]*?)\s+from\s+["']([^"']+)["'];/g,(_,names,source)=>`const ${names.trim().replace(/\bas\b/g,':')} = modules[${JSON.stringify(source)}];`).replace('export function useReadingMarkAll','function useReadingMarkAll');
  return new Function('modules',`${code}\nreturn useReadingMarkAll;`)(modules);
}
const proof=(viewer=1,count=3)=>({serverId:'server',libraryId:'library',viewerFence:'a'.repeat(64),revision:{catalog:2,viewer},count});
async function fixture({response,current=()=>proof(5,8),retireAtCommandSettlement=false}={}) {
  const h=createRenderer(),calls=[],errors=[],committedWhileLive=[];let committed=0,live=true;
  const commands = retireAtCommandSettlement ? {...readingCommands, commitReadingMarkAll: (...args) => readingCommands.commitReadingMarkAll(...args).then(value => {live=false;return value;})} : readingCommands;
  const hook=await loadHook(h,commands);
  const context={scope:{serverId:'server'},identity:{viewer:{authority:'local',accountId:'account',profileId:'profile',role:'member'}},isCurrent:()=>live,effects:{reportError:error=>errors.push(error)},api:{
    async request(path,method,body){calls.push({path,method,body});if(method==='GET')return calls.length===1?proof():current();return response?response(body):{...proof(2),operationId:body.operationId,expectedRevision:body.expectedRevision,changed:3};},
  }};
  const render=()=>h.render(()=>hook(context,'library','opening',()=>{committedWhileLive.push(live);committed++;}));
  render();await tick();render();
  return {calls,errors,render,committedWhileLive,committed:()=>committed,retire:()=>{live=false;},dispose:()=>h.dispose()};
}
test('an invalid Mark All answer keeps the exact operation for another try',async()=>{
  const f=await fixture({response:body=>({...proof(2),operationId:'wrong',expectedRevision:body.expectedRevision,changed:3})});
  try{f.render()();await tick();f.render()();await tick();assert.equal(f.committed(),0);assert.equal(f.calls[1].body.operationId,f.calls[2].body.operationId);}finally{f.dispose();}
});
test('covered conflict preserves original command for review without a replacement revision or key',async()=>{
  const f=await fixture({response:()=>{throw new ApiError(409,'personal_state_conflict','conflict');}});
  try{f.render()();await tick();assert.equal(f.calls.length,2);assert.equal(f.render(),undefined);assert.equal(f.calls[1].body.expectedRevision.viewer,1);}finally{f.dispose();}
});
test('a retired opening does not complete on a late Mark All success',async()=>{
  let resolve;const held=new Promise(yes=>{resolve=yes;}),f=await fixture({response:()=>held});
  try{f.render()();await tick();f.retire();resolve({...proof(2),operationId:f.calls[1].body.operationId,expectedRevision:f.calls[1].body.expectedRevision,changed:3});await tick();assert.equal(f.committed(),0);}finally{f.dispose();}
});


test('Mark All caller ID works on HTTP LAN without randomUUID, subtle or Web Locks',async()=>{
  const descriptor=Object.getOwnPropertyDescriptor(globalThis,'crypto'),crypto=globalThis.crypto;
  Object.defineProperty(globalThis,'crypto',{configurable:true,value:{getRandomValues:crypto.getRandomValues.bind(crypto)}});
  let f;try{f=await fixture();f.render()();await tick();assert.match(f.calls[1].body.operationId,/^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/);}finally{f?.dispose();Object.defineProperty(globalThis,'crypto',descriptor);}
});

test('completion publishes at its original guarded point before command settlement can retire the opening',async()=>{
  const f=await fixture({retireAtCommandSettlement:true});
  try{f.render()();await tick();assert.deepEqual(f.committedWhileLive,[true]);}
  finally{f.dispose();}
});
