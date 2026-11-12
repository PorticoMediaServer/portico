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
// The rebuilt server: the unread count is the item list's total, and the marking is one set_state job.
async function fixture({response,count=3,retireAtCommandSettlement=false}={}) {
  const h=createRenderer(),calls=[],errors=[],committedWhileLive=[];let committed=0,live=true;
  const commands = retireAtCommandSettlement ? {...readingCommands, commitReadingMarkAll: (...args) => readingCommands.commitReadingMarkAll(...args).then(value => {live=false;return value;})} : readingCommands;
  const hook=await loadHook(h,commands);
  const context={scope:{serverId:'server'},identity:{viewer:{authority:'local',accountId:'account',profileId:'profile',role:'member'}},isCurrent:()=>live,effects:{reportError:error=>errors.push(error)},api:{
    async request(path,method,body){calls.push({path,method,body});if(path.startsWith('/v1/items'))return {items:[],total:count};return response?response(body):{id:'job_1',kind:'set_state',state:'done'};},
  }};
  const render=()=>h.render(()=>hook(context,'library','opening',()=>{committedWhileLive.push(live);committed++;}));
  render();await tick();render();
  return {calls,errors,render,committedWhileLive,committed:()=>committed,retire:()=>{live=false;},dispose:()=>h.dispose()};
}

test('Mark all read marks every title of the library with one job and refreshes the library once',async()=>{
  const f=await fixture();
  try{f.render()();await tick();
    assert.deepEqual(f.calls.map(call=>[call.method,call.path.split('?')[0]]),[['GET','/v1/items'],['POST','/v1/jobs']]);
    assert.deepEqual(f.calls[1].body,{kind:'set_state',target:{filter:'library=library&kind=book,comic,issue&watched=false'},params:{watched:true}},'the job marks only what the count said: the unread titles');
    assert.equal(f.committed(),1);}finally{f.dispose();}
});
test('a library with nothing left to read offers no Mark all read',async()=>{
  const f=await fixture({count:0});
  try{assert.equal(f.render(),undefined);}finally{f.dispose();}
});
test('a job that fails is reported, marks nothing, and can be asked for again',async()=>{
  const f=await fixture({response:()=>({id:'job_1',kind:'set_state',state:'failed',error:'stopped'})});
  try{f.render()();await tick();assert.equal(f.committed(),0);assert.equal(f.errors.length,1);
    const again=f.render();assert.equal(typeof again,'function');again();await tick();assert.equal(f.calls.filter(call=>call.method==='POST').length,2);}finally{f.dispose();}
});
test('a retired opening does not complete on a late Mark All success',async()=>{
  let resolve;const held=new Promise(yes=>{resolve=yes;}),f=await fixture({response:()=>held});
  try{f.render()();await tick();f.retire();resolve({id:'job_1',kind:'set_state',state:'done'});await tick();assert.equal(f.committed(),0);}finally{f.dispose();}
});
test('completion publishes at its original guarded point before command settlement can retire the opening',async()=>{
  const f=await fixture({retireAtCommandSettlement:true});
  try{f.render()();await tick();assert.deepEqual(f.committedWhileLive,[true]);}
  finally{f.dispose();}
});
