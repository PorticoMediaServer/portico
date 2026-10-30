import {test,before,after} from 'node:test';
import assert from 'node:assert/strict';
import {abortScope,abortReason} from '../src/abort.ts';
import {throwIfAborted} from '../src/errors.ts';
import {resourceAssets} from '../src/epub/resources.ts';
import {pdfSequential} from '../src/pdf/sequential.ts';
import {ReaderController} from '../src/controller.ts';
import type {ReaderAdapter} from '../src/ports.ts';

// Simulate the specific absent APIs, not an iOS/WebKit qualification. Each
// Node test file has its own process; the production code patches no globals.
const missing=[['any',AbortSignal],['timeout',AbortSignal],['throwIfAborted',AbortSignal.prototype],['reason',AbortSignal.prototype]] as const;
const descriptors=missing.map(([key,target])=>Object.getOwnPropertyDescriptor(target,key));
before(()=>{for(const [key,target]of missing)Object.defineProperty(target,key,{configurable:true,value:undefined});});
after(()=>{missing.forEach(([key,target],i)=>{const descriptor=descriptors[i];if(descriptor)Object.defineProperty(target,key,descriptor);else Reflect.deleteProperty(target,key);});});
const active=()=>new AbortController().signal;
function counted(signal:AbortSignal) {
  const add=signal.addEventListener.bind(signal),remove=signal.removeEventListener.bind(signal);let listeners=0;
  signal.addEventListener=(...args)=>{if(args[0]==='abort')listeners++;return add(...args);};
  signal.removeEventListener=(...args)=>{if(args[0]==='abort')listeners--;return remove(...args);};
  return ()=>listeners;
}

test('scoped cancellation needs no modern signal API, deduplicates and releases all parent links',()=>{
  const parent=new AbortController(),other=new AbortController(),count=counted(parent.signal),countOther=counted(other.signal);
  const task=abortScope([parent.signal,parent.signal,other.signal]);assert.equal(count(),1);assert.equal(countOther(),1);
  parent.abort();assert.ok(task.signal.aborted);assert.equal(count(),0);assert.equal(countOther(),0);
  assert.throws(()=>throwIfAborted(task.signal),{code:'aborted'});assert.equal((abortReason(task.signal) as {code:string}).code,'aborted');
  task.dispose();assert.equal(count(),0);
  const completed=abortScope([other.signal]);assert.equal(countOther(),1);completed.dispose();assert.equal(countOther(),0);assert.equal(completed.signal.aborted,false);
  const already=abortScope([parent.signal,other.signal]);assert.equal(already.signal.aborted,true);assert.equal(countOther(),0);
});

test('repeated EPUB asset operations release parent links on success, missing member and failure',async()=>{
  const parent=new AbortController(),count=counted(parent.signal);let bad=false;
  const assets=resourceAssets({id:'epub',revision:'v1',size:1,resources:{entries:[{path:'chapter.xhtml',mime:'application/xhtml+xml',size:1}],async read(){return new Uint8Array(bad?2:1);}}});
  for(let i=0;i<30;i++){await assets.read('chapter.xhtml',parent.signal);assert.equal(count(),0);}
  assert.equal((await assets.read('missing.xhtml',parent.signal)).length,0);assert.equal(count(),0);
  bad=true;await assert.rejects(assets.read('chapter.xhtml',parent.signal),{code:'invalid_book'});assert.equal(count(),0);
  await assets.dispose();await assert.rejects(assets.read('chapter.xhtml',parent.signal),{code:'aborted'});assert.equal(count(),0);
});

test('progressive PDF disposal cancels an in-flight stream without reason or throwIfAborted',async()=>{
  const bytes=new Uint8Array(2048);bytes.set(new TextEncoder().encode('%PDF-1.7'));
  let cancelled=0,received=0;const parent=new AbortController(),count=counted(parent.signal);
  const stream=new ReadableStream<Uint8Array>({start(controller){controller.enqueue(bytes);},cancel(){cancelled++;}});
  const sequential=await pdfSequential({id:'pdf',revision:'v1',size:4096},async()=>stream,parent.signal);
  const running=sequential.start({chunk(){received++;},done(){assert.fail('not a complete stream');},error(){assert.fail('explicit cancellation is not a decode error');}});
  parent.abort();await running;await sequential.dispose();assert.equal(cancelled,1);assert.equal(received,1);assert.equal(count(),0);
});

test('search factory throw releases controller operation links',async()=>{
  const parent=new AbortController(),count=counted(parent.signal),location={format:'pdf' as const,page:0,progression:0};
  const adapter:ReaderAdapter<{}>={format:'pdf',async open(){return {id:'pdf',revision:'v1',format:'pdf',metadata:{pageCount:1},toc:[],
    async mount(){return {async navigate(){},async resize(){},async applyPreferences(){},getLocation(){return location;},dispose(){}};},
    search(){throw new Error('factory failed');},dispose(){}};}};
  const reader=new ReaderController({adapters:[adapter],preferences:{}});
  await reader.open({source:{id:'pdf',revision:'v1',size:1},format:'pdf',container:{} as HTMLElement,viewport:{width:100,height:100}});
  try{await assert.rejects((async()=>{for await(const _hit of reader.search('a',{signal:parent.signal,limit:2})){}})(),/factory failed/);assert.equal(count(),0);}
  finally{await reader.close();}
});
test('controller previews and early-ended searches release operation links without signal.any',async()=>{
  const parent=new AbortController(),count=counted(parent.signal),location={format:'pdf' as const,page:0,progression:0};
  const adapter:ReaderAdapter<{}>={format:'pdf',async open(){return {id:'pdf',revision:'v1',format:'pdf',metadata:{pageCount:2},toc:[],
    async mount(){return {async navigate(){},async resize(){},async applyPreferences(){},getLocation(){return location;},dispose(){}};},
    async page(){return {mime:'image/png',bytes:new Uint8Array([1]),width:1,height:1};},
    async *search(){yield {id:'a',excerpt:'a',location};yield {id:'b',excerpt:'b',location};},dispose(){}};}};
  const reader=new ReaderController({adapters:[adapter],preferences:{}});
  await reader.open({source:{id:'pdf',revision:'v1',size:1},format:'pdf',container:{} as HTMLElement,viewport:{width:100,height:100}});
  for(let i=0;i<30;i++){await reader.page(0,{width:100,signal:parent.signal});assert.equal(count(),0);}
  for await(const _result of reader.search('a',{limit:2,signal:parent.signal})){break;}assert.equal(count(),0);
  parent.abort();await assert.rejects(reader.page(0,{width:100,signal:parent.signal}),{code:'aborted'});assert.equal(count(),0);await reader.close();
});
